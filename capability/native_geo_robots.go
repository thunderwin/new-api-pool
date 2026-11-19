package capability

import (
	"encoding/xml"
	"sort"
	"strings"
	"time"
)

// geoRobotsFile is the RFC 9309 view of robots.txt plus the Cloudflare
// Content-Signal and Sitemap extensions. Parsing is deliberately deterministic:
// groups, longest matching rule and Allow-wins-on-tie.
type geoRobotsFile struct {
	Found          bool
	Status         int
	Err            string
	Groups         []geoRobotsGroup
	Sitemaps       []string
	ContentSignals []string
}

type geoRobotsGroup struct {
	Agents       []string
	Rules        []geoRobotsRule
	startedRules bool
}

type geoRobotsRule struct {
	Allow   bool
	Pattern string
}

func parseGeoRobots(body string) *geoRobotsFile {
	file := &geoRobotsFile{Found: true}
	var current *geoRobotsGroup
	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(stripGeoRobotsComment(rawLine))
		if line == "" {
			continue
		}
		key, value, ok := splitGeoDirective(line)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "user-agent", "useragent":
			value = strings.TrimSpace(value)
			if current == nil || current.startedRules {
				current = &geoRobotsGroup{}
				file.Groups = append(file.Groups, geoRobotsGroup{})
				index := len(file.Groups) - 1
				current = &file.Groups[index]
			}
			if value != "" {
				current.Agents = append(current.Agents, value)
			}
		case "allow", "disallow":
			if current == nil || len(current.Agents) == 0 {
				continue
			}
			current.Rules = append(current.Rules, geoRobotsRule{
				Allow:   strings.EqualFold(key, "allow"),
				Pattern: strings.TrimSpace(value),
			})
			current.startedRules = true
		case "sitemap":
			if value = strings.TrimSpace(value); value != "" {
				file.Sitemaps = append(file.Sitemaps, value)
			}
		case "content-signal":
			if value = strings.TrimSpace(value); value != "" {
				file.ContentSignals = append(file.ContentSignals, value)
			}
		}
	}
	return file
}

func stripGeoRobotsComment(line string) string {
	if index := strings.IndexByte(line, '#'); index >= 0 {
		return line[:index]
	}
	return line
}

func splitGeoDirective(line string) (string, string, bool) {
	index := strings.IndexByte(line, ':')
	if index <= 0 {
		return "", "", false
	}
	return line[:index], line[index+1:], true
}

// geoRobotsRulesFor returns the rules that apply to one crawler token. The most
// specific matching user-agent group wins; groups sharing that token are merged.
// Without a matching group the crawler is allowed.
func (f *geoRobotsFile) geoRobotsRulesFor(agent string) (rules []geoRobotsRule, matched string) {
	if f == nil {
		return nil, ""
	}
	agent = strings.ToLower(strings.TrimSpace(agent))
	bestLen := -1
	bestToken := ""
	for _, group := range f.Groups {
		for _, token := range group.Agents {
			token = strings.ToLower(strings.TrimSpace(token))
			if token == "" {
				continue
			}
			matches := token == "*" || strings.Contains(agent, token)
			if !matches {
				continue
			}
			specificity := 0
			if token != "*" {
				specificity = len(token)
			}
			if specificity > bestLen {
				bestLen = specificity
				bestToken = token
			}
		}
	}
	if bestLen < 0 {
		return nil, ""
	}
	for _, group := range f.Groups {
		for _, token := range group.Agents {
			if strings.EqualFold(strings.TrimSpace(token), bestToken) {
				rules = append(rules, group.Rules...)
				break
			}
		}
	}
	return rules, bestToken
}

func (f *geoRobotsFile) geoRobotsAllows(agent string, path string) (bool, string) {
	if f == nil || !f.Found {
		return true, ""
	}
	rules, _ := f.geoRobotsRulesFor(agent)
	if len(rules) == 0 {
		return true, ""
	}
	if path == "" {
		path = "/"
	}
	bestLen := -1
	bestAllow := true
	bestPattern := ""
	for _, rule := range rules {
		if rule.Pattern == "" || !geoRobotsPatternMatches(rule.Pattern, path) {
			continue
		}
		length := len(rule.Pattern)
		if length > bestLen || (length == bestLen && rule.Allow) {
			bestLen = length
			bestAllow = rule.Allow
			bestPattern = rule.Pattern
		}
	}
	if bestLen < 0 {
		return true, ""
	}
	return bestAllow, bestPattern
}

