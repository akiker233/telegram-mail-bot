package mail

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// blankLinesRe 把连续 3 个以上的换行压缩成 2 个（最多保留一个空行）。
// 邮件模板里常见的空 <div>/<p> 占位符会导致 structuralTags 堆叠出大量连续换行，
// 在 Telegram 里表现为一大段空白区域。先用 normalizeWS 抹掉换行之间的空白字符，
// 否则 `\n  \t\n  \n` 不会被 \n{3,} 捕获。
var blankLinesRe = regexp.MustCompile(`\n{3,}`)

// normalizeWS 把换行之间的空白字符（空格、制表符等）清除，使 \n{3,} 能准确匹配。
var normalizeWS = regexp.MustCompile(`[\t ]*\n[\t ]*`)

// inlineTags 把邮件里常见的富文本标签映射为 Telegram parse_mode=HTML 支持的标签。
var inlineTags = map[string]string{
	"b": "b", "strong": "b",
	"i": "i", "em": "i",
	"u": "u", "ins": "u",
	"s": "s", "strike": "s", "del": "s",
	"code": "code",
	"pre":  "pre",
}

// structuralTags 转换为换行，不保留标签本身。
var structuralTags = map[string]bool{
	"p": true, "div": true, "li": true, "tr": true,
}

// skippedTags 整个子树都不输出（标签本身和内部文字都丢弃）。
var skippedTags = map[string]bool{
	"script": true, "style": true, "head": true,
}

// htmlRenderer 把邮件 HTML 渲染成 Telegram 安全 HTML 子集，同时在达到可见字符
// 上限时安全截断（不会切到标签中间）。
type htmlRenderer struct {
	sb        strings.Builder
	remaining int
	truncated bool
}

// htmlToTelegramHTML 把邮件 HTML 转换为 Telegram parse_mode=HTML 可以安全渲染的子集。
// ok=false 表示转换结果为空（例如邮件正文只有图片/脚本），调用方应回退到纯文本处理。
func htmlToTelegramHTML(rawHTML string, maxVisibleRunes int) (result string, ok bool) {
	root, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", false
	}

	r := &htmlRenderer{remaining: maxVisibleRunes}
	r.renderChildren(root)

	// 先规范化换行之间的空白字符，再压缩连续空行。两步处理保证像
	// `\n  \t\n\n  \n` 这样的混合空白也能被正确压缩为一个空行。
	raw := r.sb.String()
	raw = normalizeWS.ReplaceAllString(raw, "\n")
	out := strings.TrimSpace(blankLinesRe.ReplaceAllString(raw, "\n\n"))
	if r.truncated {
		out += "..."
	}
	if out == "" {
		return "", false
	}
	return out, true
}

func (r *htmlRenderer) renderChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r.truncated {
			return
		}
		r.renderNode(c)
	}
}

func (r *htmlRenderer) renderNode(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		r.writeText(n.Data)

	case html.ElementNode:
		tag := strings.ToLower(n.Data)

		if skippedTags[tag] {
			return
		}

		if canonical, ok := inlineTags[tag]; ok {
			r.sb.WriteString("<" + canonical + ">")
			r.renderChildren(n)
			r.sb.WriteString("</" + canonical + ">")
			return
		}

		if tag == "a" {
			r.renderAnchor(n)
			return
		}

		if tag == "br" {
			if !r.truncated {
				r.sb.WriteString("\n")
			}
			return
		}

		if structuralTags[tag] {
			r.renderChildren(n)
			if !r.truncated {
				r.sb.WriteString("\n")
			}
			return
		}

		// 未知标签：剥壳保留内部文字。
		r.renderChildren(n)
	}
}

func (r *htmlRenderer) renderAnchor(n *html.Node) {
	href, hasHref := attrValue(n, "href")
	if hasHref && isSafeHref(href) {
		r.sb.WriteString(`<a href="` + html.EscapeString(href) + `">`)
		r.renderChildren(n)
		r.sb.WriteString("</a>")
		return
	}
	// 没有合法 href：不保留链接，只保留文字。
	r.renderChildren(n)
}

func (r *htmlRenderer) writeText(text string) {
	if r.truncated || r.remaining <= 0 {
		r.truncated = true
		return
	}

	runes := []rune(text)
	if len(runes) > r.remaining {
		runes = runes[:r.remaining]
		r.truncated = true
	}
	r.remaining -= len(runes)

	r.sb.WriteString(html.EscapeString(string(runes)))
}

func attrValue(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func isSafeHref(href string) bool {
	href = strings.TrimSpace(href)
	lower := strings.ToLower(href)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:")
}
