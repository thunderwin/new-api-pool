package capability

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func newGeoTestAudit(t *testing.T, html string, xRobots []string) (*geoAudit, *geoPage) {
	t.Helper()
	page := &geoPage{URL: "https://geo.test/page", FinalURL: "https://geo.test/page", Status: http.StatusOK}
	page.HTML = parseGeoHTML([]byte(html), page.FinalURL, "geo.test", xRobots)
	page.Words = page.HTML.Words
	page.Title = page.HTML.Title
	page.H1Count = page.HTML.H1Count
	page.LD = analyzeGeoJSONLD(page.HTML.JSONLD)
	page.SchemaTypes = page.LD.Types
	audit := &geoAudit{
		input:  geoAuditInput{URL: page.URL, MaxPages: geoAuditDefaultMaxPages, TrainingPolicy: geoTrainingPolicyAllow},
		site:   "geo.test",
		home:   page,
		robots: &geoRobotsFile{},
	}
	return audit, page
}

func findGeoPageCheck(t *testing.T, id string) geoPageCheck {
	t.Helper()
	for _, check := range geoAuditPageChecks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("page check %s is not registered", id)
	return geoPageCheck{}
}

func findGeoSiteCheck(t *testing.T, id string) geoSiteCheck {
	t.Helper()
	for _, check := range geoAuditSiteChecks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("site check %s is not registered", id)
	return geoSiteCheck{}
}

func TestGeoRobotsParsing(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		agent   string
		path    string
		allowed bool
		rule    string
	}{
		{
			name:    "star group disallows private",
			body:    "User-agent: *\nDisallow: /\n",
			agent:   "OAI-SearchBot",
			path:    "/",
			allowed: false,
			rule:    "/",
		},
		{
			name:    "specific group overrides star",
			body:    "User-agent: *\nDisallow: /\n\nUser-agent: OAI-SearchBot\nAllow: /\n",
			agent:   "OAI-SearchBot",
			path:    "/",
			allowed: true,
			rule:    "/",
		},
		{
			name:    "longest match wins and allow wins ties",
			body:    "User-agent: *\nDisallow: /blog/\nAllow: /blog/public/\nAllow: /blog\n",
			agent:   "PerplexityBot",
			path:    "/blog/public/post",
			allowed: true,
			rule:    "/blog/public/",
		},
		{
			name:    "longest disallow beats shorter allow",
			body:    "User-agent: *\nAllow: /blog\nDisallow: /blog/private\n",
			agent:   "PerplexityBot",
			path:    "/blog/private/x",
			allowed: false,
			rule:    "/blog/private",
		},
		{
			name:    "wildcard and end anchor",
			body:    "User-agent: *\nDisallow: /*.pdf$\n",
			agent:   "Googlebot",
			path:    "/files/report.pdf",
			allowed: false,
			rule:    "/*.pdf$",
		},
		{
			name:    "no matching group allows everything",
			body:    "User-agent: GPTBot\nDisallow: /\n",
			agent:   "Bingbot",
			path:    "/",
			allowed: true,
			rule:    "",
		},
		{
			name:    "empty disallow does not block",
			body:    "User-agent: *\nDisallow:\n",
			agent:   "Applebot",
			path:    "/",
			allowed: true,
			rule:    "",
		},
		{
			name:    "multi user-agent group is merged",
			body:    "User-agent: GPTBot\nUser-agent: ClaudeBot\nDisallow: /\n",
			agent:   "ClaudeBot",
			path:    "/",
			allowed: false,
			rule:    "/",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := parseGeoRobots(test.body)
			allowed, rule := parsed.geoRobotsAllows(test.agent, test.path)
			if allowed != test.allowed || rule != test.rule {
				t.Fatalf("got allowed=%v rule=%q, want allowed=%v rule=%q", allowed, rule, test.allowed, test.rule)
			}
		})
	}
}

func TestGeoRobotsExtensions(t *testing.T) {
	body := "User-agent: *\nDisallow:\nContent-Signal: search=yes, ai-train=no\nSitemap: https://geo.test/sitemap.xml\nSitemap: https://geo.test/sitemap-index.xml\n\nUser-agent: anthropic-ai\nDisallow: /\n\nUser-agent: Claude-Web\nDisallow: /\n"
	parsed := parseGeoRobots(body)
	if len(parsed.Sitemaps) != 2 || parsed.Sitemaps[0] != "https://geo.test/sitemap.xml" {
		t.Fatalf("unexpected sitemaps: %#v", parsed.Sitemaps)
	}
	if len(parsed.ContentSignals) != 1 || parsed.ContentSignals[0] != "search=yes, ai-train=no" {
		t.Fatalf("unexpected content signals: %#v", parsed.ContentSignals)
	}
	deprecated := parsed.geoDeprecatedTokens()
	if len(deprecated) != 2 || deprecated[0] != "anthropic-ai" || deprecated[1] != "claude-web" {
		t.Fatalf("unexpected deprecated tokens: %#v", deprecated)
	}
}

