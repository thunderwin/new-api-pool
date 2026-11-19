package capability

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func withGeoAuditHooks(
	t *testing.T,
	resolver func(ctx context.Context, host string) ([]net.IP, error),
	dialer func(ctx context.Context, network, address string) (net.Conn, error),
	tlsConfig *tls.Config,
) {
	t.Helper()
	previousResolver := geoAuditTestHooks.resolver
	previousDialer := geoAuditTestHooks.dialer
	previousTLS := geoAuditTestHooks.tlsConfig
	geoAuditTestHooks.resolver = resolver
	geoAuditTestHooks.dialer = dialer
	geoAuditTestHooks.tlsConfig = tlsConfig
	t.Cleanup(func() {
		geoAuditTestHooks.resolver = previousResolver
		geoAuditTestHooks.dialer = previousDialer
		geoAuditTestHooks.tlsConfig = previousTLS
	})
}

// geoTestNetwork runs TLS fixture servers under fake public hostnames. Only
// DNS resolution, the socket dialer and test-server certificate trust are
// replaced; URL validation and dial-time address checks stay on the production
// path.
type geoTestNetwork struct {
	mu       sync.Mutex
	ips      map[string]net.IP
	servers  map[string]*httptest.Server
	byIP     map[string]*httptest.Server
	nextIP   byte
	dialed   []string
	resolves int
}

func newGeoTestNetwork(t *testing.T) *geoTestNetwork {
	t.Helper()
	return &geoTestNetwork{
		ips:     map[string]net.IP{},
		servers: map[string]*httptest.Server{},
		byIP:    map[string]*httptest.Server{},
	}
}

func (n *geoTestNetwork) serve(t *testing.T, host string, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nextIP++
	ip := net.IPv4(203, 0, 113, n.nextIP)
	n.ips[host] = ip
	n.servers[host] = server
	n.byIP[ip.String()] = server
	return server
}

func (n *geoTestNetwork) install(t *testing.T) {
	t.Helper()
	withGeoAuditHooks(t,
		func(ctx context.Context, host string) ([]net.IP, error) {
			n.mu.Lock()
			defer n.mu.Unlock()
			n.resolves++
			ip, ok := n.ips[strings.TrimSuffix(strings.ToLower(host), ".")]
			if !ok {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			return []net.IP{ip}, nil
		},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			n.mu.Lock()
			n.dialed = append(n.dialed, address)
			host, _, _ := net.SplitHostPort(address)
			server := n.byIP[host]
			n.mu.Unlock()
			if server == nil {
				return nil, fmt.Errorf("no fixture server for %s", host)
			}
			return net.Dial("tcp", server.Listener.Addr().String())
		},
		// Test servers present self-signed certificates for fake hostnames.
		&tls.Config{InsecureSkipVerify: true},
	)
}

func TestGeoPublicIPClassification(t *testing.T) {
	tests := []struct {
		address string
		public  bool
	}{
		{"93.184.216.34", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"100.127.255.255", false},
		{"0.0.0.0", false},
		{"224.0.0.1", false},
		{"198.18.0.1", false},
		{"::1", false},
		{"fd00::1", false},
		{"fc00::1", false},
		{"fe80::1", false},
		{"ff02::1", false},
		{"::", false},
		{"2606:2800:220:1:248:1893:25c8:1946", true},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			ip := net.ParseIP(test.address)
			if ip == nil {
				t.Fatalf("invalid test address %s", test.address)
			}
			if got := geoPublicIP(ip); got != test.public {
				t.Fatalf("geoPublicIP(%s) = %v, want %v", test.address, got, test.public)
			}
		})
	}
}

func TestGeoSameSiteBoundary(t *testing.T) {
	tests := []struct {
		host string
		site string
		want bool
	}{
		{"example.com", "example.com", true},
		{"www.example.com", "example.com", true},
		{"deep.www.example.com", "example.com", true},
		{"example.com.evil.net", "example.com", false},
		{"notexample.com", "example.com", false},
		{"foo.co.uk", "foo.co.uk", true},
		{"bar.foo.co.uk", "foo.co.uk", true},
		{"bar.co.uk", "foo.co.uk", false},
		{"geo.test", "geo.test", true},
	}
	for _, test := range tests {
		if got := geoSameSite(test.host, test.site); got != test.want {
			t.Fatalf("geoSameSite(%q, %q) = %v, want %v", test.host, test.site, got, test.want)
		}
	}
	if domain := geoSiteDomain("deep.www.example.com"); domain != "example.com" {
		t.Fatalf("geoSiteDomain returned %q", domain)
	}
}

