package web

import (
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var whitespaceRE = regexp.MustCompile(`\s+`)

type readabilityExtractor struct {
	lines   []string
	current strings.Builder
}

func htmlToReadableText(r io.Reader) string {
	doc, err := html.Parse(r)
	if err != nil {
		return ""
	}
	extractor := &readabilityExtractor{}
	extractor.walk(doc, false)
	extractor.flushLine(false)
	return cleanupReadableLines(extractor.lines)
}

func (e *readabilityExtractor) walk(n *html.Node, skip bool) {
	if n == nil {
		return
	}

	name := ""
	prefix := ""
	if n.Type == html.ElementNode {
		name = strings.ToLower(n.Data)
		if isNoiseElement(name) || hasNoiseAttrs(n) {
			skip = true
		}
		if !skip {
			switch {
			case isHeadingElement(name):
				e.startBlock()
				prefix = strings.Repeat("#", headingLevel(name)) + " "
				e.writeText(prefix)
			case name == "li":
				e.startBlock()
				prefix = "- "
				e.writeText(prefix)
			case name == "br":
				e.flushLine(false)
			case isBlockElement(name):
				e.startBlock()
			}
		}
	}

	if !skip && n.Type == html.TextNode {
		e.writeText(n.Data)
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		e.walk(child, skip)
	}

	if !skip && n.Type == html.ElementNode {
		if isBlockElement(name) || isHeadingElement(name) || name == "li" {
			e.flushLine(true)
		}
	}
}

func (e *readabilityExtractor) writeText(text string) {
	text = normalizeSpace(html.UnescapeString(text))
	if text == "" {
		return
	}
	if e.current.Len() > 0 && !strings.HasSuffix(e.current.String(), " ") && !isPunctuationPrefix(text) {
		e.current.WriteByte(' ')
	}
	e.current.WriteString(text)
}

func (e *readabilityExtractor) startBlock() {
	e.flushLine(true)
}

func (e *readabilityExtractor) flushLine(addBlank bool) {
	line := normalizeSpace(e.current.String())
	if line != "" {
		e.lines = append(e.lines, line)
		e.current.Reset()
	}
	if addBlank && len(e.lines) > 0 && e.lines[len(e.lines)-1] != "" {
		e.lines = append(e.lines, "")
	}
}

func cleanupReadableLines(lines []string) string {
	var out []string
	blank := true
	for _, line := range lines {
		line = normalizeSpace(stripTags(line))
		if line == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isNoiseElement(name string) bool {
	switch name {
	case "head", "title", "meta", "link", "script", "style", "noscript", "template", "svg", "canvas", "iframe", "nav", "footer", "header", "aside", "form", "button", "input", "textarea", "select", "option":
		return true
	default:
		return false
	}
}

func hasNoiseAttrs(n *html.Node) bool {
	for _, attr := range n.Attr {
		key := strings.ToLower(attr.Key)
		value := strings.ToLower(attr.Val)
		if key == "hidden" || key == "aria-hidden" && value == "true" {
			return true
		}
		if key == "class" || key == "id" || key == "role" {
			for _, token := range []string{"cookie", "banner", "advert", "ad-", "ads", "promo", "subscribe", "newsletter", "nav", "menu", "sidebar", "footer", "header", "modal", "popup", "social", "share", "sponsor", "related"} {
				if strings.Contains(value, token) {
					return true
				}
			}
		}
	}
	return false
}

func isBlockElement(name string) bool {
	switch name {
	case "address", "article", "blockquote", "body", "div", "dl", "fieldset", "figcaption", "figure", "hr", "main", "ol", "p", "pre", "section", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func isHeadingElement(name string) bool {
	return name == "h1" || name == "h2" || name == "h3" || name == "h4" || name == "h5" || name == "h6"
}

func headingLevel(name string) int {
	if len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6' {
		return int(name[1] - '0')
	}
	return 1
}

func isPunctuationPrefix(text string) bool {
	if text == "" {
		return false
	}
	switch []rune(text)[0] {
	case '.', ',', ':', ';', '!', '?', ')', ']', '}':
		return true
	default:
		return false
	}
}

func normalizeSpace(text string) string {
	return strings.TrimSpace(whitespaceRE.ReplaceAllString(text, " "))
}

func stripTags(text string) string {
	var b strings.Builder
	inTag := false
	for _, r := range text {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return html.UnescapeString(b.String())
}
