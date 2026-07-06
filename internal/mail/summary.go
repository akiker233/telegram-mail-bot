package mail

import (
	"io"
	"regexp"
	"strings"

	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"

	// 注册字符集解码器（GBK/GB2312 等国内邮箱常用编码），仅需导入即生效。
	_ "github.com/emersion/go-message/charset"
)

const summaryMaxRunes = 500

// Summary 是一封邮件的摘要信息，用于推送到 Telegram。
type Summary struct {
	From    string
	Subject string
	Body    string
	// BodyIsHTML 为 true 时，Body 是 Telegram parse_mode=HTML 可安全渲染的子集；
	// 为 false 时，Body 是纯文本，发送时不应带 parse_mode。
	BodyIsHTML bool
}

var htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
var htmlWhitespaceRe = regexp.MustCompile(`[ \t]*\n[ \t]*`)

// BuildSummary 解析原始邮件（RFC 5322），提取发件人、主题和正文摘要。
func BuildSummary(raw io.Reader) (*Summary, error) {
	reader, err := mail.CreateReader(raw)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	summary := &Summary{
		From:    formatFrom(reader.Header),
		Subject: subjectOrDefault(reader.Header),
	}

	var plainBody, htmlBody string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()
			data, readErr := io.ReadAll(part.Body)
			if readErr != nil {
				continue
			}
			switch contentType {
			case "text/plain":
				if plainBody == "" {
					plainBody = string(data)
				}
			case "text/html":
				if htmlBody == "" {
					htmlBody = string(data)
				}
			}
		default:
			// 附件：按需求不处理附件内容。
		}
	}

	if htmlBody != "" {
		if rendered, ok := htmlToTelegramHTML(htmlBody, summaryMaxRunes); ok {
			summary.Body = rendered
			summary.BodyIsHTML = true
			return summary, nil
		}
	}

	if plainBody != "" {
		truncated := truncate(strings.TrimSpace(plainBody), summaryMaxRunes)
		if linked, hasURL := autoLinkURLs(truncated); hasURL {
			summary.Body = linked
			summary.BodyIsHTML = true
		} else {
			summary.Body = truncated
		}
		return summary, nil
	}

	if htmlBody != "" {
		summary.Body = truncate(strings.TrimSpace(htmlToText(htmlBody)), summaryMaxRunes)
	}

	return summary, nil
}

func formatFrom(h mail.Header) string {
	addrs, err := h.AddressList("From")
	if err != nil || len(addrs) == 0 {
		return "(未知发件人)"
	}
	addr := addrs[0]
	if addr.Name != "" {
		return addr.Name + " <" + addr.Address + ">"
	}
	return addr.Address
}

func subjectOrDefault(h mail.Header) string {
	subject, err := h.Subject()
	if err != nil || subject == "" {
		return "(无主题)"
	}
	return subject
}

func htmlToText(html string) string {
	text := htmlTagRe.ReplaceAllString(html, "\n")
	text = htmlWhitespaceRe.ReplaceAllString(text, "\n")
	return strings.TrimSpace(text)
}

// truncate 按 rune 截断，避免在多字节字符中间截断产生乱码。
func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// urlRe 匹配纯文本中的 http/https URL，用于自动链接化。
// 终止字符集合：空白、引号、尖括号、以及中文全角标点（避免把中文标点也吞进去）。
var urlRe = regexp.MustCompile(`https?://[^\s<>"'()\[\]{}，。；：！？、（）【】「」《》]+`)

// autoLinkURLs 把纯文本中的 http/https URL 替换为 <a> 标签，返回 Telegram 安全 HTML。
// 第二步先做整体 HTML 转义，再用 ReplaceAllStringFunc 精确替换 URL 部分。
// hasURL=false 表示没有找到 URL，调用方应按纯文本路径发送（节省一次 parse_mode 开销）。
func autoLinkURLs(text string) (string, bool) {
	if !urlRe.MatchString(text) {
		return "", false
	}
	escaped := html.EscapeString(text)
	result := urlRe.ReplaceAllStringFunc(escaped, func(url string) string {
		return `<a href="` + url + `">` + url + `</a>`
	})
	return result, true
}