func TestGeoTrainingPolicyCheck(t *testing.T) {
	robots := parseGeoRobots("User-agent: *\nDisallow:\n\nUser-agent: GPTBot\nDisallow: /\n\nUser-agent: CCBot\nDisallow: /\n")
	for _, test := range []struct {
		policy string
		status string
	}{
		{geoTrainingPolicyAllow, geoStatusWarn},
		{geoTrainingPolicyBlock, geoStatusWarn},
		{geoTrainingPolicyAny, geoStatusInfo},
	} {
		audit := &geoAudit{input: geoAuditInput{TrainingPolicy: test.policy}, robots: robots}
		status, evidence := checkRobotsTrainingPolicy(audit)
		if status != test.status {
			t.Fatalf("policy %s: got %s (%s), want %s", test.policy, status, evidence, test.status)
		}
	}

	allBlocked := parseGeoRobots("User-agent: GPTBot\nDisallow: /\nUser-agent: ClaudeBot\nDisallow: /\nUser-agent: Google-Extended\nDisallow: /\nUser-agent: Applebot-Extended\nDisallow: /\nUser-agent: CCBot\nDisallow: /\nUser-agent: meta-externalagent\nDisallow: /\n")
	audit := &geoAudit{input: geoAuditInput{TrainingPolicy: geoTrainingPolicyBlock}, robots: allBlocked}
	if status, evidence := checkRobotsTrainingPolicy(audit); status != geoStatusPass {
		t.Fatalf("block policy with all crawlers blocked should pass: %s (%s)", status, evidence)
	}

	allAllowed := parseGeoRobots("User-agent: *\nDisallow:\n")
	audit = &geoAudit{input: geoAuditInput{TrainingPolicy: geoTrainingPolicyAllow}, robots: allAllowed}
	if status, evidence := checkRobotsTrainingPolicy(audit); status != geoStatusPass {
		t.Fatalf("allow policy with no blocks should pass: %s (%s)", status, evidence)
	}
}

func TestGeoRobotsAISearchAccess(t *testing.T) {
	audit := &geoAudit{
		input:  geoAuditInput{URL: "https://geo.test/"},
		home:   &geoPage{URL: "https://geo.test/"},
		robots: parseGeoRobots("User-agent: *\nDisallow:\n\nUser-agent: PerplexityBot\nDisallow: /\n"),
	}
	status, evidence := checkRobotsAISearchAccess(audit)
	if status != geoStatusFail || !strings.Contains(evidence, "PerplexityBot") {
		t.Fatalf("expected PerplexityBot failure, got %s (%s)", status, evidence)
	}

	audit.robots = parseGeoRobots("User-agent: *\nDisallow:\n")
	if status, evidence := checkRobotsAISearchAccess(audit); status != geoStatusPass {
		t.Fatalf("expected pass, got %s (%s)", status, evidence)
	}

	audit.robots = &geoRobotsFile{Err: "http_503"}
	if status, _ := checkRobotsAISearchAccess(audit); status != geoStatusFail {
		t.Fatalf("robots 5xx must fail, got %s", status)
	}

	audit.robots = &geoRobotsFile{}
	if status, _ := checkRobotsAISearchAccess(audit); status != geoStatusPass {
		t.Fatalf("missing robots.txt must pass, got %s", status)
	}

	audit.robots = &geoRobotsFile{Err: "timeout"}
	if status, evidence := checkRobotsAISearchAccess(audit); status != geoStatusWarn || !strings.Contains(evidence, "could not be fetched") {
		t.Fatalf("unreachable robots.txt must warn, got %s (%s)", status, evidence)
	}
	if status, _ := checkRobotsDeprecatedTokens(audit); status != geoStatusInfo {
		t.Fatalf("unreachable robots.txt must skip the deprecated token scan, got %s", status)
	}
}

func TestGeoChallengeDetection(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"403 is a block", 403, "", true},
		{"429 is a block", 429, "", true},
		{"503 is a block", 503, "", true},
		{"cloudflare challenge body", 200, "<html><script src=\"/cdn-cgi/challenge-platform/x\"></script></html>", true},
		{"cf-chl marker", 200, "<div id=\"cf-chl-widget\"></div>", true},
		{"attention required", 200, "<title>Attention Required! | Cloudflare</title>", true},
		{"normal page", 200, "<html><body>hello</body></html>", false},
		{"404 is not a challenge", 404, "not found", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := geoLooksLikeChallenge(test.status, []byte(test.body)); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestGeoPageIndexabilityAndSnippetControls(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		xRobots []string
		check   string
		status  string
		want    string
	}{
		{
			name:   "meta noindex fails",
			html:   `<html lang="en"><head><meta name="robots" content="noindex, follow"></head><body><h1>t</h1></body></html>`,
			check:  "page_indexability",
			status: geoStatusFail,
			want:   "noindex",
		},
		{
			name:    "googlebot x-robots noindex fails",
			html:    `<html lang="en"><head><title>t</title></head><body><h1>t</h1></body></html>`,
			xRobots: []string{"googlebot: noindex"},
			check:   "page_indexability",
			status:  geoStatusFail,
			want:    "googlebot",
		},
		{
			name:   "indexable page passes",
			html:   `<html lang="en"><head><meta name="robots" content="index, follow"></head><body><h1>t</h1></body></html>`,
			check:  "page_indexability",
			status: geoStatusPass,
		},
		{
			name:   "nosnippet warns",
			html:   `<html lang="en"><head><meta name="robots" content="nosnippet"></head><body><h1>t</h1></body></html>`,
			check:  "page_snippet_controls",
			status: geoStatusWarn,
			want:   "nosnippet",
		},
		{
			name:   "max-snippet zero warns",
			html:   `<html lang="en"><head><meta name="robots" content="max-snippet:0"></head><body><h1>t</h1></body></html>`,
			check:  "page_snippet_controls",
			status: geoStatusWarn,
			want:   "max-snippet=0",
		},
		{
			name:   "max-snippet twenty warns",
			html:   `<html lang="en"><head><meta name="robots" content="max-snippet:20"></head><body><h1>t</h1></body></html>`,
			check:  "page_snippet_controls",
			status: geoStatusWarn,
		},
		{
			name:   "max-snippet hundred passes",
			html:   `<html lang="en"><head><meta name="robots" content="max-snippet:100"></head><body><h1>t</h1></body></html>`,
			check:  "page_snippet_controls",
			status: geoStatusPass,
		},
		{
			name:   "max-snippet minus one means no limit",
			html:   `<html lang="en"><head><meta name="robots" content="max-snippet:-1"></head><body><h1>t</h1></body></html>`,
			check:  "page_snippet_controls",
			status: geoStatusPass,
		},
		{
			name:   "data-nosnippet is informational",
			html:   `<html lang="en"><head><title>t</title></head><body><h1>t</h1><p data-nosnippet>secret</p></body></html>`,
			check:  "page_snippet_controls",
			status: geoStatusInfo,
			want:   "1 data-nosnippet",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audit, page := newGeoTestAudit(t, test.html, test.xRobots)
			status, evidence := findGeoPageCheck(t, test.check).Evaluate(audit, page)
			if status != test.status {
				t.Fatalf("got %s (%s), want %s", status, evidence, test.status)
			}
			if test.want != "" && !strings.Contains(evidence, test.want) {
				t.Fatalf("evidence %q does not contain %q", evidence, test.want)
			}
		})
	}
}

