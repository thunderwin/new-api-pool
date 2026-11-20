package capability

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	geoStatusPass = "pass"
	geoStatusWarn = "warn"
	geoStatusFail = "fail"
	geoStatusInfo = "info"

	geoSourceOfficial = "official"
	geoSourceResearch = "research"
	geoSourceIndustry = "industry"

	geoScopeSite = "site"
	geoScopePage = "page"

	geoCategoryCrawl      = "crawl"
	geoCategoryIndex      = "index"
	geoCategoryRender     = "render"
	geoCategoryStructure  = "structure"
	geoCategorySchema     = "schema"
	geoCategoryFreshness  = "freshness"
	geoCategoryCitability = "citability"
	geoCategoryEntity     = "entity"
)

// Official references: vendor documentation, standards or search-engine docs.
// Research references: published studies. Industry references: practitioner
// guidance without a single normative source.
const (
	geoRefRFC9309            = "https://www.rfc-editor.org/rfc/rfc9309"
	geoRefOpenAIBots         = "https://platform.openai.com/docs/bots"
	geoRefPerplexityBots     = "https://docs.perplexity.ai/docs/resources/perplexity-crawlers"
	geoRefAnthropicBots      = "https://support.claude.com/en/articles/8896518-does-anthropic-crawl-data-from-the-web-and-how-can-site-owners-block-the-crawler"
	geoRefApplebot           = "https://support.apple.com/en-us/119829"
	geoRefBingCrawlers       = "https://www.bing.com/webmasters/help/which-crawlers-does-bing-use-8c184ec0"
	geoRefGoogleRobotsMeta   = "https://developers.google.com/search/docs/crawling-indexing/robots-meta-tag"
	geoRefGoogleCanonical    = "https://developers.google.com/search/docs/crawling-indexing/consolidate-duplicate-urls"
	geoRefGoogleSnippet      = "https://developers.google.com/search/docs/appearance/snippet"
	geoRefGoogleTitle        = "https://developers.google.com/search/docs/appearance/title-link"
	geoRefGoogleStructured   = "https://developers.google.com/search/docs/appearance/structured-data/intro-structured-data"
	geoRefGoogleArticle      = "https://developers.google.com/search/docs/appearance/structured-data/article"
	geoRefGoogleProduct      = "https://developers.google.com/search/docs/appearance/structured-data/product"
	geoRefGoogleFAQ          = "https://developers.google.com/search/docs/appearance/structured-data/faqpage"
	geoRefGoogleOrganization = "https://developers.google.com/search/docs/appearance/structured-data/organization"
	geoRefGoogleSitemap      = "https://developers.google.com/search/docs/crawling-indexing/sitemaps/overview"
	geoRefGoogleRobots       = "https://developers.google.com/search/docs/crawling-indexing/robots/intro"
	geoRefContentSignals     = "https://blog.cloudflare.com/content-signals-policy/"
	geoRefLLMsTxt            = "https://llmstxt.org/"
	geoRefGEOPaper           = "https://arxiv.org/abs/2311.09735"
)

type geoCheckResult struct {
	ID        string
	Category  string
	Source    string
	Scope     string
	URL       string
	Status    string
	Evidence  string
	Reference string
}

func (c geoCheckResult) payload() map[string]any {
	out := map[string]any{
		"id":        c.ID,
		"category":  c.Category,
		"source":    c.Source,
		"scope":     c.Scope,
		"status":    c.Status,
		"evidence":  c.Evidence,
		"reference": c.Reference,
	}
	if c.Scope == geoScopePage {
		out["url"] = c.URL
	}
	return out
}

type geoPageCheck struct {
	ID        string
	Category  string
	Source    string
	Reference string
	Evaluate  func(a *geoAudit, p *geoPage) (string, string)
}

type geoSiteCheck struct {
	ID        string
	Category  string
	Source    string
	Reference string
	Evaluate  func(a *geoAudit) (string, string)
}

func (a *geoAudit) buildChecks() {
	a.checks = make([]geoCheckResult, 0, len(geoAuditSiteChecks)+len(geoAuditPageChecks)*len(a.auditedPages()))
	for _, check := range geoAuditSiteChecks {
		status, evidence := check.Evaluate(a)
		a.checks = append(a.checks, geoCheckResult{
			ID:        check.ID,
			Category:  check.Category,
			Source:    check.Source,
			Scope:     geoScopeSite,
			Status:    status,
			Evidence:  geoEvidence(evidence),
			Reference: check.Reference,
		})
	}
	for _, page := range a.auditedPages() {
		for _, check := range geoAuditPageChecks {
			status, evidence := check.Evaluate(a, page)
			a.checks = append(a.checks, geoCheckResult{
				ID:        check.ID,
				Category:  check.Category,
				Source:    check.Source,
				Scope:     geoScopePage,
				URL:       page.URL,
				Status:    status,
				Evidence:  geoEvidence(evidence),
				Reference: check.Reference,
			})
		}
	}
}

