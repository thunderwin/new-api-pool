package capability

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// geoHTMLDoc is the deterministic, no-JavaScript view of one page. Only data
// needed by the check tables is kept; raw HTML never leaves this parser.
type geoHTMLDoc struct {
	Title           string
	MetaDescription string
	Lang            string
	Canonical       string
	H1Count         int
	H1Text          string
	FirstParagraph  string
	MetaRobots      []string
	XRobots         []string
	DataNosnippet   int
	Text            string
	Words           int
	Images          int
	ImagesWithAlt   int
	Lists           int
	Tables          int
	Blockquotes     int
	ExternalLinks   int
	SPAMount        bool
	JSONLD          []string
	TimeDate        string
	VisibleDate     string
}

var geoSkipSubtreeTags = map[string]struct{}{
	"script": {}, "style": {}, "noscript": {}, "template": {}, "svg": {}, "iframe": {}, "canvas": {},
}

var geoBlockTextTags = map[string]struct{}{
	"p": {}, "div": {}, "br": {}, "li": {}, "ul": {}, "ol": {}, "dl": {}, "dt": {}, "dd": {},
	"tr": {}, "td": {}, "th": {}, "table": {}, "thead": {}, "tbody": {},
	"section": {}, "article": {}, "aside": {}, "header": {}, "footer": {}, "main": {}, "nav": {},
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {}, "blockquote": {}, "pre": {},
	"figure": {}, "figcaption": {},
}

var geoSPAMountIDs = map[string]struct{}{
	"root": {}, "app": {}, "__next": {}, "__nuxt": {}, "___gatsby": {}, "svelte": {},
}

type geoWalkState struct {
	doc               *geoHTMLDoc
	base              *url.URL
	site              string
	inBody            bool
	text              strings.Builder
	sawH1             bool
	paragraphCaptured bool
}

func parseGeoHTML(body []byte, pageURL string, site string, xRobots []string) *geoHTMLDoc {
	doc := &geoHTMLDoc{XRobots: xRobots}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return doc
	}
	base, _ := url.Parse(pageURL)
	state := &geoWalkState{doc: doc, base: base, site: site}
	walkGeoDOM(root, state)
	doc.Text = geoCleanText(state.text.String())
	doc.Words = geoWordCount(doc.Text)
	doc.H1Text = geoCleanText(doc.H1Text)
	doc.FirstParagraph = geoCleanText(doc.FirstParagraph)
	doc.Title = geoCleanText(doc.Title)
	doc.MetaDescription = geoCleanText(doc.MetaDescription)
	doc.Canonical = strings.TrimSpace(doc.Canonical)
	doc.VisibleDate = geoFindVisibleDate(doc.Text, doc.TimeDate)
	return doc
}

func walkGeoDOM(node *html.Node, state *geoWalkState) {
	if node.Type == html.TextNode {
		if state.inBody {
			state.text.WriteString(node.Data)
		}
		return
	}
	if node.Type == html.ElementNode {
		tag := strings.ToLower(node.Data)
		if tag == "body" {
			state.inBody = true
			defer func() { state.inBody = false }()
		}
		if tag == "script" && isGeoJSONLD(node) {
			if content := geoRawText(node); strings.TrimSpace(content) != "" {
				state.doc.JSONLD = append(state.doc.JSONLD, content)
			}
			return
		}
		if _, skip := geoSkipSubtreeTags[tag]; skip {
			return
		}
		geoCollectElement(node, tag, state)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkGeoDOM(child, state)
	}
	if node.Type == html.ElementNode {
		if _, isBlock := geoBlockTextTags[strings.ToLower(node.Data)]; isBlock {
			state.text.WriteByte(' ')
		}
	}
}

func geoCollectElement(node *html.Node, tag string, state *geoWalkState) {
	doc := state.doc
	switch tag {
	case "html":
		if lang := geoAttr(node, "lang"); lang != "" && doc.Lang == "" {
			doc.Lang = lang
		}
	case "title":
		if doc.Title == "" {
			doc.Title = geoNodeText(node)
		}
	case "meta":
		name := strings.ToLower(strings.TrimSpace(geoAttr(node, "name")))
		content := strings.TrimSpace(geoAttr(node, "content"))
		switch name {
		case "description":
			if doc.MetaDescription == "" {
				doc.MetaDescription = content
			}
		case "robots", "googlebot", "bingbot", "slurp":
			if content != "" {
				doc.MetaRobots = append(doc.MetaRobots, name+": "+content)
			}
		}
	case "link":
		if doc.Canonical == "" && strings.Contains(strings.ToLower(geoAttr(node, "rel")), "canonical") {
			doc.Canonical = strings.TrimSpace(geoAttr(node, "href"))
		}
	case "h1":
		doc.H1Count++
		if doc.H1Text == "" {
			doc.H1Text = geoNodeText(node)
		}
		state.sawH1 = true
	case "p":
		if state.sawH1 && !state.paragraphCaptured {
			state.paragraphCaptured = true
			doc.FirstParagraph = geoNodeText(node)
		}
	case "img":
		doc.Images++
		if strings.TrimSpace(geoAttr(node, "alt")) != "" {
			doc.ImagesWithAlt++
		}
	case "ul", "ol":
		doc.Lists++
	case "table":
		doc.Tables++
	case "blockquote":
		doc.Blockquotes++
	case "time":
		if value := strings.TrimSpace(geoAttr(node, "datetime")); value != "" {
			doc.TimeDate = value
		} else if text := geoCleanText(geoNodeText(node)); text != "" {
			doc.TimeDate = text
		}
	case "a":
		geoCountExternalLink(node, state)
	}
	if _, ok := geoAttrValue(node, "data-nosnippet"); ok {
		doc.DataNosnippet++
	}
	if id := strings.ToLower(strings.TrimSpace(geoAttr(node, "id"))); id != "" {
		if _, ok := geoSPAMountIDs[id]; ok {
			doc.SPAMount = true
		}
	}
}

