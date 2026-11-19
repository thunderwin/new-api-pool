package capability

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	geoAuditRequestTimeout = 15 * time.Second
	geoAuditConnectTimeout = 5 * time.Second
	geoAuditConcurrency    = 4
	geoAuditMaxRedirects   = 5
	geoAuditPageMaxBytes   = 3 * 1024 * 1024
)

// geoFetchError carries a stable, content-free failure reason. blocked marks
// requests rejected by the SSRF or same-site policy; the message never contains
// the resolved address or the underlying transport error.
type geoFetchError struct {
	code    string
	blocked bool
}

func (e *geoFetchError) Error() string { return e.code }

func newGeoFetchError(code string) *geoFetchError {
	return &geoFetchError{code: code}
}

func newGeoBlockedError(code string) *geoFetchError {
	return &geoFetchError{code: code, blocked: true}
}

// geoAuditTestHooks is the only injection point for offline tests. Production
// leaves all fields nil: every connection then resolves the hostname itself,
// validates every resolved address before dialing (including redirects) and
// verifies the server certificate against the URL hostname.
var geoAuditTestHooks struct {
	resolver  func(ctx context.Context, host string) ([]net.IP, error)
	dialer    func(ctx context.Context, network, address string) (net.Conn, error)
	tlsConfig *tls.Config
}

type geoResponse struct {
	RequestedURL string
	FinalURL     string
	Status       int
	Header       http.Header
	Body         []byte
}

type geoFetcher struct {
	site     string
	maxBytes int64
	sem      chan struct{}
	resolve  func(ctx context.Context, host string) ([]net.IP, error)
	dial     func(ctx context.Context, network, address string) (net.Conn, error)
	client   *http.Client
}

func newGeoFetcher(site string) *geoFetcher {
	f := &geoFetcher{
		site:     site,
		maxBytes: geoAuditPageMaxBytes,
		sem:      make(chan struct{}, geoAuditConcurrency),
	}
	f.resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		if geoAuditTestHooks.resolver != nil {
			return geoAuditTestHooks.resolver(ctx, host)
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		ips := make([]net.IP, 0, len(addrs))
		for _, addr := range addrs {
			ips = append(ips, addr.IP)
		}
		return ips, nil
	}
	f.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if geoAuditTestHooks.dialer != nil {
			return geoAuditTestHooks.dialer(ctx, network, address)
		}
		dialer := &net.Dialer{Timeout: geoAuditConnectTimeout, KeepAlive: 30 * time.Second}
		return dialer.DialContext(ctx, network, address)
	}
	transport := &http.Transport{
		// No proxy: proxies would re-resolve the target and bypass the dial-time
		// address checks.
		Proxy:                 nil,
		DialContext:           f.safeDialContext,
		TLSHandshakeTimeout:   geoAuditRequestTimeout,
		ResponseHeaderTimeout: geoAuditRequestTimeout,
		MaxIdleConns:          geoAuditConcurrency * 2,
		MaxIdleConnsPerHost:   geoAuditConcurrency,
		IdleConnTimeout:       30 * time.Second,
		TLSClientConfig:       geoAuditTestHooks.tlsConfig,
	}
	f.client = &http.Client{
		Transport:     transport,
		CheckRedirect: f.checkRedirect,
	}
	return f
}

