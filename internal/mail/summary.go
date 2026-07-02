package mail

import (
	"io"
	"regexp"
	"strings"

	"github.com/emersion/go-message/mail"

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

	if plainBody != "" {
		summary.Body = truncate(strings.TrimSpace(plainBody), summaryMaxRunes)
		return summary, nil
	}

	if htmlBody != "" {
		if rendered, ok := htmlToTelegramHTML(htmlBody, summaryMaxRunes); ok {
			summary.Body = rendered
			summary.BodyIsHTML = true
			return summary, nil
		}
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