func geoCountExternalLink(node *html.Node, state *geoWalkState) {
	href := strings.TrimSpace(geoAttr(node, "href"))
	if href == "" || state.base == nil {
		return
	}
	resolved, err := state.base.Parse(href)
	if err != nil || resolved.Hostname() == "" {
		return
	}
	if !strings.EqualFold(resolved.Scheme, "https") && !strings.EqualFold(resolved.Scheme, "http") {
		return
	}
	host := strings.TrimSuffix(strings.ToLower(resolved.Hostname()), ".")
	if host == "" || host == strings.TrimSuffix(strings.ToLower(state.base.Hostname()), ".") {
		return
	}
	if geoSameSite(host, state.site) {
		return
	}
	state.doc.ExternalLinks++
}

func isGeoJSONLD(node *html.Node) bool {
	contentType := strings.ToLower(strings.TrimSpace(geoAttr(node, "type")))
	return strings.Contains(contentType, "ld+json")
}

func geoAttr(node *html.Node, name string) string {
	value, _ := geoAttrValue(node, name)
	return value
}

func geoAttrValue(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val, true
		}
	}
	return "", false
}

// geoNodeText collects the text of a small subtree (title, h1, paragraph,
// JSON-LD script). It deliberately skips script/style/noscript content.
func geoNodeText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			return
		}
		if current.Type != html.ElementNode {
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			return
		}
		tag := strings.ToLower(current.Data)
		if tag == "script" || tag == "style" || tag == "noscript" || tag == "template" {
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if _, isBlock := geoBlockTextTags[tag]; isBlock {
			builder.WriteByte(' ')
		}
	}
	walk(node)
	return builder.String()
}

func geoCleanText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func geoRawText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

var geoCJKRange = func(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

// geoWordCount counts Latin word tokens and estimates CJK words as two
// characters per word.
func geoWordCount(text string) int {
	latin := 0
	cjk := 0
	inWord := false
	for _, r := range text {
		switch {
		case geoCJKRange(r):
			cjk++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				latin++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return latin + cjk/2
}

var (
	geoDateNumericPattern = regexp.MustCompile(`\b20\d{2}[-/.]\d{1,2}[-/.]\d{1,2}\b`)
	geoDateMonthFirst     = regexp.MustCompile(`\b(?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},?\s+20\d{2}\b`)
	geoDateDayFirst       = regexp.MustCompile(`\b\d{1,2}\s+(?:January|February|March|April|May|June|July|August|September|October|November|December)\s+20\d{2}\b`)
	geoDateCJKPattern     = regexp.MustCompile(`20\d{2}\s*年\s*\d{1,2}\s*月(?:\s*\d{1,2}\s*日)?`)
)

func geoFindVisibleDate(text string, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	for _, pattern := range []*regexp.Regexp{geoDateNumericPattern, geoDateMonthFirst, geoDateDayFirst, geoDateCJKPattern} {
		if match := pattern.FindString(text); match != "" {
			return strings.TrimSpace(match)
		}
	}
	return ""
}

var geoStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {}, "from": {}, "your": {},
	"you": {}, "our": {}, "are": {}, "was": {}, "were": {}, "has": {}, "have": {}, "had": {},
	"not": {}, "but": {}, "all": {}, "can": {}, "will": {}, "one": {}, "more": {}, "most": {},
	"how": {}, "what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {}, "into": {},
	"its": {}, "their": {}, "they": {}, "them": {}, "these": {}, "those": {}, "there": {}, "here": {},
	"about": {}, "also": {}, "than": {}, "then": {}, "use": {}, "using": {}, "used": {}, "get": {},
	"may": {}, "should": {}, "would": {}, "could": {}, "does": {}, "did": {}, "been": {}, "being": {},
	"each": {}, "other": {}, "some": {}, "such": {}, "only": {}, "very": {}, "just": {}, "like": {},
}

var geoTermPattern = regexp.MustCompile(`[a-z][a-z0-9'-]{2,}`)

// geoTopTerm returns the most frequent non-stopword Latin term and its share of
// all Latin words.
func geoTopTerm(text string) (string, float64, int) {
	matches := geoTermPattern.FindAllString(strings.ToLower(text), -1)
	total := len(matches)
	if total == 0 {
		return "", 0, 0
	}
	counts := map[string]int{}
	best := ""
	bestCount := 0
	for _, term := range matches {
		if _, stop := geoStopwords[term]; stop {
			continue
		}
		counts[term]++
		if counts[term] > bestCount || (counts[term] == bestCount && (best == "" || term < best)) {
			best = term
			bestCount = counts[term]
		}
	}
	if best == "" {
		return "", 0, total
	}
	return best, float64(bestCount) / float64(total), total
}

var geoStatPattern = regexp.MustCompile(`(?i)\b\d[\d,.]*\s?(?:%|percent|million|billion|trillion|thousand|万|亿|倍|times|x\b|usd|dollars?|\$|mb|gb|kb|seconds?|minutes?|hours?|days?|weeks?|months?|years?)`)

func geoStatCount(text string) int {
	return len(geoStatPattern.FindAllString(text, -1))
}

var geoQuotePattern = regexp.MustCompile(`["“][^"”]{40,}["”]`)

func geoQuoteCount(doc *geoHTMLDoc) int {
	if doc == nil {
		return 0
	}
	return doc.Blockquotes + len(geoQuotePattern.FindAllString(doc.Text, -1))
}