func (f *geoFetcher) close() {
	if f == nil || f.client == nil {
		return
	}
	if transport, ok := f.client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

// safeDialContext resolves the host at dial time, rejects any non-public
// address and dials the validated IP literal. TLS keeps using the URL hostname
// for SNI and certificate verification, so no request can reach a private
// address even when DNS answers change between lookups.
func (f *geoFetcher) safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, newGeoBlockedError("invalid_address")
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if port != "443" {
		return nil, newGeoBlockedError("non_default_port")
	}
	ips, err := f.resolve(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, newGeoFetchError("dns_failed")
	}
	for _, ip := range ips {
		if !geoPublicIP(ip) {
			return nil, newGeoBlockedError("blocked_address")
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, dialErr := f.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil && errors.Is(lastErr, context.DeadlineExceeded) {
		return nil, newGeoFetchError("timeout")
	}
	return nil, newGeoFetchError("connect_failed")
}

func (f *geoFetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > geoAuditMaxRedirects {
		return newGeoBlockedError("too_many_redirects")
	}
	return validateGeoFetchURL(req.URL, f.site)
}

// validateGeoFetchURL enforces the same-site crawling policy for every fetched
// URL: https, 443, no userinfo, no IP literal and the same registrable domain
// as the audited site.
func validateGeoFetchURL(u *url.URL, site string) error {
	if u == nil {
		return newGeoBlockedError("invalid_url")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return newGeoBlockedError("insecure_scheme")
	}
	if u.User != nil {
		return newGeoBlockedError("userinfo_not_allowed")
	}
	if port := u.Port(); port != "" && port != "443" {
		return newGeoBlockedError("non_default_port")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || net.ParseIP(host) != nil {
		return newGeoBlockedError("ip_literal_not_allowed")
	}
	if !geoSameSite(host, site) {
		return newGeoBlockedError("cross_site_redirect")
	}
	return nil
}

func (f *geoFetcher) get(ctx context.Context, rawURL string, userAgent string) (*geoResponse, error) {
	return f.getWithLimit(ctx, rawURL, userAgent, f.maxBytes, true)
}

// getWithLimit reads at most limit bytes. Probe requests pass strict=false so a
// large homepage is not downloaded nine times just to compare status codes.
func (f *geoFetcher) getWithLimit(ctx context.Context, rawURL string, userAgent string, limit int64, strict bool) (*geoResponse, error) {
	select {
	case f.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, newGeoFetchError("timeout")
	}
	defer func() { <-f.sem }()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, newGeoFetchError("invalid_url")
	}
	if err := validateGeoFetchURL(parsed, f.site); err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, geoAuditRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, newGeoFetchError("invalid_url")
	}
	// GET only, no cookies, no credentials.
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, classifyGeoTransportError(ctx, reqCtx, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if readErr != nil {
		if errors.Is(readErr, context.DeadlineExceeded) {
			return nil, newGeoFetchError("timeout")
		}
		return nil, newGeoFetchError("read_failed")
	}
	if int64(len(body)) > limit {
		if strict {
			return nil, newGeoFetchError("response_too_large")
		}
		body = body[:limit]
	}
	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return &geoResponse{
		RequestedURL: rawURL,
		FinalURL:     finalURL,
		Status:       resp.StatusCode,
		Header:       resp.Header.Clone(),
		Body:         body,
	}, nil
}

func classifyGeoTransportError(parent context.Context, requestCtx context.Context, err error) error {
	var fetchErr *geoFetchError
	if errors.As(err, &fetchErr) {
		return fetchErr
	}
	if parent.Err() != nil || requestCtx.Err() != nil ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return newGeoFetchError("timeout")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return newGeoFetchError("dns_failed")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newGeoFetchError("timeout")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		if strings.Contains(strings.ToLower(urlErr.Err.Error()), "tls") ||
			strings.Contains(strings.ToLower(urlErr.Err.Error()), "certificate") {
			return newGeoFetchError("tls_failed")
		}
	}
	return newGeoFetchError("connect_failed")
}

// geoAuditErrorCode maps a fetch failure to the stable per-page code exposed in
// the public output. No underlying error text is included.
func geoAuditErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var fetchErr *geoFetchError
	if errors.As(err, &fetchErr) {
		if fetchErr.blocked {
			switch fetchErr.code {
			case "blocked_address", "cross_site_redirect", "insecure_scheme",
				"ip_literal_not_allowed", "userinfo_not_allowed", "non_default_port",
				"too_many_redirects", "invalid_url", "invalid_address":
				return "blocked"
			}
		}
		return fetchErr.code
	}
	return "fetch_failed"
}

// geoPublicIP reports whether an address may be dialed: no loopback, private,
// link-local, unspecified, multicast, CGNAT or IPv6 ULA ranges.
func geoPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4.IsLoopback() || ip4.IsPrivate() || ip4.IsLinkLocalUnicast() ||
			ip4.IsLinkLocalMulticast() || ip4.IsUnspecified() || ip4.IsMulticast() {
			return false
		}
		// 100.64.0.0/10 carrier-grade NAT, 0.0.0.0/8, 192.0.0.0/24 and
		// 198.18.0.0/15 benchmarking ranges are not public destinations.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
		if ip4[0] == 0 {
			return false
		}
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0 {
			return false
		}
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
			return false
		}
		if ip4[0] >= 240 {
			return false
		}
		return true
	}
	ip = ip.To16()
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// IPv6 ULA fc00::/7 is covered by IsPrivate in modern Go, but check the
	// first byte explicitly so the guarantee does not depend on stdlib version.
	if ip[0]&0xfe == 0xfc {
		return false
	}
	return true
}

// geoSiteDomain returns the registrable domain (eTLD+1) used for the same-site
// crawl boundary. Unknown TLDs fall back to the last two labels.
func geoSiteDomain(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && domain != "" {
		return domain
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func geoSameSite(host string, site string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	site = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), ".")
	if host == "" || site == "" {
		return false
	}
	if host == site {
		return true
	}
	if !strings.HasSuffix(host, "."+site) {
		return false
	}
	// The prefix must stay inside the registrable domain, so
	// evil.example.com is fine for site example.com but example.com.evil.net
	// never matches.
	return geoSiteDomain(host) == site
}

// geoParseIPLiteral reports whether the host is an IP literal.
func geoParseIPLiteral(host string) (net.IP, bool) {
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip, ip != nil
}
