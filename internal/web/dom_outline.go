package web

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

func htmlToDOMOutline(reader io.Reader) string {
	doc, err := html.Parse(reader)
	if err != nil {
		return ""
	}
	var lines []string
	var walk func(*html.Node, int)
	walk = func(n *html.Node, depth int) {
		if n.Type == html.ElementNode {
			if !shouldSkipOutlineNode(n) {
				lines = append(lines, strings.Repeat("  ", depth)+outlineNodeLabel(n))
				depth++
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, depth)
		}
	}
	walk(doc, 0)
	if len(lines) > 400 {
		lines = append(lines[:400], "...[outline truncated]")
	}
	return strings.Join(lines, "\n")
}

func shouldSkipOutlineNode(n *html.Node) bool {
	switch strings.ToLower(n.Data) {
	case "script", "style", "noscript", "template", "meta", "link", "head":
		return true
	default:
		return false
	}
}

func outlineNodeLabel(n *html.Node) string {
	parts := []string{"<" + n.Data + ">"}
	for _, attr := range n.Attr {
		key := strings.ToLower(attr.Key)
		value := strings.TrimSpace(attr.Val)
		if value == "" {
			continue
		}
		switch key {
		case "id":
			parts = append(parts, "#"+value)
		case "class":
			parts = append(parts, "."+strings.Join(strings.Fields(value), "."))
		case "role", "aria-label", "name", "type", "href":
			parts = append(parts, fmt.Sprintf("%s=%q", key, truncateText(value, 80)))
		}
	}
	label := strings.Join(parts, " ")
	text := directNodeText(n)
	if text != "" {
		label += " — " + truncateText(text, 120)
	}
	return label
}

func directNodeText(n *html.Node) string {
	var chunks []string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			text := strings.Join(strings.Fields(child.Data), " ")
			if text != "" {
				chunks = append(chunks, text)
			}
		}
	}
	return strings.Join(chunks, " ")
}