func TestGeoAuditRejectsPrivateResolvedAddresses(t *testing.T) {
	addresses := []string{
		"127.0.0.1", "10.1.2.3", "172.20.0.1", "192.168.0.10", "169.254.169.254",
		"100.64.1.1", "0.0.0.0", "::1", "fd12:3456::1", "fe80::1",
	}
	manifest, ok := Get("geo-site-audit-v1")
	if !ok {
		t.Fatal("geo-site-audit-v1 manifest is missing")
	}
	for _, address := range addresses {
		t.Run(address, func(t *testing.T) {
			dialed := false
			withGeoAuditHooks(t,
				func(ctx context.Context, host string) ([]net.IP, error) {
					return []net.IP{net.ParseIP(address)}, nil
				},
				func(ctx context.Context, network, target string) (net.Conn, error) {
					dialed = true
					return nil, errors.New("should not dial")
				},
				nil,
			)
			_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"url": "https://blocked.test/"}, "")
			if execErr == nil || execErr.Code != "site_not_allowed" {
				t.Fatalf("expected site_not_allowed, got %#v", execErr)
			}
			if dialed {
				t.Fatal("private address must be rejected before dialing")
			}
		})
	}
}

func TestGeoAuditRejectsRebindingAtDialTime(t *testing.T) {
	manifest, ok := Get("geo-site-audit-v1")
	if !ok {
		t.Fatal("geo-site-audit-v1 manifest is missing")
	}
	var dialed []string
	var resolveCount int
	withGeoAuditHooks(t,
		func(ctx context.Context, host string) ([]net.IP, error) {
			resolveCount++
			// First lookup (robots.txt) answers public; the next connection for
			// the same hostname is rebound to a private address.
			if resolveCount == 1 {
				return []net.IP{net.ParseIP("203.0.113.10")}, nil
			}
			return []net.IP{net.ParseIP("10.0.0.7")}, nil
		},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("dial refused")
		},
		nil,
	)
	_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"url": "https://rebind.test/"}, "")
	if execErr == nil || execErr.Code != "site_not_allowed" {
		t.Fatalf("expected site_not_allowed for a rebound lookup, got %#v", execErr)
	}
	if len(dialed) != 1 || dialed[0] != "203.0.113.10:443" {
		t.Fatalf("expected exactly one dial to the validated IP literal, got %#v", dialed)
	}
}

func TestGeoAuditRejectsInsecureRedirect(t *testing.T) {
	network := newGeoTestNetwork(t)
	network.serve(t, "downgrade.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://downgrade.test/plain", http.StatusFound)
	}))
	network.install(t)

	manifest, _ := Get("geo-site-audit-v1")
	_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"url": "https://downgrade.test/"}, "")
	if execErr == nil || execErr.Code != "site_not_allowed" {
		t.Fatalf("expected http redirect to be blocked, got %#v", execErr)
	}
}

func TestGeoAuditRejectsCrossSiteRedirect(t *testing.T) {
	network := newGeoTestNetwork(t)
	network.serve(t, "site.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://other.test/landing", http.StatusFound)
	}))
	network.serve(t, "other.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>other</body></html>"))
	}))
	network.install(t)

	manifest, _ := Get("geo-site-audit-v1")
	_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"url": "https://site.test/"}, "")
	if execErr == nil || execErr.Code != "site_not_allowed" {
		t.Fatalf("expected cross-site redirect to be blocked, got %#v", execErr)
	}
	for _, target := range network.dialed {
		if target == "203.0.113.2:443" {
			t.Fatal("cross-site redirect target must not be dialed")
		}
	}
}

func TestGeoAuditStopsAfterFiveRedirects(t *testing.T) {
	network := newGeoTestNetwork(t)
	var hops int
	network.serve(t, "loop.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" || r.URL.Path == "/sitemap.xml" || r.URL.Path == "/llms.txt" {
			http.NotFound(w, r)
			return
		}
		hops++
		http.Redirect(w, r, fmt.Sprintf("/hop-%d", hops), http.StatusFound)
	}))
	network.install(t)

	manifest, _ := Get("geo-site-audit-v1")
	_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"url": "https://loop.test/"}, "")
	if execErr == nil || execErr.Code != "site_not_allowed" {
		t.Fatalf("expected redirect loop to be blocked, got %#v", execErr)
	}
	if hops != geoAuditMaxRedirects+1 {
		t.Fatalf("expected %d requests (initial + 5 hops), got %d", geoAuditMaxRedirects+1, hops)
	}
}