// geoRobotsPatternMatches implements RFC 9309 path matching: '*' matches any
// sequence, a trailing '$' anchors the end, otherwise the pattern is a prefix.
func geoRobotsPatternMatches(pattern string, path string) bool {
	if pattern == "" {
		return false
	}
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	segments := strings.Split(pattern, "*")
	position := 0
	if segments[0] != "" {
		if !strings.HasPrefix(path, segments[0]) {
			return false
		}
		position = len(segments[0])
	}
	for _, segment := range segments[1:] {
		if segment == "" {
			continue
		}
		index := strings.Index(path[position:], segment)
		if index < 0 {
			return false
		}
		position += index + len(segment)
	}
	if anchored {
		return position == len(path)
	}
	return true
}

func (f *geoRobotsFile) geoDeprecatedTokens() []string {
	if f == nil {
		return nil
	}
	deprecated := []string{"anthropic-ai", "claude-web"}
	found := make([]string, 0, len(deprecated))
	for _, group := range f.Groups {
		for _, token := range group.Agents {
			for _, name := range deprecated {
				if strings.EqualFold(strings.TrimSpace(token), name) {
					found = append(found, name)
				}
			}
		}
	}
	return uniqueGeoStrings(found)
}

type geoSitemapEntry struct {
	Loc     string
	LastMod string
}

type geoSitemapXML struct {
	XMLName  xml.Name
	URLs     []geoSitemapXMLLoc `xml:"url"`
	Sitemaps []geoSitemapXMLLoc `xml:"sitemap"`
}

type geoSitemapXMLLoc struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

// parseGeoSitemap reads urlset and sitemapindex documents. It never expands
// children itself; the caller enforces the one-level and child-count limits.
func parseGeoSitemap(body []byte) (entries []geoSitemapEntry, children []string, err error) {
	var doc geoSitemapXML
	if unmarshalErr := xml.Unmarshal(body, &doc); unmarshalErr != nil {
		return nil, nil, unmarshalErr
	}
	for _, item := range doc.URLs {
		loc := strings.TrimSpace(item.Loc)
		if loc == "" {
			continue
		}
		entries = append(entries, geoSitemapEntry{Loc: loc, LastMod: strings.TrimSpace(item.LastMod)})
	}
	for _, item := range doc.Sitemaps {
		loc := strings.TrimSpace(item.Loc)
		if loc == "" {
			continue
		}
		children = append(children, loc)
	}
	return entries, children, nil
}

func parseGeoLastmod(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05-0700",
		"2006-01-02",
		"2006-01",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// selectGeoSitemapPages spreads picks across path prefixes instead of taking
// the first N URLs, which are usually one blog's newest posts.
func selectGeoSitemapPages(entries []geoSitemapEntry, site string, homepagePath string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	byPrefix := map[string][]string{}
	var prefixes []string
	seen := map[string]struct{}{}
	for _, entry := range entries {
		host, path := geoSplitURLPath(entry.Loc)
		if host == "" || !geoSameSite(host, site) {
			continue
		}
		if path == homepagePath {
			continue
		}
		if _, exists := seen[entry.Loc]; exists {
			continue
		}
		seen[entry.Loc] = struct{}{}
		prefix := geoPathPrefix(path)
		if _, exists := byPrefix[prefix]; !exists {
			prefixes = append(prefixes, prefix)
		}
		byPrefix[prefix] = append(byPrefix[prefix], entry.Loc)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		sort.Strings(byPrefix[prefix])
	}
	picked := make([]string, 0, limit)
	for round := 0; len(picked) < limit; round++ {
		advanced := false
		for _, prefix := range prefixes {
			urls := byPrefix[prefix]
			if round >= len(urls) {
				continue
			}
			advanced = true
			picked = append(picked, urls[round])
			if len(picked) >= limit {
				break
			}
		}
		if !advanced {
			break
		}
	}
	return picked
}

func geoPathPrefix(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "/"
	}
	if index := strings.IndexByte(trimmed, '/'); index >= 0 {
		return "/" + trimmed[:index] + "/"
	}
	return "/" + trimmed
}

func geoSplitURLPath(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "https://") {
		return "", ""
	}
	rest := trimmed[len("https://"):]
	slash := strings.IndexByte(rest, '/')
	host := rest
	path := "/"
	if slash >= 0 {
		host = rest[:slash]
		path = rest[slash:]
		if index := strings.IndexByte(path, '?'); index >= 0 {
			path = path[:index]
		}
		if index := strings.IndexByte(path, '#'); index >= 0 {
			path = path[:index]
		}
	}
	if host == "" || strings.Contains(host, "@") {
		return "", ""
	}
	return strings.ToLower(host), path
}
