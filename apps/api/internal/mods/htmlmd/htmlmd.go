// Package htmlmd converts the simple author-written HTML that plugin/mod
// registries serve (CurseForge changelogs, SpigotMC resource descriptions)
// into Markdown. The frontend renders Markdown only, so HTML is converted
// rather than passed through (also avoids handing author-controlled HTML to
// the browser).
package htmlmd

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// ToMarkdown converts simple HTML (headings, paragraphs, lists, links, inline
// styling, code) into Markdown. Unknown tags contribute only their text
// content; plain-text input passes through unchanged.
func ToMarkdown(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return strings.TrimSpace(src)
	}
	return tidyMarkdown(nodeMD(doc, mdCtx{}))
}

type mdCtx struct {
	listDepth int
	pre       bool
}

func nodeMD(n *html.Node, ctx mdCtx) string {
	switch n.Type {
	case html.TextNode:
		if ctx.pre {
			return n.Data
		}
		return wsRe.ReplaceAllString(n.Data, " ")
	case html.DocumentNode:
		return childrenMD(n, ctx)
	case html.ElementNode:
	default:
		return ""
	}

	switch n.Data {
	case "script", "style", "head":
		return ""
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		return "\n\n" + strings.Repeat("#", level) + " " + strings.TrimSpace(childrenMD(n, ctx)) + "\n\n"
	case "p", "div":
		return "\n\n" + strings.TrimSpace(childrenMD(n, ctx)) + "\n\n"
	case "br":
		return "\n"
	case "hr":
		return "\n\n---\n\n"
	case "ul", "ol":
		return "\n\n" + listMD(n, ctx) + "\n\n"
	case "a":
		text := strings.TrimSpace(childrenMD(n, ctx))
		if href := attr(n, "href"); href != "" && text != "" {
			return "[" + text + "](" + href + ")"
		}
		return text
	case "img":
		if src := attr(n, "src"); src != "" {
			return "![" + attr(n, "alt") + "](" + src + ")"
		}
		return ""
	case "strong", "b":
		if inner := strings.TrimSpace(childrenMD(n, ctx)); inner != "" {
			return "**" + inner + "**"
		}
		return ""
	case "em", "i":
		if inner := strings.TrimSpace(childrenMD(n, ctx)); inner != "" {
			return "*" + inner + "*"
		}
		return ""
	case "code":
		if ctx.pre {
			return childrenMD(n, ctx)
		}
		if inner := strings.TrimSpace(childrenMD(n, ctx)); inner != "" {
			return "`" + inner + "`"
		}
		return ""
	case "pre":
		ctx.pre = true
		return "\n\n```\n" + strings.Trim(childrenMD(n, ctx), "\n") + "\n```\n\n"
	case "blockquote":
		inner := strings.TrimSpace(childrenMD(n, ctx))
		lines := strings.Split(inner, "\n")
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return "\n\n" + strings.Join(lines, "\n") + "\n\n"
	default:
		return childrenMD(n, ctx)
	}
}

func childrenMD(n *html.Node, ctx mdCtx) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeMD(c, ctx))
	}
	return b.String()
}

// listMD renders a ul/ol; nested lists inside an item land on continuation
// lines indented under their parent bullet.
func listMD(n *html.Node, ctx mdCtx) string {
	indent := strings.Repeat("  ", ctx.listDepth)
	inner := mdCtx{listDepth: ctx.listDepth + 1}
	var b strings.Builder
	idx := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			continue
		}
		content := strings.TrimSpace(blankRe.ReplaceAllString(childrenMD(c, inner), "\n"))
		if content == "" {
			continue
		}
		idx++
		marker := "- "
		if n.Data == "ol" {
			marker = fmt.Sprintf("%d. ", idx)
		}
		lines := strings.Split(content, "\n")
		b.WriteString(indent + marker + lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString(indent + "  " + strings.TrimLeft(l, " ") + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

var (
	wsRe    = regexp.MustCompile(`\s+`)
	blankRe = regexp.MustCompile(`\n\s*\n+`)
)

// tidyMarkdown collapses the blank-line runs block rendering leaves behind.
func tidyMarkdown(s string) string {
	s = strings.TrimSpace(blankRe.ReplaceAllString(s, "\n\n"))
	return s
}