func TestGeoCanonicalCheck(t *testing.T) {
	audit, page := newGeoTestAudit(t, `<html lang="en"><head><title>t</title></head><body><h1>t</h1></body></html>`, nil)
	if status, _ := findGeoPageCheck(t, "page_canonical").Evaluate(audit, page); status != geoStatusWarn {
		t.Fatalf("missing canonical must warn, got %s", status)
	}

	audit, page = newGeoTestAudit(t, `<html lang="en"><head><title>t</title><link rel="canonical" href="https://geo.test/other"></head><body><h1>t</h1></body></html>`, nil)
	status, evidence := findGeoPageCheck(t, "page_canonical").Evaluate(audit, page)
	if status != geoStatusInfo || !strings.Contains(evidence, "https://geo.test/other") {
		t.Fatalf("cross canonical should be info with URL, got %s (%s)", status, evidence)
	}

	audit, page = newGeoTestAudit(t, `<html lang="en"><head><title>t</title><link rel="canonical" href="/page"></head><body><h1>t</h1></body></html>`, nil)
	if status, evidence := findGeoPageCheck(t, "page_canonical").Evaluate(audit, page); status != geoStatusPass {
		t.Fatalf("self canonical should pass, got %s (%s)", status, evidence)
	}
}

func TestGeoServerRenderedTextCheck(t *testing.T) {
	spa := `<html lang="en"><head><title>app</title></head><body><div id="root"></div><script src="/app.js"></script></body></html>`
	audit, page := newGeoTestAudit(t, spa, nil)
	if status, evidence := findGeoPageCheck(t, "page_server_rendered_text").Evaluate(audit, page); status != geoStatusFail {
		t.Fatalf("SPA shell must fail, got %s (%s)", status, evidence)
	}

	short := `<html lang="en"><head><title>t</title></head><body><h1>t</h1><p>Only a handful of words here.</p></body></html>`
	audit, page = newGeoTestAudit(t, short, nil)
	if status, _ := findGeoPageCheck(t, "page_server_rendered_text").Evaluate(audit, page); status != geoStatusWarn {
		t.Fatalf("short text must warn, got %s", status)
	}

	long := `<html lang="en"><head><title>t</title></head><body><h1>t</h1><p>` + strings.Repeat("word ", 200) + `</p></body></html>`
	audit, page = newGeoTestAudit(t, long, nil)
	if status, _ := findGeoPageCheck(t, "page_server_rendered_text").Evaluate(audit, page); status != geoStatusPass {
		t.Fatalf("long text must pass, got %s", status)
	}
}