// geoAuditSiteChecks is the site-level rule table. Order defines output order.
var geoAuditSiteChecks = []geoSiteCheck{
	{ID: "robots_ai_search_access", Category: geoCategoryCrawl, Source: geoSourceOfficial, Reference: geoRefRFC9309, Evaluate: checkRobotsAISearchAccess},
	{ID: "robots_user_fetch", Category: geoCategoryCrawl, Source: geoSourceOfficial, Reference: geoRefPerplexityBots, Evaluate: checkRobotsUserFetch},
	{ID: "robots_training_policy", Category: geoCategoryCrawl, Source: geoSourceOfficial, Reference: geoRefOpenAIBots, Evaluate: checkRobotsTrainingPolicy},
	{ID: "robots_deprecated_tokens", Category: geoCategoryCrawl, Source: geoSourceOfficial, Reference: geoRefAnthropicBots, Evaluate: checkRobotsDeprecatedTokens},
	{ID: "ai_ua_reachability", Category: geoCategoryCrawl, Source: geoSourceOfficial, Reference: geoRefOpenAIBots, Evaluate: checkAIUserAgentReachability},
	{ID: "sitemap_discovery", Category: geoCategoryCrawl, Source: geoSourceOfficial, Reference: geoRefGoogleSitemap, Evaluate: checkSitemapDiscovery},
	{ID: "llms_txt", Category: geoCategoryCrawl, Source: geoSourceIndustry, Reference: geoRefLLMsTxt, Evaluate: checkLLMsTxt},
	{ID: "entity_consistency", Category: geoCategoryEntity, Source: geoSourceIndustry, Reference: geoRefGoogleOrganization, Evaluate: checkEntityConsistency},
}

// geoAuditPageChecks is the page-level rule table.
var geoAuditPageChecks = []geoPageCheck{
	{ID: "page_http_status", Category: geoCategoryCrawl, Source: geoSourceOfficial, Reference: geoRefGoogleRobots, Evaluate: checkPageHTTPStatus},
	{ID: "page_indexability", Category: geoCategoryIndex, Source: geoSourceOfficial, Reference: geoRefGoogleRobotsMeta, Evaluate: checkPageIndexability},
	{ID: "page_snippet_controls", Category: geoCategoryIndex, Source: geoSourceOfficial, Reference: geoRefGoogleSnippet, Evaluate: checkPageSnippetControls},
	{ID: "page_canonical", Category: geoCategoryIndex, Source: geoSourceOfficial, Reference: geoRefGoogleCanonical, Evaluate: checkPageCanonical},
	{ID: "page_server_rendered_text", Category: geoCategoryRender, Source: geoSourceOfficial, Reference: geoRefGoogleRobots, Evaluate: checkPageServerRenderedText},
	{ID: "page_jsonld_parse", Category: geoCategorySchema, Source: geoSourceOfficial, Reference: geoRefGoogleStructured, Evaluate: checkPageJSONLDParse},
	{ID: "page_jsonld_organization", Category: geoCategorySchema, Source: geoSourceOfficial, Reference: geoRefGoogleOrganization, Evaluate: checkPageJSONLDOrganization},
	{ID: "page_jsonld_product", Category: geoCategorySchema, Source: geoSourceOfficial, Reference: geoRefGoogleProduct, Evaluate: checkPageJSONLDProduct},
	{ID: "page_jsonld_article", Category: geoCategorySchema, Source: geoSourceOfficial, Reference: geoRefGoogleArticle, Evaluate: checkPageJSONLDArticle},
	{ID: "page_jsonld_faq", Category: geoCategorySchema, Source: geoSourceOfficial, Reference: geoRefGoogleFAQ, Evaluate: checkPageJSONLDFAQ},
	{ID: "page_title", Category: geoCategoryStructure, Source: geoSourceOfficial, Reference: geoRefGoogleTitle, Evaluate: checkPageTitle},
	{ID: "page_meta_description", Category: geoCategoryStructure, Source: geoSourceOfficial, Reference: geoRefGoogleSnippet, Evaluate: checkPageMetaDescription},
	{ID: "page_h1", Category: geoCategoryStructure, Source: geoSourceOfficial, Reference: geoRefGoogleTitle, Evaluate: checkPageH1},
	{ID: "page_lang", Category: geoCategoryStructure, Source: geoSourceOfficial, Reference: geoRefGoogleStructured, Evaluate: checkPageLang},
	{ID: "page_image_alt", Category: geoCategoryStructure, Source: geoSourceOfficial, Reference: geoRefGoogleStructured, Evaluate: checkPageImageAlt},
	{ID: "page_article_date", Category: geoCategoryFreshness, Source: geoSourceOfficial, Reference: geoRefGoogleArticle, Evaluate: checkPageArticleDate},
	{ID: "answer_first_paragraph", Category: geoCategoryCitability, Source: geoSourceIndustry, Reference: geoRefGEOPaper, Evaluate: checkAnswerFirstParagraph},
	{ID: "content_lists_tables", Category: geoCategoryCitability, Source: geoSourceIndustry, Reference: geoRefGEOPaper, Evaluate: checkContentListsTables},
	{ID: "stats_density", Category: geoCategoryCitability, Source: geoSourceResearch, Reference: geoRefGEOPaper, Evaluate: checkStatsDensity},
	{ID: "quotes_in_content", Category: geoCategoryCitability, Source: geoSourceResearch, Reference: geoRefGEOPaper, Evaluate: checkQuotesInContent},
	{ID: "external_citations", Category: geoCategoryCitability, Source: geoSourceResearch, Reference: geoRefGEOPaper, Evaluate: checkExternalCitations},
	{ID: "keyword_stuffing", Category: geoCategoryCitability, Source: geoSourceResearch, Reference: geoRefGEOPaper, Evaluate: checkKeywordStuffing},
}