func TestGeoAuditOversizedResponse(t *testing.T) {
	t.Run("extra page", func(t *testing.T) {
		network := newGeoTestNetwork(t)
		network.serve(t, "big.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" || r.URL.Path == "/sitemap.xml" || r.URL.Path == "/llms.txt" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Path == "/big" {
				chunk := strings.Repeat("x", 64*1024)
				for i := 0; i < 50; i++ {
					if _, err := w.Write([]byte(chunk)); err != nil {
						return
					}
				}
				return
			}
			_, _ = w.Write([]byte("<html lang=\"en\"><head><title>tiny</title></head><body><h1>tiny</h1></body></html>"))
		}))
		network.install(t)

		manifest, _ := Get("geo-site-audit-v1")
		result, execErr := ExecuteSync(context.Background(), manifest, map[string]any{
			"url":       "https://big.test/",
			"pages":     []any{"https://big.test/big"},
			"max_pages": 2,
		}, "")
		if execErr != nil {
			t.Fatalf("oversized extra page must not fail the whole audit: %v", execErr)
		}
		pages := result.Output["pages"].([]any)
		if len(pages) != 2 {
			t.Fatalf("expected homepage and failed page, got %#v", pages)
		}
		failed := pages[1].(map[string]any)
		if failed["error"] != "response_too_large" {
			t.Fatalf("expected response_too_large, got %#v", failed)
		}
	})

	t.Run("homepage", func(t *testing.T) {
		network := newGeoTestNetwork(t)
		network.serve(t, "huge.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" || r.URL.Path == "/sitemap.xml" || r.URL.Path == "/llms.txt" {
				http.NotFound(w, r)
				return
			}
			chunk := strings.Repeat("x", 64*1024)
			for i := 0; i < 50; i++ {
				if _, err := w.Write([]byte(chunk)); err != nil {
					return
				}
			}
		}))
		network.install(t)

		manifest, _ := Get("geo-site-audit-v1")
		_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"url": "https://huge.test/"}, "")
		if execErr == nil || execErr.Code != "site_response_too_large" {
			t.Fatalf("expected site_response_too_large, got %#v", execErr)
		}
	})
}

func TestGeoAuditTimeout(t *testing.T) {
	t.Run("homepage deadline", func(t *testing.T) {
		network := newGeoTestNetwork(t)
		network.serve(t, "slow.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
			}
			_, _ = w.Write([]byte("<html><body>late</body></html>"))
		}))
		network.install(t)

		manifest, _ := Get("geo-site-audit-v1")
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_, execErr := ExecuteSync(ctx, manifest, map[string]any{"url": "https://slow.test/"}, "")
		if execErr == nil || execErr.Code != "site_timeout" {
			t.Fatalf("expected site_timeout, got %#v", execErr)
		}
	})

	t.Run("extra page deadline is reported per page", func(t *testing.T) {
		network := newGeoTestNetwork(t)
		network.serve(t, "partial.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" || r.URL.Path == "/sitemap.xml" || r.URL.Path == "/llms.txt" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Path == "/slow" {
				select {
				case <-time.After(5 * time.Second):
				case <-r.Context().Done():
				}
				return
			}
			_, _ = w.Write([]byte("<html lang=\"en\"><head><title>fast</title></head><body><h1>fast</h1></body></html>"))
		}))
		network.install(t)

		manifest, _ := Get("geo-site-audit-v1")
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()
		result, execErr := ExecuteSync(ctx, manifest, map[string]any{
			"url":       "https://partial.test/",
			"pages":     []any{"https://partial.test/slow"},
			"max_pages": 2,
		}, "")
		if execErr != nil {
			t.Fatalf("slow extra page must not fail the audit: %v", execErr)
		}
		pages := result.Output["pages"].([]any)
		slow := pages[1].(map[string]any)
		if slow["error"] != "timeout" {
			t.Fatalf("expected timeout error code, got %#v", slow)
		}
	})
}

func TestGeoAuditRejectsNonPublicPortAndScheme(t *testing.T) {
	for _, raw := range []string{
		"http://geo.test/",
		"https://geo.test:8443/",
		"https://geo.test@evil.test/",
	} {
		if _, execErr := parseGeoAuditInput(map[string]any{"url": raw}); execErr == nil {
			t.Fatalf("%s should be rejected", raw)
		}
	}
}