func TestGeoJSONLDChecks(t *testing.T) {
	visible := `<html lang="en"><head><title>t</title><script type="application/ld+json">{"@context":"https://schema.org","@type":"FAQPage","mainEntity":[{"@type":"Question","name":"What is GEO?","acceptedAnswer":{"@type":"Answer","text":"Generative engine optimization."}}]}</script></head><body><h1>GEO</h1><p>What is GEO? Generative engine optimization explained in detail for teams.</p></body></html>`
	audit, page := newGeoTestAudit(t, visible, nil)
	if status, evidence := findGeoPageCheck(t, "page_jsonld_faq").Evaluate(audit, page); status != geoStatusPass {
		t.Fatalf("visible FAQ question should pass, got %s (%s)", status, evidence)
	}

	hidden := strings.Replace(visible, "What is GEO? Generative engine optimization explained in detail for teams.", "Unrelated body copy.", 1)
	audit, page = newGeoTestAudit(t, hidden, nil)
	if status, evidence := findGeoPageCheck(t, "page_jsonld_faq").Evaluate(audit, page); status != geoStatusFail {
		t.Fatalf("invisible FAQ question must fail, got %s (%s)", status, evidence)
	}

	broken := `<html lang="en"><head><title>t</title><script type="application/ld+json">{not json</script></head><body><h1>t</h1></body></html>`
	audit, page = newGeoTestAudit(t, broken, nil)
	if status, evidence := findGeoPageCheck(t, "page_jsonld_parse").Evaluate(audit, page); status != geoStatusFail {
		t.Fatalf("invalid JSON-LD must fail, got %s (%s)", status, evidence)
	}

	graph := `<html lang="en"><head><title>t</title><script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"Organization","name":"Geo Co","url":"https://geo.test","logo":"https://geo.test/logo.png","sameAs":["https://x.test/geo"]},{"@type":"Product","name":"Geo Kit","offers":{"@type":"Offer","price":"10"}}]}</script></head><body><h1>t</h1><p>Geo Kit is available now.</p></body></html>`
	audit, page = newGeoTestAudit(t, graph, nil)
	if status, evidence := findGeoPageCheck(t, "page_jsonld_organization").Evaluate(audit, page); status != geoStatusPass {
		t.Fatalf("complete Organization should pass, got %s (%s)", status, evidence)
	}
	if status, evidence := findGeoPageCheck(t, "page_jsonld_product").Evaluate(audit, page); status != geoStatusPass {
		t.Fatalf("Product with offers and visible name should pass, got %s (%s)", status, evidence)
	}

	productHidden := strings.Replace(graph, "Geo Kit is available now.", "Nothing here.", 1)
	audit, page = newGeoTestAudit(t, productHidden, nil)
	if status, _ := findGeoPageCheck(t, "page_jsonld_product").Evaluate(audit, page); status != geoStatusFail {
		t.Fatalf("Product name missing from visible text must fail, got %s", status)
	}

	article := `<html lang="en"><head><title>t</title><script type="application/ld+json">{"@type":"BlogPosting","datePublished":"2026-01-01","dateModified":"2026-02-01","author":{"@type":"Person","name":"A"}}</script></head><body><h1>t</h1><p>Body.</p></body></html>`
	audit, page = newGeoTestAudit(t, article, nil)
	if status, evidence := findGeoPageCheck(t, "page_jsonld_article").Evaluate(audit, page); status != geoStatusPass {
		t.Fatalf("complete BlogPosting should pass, got %s (%s)", status, evidence)
	}

	missing := strings.Replace(article, `"dateModified":"2026-02-01",`, "", 1)
	audit, page = newGeoTestAudit(t, missing, nil)
	if status, evidence := findGeoPageCheck(t, "page_jsonld_article").Evaluate(audit, page); status != geoStatusWarn || !strings.Contains(evidence, "dateModified") {
		t.Fatalf("missing dateModified should warn, got %s (%s)", status, evidence)
	}
}

func TestGeoStructureChecks(t *testing.T) {
	audit, page := newGeoTestAudit(t, `<html><head></head><body><h1>one</h1><h1>two</h1><img src="a.png"><img src="b.png" alt="b"></body></html>`, nil)
	for _, test := range []struct{ check, status string }{
		{"page_title", geoStatusWarn},
		{"page_meta_description", geoStatusWarn},
		{"page_h1", geoStatusWarn},
		{"page_lang", geoStatusWarn},
		{"page_image_alt", geoStatusWarn},
	} {
		if got, evidence := findGeoPageCheck(t, test.check).Evaluate(audit, page); got != test.status {
			t.Fatalf("%s: got %s (%s), want %s", test.check, got, evidence, test.status)
		}
	}

	good := `<html lang="en"><head><title>Unique title</title><meta name="description" content="A description."></head><body><h1>Heading</h1><img src="a.png" alt="a"></body></html>`
	audit, page = newGeoTestAudit(t, good, nil)
	for _, test := range []struct{ check, status string }{
		{"page_title", geoStatusPass},
		{"page_meta_description", geoStatusPass},
		{"page_h1", geoStatusPass},
		{"page_lang", geoStatusPass},
		{"page_image_alt", geoStatusPass},
	} {
		if got, evidence := findGeoPageCheck(t, test.check).Evaluate(audit, page); got != test.status {
			t.Fatalf("%s: got %s (%s), want %s", test.check, got, evidence, test.status)
		}
	}

	// A second page with the same title must be reported as a duplicate.
	duplicate := &geoPage{URL: "https://geo.test/two", Title: "Unique title"}
	audit.pages = []*geoPage{duplicate}
	if got, evidence := findGeoPageCheck(t, "page_title").Evaluate(audit, page); got != geoStatusWarn || !strings.Contains(evidence, "shared by 2") {
		t.Fatalf("duplicate title: got %s (%s)", got, evidence)
	}
}

func TestGeoArticleDateCheck(t *testing.T) {
	html := `<html lang="en"><head><title>t</title></head><body><h1>t</h1><p>Body text.</p></body></html>`
	audit, page := newGeoTestAudit(t, html, nil)
	page.URL = "https://geo.test/blog/post"
	if status, _ := findGeoPageCheck(t, "page_article_date").Evaluate(audit, page); status != geoStatusWarn {
		t.Fatalf("blog page without a date must warn, got %s", status)
	}

	withTime := `<html lang="en"><head><title>t</title></head><body><h1>t</h1><time datetime="2026-03-04">March 4, 2026</time><p>Body.</p></body></html>`
	audit, page = newGeoTestAudit(t, withTime, nil)
	page.URL = "https://geo.test/news/post"
	if status, evidence := findGeoPageCheck(t, "page_article_date").Evaluate(audit, page); status != geoStatusPass || !strings.Contains(evidence, "2026-03-04") {
		t.Fatalf("time element should pass with date, got %s (%s)", status, evidence)
	}

	textDate := `<html lang="en"><head><title>t</title></head><body><h1>t</h1><p>Published 2026-05-06. Body.</p></body></html>`
	audit, page = newGeoTestAudit(t, textDate, nil)
	page.URL = "https://geo.test/posts/post"
	if status, evidence := findGeoPageCheck(t, "page_article_date").Evaluate(audit, page); status != geoStatusPass || !strings.Contains(evidence, "2026-05-06") {
		t.Fatalf("text date should pass, got %s (%s)", status, evidence)
	}

	audit, page = newGeoTestAudit(t, html, nil)
	audit.home = page
	if status, _ := findGeoPageCheck(t, "page_article_date").Evaluate(audit, page); status != geoStatusInfo {
		t.Fatalf("non-article page should be info, got %s", status)
	}
}