func geoEvidence(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= geoAuditMaxEvidence {
		return text
	}
	return string(runes[:geoAuditMaxEvidence-1]) + "…"
}

func geoEvidencef(format string, args ...any) string {
	return geoEvidence(fmt.Sprintf(format, args...))
}

func geoListOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func geoRobotsUnavailable(a *geoAudit) bool {
	return a.robots == nil || !a.robots.Found
}

func geoRobotsServerError(a *geoAudit) bool {
	if a.robots == nil {
		return false
	}
	return strings.HasPrefix(a.robots.Err, "http_5")
}

func checkRobotsAISearchAccess(a *geoAudit) (string, string) {
	paths := a.auditedPaths()
	if geoRobotsServerError(a) {
		return geoStatusFail, geoEvidencef("robots.txt returned %s; RFC 9309 requires treating an unavailable robots.txt as a full disallow", a.robots.Err)
	}
	if a.robots != nil && a.robots.Err != "" {
		return geoStatusWarn, geoEvidencef("robots.txt could not be fetched (%s); AI crawler rules could not be verified", a.robots.Err)
	}
	if geoRobotsUnavailable(a) {
		return geoStatusPass, "robots.txt not found; RFC 9309 allows all crawling when robots.txt is absent"
	}
	blocked := make([]string, 0)
	for _, bot := range geoSearchRobots {
		for _, path := range paths {
			allowed, rule := a.robots.geoRobotsAllows(bot.Token, path)
			if allowed {
				continue
			}
			blocked = append(blocked, geoBlockedEntry(bot, path, rule))
		}
	}
	if len(blocked) > 0 {
		return geoStatusFail, geoEvidencef("AI search crawling restricted: %s", strings.Join(blocked, "; "))
	}
	return geoStatusPass, geoEvidencef("%d AI search crawlers allowed on %d audited paths", len(geoSearchRobots), len(paths))
}

func geoBlockedEntry(bot geoBot, path, rule string) string {
	return bot.Engine + " " + bot.Token + " " + path + " -> Disallow: " + rule
}

// checkRobotsUserFetch reports robots rules for user-initiated fetchers
// separately, because they do not decide whether a site appears in AI search.
func checkRobotsUserFetch(a *geoAudit) (string, string) {
	if a.robots != nil && a.robots.Err != "" {
		return geoStatusInfo, geoEvidencef("robots.txt could not be fetched (%s); user-initiated fetch rules could not be verified", a.robots.Err)
	}
	if geoRobotsUnavailable(a) {
		return geoStatusPass, "robots.txt not found; user-initiated AI fetchers are not restricted"
	}
	paths := a.auditedPaths()
	honoured := make([]string, 0)
	mayIgnore := make([]string, 0)
	for _, bot := range geoUserFetchRobots {
		for _, path := range paths {
			allowed, rule := a.robots.geoRobotsAllows(bot.Token, path)
			if allowed {
				continue
			}
			if bot.MayIgnoreRobots {
				mayIgnore = append(mayIgnore, geoBlockedEntry(bot, path, rule))
			} else {
				honoured = append(honoured, geoBlockedEntry(bot, path, rule))
			}
		}
	}
	if len(honoured) > 0 {
		evidence := "these assistants honour robots.txt and cannot open the page when a user asks: " + strings.Join(honoured, "; ") + ". AI search visibility is checked separately"
		if len(mayIgnore) > 0 {
			evidence += "; also disallowed, but robots.txt may not apply: " + strings.Join(mayIgnore, "; ")
		}
		return geoStatusWarn, geoEvidence(evidence)
	}
	if len(mayIgnore) > 0 {
		return geoStatusInfo, geoEvidencef("disallowed, but the vendor says robots.txt may not apply to user-initiated fetches, so this alone does not stop them: %s. AI search visibility is checked separately", strings.Join(mayIgnore, "; "))
	}
	return geoStatusPass, geoEvidencef("%d user-initiated AI fetchers allowed on %d audited paths", len(geoUserFetchRobots), len(paths))
}

func checkRobotsTrainingPolicy(a *geoAudit) (string, string) {
	note := ""
	if a.robots != nil && a.robots.Err != "" {
		note = "robots.txt unavailable (" + a.robots.Err + "); no rules found, "
	}
	allowed := make([]string, 0, len(geoTrainingRobots))
	blocked := make([]string, 0, len(geoTrainingRobots))
	for _, bot := range geoTrainingRobots {
		ok, _ := a.robots.geoRobotsAllows(bot.Token, "/")
		if ok {
			allowed = append(allowed, bot.Token)
		} else {
			blocked = append(blocked, bot.Token)
		}
	}
	switch a.input.TrainingPolicy {
	case geoTrainingPolicyAllow:
		if len(blocked) > 0 {
			return geoStatusWarn, geoEvidencef("%straining_policy=allow, but robots.txt blocks: %s", note, strings.Join(blocked, ", "))
		}
		return geoStatusPass, geoEvidencef("%straining_policy=allow and all %d training crawlers are allowed", note, len(geoTrainingRobots))
	case geoTrainingPolicyBlock:
		if len(allowed) > 0 {
			return geoStatusWarn, geoEvidencef("%straining_policy=block, but robots.txt allows: %s", note, strings.Join(allowed, ", "))
		}
		return geoStatusPass, geoEvidencef("%straining_policy=block and all %d training crawlers are disallowed", note, len(geoTrainingRobots))
	default:
		return geoStatusInfo, geoEvidencef("%straining_policy=any; allowed=[%s] blocked=[%s]", note, geoListOrNone(allowed), geoListOrNone(blocked))
	}
}

