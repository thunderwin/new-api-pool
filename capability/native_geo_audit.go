package capability

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	geoAuditNativeName = "geo-site-audit"

	geoAuditDefaultUA = "Mozilla/5.0 (compatible; GEO-Audit/1.0)"
	geoAuditBrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	geoAuditDefaultMaxPages = 8
	geoAuditMaxPages        = 15
	geoAuditMaxExtraPages   = 10
	geoAuditMaxSitemaps     = 3
	geoAuditStaleDays       = 180
	geoAuditMinWords        = 150
	geoAuditMaxEvidence     = 300

	geoTrainingPolicyAllow = "allow"
	geoTrainingPolicyBlock = "block"
	geoTrainingPolicyAny   = "any"
)

// geoBot describes one published crawler identity used for robots evaluation
// or user-agent reachability probing.
type geoBot struct {
	Token  string
	Engine string
	UA     string
	// MayIgnoreRobots marks user-initiated fetchers whose vendor documents that
	// robots.txt may not apply to them.
	MayIgnoreRobots bool
}

// geoSearchRobots are the AI search crawlers that official vendor docs say
// should be allowed for a site to surface in AI search answers.
var geoSearchRobots = []geoBot{
	{Token: "OAI-SearchBot", Engine: "ChatGPT", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; OAI-SearchBot/1.0; +https://openai.com/searchbot) Chrome/131.0.0.0 Safari/537.36"},
	{Token: "PerplexityBot", Engine: "Perplexity", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot) Chrome/119.0.0.0 Safari/537.36"},
	{Token: "Claude-SearchBot", Engine: "Claude", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-SearchBot/1.0; +claudebot@anthropic.com) Chrome/119.0.0.0 Safari/537.36"},
	{Token: "Googlebot", Engine: "Google", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Googlebot/2.1; +http://www.google.com/bot.html) Chrome/131.0.0.0 Safari/537.36"},
	{Token: "Bingbot", Engine: "Bing/Copilot", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm) Chrome/131.0.0.0 Safari/537.36"},
	{Token: "Applebot", Engine: "Apple", UA: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15 (compatible; Applebot/0.1; +http://www.apple.com/go/applebot)"},
}

// geoUserFetchRobots open a page only when a user asks the assistant to. They
// do not decide search visibility, and OpenAI and Perplexity say robots.txt
// may not apply to them; Anthropic says its bots honour robots.txt.
var geoUserFetchRobots = []geoBot{
	{Token: "ChatGPT-User", Engine: "ChatGPT", MayIgnoreRobots: true, UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ChatGPT-User/1.0; +https://openai.com/bot) Chrome/131.0.0.0 Safari/537.36"},
	{Token: "Perplexity-User", Engine: "Perplexity", MayIgnoreRobots: true, UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Perplexity-User/1.0; +https://perplexity.ai/perplexity-user) Chrome/119.0.0.0 Safari/537.36"},
	{Token: "Claude-User", Engine: "Claude", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-User/1.0; +Claude-User@anthropic.com) Chrome/131.0.0.0 Safari/537.36"},
}

// geoTrainingRobots are crawlers whose robots rules express a training-data
// policy rather than a search-visibility policy.
var geoTrainingRobots = []geoBot{
	{Token: "GPTBot"},
	{Token: "ClaudeBot"},
	{Token: "Google-Extended"},
	{Token: "Applebot-Extended"},
	{Token: "CCBot"},
	{Token: "meta-externalagent"},
}

// geoProbeBots are the user agents fetched once against the homepage to detect
// UA-level blocking or bot challenges. Training crawlers are not probed: a
// firewall rule against them is a training choice, not a visibility problem.
var geoProbeBots = []geoBot{
	geoSearchRobots[0],    // OAI-SearchBot
	geoUserFetchRobots[0], // ChatGPT-User
	geoSearchRobots[1],    // PerplexityBot
	geoSearchRobots[2],    // Claude-SearchBot
	geoSearchRobots[3],    // Googlebot
	geoSearchRobots[4],    // Bingbot
}

func init() {
	if err := RegisterNativeSync(geoAuditNativeName, NativeSyncSpec{
		Validate: validateGEOSiteAuditInput,
		Run:      runGEOSiteAudit,
	}); err != nil {
		common.SysLog("capability: failed to register native handler " + geoAuditNativeName + ": " + err.Error())
	}
}

type geoAuditInput struct {
	URL            string
	Pages          []string
	MaxPages       int
	TrainingPolicy string
}

// validateGEOSiteAuditInput runs before billing, so malformed URLs never start
// an outbound crawl.
func validateGEOSiteAuditInput(m *Manifest, input map[string]any) *ExecutionError {
	_, execErr := parseGeoAuditInput(input)
	return execErr
}

func parseGeoAuditInput(input map[string]any) (geoAuditInput, *ExecutionError) {
	invalid := func(message string) (geoAuditInput, *ExecutionError) {
		return geoAuditInput{}, &ExecutionError{
			Code:       "invalid_request",
			Message:    message,
			HTTPStatus: http.StatusBadRequest,
		}
	}
	rawURL, ok := input["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return invalid("input.url must be a public https URL")
	}
	parsed, err := parseGeoAuditURL(rawURL)
	if err != nil {
		return invalid("input.url must be a public https URL on port 443 without credentials")
	}
	site := geoSiteDomain(parsed.Hostname())
	if site == "" {
		return invalid("input.url must be a public https URL")
	}

	maxPages := geoAuditDefaultMaxPages
	if value, exists := input["max_pages"]; exists && value != nil {
		number, ok := NumberValue(value)
		if !ok || number != float64(int(number)) || number < 1 || number > geoAuditMaxPages {
			return invalid("input.max_pages must be an integer between 1 and 15")
		}
		maxPages = int(number)
	}

	policy := geoTrainingPolicyAllow
	if value, exists := input["training_policy"]; exists && value != nil {
		text, ok := value.(string)
		if !ok {
			return invalid("input.training_policy must be allow, block or any")
		}
		policy = strings.ToLower(strings.TrimSpace(text))
		switch policy {
		case geoTrainingPolicyAllow, geoTrainingPolicyBlock, geoTrainingPolicyAny:
		default:
			return invalid("input.training_policy must be allow, block or any")
		}
	}

	extraPages, execErr := parseGeoAuditPages(input["pages"], site)
	if execErr != nil {
		return geoAuditInput{}, execErr
	}

	return geoAuditInput{
		URL:            geoNormalizeAuditURL(parsed),
		Pages:          extraPages,
		MaxPages:       maxPages,
		TrainingPolicy: policy,
	}, nil
}

func parseGeoAuditPages(value any, site string) ([]string, *ExecutionError) {
	invalid := func(message string) ([]string, *ExecutionError) {
		return nil, &ExecutionError{
			Code:       "invalid_request",
			Message:    message,
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if value == nil {
		return nil, nil
	}
	var raw []string
	switch typed := value.(type) {
	case []string:
		raw = typed
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return invalid("input.pages must be an array of same-site https URLs")
			}
			raw = append(raw, text)
		}
	default:
		return invalid("input.pages must be an array of same-site https URLs")
	}
	if len(raw) > geoAuditMaxExtraPages {
		return invalid("input.pages accepts at most 10 URLs")
	}
	seen := map[string]struct{}{}
	pages := make([]string, 0, len(raw))
	for _, item := range raw {
		parsed, err := parseGeoAuditURL(item)
		if err != nil {
			return invalid("input.pages must contain public https URLs on port 443 without credentials")
		}
		host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
		if !geoSameSite(host, site) {
			return invalid("input.pages must stay on the same registrable domain as input.url")
		}
		normalized := geoNormalizeAuditURL(parsed)
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		pages = append(pages, normalized)
	}
	return pages, nil
}

// parseGeoAuditURL accepts public https URLs on the default port only. Address
// reachability is enforced later, at dial time.
func parseGeoAuditURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("invalid url")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("only https is allowed")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("userinfo is not allowed")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if _, isIP := geoParseIPLiteral(host); isIP {
		return nil, fmt.Errorf("ip literals are not allowed")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return nil, fmt.Errorf("only port 443 is allowed")
	}
	return parsed, nil
}

func geoNormalizeAuditURL(parsed *url.URL) string {
	normalized := *parsed
	normalized.Fragment = ""
	normalized.Host = strings.TrimSuffix(strings.ToLower(normalized.Hostname()), ".")
	if normalized.Path == "" {
		normalized.Path = "/"
	}
	return normalized.String()
}

type geoPage struct {
	URL         string
	FinalURL    string
	Status      int
	Error       string
	Words       int
	Title       string
	H1Count     int
	SchemaTypes []string
	HTML        *geoHTMLDoc
	LD          *geoJSONLDData
}

type geoUABotResult struct {
	Token     string
	Status    int
	Challenge bool
	Error     string
}

type geoUAProbeResult struct {
	BrowserStatus    int
	BrowserChallenge bool
	BrowserError     string
	Bots             []geoUABotResult
}

type geoSitemapData struct {
	Found     bool
	Source    string
	Documents int
	Children  int
	Entries   []geoSitemapEntry
	Errors    []string
}

type geoAudit struct {
	input   geoAuditInput
	site    string
	origin  *url.URL
	started time.Time
	fetch   *geoFetcher

	robots   *geoRobotsFile
	home     *geoPage
	pages    []*geoPage
	sitemap  *geoSitemapData
	llmsTxt  bool
	llmsSize int
	probe    *geoUAProbeResult

	checks []geoCheckResult
	notes  []string
}

func runGEOSiteAudit(ctx context.Context, m *Manifest, input map[string]any) (*SyncResult, *ExecutionError) {
	parsed, execErr := parseGeoAuditInput(input)
	if execErr != nil {
		return nil, execErr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	origin, err := url.Parse(parsed.URL)
	if err != nil {
		return nil, &ExecutionError{Code: "invalid_request", Message: "input.url must be a public https URL", HTTPStatus: http.StatusBadRequest}
	}
	site := geoSiteDomain(origin.Hostname())
	audit := &geoAudit{
		input:   parsed,
		site:    site,
		origin:  origin,
		started: time.Now().UTC(),
		fetch:   newGeoFetcher(site),
	}
	defer audit.fetch.close()

	output, execErr := audit.execute(ctx)
	if execErr != nil {
		return nil, execErr
	}
	return &SyncResult{Output: output, Raw: output, HTTPStatus: http.StatusOK}, nil
}

func (a *geoAudit) execute(ctx context.Context) (map[string]any, *ExecutionError) {
	a.notes = append(a.notes,
		"pages are fetched with plain HTTPS GET and no JavaScript execution; script-only content is not evaluated",
		"AI user-agent reachability only measures the User-Agent layer; IP or reverse-DNS verified-bot rules are not observable, and a firewall may reject a spoofed crawler user agent that the real crawler passes",
	)
	a.loadRobots(ctx)

	homeResponse, err := a.fetch.get(ctx, a.input.URL, geoAuditDefaultUA)
	if err != nil {
		return nil, a.fatalFetchError(err)
	}
	a.home = a.newPage(a.input.URL, homeResponse)

	a.loadLlmsTxt(ctx)
	a.probeUserAgents(ctx)
	a.loadSitemap(ctx)
	a.collectExtraPages(ctx)

	a.buildChecks()
	return a.buildOutput(), nil
}

func (a *geoAudit) fatalFetchError(err error) *ExecutionError {
	switch geoAuditErrorCode(err) {
	case "blocked":
		return &ExecutionError{
			Code:       "site_not_allowed",
			Message:    "the requested site is not reachable over public https",
			HTTPStatus: http.StatusBadRequest,
		}
	case "timeout":
		return &ExecutionError{
			Code:       "site_timeout",
			Message:    "the site did not respond before the audit deadline",
			HTTPStatus: http.StatusGatewayTimeout,
		}
	case "response_too_large":
		return &ExecutionError{
			Code:       "site_response_too_large",
			Message:    "the homepage exceeded the per-page size limit",
			HTTPStatus: http.StatusBadGateway,
		}
	default:
		return &ExecutionError{
			Code:       "site_unreachable",
			Message:    "the site could not be fetched",
			HTTPStatus: http.StatusBadGateway,
		}
	}
}

func (a *geoAudit) loadRobots(ctx context.Context) {
	a.robots = &geoRobotsFile{}
	robotsURL := a.origin.Scheme + "://" + a.origin.Host + "/robots.txt"
	response, err := a.fetch.get(ctx, robotsURL, geoAuditDefaultUA)
	if err != nil {
		a.robots.Err = geoAuditErrorCode(err)
		return
	}
	a.robots.Status = response.Status
	if response.Status >= 200 && response.Status < 300 {
		parsed := parseGeoRobots(string(response.Body))
		parsed.Status = response.Status
		a.robots = parsed
		a.robots.Found = true
		return
	}
	a.robots.Found = false
	if response.Status >= 500 {
		a.robots.Err = "http_" + strconv.Itoa(response.Status)
	}
}

func (a *geoAudit) newPage(requested string, response *geoResponse) *geoPage {
	page := &geoPage{URL: requested, FinalURL: response.FinalURL, Status: response.Status}
	if response.Status < 200 || response.Status >= 300 {
		return page
	}
	page.HTML = parseGeoHTML(response.Body, response.FinalURL, a.site, response.Header.Values("X-Robots-Tag"))
	page.Words = page.HTML.Words
	page.Title = page.HTML.Title
	page.H1Count = page.HTML.H1Count
	page.LD = analyzeGeoJSONLD(page.HTML.JSONLD)
	page.SchemaTypes = page.LD.Types
	return page
}

func (a *geoAudit) loadLlmsTxt(ctx context.Context) {
	llmsURL := a.origin.Scheme + "://" + a.origin.Host + "/llms.txt"
	response, err := a.fetch.get(ctx, llmsURL, geoAuditDefaultUA)
	if err != nil || response.Status < 200 || response.Status >= 300 {
		return
	}
	if len(response.Body) == 0 {
		return
	}
	a.llmsTxt = true
	a.llmsSize = len(response.Body)
}

func (a *geoAudit) probeUserAgents(ctx context.Context) {
	probe := &geoUAProbeResult{Bots: make([]geoUABotResult, len(geoProbeBots))}
	browserStatus, browserChallenge, browserErr := a.probeOne(ctx, geoAuditBrowserUA)
	probe.BrowserStatus = browserStatus
	probe.BrowserChallenge = browserChallenge
	probe.BrowserError = browserErr

	var wg sync.WaitGroup
	for index, bot := range geoProbeBots {
		wg.Add(1)
		go func(index int, bot geoBot) {
			defer wg.Done()
			status, challenge, errCode := a.probeOne(ctx, bot.UA)
			probe.Bots[index] = geoUABotResult{Token: bot.Token, Status: status, Challenge: challenge, Error: errCode}
		}(index, bot)
	}
	wg.Wait()
	a.probe = probe
}

func (a *geoAudit) probeOne(ctx context.Context, userAgent string) (int, bool, string) {
	if ctx.Err() != nil {
		return 0, false, "timeout"
	}
	response, err := a.fetch.getWithLimit(ctx, a.input.URL, userAgent, 64*1024, false)
	if err != nil {
		return 0, false, geoAuditErrorCode(err)
	}
	return response.Status, geoLooksLikeChallenge(response.Status, response.Body), ""
}

var geoChallengeMarkers = []string{
	"cf-chl", "challenge-platform", "attention required", "just a moment",
	"enable javascript and cookies to continue", "captcha",
}

func geoLooksLikeChallenge(status int, body []byte) bool {
	if status == http.StatusForbidden || status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return true
	}
	if status < 200 || status >= 400 {
		return false
	}
	sample := strings.ToLower(string(body))
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	for _, marker := range geoChallengeMarkers {
		if strings.Contains(sample, marker) {
			return true
		}
	}
	return false
}

func (a *geoAudit) loadSitemap(ctx context.Context) {
	a.sitemap = &geoSitemapData{}
	candidates := make([]string, 0, len(a.robots.Sitemaps)+1)
	for _, declared := range a.robots.Sitemaps {
		parsed, err := url.Parse(strings.TrimSpace(declared))
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
			continue
		}
		if !geoSameSite(parsed.Hostname(), a.site) {
			a.notes = append(a.notes, "robots.txt declares a sitemap on another site; skipped")
			continue
		}
		candidates = append(candidates, geoNormalizeAuditURL(parsed))
	}
	if len(candidates) == 0 {
		candidates = append(candidates, a.origin.Scheme+"://"+a.origin.Host+"/sitemap.xml")
	}

	var entries []geoSitemapEntry
	var children []string
	var source string
	lastError := ""
	for index, candidate := range candidates {
		if index >= geoAuditMaxSitemaps {
			break
		}
		documentEntries, documentChildren, documentSource, errCode := a.fetchSitemapDocument(ctx, candidate)
		if errCode != "" {
			lastError = errCode
			continue
		}
		entries, children, source = documentEntries, documentChildren, documentSource
		break
	}
	if source == "" {
		if lastError == "" {
			lastError = "not_found"
		}
		a.sitemap.Errors = append(a.sitemap.Errors, lastError)
		return
	}
	a.sitemap.Found = true
	a.sitemap.Source = source
	a.sitemap.Documents = 1
	a.sitemap.Entries = append(a.sitemap.Entries, entries...)
	if len(children) == 0 {
		return
	}
	expanded := 0
	for _, child := range children {
		if expanded >= geoAuditMaxSitemaps {
			break
		}
		parsed, err := url.Parse(child)
		if err != nil || !geoSameSite(parsed.Hostname(), a.site) {
			continue
		}
		childEntries, _, _, childErr := a.fetchSitemapDocument(ctx, geoNormalizeAuditURL(parsed))
		if childErr != "" {
			a.sitemap.Errors = append(a.sitemap.Errors, childErr)
			continue
		}
		expanded++
		a.sitemap.Children++
		a.sitemap.Documents++
		a.sitemap.Entries = append(a.sitemap.Entries, childEntries...)
	}
}

func (a *geoAudit) fetchSitemapDocument(ctx context.Context, sitemapURL string) ([]geoSitemapEntry, []string, string, string) {
	response, err := a.fetch.get(ctx, sitemapURL, geoAuditDefaultUA)
	if err != nil {
		return nil, nil, sitemapURL, geoAuditErrorCode(err)
	}
	if response.Status < 200 || response.Status >= 300 {
		return nil, nil, sitemapURL, "http_" + strconv.Itoa(response.Status)
	}
	entries, children, parseErr := parseGeoSitemap(response.Body)
	if parseErr != nil {
		return nil, nil, sitemapURL, "invalid_xml"
	}
	return entries, children, sitemapURL, ""
}

func (a *geoAudit) collectExtraPages(ctx context.Context) {
	remaining := a.input.MaxPages - 1
	if remaining <= 0 {
		return
	}
	selected := make([]string, 0, remaining)
	seen := map[string]struct{}{a.input.URL: {}}
	add := func(candidate string) {
		if len(selected) >= remaining {
			return
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Hostname() == "" {
			return
		}
		normalized := geoNormalizeAuditURL(parsed)
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		selected = append(selected, normalized)
	}
	for _, page := range a.input.Pages {
		add(page)
	}
	homePath := a.inputPath()
	skippedCrossSite := 0
	for _, entry := range a.sitemap.Entries {
		host, _ := geoSplitURLPath(entry.Loc)
		if host != "" && !geoSameSite(host, a.site) {
			skippedCrossSite++
		}
	}
	if skippedCrossSite > 0 {
		a.notes = append(a.notes, fmt.Sprintf("%d cross-site sitemap URL(s) skipped", skippedCrossSite))
	}
	for _, candidate := range selectGeoSitemapPages(a.sitemap.Entries, a.site, homePath, remaining-len(selected)) {
		add(candidate)
	}

	if len(selected) == 0 {
		return
	}
	a.pages = make([]*geoPage, len(selected))
	sem := make(chan struct{}, geoAuditConcurrency)
	var wg sync.WaitGroup
	for index, pageURL := range selected {
		wg.Add(1)
		go func(index int, pageURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				a.pages[index] = &geoPage{URL: pageURL, Error: "timeout"}
				return
			}
			response, err := a.fetch.get(ctx, pageURL, geoAuditDefaultUA)
			if err != nil {
				a.pages[index] = &geoPage{URL: pageURL, Error: geoAuditErrorCode(err)}
				return
			}
			a.pages[index] = a.newPage(pageURL, response)
		}(index, pageURL)
	}
	wg.Wait()
}

func (a *geoAudit) inputPath() string {
	path := a.origin.Path
	if path == "" {
		return "/"
	}
	return path
}

func (a *geoAudit) auditedPages() []*geoPage {
	pages := make([]*geoPage, 0, len(a.pages)+1)
	if a.home != nil {
		pages = append(pages, a.home)
	}
	pages = append(pages, a.pages...)
	return pages
}

func (a *geoAudit) auditedPaths() []string {
	paths := make([]string, 0, len(a.pages)+1)
	seen := map[string]struct{}{}
	appendPath := func(raw string) {
		parsed, err := url.Parse(raw)
		if err != nil {
			return
		}
		path := parsed.Path
		if path == "" {
			path = "/"
		}
		if _, exists := seen[path]; exists {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	if a.home != nil {
		appendPath(a.home.URL)
	} else {
		appendPath(a.input.URL)
	}
	for _, page := range a.pages {
		appendPath(page.URL)
	}
	return paths
}

func (a *geoAudit) buildOutput() map[string]any {
	pages := make([]any, 0, len(a.pages)+1)
	if a.home != nil {
		pages = append(pages, a.home.payload())
	}
	for _, page := range a.pages {
		pages = append(pages, page.payload())
	}
	checks := make([]any, 0, len(a.checks))
	for _, check := range a.checks {
		checks = append(checks, check.payload())
	}
	contentSignals := []string{}
	sitemaps := []string{}
	foundRobots := false
	if a.robots != nil {
		foundRobots = a.robots.Found
		contentSignals = append(contentSignals, a.robots.ContentSignals...)
		sitemaps = append(sitemaps, a.robots.Sitemaps...)
	}
	notes := a.notes
	if len(notes) == 0 {
		notes = []string{}
	}
	return map[string]any{
		"site":            a.site,
		"fetched_at":      time.Now().UTC().Format(time.RFC3339),
		"training_policy": a.input.TrainingPolicy,
		"summary":         a.summary(),
		"robots": map[string]any{
			"found":           foundRobots,
			"content_signals": contentSignals,
			"sitemaps":        sitemaps,
		},
		"pages":  pages,
		"checks": checks,
		"notes":  notes,
	}
}

func (p *geoPage) payload() map[string]any {
	payload := map[string]any{
		"url":          p.URL,
		"status":       p.Status,
		"words":        p.Words,
		"title":        p.Title,
		"h1_count":     p.H1Count,
		"schema_types": append([]string{}, p.SchemaTypes...),
	}
	if p.Error != "" {
		payload["error"] = p.Error
	}
	return payload
}

func (a *geoAudit) summary() map[string]any {
	counts := map[string]map[string]int{
		geoSourceOfficial: {"pass": 0, "warn": 0, "fail": 0, "info": 0},
		geoSourceResearch: {"pass": 0, "warn": 0, "fail": 0, "info": 0},
		geoSourceIndustry: {"pass": 0, "warn": 0, "fail": 0, "info": 0},
	}
	for _, check := range a.checks {
		bucket, ok := counts[check.Source]
		if !ok {
			continue
		}
		bucket[check.Status]++
	}
	output := make(map[string]any, len(counts))
	for source, bucket := range counts {
		output[source] = bucket
	}
	return output
}