func TestGeoAnswerFirstAndMetrics(t *testing.T) {
	paragraph := strings.Repeat("answer ", 60)
	html := `<html lang="en"><head><title>t</title></head><body><h1>t</h1><p>` + paragraph + `</p><ul><li>a</li></ul><table><tr><td>1</td></tr></table><blockquote>quote</blockquote><p>Growth reached 42% in 2025 and 3 million users, according to the report.</p><a href="https://other.test/study">source</a></body></html>`
	audit, page := newGeoTestAudit(t, html, nil)
	if status, evidence := findGeoPageCheck(t, "answer_first_paragraph").Evaluate(audit, page); status != geoStatusPass {
		t.Fatalf("60-word answer paragraph should pass, got %s (%s)", status, evidence)
	}
	if status, evidence := findGeoPageCheck(t, "content_lists_tables").Evaluate(audit, page); status != geoStatusInfo || !strings.Contains(evidence, "1 list") {
		t.Fatalf("lists/tables: got %s (%s)", status, evidence)
	}
	if status, evidence := findGeoPageCheck(t, "stats_density").Evaluate(audit, page); status != geoStatusInfo || !strings.Contains(evidence, "statistics per 1000 words") {
		t.Fatalf("stats density: got %s (%s)", status, evidence)
	}
	if status, evidence := findGeoPageCheck(t, "quotes_in_content").Evaluate(audit, page); status != geoStatusInfo || !strings.Contains(evidence, "1 quote") {
		t.Fatalf("quotes: got %s (%s)", status, evidence)
	}
	if status, evidence := findGeoPageCheck(t, "external_citations").Evaluate(audit, page); status != geoStatusInfo || !strings.Contains(evidence, "1 link") {
		t.Fatalf("external citations: got %s (%s)", status, evidence)
	}

	stuffing := `<html lang="en"><head><title>t</title></head><body><h1>t</h1><p>` + strings.Repeat("seo ", 40) + strings.Repeat("other ", 60) + `</p></body></html>`
	audit, page = newGeoTestAudit(t, stuffing, nil)
	if status, evidence := findGeoPageCheck(t, "keyword_stuffing").Evaluate(audit, page); status != geoStatusWarn || !strings.Contains(evidence, "seo") {
		t.Fatalf("keyword stuffing should warn, got %s (%s)", status, evidence)
	}
}

func TestGeoEntityConsistency(t *testing.T) {
	pageHTML := func(phone string) string {
		return `<html lang="en"><head><title>t</title><script type="application/ld+json">{"@type":"Organization","name":"Geo Co","url":"https://geo.test","logo":"https://geo.test/l.png","sameAs":["https://x.test"],"telephone":"` + phone + `","address":{"@type":"PostalAddress","streetAddress":"1 Main St","addressLocality":"Berlin"}}</script></head><body><h1>t</h1></body></html>`
	}
	audit, first := newGeoTestAudit(t, pageHTML("+49-30-111"), nil)
	second := &geoPage{URL: "https://geo.test/about", LD: analyzeGeoJSONLD([]string{`{"@type":"Organization","name":"Geo Co","telephone":"+49-30-222","address":{"streetAddress":"1 Main St","addressLocality":"Berlin"}}`})}
	audit.pages = []*geoPage{second}
	if status, evidence := checkEntityConsistency(audit); status != geoStatusWarn || !strings.Contains(evidence, "telephone") {
		t.Fatalf("inconsistent entity should warn, got %s (%s)", status, evidence)
	}

	second.LD = analyzeGeoJSONLD([]string{`{"@type":"Organization","name":"Geo Co","telephone":"+49-30-111","address":{"streetAddress":"1 Main St","addressLocality":"Berlin"}}`})
	if status, evidence := checkEntityConsistency(audit); status != geoStatusPass {
		t.Fatalf("consistent entity should pass, got %s (%s)", status, evidence)
	}
	_ = first
}

func TestGeoSitemapIndexAndSelection(t *testing.T) {
	index := `<?xml version="1.0"?><sitemapindex><sitemap><loc>https://geo.test/sitemap-a.xml</loc></sitemap><sitemap><loc>https://other.test/sitemap.xml</loc></sitemap></sitemapindex>`
	entries, children, err := parseGeoSitemap([]byte(index))
	if err != nil || len(entries) != 0 || len(children) != 2 {
		t.Fatalf("unexpected index parse: entries=%v children=%v err=%v", entries, children, err)
	}

	urlset := `<?xml version="1.0"?><urlset><url><loc>https://geo.test/blog/a</loc><lastmod>2026-01-01</lastmod></url><url><loc>https://geo.test/news/b</loc></url></urlset>`
	entries, children, err = parseGeoSitemap([]byte(urlset))
	if err != nil || len(entries) != 2 || len(children) != 0 {
		t.Fatalf("unexpected urlset parse: %v %v %v", entries, children, err)
	}
	if entries[0].LastMod != "2026-01-01" || entries[1].LastMod != "" {
		t.Fatalf("unexpected lastmod values: %#v", entries)
	}

	fixtures := []geoSitemapEntry{
		{Loc: "https://geo.test/blog/1"}, {Loc: "https://geo.test/blog/2"}, {Loc: "https://geo.test/blog/3"},
		{Loc: "https://geo.test/news/1"}, {Loc: "https://geo.test/docs/1"}, {Loc: "https://other.test/x"},
	}
	selection := selectGeoSitemapPages(fixtures, "geo.test", "/", 4)
	if len(selection) != 4 {
		t.Fatalf("expected 4 picks, got %#v", selection)
	}
	if selection[0] != "https://geo.test/blog/1" || selection[1] != "https://geo.test/docs/1" || selection[2] != "https://geo.test/news/1" {
		t.Fatalf("expected prefix spread, got %#v", selection)
	}
	for _, picked := range selection {
		if strings.Contains(picked, "other.test") {
			t.Fatalf("cross-site sitemap URL was selected: %s", picked)
		}
	}
}