func checkRobotsDeprecatedTokens(a *geoAudit) (string, string) {
	if a.robots != nil && a.robots.Err != "" {
		return geoStatusInfo, geoEvidencef("robots.txt could not be fetched (%s); obsolete token scan skipped", a.robots.Err)
	}
	if geoRobotsUnavailable(a) {
		return geoStatusPass, "robots.txt not found; no obsolete Anthropic crawler tokens"
	}
	found := a.robots.geoDeprecatedTokens()
	if len(found) > 0 {
		return geoStatusWarn, geoEvidencef("obsolete Anthropic user-agent tokens have no effect: %s", strings.Join(found, ", "))
	}
	return geoStatusPass, "no obsolete anthropic-ai or Claude-Web user-agent tokens"
}

func checkAIUserAgentReachability(a *geoAudit) (string, string) {
	if a.probe == nil {
		return geoStatusInfo, "user-agent probes were not completed"
	}
	probe := a.probe
	blocked := make([]string, 0, len(probe.Bots))
	for _, bot := range probe.Bots {
		if bot.Error != "" {
			blocked = append(blocked, bot.Token+"="+bot.Error)
			continue
		}
		if bot.Status != 200 || bot.Challenge {
			blocked = append(blocked, fmt.Sprintf("%s=%d%s", bot.Token, bot.Status, geoChallengeSuffix(bot.Challenge)))
		}
	}
	baselineOK := probe.BrowserError == "" && probe.BrowserStatus == 200 && !probe.BrowserChallenge
	baseline := fmt.Sprintf("browser UA %d", probe.BrowserStatus)
	if probe.BrowserError != "" {
		baseline = "browser UA " + probe.BrowserError
	}
	if len(blocked) == 0 {
		return geoStatusPass, geoEvidencef("all %d AI user agents reached the homepage with HTTP 200 (%s). User-Agent layer only; IP/reverse-DNS verified-bot rules are not observable", len(probe.Bots), baseline)
	}
	if !baselineOK {
		return geoStatusInfo, geoEvidencef("baseline (%s) also failed, so AI user-agent blocking cannot be isolated: %s", baseline, strings.Join(blocked, "; "))
	}
	return geoStatusWarn, geoEvidencef("suspected block: browser UA reached the homepage but spoofed AI user agents were rejected: %s. A firewall may reject a spoofed user agent that the real crawler passes; confirm in the CDN/firewall bot log or server access log", strings.Join(blocked, "; "))
}

func geoChallengeSuffix(challenge bool) string {
	if challenge {
		return " (challenge)"
	}
	return ""
}

func checkSitemapDiscovery(a *geoAudit) (string, string) {
	if a.sitemap == nil || !a.sitemap.Found {
		return geoStatusWarn, "no sitemap found via robots.txt Sitemap: or /sitemap.xml"
	}
	if len(a.sitemap.Entries) == 0 {
		return geoStatusWarn, geoEvidencef("%s had no <loc> entries", a.sitemap.Source)
	}
	total := len(a.sitemap.Entries)
	parsedLastmods := make(map[string]struct{}, total)
	lastmodValues := make([]string, 0, total)
	parsedCount := 0
	newest := ""
	newestTime := time.Time{}
	for _, entry := range a.sitemap.Entries {
		if strings.TrimSpace(entry.LastMod) == "" {
			continue
		}
		lastmodValues = append(lastmodValues, entry.LastMod)
		parsedLastmods[entry.LastMod] = struct{}{}
		if parsed, ok := parseGeoLastmod(entry.LastMod); ok {
			parsedCount++
			if newest == "" || parsed.After(newestTime) {
				newest = entry.LastMod
				newestTime = parsed
			}
		}
	}
	coverage := 0
	if total > 0 {
		coverage = parsedCount * 100 / total
	}
	issues := make([]string, 0)
	if len(lastmodValues) > 1 && len(parsedLastmods) == 1 {
		issues = append(issues, "all lastmod values are identical")
	}
	if newest != "" {
		age := int(time.Since(newestTime).Hours() / 24)
		if age > geoAuditStaleDays {
			issues = append(issues, fmt.Sprintf("newest lastmod %s is %d days old", newest, age))
		}
	}
	evidence := fmt.Sprintf("%d URLs from %s (%d documents), lastmod coverage %d%%", total, a.sitemap.Source, a.sitemap.Documents, coverage)
	if len(a.sitemap.Errors) > 0 {
		evidence += ", skipped: " + strings.Join(a.sitemap.Errors, ", ")
	}
	if len(issues) > 0 {
		return geoStatusWarn, geoEvidencef("%s; %s", evidence, strings.Join(issues, "; "))
	}
	return geoStatusPass, geoEvidence(evidence)
}

func checkLLMsTxt(a *geoAudit) (string, string) {
	if a.llmsTxt {
		return geoStatusInfo, geoEvidencef("llms.txt found (%d bytes); mainstream AI search engines do not currently read it", a.llmsSize)
	}
	return geoStatusInfo, "no llms.txt; mainstream AI search engines do not currently read it"
}

func checkEntityConsistency(a *geoAudit) (string, string) {
	type identity struct {
		page    string
		name    string
		phone   string
		address string
	}
	identities := make([]identity, 0)
	for _, page := range a.auditedPages() {
		if page == nil || page.LD == nil {
			continue
		}
		entry := identity{page: page.URL}
		for _, entity := range geoEntitiesOfType(page.LD, "Organization", "LocalBusiness") {
			if entry.name == "" {
				entry.name = geoJSONScalar(entity["name"])
			}
			if entry.phone == "" {
				entry.phone = geoOrgPhone(entity)
			}
			if entry.address == "" {
				entry.address = geoOrgAddress(entity)
			}
		}
		if entry.name != "" || entry.phone != "" || entry.address != "" {
			identities = append(identities, entry)
		}
	}
	if len(identities) < 2 {
		return geoStatusInfo, geoEvidencef("%d page(s) expose Organization/LocalBusiness identity data; at least 2 are needed to compare", len(identities))
	}
	inconsistent := make([]string, 0)
	for _, field := range []struct {
		name string
		get  func(identity) string
	}{{"name", func(i identity) string { return i.name }}, {"telephone", func(i identity) string { return i.phone }}, {"address", func(i identity) string { return i.address }}} {
		values := map[string]struct{}{}
		for _, entry := range identities {
			if value := strings.TrimSpace(field.get(entry)); value != "" {
				values[strings.ToLower(value)] = struct{}{}
			}
		}
		if len(values) > 1 {
			inconsistent = append(inconsistent, field.name)
		}
	}
	if len(inconsistent) > 0 {
		return geoStatusWarn, geoEvidencef("%d pages expose Organization data but differ on: %s", len(identities), strings.Join(inconsistent, ", "))
	}
	return geoStatusPass, geoEvidencef("%d pages expose consistent Organization name, telephone and address", len(identities))
}

func checkPageHTTPStatus(a *geoAudit, p *geoPage) (string, string) {
	if p == nil {
		return geoStatusInfo, "page was not audited"
	}
	if p.Error != "" {
		return geoStatusFail, geoEvidencef("page fetch failed: %s", p.Error)
	}
	if p.Status == 200 {
		return geoStatusPass, "HTTP 200"
	}
	return geoStatusFail, geoEvidencef("HTTP %d; AI crawlers only index successfully fetched pages", p.Status)
}

func checkPageIndexability(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, indexability not evaluated"
	}
	evidence := geoNoindexEvidence(p.HTML)
	if len(evidence) > 0 {
		return geoStatusFail, geoEvidencef("noindex blocks indexing: %s", strings.Join(evidence, "; "))
	}
	return geoStatusPass, "no noindex directive in meta robots or X-Robots-Tag"
}

func geoNoindexEvidence(doc *geoHTMLDoc) []string {
	found := make([]string, 0, 2)
	for _, entry := range doc.MetaRobots {
		if strings.Contains(strings.ToLower(entry), "noindex") {
			found = append(found, "meta robots "+entry)
		}
	}
	for _, value := range doc.XRobots {
		if strings.Contains(strings.ToLower(value), "noindex") {
			found = append(found, "x-robots-tag "+value)
		}
	}
	return found
}

func checkPageSnippetControls(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, snippet controls not evaluated"
	}
	controls := geoParseRobotsControls(p.HTML)
	parts := make([]string, 0, 3)
	status := geoStatusPass
	if controls.Nosnippet {
		status = geoStatusWarn
		parts = append(parts, "nosnippet present")
	}
	if controls.MaxSnippet != nil {
		parts = append(parts, fmt.Sprintf("max-snippet=%d", *controls.MaxSnippet))
		if *controls.MaxSnippet >= 0 && *controls.MaxSnippet < 50 {
			status = geoStatusWarn
		}
	}
	if controls.DataNosnippet > 0 {
		parts = append(parts, fmt.Sprintf("%d data-nosnippet block(s)", controls.DataNosnippet))
		if status == geoStatusPass {
			status = geoStatusInfo
		}
	}
	if len(parts) == 0 {
		return geoStatusPass, "no nosnippet, max-snippet or data-nosnippet restrictions"
	}
	return status, strings.Join(parts, ", ")
}

func checkPageCanonical(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, canonical not evaluated"
	}
	if p.HTML.Canonical == "" {
		return geoStatusWarn, "no rel=canonical link element"
	}
	canonical := p.HTML.Canonical
	if resolved, err := url.Parse(p.HTML.Canonical); err == nil && !resolved.IsAbs() && p.FinalURL != "" {
		if base, err := url.Parse(p.FinalURL); err == nil {
			if absolute := base.ResolveReference(resolved); absolute != nil {
				canonical = absolute.String()
			}
		}
	}
	if geoCanonicalComparable(canonical) != geoCanonicalComparable(p.FinalURL) {
		return geoStatusInfo, geoEvidencef("canonical points to another URL: %s", canonical)
	}
	return geoStatusPass, geoEvidencef("self-referencing canonical: %s", canonical)
}

func geoCanonicalComparable(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.Host = strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	return parsed.String()
}