func TestGeoSitemapChecks(t *testing.T) {
	stale := &geoAudit{sitemap: &geoSitemapData{Found: true, Source: "https://geo.test/sitemap.xml", Documents: 1, Entries: []geoSitemapEntry{
		{Loc: "https://geo.test/a", LastMod: "2020-01-01"},
		{Loc: "https://geo.test/b", LastMod: "2020-01-01"},
	}}}
	status, evidence := checkSitemapDiscovery(stale)
	if status != geoStatusWarn || !strings.Contains(evidence, "identical") || !strings.Contains(evidence, "days old") {
		t.Fatalf("stale identical sitemap should warn, got %s (%s)", status, evidence)
	}

	fresh := &geoAudit{sitemap: &geoSitemapData{Found: true, Source: "https://geo.test/sitemap.xml", Documents: 1, Entries: []geoSitemapEntry{
		{Loc: "https://geo.test/a", LastMod: time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")},
		{Loc: "https://geo.test/b", LastMod: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")},
	}}}
	if status, evidence := checkSitemapDiscovery(fresh); status != geoStatusPass {
		t.Fatalf("fresh sitemap should pass, got %s (%s)", status, evidence)
	}

	missing := &geoAudit{sitemap: &geoSitemapData{}}
	if status, _ := checkSitemapDiscovery(missing); status != geoStatusWarn {
		t.Fatalf("missing sitemap must warn, got %s", status)
	}
}

func TestGeoLLMsTxtCheck(t *testing.T) {
	audit := &geoAudit{llmsTxt: true, llmsSize: 120}
	status, evidence := checkLLMsTxt(audit)
	if status != geoStatusInfo || !strings.Contains(evidence, "120 bytes") || !strings.Contains(evidence, "do not currently read") {
		t.Fatalf("unexpected llms.txt evidence: %s (%s)", status, evidence)
	}
	audit = &geoAudit{}
	if status, _ := checkLLMsTxt(audit); status != geoStatusInfo {
		t.Fatalf("missing llms.txt must stay info, got %s", status)
	}
}

func TestGeoNativeManifest(t *testing.T) {
	manifest, ok := Get("geo-site-audit-v1")
	if !ok {
		t.Fatal("geo-site-audit-v1 manifest is missing")
	}
	if manifest.Native != geoAuditNativeName || manifest.Provider != "native" || manifest.Mode != ModeSync {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Billing.Type != BillingPerCall || manifest.Billing.PriceUSD != 0 {
		t.Fatalf("expected free per_call billing, got %#v", manifest.Billing)
	}
	if manifest.Limits.Timeout != "90s" || manifest.Limits.MaxResponseBytes != 8*1024*1024 {
		t.Fatalf("unexpected limits: %#v", manifest.Limits)
	}
	if _, registered := nativeSyncHandler(manifest.Native); !registered {
		t.Fatal("native handler is not registered")
	}

	unknown := &Manifest{
		ID:       "native-unknown-v1",
		Provider: "native",
		Mode:     ModeSync,
		Native:   "does-not-exist",
		Billing:  BillingSpec{Type: BillingPerCall},
		Limits:   LimitsSpec{Timeout: "10s"},
	}
	if err := unknown.Validate(); err == nil {
		t.Fatal("manifest with an unregistered native handler must not validate")
	}

	withEndpoint := &Manifest{
		ID:       "native-endpoint-v1",
		Provider: "native",
		Mode:     ModeSync,
		Native:   geoAuditNativeName,
		Endpoint: "https://example.com/x",
		Billing:  BillingSpec{Type: BillingPerCall},
		Limits:   LimitsSpec{Timeout: "10s"},
	}
	if err := withEndpoint.Validate(); err == nil {
		t.Fatal("native manifest with an endpoint must not validate")
	}

	async := &Manifest{
		ID:       "native-async-v1",
		Provider: "native",
		Mode:     ModeAsync,
		Native:   geoAuditNativeName,
		Billing:  BillingSpec{Type: BillingPerCall},
		Limits:   LimitsSpec{Timeout: "10s"},
	}
	if err := async.Validate(); err == nil {
		t.Fatal("async native manifest must not validate")
	}
}

func TestGeoAuditInputValidation(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		code  string
	}{
		{"missing url", map[string]any{}, "invalid_request"},
		{"http url", map[string]any{"url": "http://geo.test/"}, "invalid_request"},
		{"userinfo", map[string]any{"url": "https://user:pass@geo.test/"}, "invalid_request"},
		{"ip literal", map[string]any{"url": "https://93.184.216.34/"}, "invalid_request"},
		{"port 8443", map[string]any{"url": "https://geo.test:8443/"}, "invalid_request"},
		{"max_pages zero", map[string]any{"url": "https://geo.test/", "max_pages": 0}, "invalid_request"},
		{"max_pages too large", map[string]any{"url": "https://geo.test/", "max_pages": 16}, "invalid_request"},
		{"bad training policy", map[string]any{"url": "https://geo.test/", "training_policy": "maybe"}, "invalid_request"},
		{"cross-site extra page", map[string]any{"url": "https://geo.test/", "pages": []any{"https://other.test/x"}}, "invalid_request"},
		{"too many extra pages", map[string]any{"url": "https://geo.test/", "pages": []any{
			"https://geo.test/1", "https://geo.test/2", "https://geo.test/3", "https://geo.test/4", "https://geo.test/5",
			"https://geo.test/6", "https://geo.test/7", "https://geo.test/8", "https://geo.test/9", "https://geo.test/10", "https://geo.test/11",
		}}, "invalid_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, execErr := parseGeoAuditInput(test.input)
			if execErr == nil || execErr.Code != test.code {
				t.Fatalf("got %#v, want code %s", execErr, test.code)
			}
		})
	}

	parsed, execErr := parseGeoAuditInput(map[string]any{
		"url":             "https://WWW.Geo.test/",
		"pages":           []any{"https://geo.test/about", "https://GEO.test/about#frag"},
		"max_pages":       float64(5),
		"training_policy": "BLOCK",
	})
	if execErr != nil {
		t.Fatalf("valid input rejected: %v", execErr)
	}
	if parsed.URL != "https://www.geo.test/" || len(parsed.Pages) != 1 || parsed.MaxPages != 5 || parsed.TrainingPolicy != geoTrainingPolicyBlock {
		t.Fatalf("unexpected parsed input: %#v", parsed)
	}
}

func TestGeoWordCountAndDates(t *testing.T) {
	if words := geoWordCount("hello world 中文测试"); words != 2+2 {
		t.Fatalf("expected 2 Latin words plus 2 estimated CJK words, got %d", words)
	}
	for _, text := range []string{"Published 2026-05-06.", "Updated May 6, 2026.", "6 May 2026", "2026年5月6日"} {
		if got := geoFindVisibleDate(text, ""); got == "" {
			t.Fatalf("expected a date in %q", text)
		}
	}
	if geoFindVisibleDate("no date here", "") != "" {
		t.Fatal("unexpected date match")
	}
}

func TestGeoCanonicalComparableIgnoresFragment(t *testing.T) {
	left := geoCanonicalComparable("https://GEO.test/page#top")
	right := geoCanonicalComparable("https://geo.test/page")
	if left != right {
		t.Fatalf("expected equal canonical forms: %q vs %q", left, right)
	}
}

// TestGeoAuditExecuteSyncEndToEnd wires the embedded Manifest through
// ExecuteSync against an offline fixture site.
func TestGeoAuditExecuteSyncEndToEnd(t *testing.T) {
	network := newGeoTestNetwork(t)
	network.serve(t, "geo.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userAgent := r.Header.Get("User-Agent"); strings.Contains(userAgent, "ClaudeBot/") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html><title>Attention Required! | Cloudflare</title></html>"))
			return
		}
		switch r.URL.Path {
		case "/robots.txt":
			_, _ = w.Write([]byte("User-agent: *\nAllow: /\nDisallow: /private/\n\nUser-agent: GPTBot\nDisallow: /\n\nUser-agent: anthropic-ai\nDisallow: /\n\nContent-Signal: search=yes, ai-train=no\nSitemap: https://geo.test/sitemap-index.xml\n"))
		case "/sitemap-index.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><sitemapindex><sitemap><loc>https://geo.test/sitemap-a.xml</loc></sitemap><sitemap><loc>https://geo.test/sitemap-b.xml</loc></sitemap></sitemapindex>`))
		case "/sitemap-a.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><urlset><url><loc>https://geo.test/blog/post-one</loc><lastmod>2026-01-02</lastmod></url><url><loc>https://geo.test/news/press</loc><lastmod>2026-01-03</lastmod></url></urlset>`))
		case "/sitemap-b.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><urlset><url><loc>https://geo.test/docs/guide</loc><lastmod>2026-01-04</lastmod></url><url><loc>https://other.test/x</loc></url></urlset>`))
		case "/llms.txt":
			_, _ = w.Write([]byte("# Geo Test\n\n- https://geo.test/blog/post-one\n"))
		case "/blog/post-one":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(geoFixtureArticlePage("Post one", "2026-01-02")))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(geoFixtureHomePage()))
		}
	}))
	network.install(t)

	manifest, ok := Get("geo-site-audit-v1")
	if !ok {
		t.Fatal("geo-site-audit-v1 manifest is missing")
	}
	result, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"url": "https://geo.test/"}, "")
	if execErr != nil {
		t.Fatalf("ExecuteSync failed: %v (%s)", execErr, execErr.Code)
	}
	output := result.Output
	if output["site"] != "geo.test" || output["training_policy"] != geoTrainingPolicyAllow {
		t.Fatalf("unexpected site/policy: %#v", output)
	}
	robots, ok := output["robots"].(map[string]any)
	if !ok || robots["found"] != true {
		t.Fatalf("unexpected robots output: %#v", output["robots"])
	}
	signals, ok := robots["content_signals"].([]string)
	if !ok || len(signals) != 1 || signals[0] != "search=yes, ai-train=no" {
		t.Fatalf("unexpected content signals: %#v", robots["content_signals"])
	}
	pages, ok := output["pages"].([]any)
	if !ok || len(pages) < 3 {
		t.Fatalf("expected homepage and sitemap pages, got %#v", output["pages"])
	}
	for _, raw := range pages {
		if strings.Contains(raw.(map[string]any)["url"].(string), "other.test") {
			t.Fatalf("cross-site sitemap URL was audited: %#v", raw)
		}
	}
	firstPage, _ := pages[0].(map[string]any)
	if firstPage["url"] != "https://geo.test/" || firstPage["status"] != http.StatusOK {
		t.Fatalf("unexpected homepage payload: %#v", firstPage)
	}
	if types, ok := firstPage["schema_types"].([]string); !ok || len(types) == 0 {
		t.Fatalf("expected schema types on the homepage: %#v", firstPage["schema_types"])
	}

	statuses := map[string]string{}
	for _, raw := range output["checks"].([]any) {
		check := raw.(map[string]any)
		checkURL, _ := check["url"].(string)
		statuses[check["id"].(string)+"|"+checkURL] = check["status"].(string)
	}
	if statuses["robots_ai_search_access|"] != geoStatusPass {
		t.Fatalf("robots access should pass, got %v", statuses["robots_ai_search_access|"])
	}
	if statuses["robots_training_policy|"] != geoStatusWarn {
		t.Fatalf("training policy should warn for a blocked GPTBot, got %v", statuses["robots_training_policy|"])
	}
	if statuses["robots_deprecated_tokens|"] != geoStatusWarn {
		t.Fatalf("deprecated token should warn, got %v", statuses["robots_deprecated_tokens|"])
	}
	if statuses["ai_ua_reachability|"] != geoStatusFail {
		t.Fatalf("ClaudeBot challenge should fail UA reachability, got %v", statuses["ai_ua_reachability|"])
	}
	if statuses["llms_txt|"] != geoStatusInfo {
		t.Fatalf("llms.txt should be info, got %v", statuses["llms_txt|"])
	}
	if statuses["page_server_rendered_text|https://geo.test/"] != geoStatusPass {
		t.Fatalf("homepage should be server rendered, got %v", statuses["page_server_rendered_text|https://geo.test/"])
	}
	if statuses["page_jsonld_faq|https://geo.test/blog/post-one"] != geoStatusPass {
		t.Fatalf("fixture FAQ should match visible text, got %v", statuses["page_jsonld_faq|https://geo.test/blog/post-one"])
	}

	summary := output["summary"].(map[string]any)
	official := summary[geoSourceOfficial].(map[string]int)
	if official["fail"] == 0 || official["pass"] == 0 {
		t.Fatalf("unexpected official summary: %#v", official)
	}
	notes := output["notes"].([]string)
	if len(notes) == 0 {
		t.Fatal("expected audit notes")
	}
	joinedNotes := strings.Join(notes, " | ")
	if !strings.Contains(joinedNotes, "cross-site sitemap URL(s) skipped") {
		t.Fatalf("expected a cross-site sitemap note, got %s", joinedNotes)
	}
	if !strings.Contains(joinedNotes, "no JavaScript execution") {
		t.Fatalf("expected a JavaScript disclosure note, got %s", joinedNotes)
	}

	// Nothing from the fixture HTML may leak into checks or notes.
	serialized, err := common.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"<html", "fixture-only-marker", "<script"} {
		if strings.Contains(string(serialized), needle) {
			t.Fatalf("audit output leaked page markup (%s)", needle)
		}
	}
	if manifest.Billing.PriceUSD != 0 {
		t.Fatalf("audit must stay free, got %v", manifest.Billing.PriceUSD)
	}
}