func checkPageServerRenderedText(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, server-rendered text not evaluated"
	}
	words := p.HTML.Words
	if words < 10 && p.HTML.SPAMount {
		return geoStatusFail, geoEvidencef("almost no server-rendered text (%d words) and an SPA mount point; crawlers that do not execute JavaScript see an empty page", words)
	}
	if words < geoAuditMinWords {
		return geoStatusWarn, geoEvidencef("only %d words of server-rendered text (target >= %d)", words, geoAuditMinWords)
	}
	return geoStatusPass, geoEvidencef("%d words of server-rendered text", words)
}

func checkPageJSONLDParse(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, JSON-LD not evaluated"
	}
	ld := p.LD
	if ld == nil || ld.Blocks == 0 {
		return geoStatusInfo, "no application/ld+json blocks"
	}
	if ld.ParseErrors > 0 {
		return geoStatusFail, geoEvidencef("%d of %d JSON-LD block(s) are not valid JSON", ld.ParseErrors, ld.Blocks)
	}
	return geoStatusPass, geoEvidencef("%d JSON-LD block(s), types: %s", ld.Blocks, geoListOrNone(ld.Types))
}

func checkPageJSONLDOrganization(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.LD == nil {
		return geoStatusInfo, "page was not fetched, Organization data not evaluated"
	}
	entities := geoEntitiesOfType(p.LD, "Organization", "LocalBusiness")
	if len(entities) == 0 {
		return geoStatusInfo, "no Organization or LocalBusiness entity"
	}
	missing := make([]string, 0, 4)
	for _, key := range []string{"name", "url", "logo", "sameAs"} {
		if !geoJSONHasValue(entities, key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return geoStatusWarn, geoEvidencef("Organization/LocalBusiness is missing: %s", strings.Join(missing, ", "))
	}
	return geoStatusPass, geoEvidencef("Organization/LocalBusiness exposes name, url, logo and sameAs (%d entity)", len(entities))
}

func checkPageJSONLDProduct(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.LD == nil {
		return geoStatusInfo, "page was not fetched, Product data not evaluated"
	}
	entities := geoEntitiesOfType(p.LD, "Product")
	if len(entities) == 0 {
		return geoStatusInfo, "no Product entity"
	}
	for _, entity := range entities {
		if !geoJSONHasValue([]map[string]any{entity}, "offers") {
			return geoStatusWarn, "Product entity has no offers"
		}
	}
	visible := geoNormalizedText(p.HTML)
	missing := make([]string, 0, len(entities))
	for _, entity := range entities {
		name := geoJSONScalar(entity["name"])
		if name != "" && !strings.Contains(visible, geoNormalizeMatch(name)) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return geoStatusFail, geoEvidencef("Product name does not appear in visible text: %s", strings.Join(missing, ", "))
	}
	return geoStatusPass, geoEvidencef("%d Product entit(y/ies) with offers and matching visible name", len(entities))
}

func checkPageJSONLDArticle(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.LD == nil {
		return geoStatusInfo, "page was not fetched, Article data not evaluated"
	}
	entities := geoEntitiesOfType(p.LD, "Article", "BlogPosting", "NewsArticle")
	if len(entities) == 0 {
		return geoStatusInfo, "no Article or BlogPosting entity"
	}
	missing := make([]string, 0, 3)
	for _, key := range []string{"datePublished", "dateModified", "author"} {
		if !geoJSONHasValue(entities, key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return geoStatusWarn, geoEvidencef("Article/BlogPosting is missing: %s", strings.Join(missing, ", "))
	}
	return geoStatusPass, geoEvidencef("%d Article/BlogPosting entit(y/ies) with datePublished, dateModified and author", len(entities))
}

func checkPageJSONLDFAQ(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.LD == nil {
		return geoStatusInfo, "page was not fetched, FAQ data not evaluated"
	}
	entities := geoEntitiesOfType(p.LD, "FAQPage")
	if len(entities) == 0 {
		return geoStatusInfo, "no FAQPage entity"
	}
	visible := geoNormalizedText(p.HTML)
	questions := 0
	missing := make([]string, 0, 2)
	for _, entity := range entities {
		for _, question := range geoFAQQuestions(entity) {
			questions++
			if !strings.Contains(visible, geoNormalizeMatch(question)) {
				missing = append(missing, question)
			}
		}
	}
	if questions == 0 {
		return geoStatusInfo, "FAQPage entity has no mainEntity questions"
	}
	if len(missing) > 0 {
		return geoStatusFail, geoEvidencef("%d FAQ question(s) do not appear in visible text: %s", len(missing), strings.Join(missing, " | "))
	}
	return geoStatusPass, geoEvidencef("%d FAQ question(s) match visible text", questions)
}

func checkPageTitle(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, title not evaluated"
	}
	title := strings.TrimSpace(p.HTML.Title)
	if title == "" {
		return geoStatusWarn, "no <title> element"
	}
	if duplicates := geoDuplicateTitleCount(a, p); duplicates > 1 {
		return geoStatusWarn, geoEvidencef("title is shared by %d audited pages: %s", duplicates, title)
	}
	return geoStatusPass, geoEvidencef("title (%d chars): %s", len([]rune(title)), title)
}

func checkPageMetaDescription(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, meta description not evaluated"
	}
	description := strings.TrimSpace(p.HTML.MetaDescription)
	if description == "" {
		return geoStatusWarn, "no meta description"
	}
	return geoStatusPass, geoEvidencef("meta description (%d chars): %s", len([]rune(description)), description)
}

func checkPageH1(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, H1 not evaluated"
	}
	if p.HTML.H1Count == 1 {
		return geoStatusPass, geoEvidencef("exactly one H1: %s", p.HTML.H1Text)
	}
	if p.HTML.H1Count == 0 {
		return geoStatusWarn, "no H1 element"
	}
	return geoStatusWarn, geoEvidencef("%d H1 elements; exactly one is expected", p.HTML.H1Count)
}

func checkPageLang(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, lang not evaluated"
	}
	if strings.TrimSpace(p.HTML.Lang) == "" {
		return geoStatusWarn, "<html> has no lang attribute"
	}
	return geoStatusPass, geoEvidencef("<html lang=\"%s\">", p.HTML.Lang)
}

func checkPageImageAlt(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, image alt not evaluated"
	}
	total := p.HTML.Images
	if total == 0 {
		return geoStatusInfo, "no <img> elements"
	}
	coverage := p.HTML.ImagesWithAlt * 100 / total
	if coverage < 80 {
		return geoStatusWarn, geoEvidencef("%d of %d images have alt text (%d%%)", p.HTML.ImagesWithAlt, total, coverage)
	}
	return geoStatusPass, geoEvidencef("%d of %d images have alt text (%d%%)", p.HTML.ImagesWithAlt, total, coverage)
}

func checkPageArticleDate(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, publication date not evaluated"
	}
	if !geoIsArticlePage(p) {
		return geoStatusInfo, "not an article-style page"
	}
	if date := strings.TrimSpace(p.HTML.VisibleDate); date != "" {
		return geoStatusPass, geoEvidencef("visible date: %s", date)
	}
	return geoStatusWarn, "article-style page has no visible date (<time> or a common date format)"
}

func checkAnswerFirstParagraph(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, answer-first structure not evaluated"
	}
	paragraph := p.HTML.FirstParagraph
	if strings.TrimSpace(paragraph) == "" {
		return geoStatusWarn, "no paragraph directly after the H1"
	}
	words := geoWordCount(paragraph)
	if words >= 40 && words <= 120 {
		return geoStatusPass, geoEvidencef("first paragraph after H1 has %d words (40-120)", words)
	}
	return geoStatusWarn, geoEvidencef("first paragraph after H1 has %d words; 40-120 is the recommended answer-first range", words)
}

func checkContentListsTables(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, lists and tables not evaluated"
	}
	return geoStatusInfo, geoEvidencef("%d list(s), %d table(s)", p.HTML.Lists, p.HTML.Tables)
}

func checkStatsDensity(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, statistics not evaluated"
	}
	words := p.HTML.Words
	stats := geoStatCount(p.HTML.Text)
	if words == 0 {
		return geoStatusInfo, "no visible text to measure"
	}
	density := float64(stats) * 1000 / float64(words)
	return geoStatusInfo, geoEvidencef("%.1f statistics per 1000 words (%d in %d words); sourced statistics correlate with AI citations", density, stats, words)
}

func checkQuotesInContent(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, quotes not evaluated"
	}
	return geoStatusInfo, geoEvidencef("%d quote(s) from blockquotes or long quoted passages; quotations correlate with AI citations", geoQuoteCount(p.HTML))
}

func checkExternalCitations(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, external citations not evaluated"
	}
	return geoStatusInfo, geoEvidencef("%d link(s) to external sites; citing sources correlates with AI citations", p.HTML.ExternalLinks)
}

func checkKeywordStuffing(a *geoAudit, p *geoPage) (string, string) {
	if p == nil || p.HTML == nil {
		return geoStatusInfo, "page was not fetched, keyword density not evaluated"
	}
	term, share, total := geoTopTerm(p.HTML.Text)
	if total < 100 || term == "" {
		return geoStatusInfo, geoEvidencef("too little text to measure keyword density (%d words)", total)
	}
	percent := share * 100
	if percent > 4 {
		return geoStatusWarn, geoEvidencef("term %q is %.1f%% of %d words; stuffing above 4%% does not improve AI visibility", term, percent, total)
	}
	return geoStatusPass, geoEvidencef("most frequent term %q is %.1f%% of %d words (limit 4%%)", term, percent, total)
}

func geoDuplicateTitleCount(a *geoAudit, p *geoPage) int {
	target := strings.ToLower(strings.TrimSpace(p.Title))
	if target == "" {
		return 0
	}
	count := 0
	for _, page := range a.auditedPages() {
		if strings.ToLower(strings.TrimSpace(page.Title)) == target {
			count++
		}
	}
	return count
}

func geoIsArticlePage(p *geoPage) bool {
	if p == nil {
		return false
	}
	if p.LD != nil && len(geoEntitiesOfType(p.LD, "Article", "BlogPosting", "NewsArticle")) > 0 {
		return true
	}
	parsed, err := url.Parse(p.URL)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	for _, prefix := range []string{"/blog/", "/news/", "/posts/"} {
		if strings.Contains(path, prefix) {
			return true
		}
	}
	return false
}

// --- robots meta controls ---

type geoRobotsControls struct {
	Noindex       bool
	Nosnippet     bool
	MaxSnippet    *int
	DataNosnippet int
}