func geoFixtureHomePage() string {
	return `<html lang="en"><head><title>Geo Test — generative engine optimization</title>
<meta name="description" content="Geo Test explains how generative search picks sources.">
<link rel="canonical" href="https://geo.test/">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Organization","name":"Geo Test","url":"https://geo.test","logo":"https://geo.test/logo.png","sameAs":["https://x.test/geo"],"telephone":"+49-30-111","address":{"@type":"Address","streetAddress":"1 Main St","addressLocality":"Berlin"}}</script>
</head><body><h1>Generative engine optimization for real sites</h1>
<p>` + strings.Repeat("Generative search engines summarize pages and cite sources when the page answers a question directly. ", 10) + `</p>
<p>Teams measured a 42% lift in 2025 across 3 million impressions, according to an independent study.</p>
<ul><li>Crawl access</li><li>Clear structure</li></ul>
<blockquote>Answer first, then explain the evidence.</blockquote>
<a href="https://other.test/study">Independent study</a>
<img src="/hero.png" alt="Hero"></body></html>`
}

func geoFixtureArticlePage(title string, date string) string {
	return `<html lang="en"><head><title>` + title + `</title>
<meta name="description" content="` + title + ` description.">
<link rel="canonical" href="https://geo.test/blog/post-one">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"BlogPosting","headline":"` + title + `","datePublished":"` + date + `","dateModified":"` + date + `","author":{"@type":"Person","name":"Ana"}}</script>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"FAQPage","mainEntity":[{"@type":"Question","name":"How do AI search engines pick sources?","acceptedAnswer":{"@type":"Answer","text":"They cite pages that answer directly."}}]}</script>
</head><body><h1>` + title + `</h1><time datetime="` + date + `">` + date + `</time>
<p>` + strings.Repeat("Direct answers with sources are easier to quote. ", 12) + `</p>
<p>How do AI search engines pick sources? They prefer explicit, well-structured answers with dates and citations.</p>
<img src="/a.png" alt="A"></body></html>`
}

func TestGeoPagePayloadShape(t *testing.T) {
	page := &geoPage{URL: "https://geo.test/x", Status: 200, Words: 42, Title: "T", H1Count: 1, SchemaTypes: []string{"Organization"}}
	payload := page.payload()
	for _, key := range []string{"url", "status", "words", "title", "h1_count", "schema_types"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("page payload is missing %s: %#v", key, payload)
		}
	}
	failed := (&geoPage{URL: "https://geo.test/x", Error: "timeout"}).payload()
	if failed["error"] != "timeout" {
		t.Fatalf("failed page should expose a stable error code: %#v", failed)
	}
}