func geoParseRobotsControls(doc *geoHTMLDoc) geoRobotsControls {
	controls := geoRobotsControls{DataNosnippet: doc.DataNosnippet}
	values := make([]string, 0, len(doc.MetaRobots)+len(doc.XRobots))
	values = append(values, doc.MetaRobots...)
	values = append(values, doc.XRobots...)
	for _, raw := range values {
		geoCollectRobotsDirectives(raw, &controls)
	}
	return controls
}

func geoCollectRobotsDirectives(raw string, controls *geoRobotsControls) {
	for _, part := range strings.Split(strings.ToLower(raw), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value := part, ""
		if index := strings.IndexByte(part, ':'); index >= 0 {
			key = strings.TrimSpace(part[:index])
			value = strings.TrimSpace(part[index+1:])
		}
		switch key {
		case "noindex":
			controls.Noindex = true
		case "nosnippet":
			controls.Nosnippet = true
		case "max-snippet":
			if number, err := strconv.Atoi(value); err == nil {
				snippet := number
				controls.MaxSnippet = &snippet
			}
		case "max-image-preview", "max-video-preview", "follow", "nofollow", "index", "all", "none", "notranslate", "noarchive", "nocache":
		default:
			// User-agent scoped directives such as "googlebot: noindex".
			if value != "" && geoIsUserAgentToken(key) {
				geoCollectRobotsDirectives(value, controls)
			}
		}
	}
}

func geoIsUserAgentToken(key string) bool {
	if key == "" || strings.ContainsAny(key, " \t") {
		return false
	}
	return strings.Contains(key, "bot") || key == "robots" || key == "slurp"
}

// --- JSON-LD ---

type geoJSONLDData struct {
	Blocks      int
	ParseErrors int
	Types       []string
	Entities    []map[string]any
}

func analyzeGeoJSONLD(blocks []string) *geoJSONLDData {
	data := &geoJSONLDData{Blocks: len(blocks)}
	for _, block := range blocks {
		var value any
		if err := common.Unmarshal([]byte(block), &value); err != nil {
			data.ParseErrors++
			continue
		}
		data.collect(value)
	}
	data.Types = uniqueGeoStrings(data.Types)
	sort.Strings(data.Types)
	return data
}

func (d *geoJSONLDData) collect(value any) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			d.collect(item)
		}
	case map[string]any:
		if graph, ok := typed["@graph"]; ok {
			d.collect(graph)
		}
		types := geoJSONTypes(typed)
		if len(types) == 0 {
			return
		}
		d.Entities = append(d.Entities, typed)
		d.Types = append(d.Types, types...)
	}
}

func geoJSONTypes(node map[string]any) []string {
	switch value := node["@type"].(type) {
	case string:
		return []string{strings.TrimSpace(value)}
	case []any:
		types := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				types = append(types, strings.TrimSpace(text))
			}
		}
		return types
	default:
		return nil
	}
}

func geoEntitiesOfType(data *geoJSONLDData, types ...string) []map[string]any {
	if data == nil {
		return nil
	}
	match := make(map[string]struct{}, len(types))
	for _, name := range types {
		match[strings.ToLower(name)] = struct{}{}
	}
	entities := make([]map[string]any, 0)
	for _, entity := range data.Entities {
		for _, entityType := range geoJSONTypes(entity) {
			if _, ok := match[strings.ToLower(entityType)]; ok {
				entities = append(entities, entity)
				break
			}
		}
	}
	return entities
}

func geoJSONScalar(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64, int, int64:
		if text, ok := StringValue(typed); ok {
			return text
		}
	}
	return ""
}

func geoJSONHasValue(entities []map[string]any, key string) bool {
	for _, entity := range entities {
		if geoJSONValuePresent(entity[key]) {
			return true
		}
	}
	return false
}

func geoJSONValuePresent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return true
	}
}

func geoOrgPhone(entity map[string]any) string {
	if phone := geoJSONScalar(entity["telephone"]); phone != "" {
		return phone
	}
	for _, contact := range geoJSONObjects(entity["contactPoint"]) {
		if phone := geoJSONScalar(contact["telephone"]); phone != "" {
			return phone
		}
	}
	return ""
}

func geoOrgAddress(entity map[string]any) string {
	switch address := entity["address"].(type) {
	case string:
		return strings.TrimSpace(address)
	case map[string]any:
		parts := []string{"streetAddress", "addressLocality", "addressRegion", "postalCode", "addressCountry"}
		values := make([]string, 0, len(parts))
		for _, key := range parts {
			if value := geoJSONScalar(address[key]); value != "" {
				values = append(values, value)
			}
		}
		return strings.Join(values, ", ")
	}
	return ""
}

func geoJSONObjects(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		objects := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				objects = append(objects, object)
			}
		}
		return objects
	}
	return nil
}

func geoFAQQuestions(entity map[string]any) []string {
	questions := make([]string, 0)
	for _, item := range geoJSONObjects(entity["mainEntity"]) {
		name := geoJSONScalar(item["name"])
		if name != "" {
			questions = append(questions, name)
		}
	}
	return questions
}

func geoNormalizedText(doc *geoHTMLDoc) string {
	if doc == nil {
		return ""
	}
	return strings.ToLower(strings.Join(strings.Fields(doc.Text), " "))
}

func geoNormalizeMatch(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func uniqueGeoStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
