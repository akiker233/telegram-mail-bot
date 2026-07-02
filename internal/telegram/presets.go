package telegram

import "strings"

// Preset 是常见邮箱服务商的 IMAP/POP3/SMTP 连接信息，用于跳过多余的问答步骤。
type Preset struct {
	Host     string // IMAP host
	Port     int    // IMAP port
	POP3Host string
	POP3Port int
	SMTPHost string
	SMTPPort int
	Hint     string // 添加账号时给用户的密码提示
}

var presetsByDomain = map[string]Preset{
	"gmail.com":   {Host: "imap.gmail.com", Port: 993, POP3Host: "pop.gmail.com", POP3Port: 995, SMTPHost: "smtp.gmail.com", SMTPPort: 587, Hint: "请输入应用专用密码（App Password），不是登录密码"},
	"outlook.com": {Host: "outlook.office365.com", Port: 993, POP3Host: "outlook.office365.com", POP3Port: 995, SMTPHost: "smtp-mail.outlook.com", SMTPPort: 587, Hint: "请输入应用专用密码（App Password），不是登录密码"},
	"hotmail.com": {Host: "outlook.office365.com", Port: 993, POP3Host: "outlook.office365.com", POP3Port: 995, SMTPHost: "smtp-mail.outlook.com", SMTPPort: 587, Hint: "请输入应用专用密码（App Password），不是登录密码"},
	"qq.com":      {Host: "imap.qq.com", Port: 993, POP3Host: "pop.qq.com", POP3Port: 995, SMTPHost: "smtp.qq.com", SMTPPort: 587, Hint: "请输入 IMAP 授权码，不是QQ密码"},
	"163.com":     {Host: "imap.163.com", Port: 993, POP3Host: "pop.163.com", POP3Port: 995, SMTPHost: "smtp.163.com", SMTPPort: 465, Hint: "请输入客户端授权码，不是登录密码"},
	"126.com":     {Host: "imap.126.com", Port: 993, POP3Host: "pop.126.com", POP3Port: 995, SMTPHost: "smtp.126.com", SMTPPort: 465, Hint: "请输入客户端授权码，不是登录密码"},
}

// LookupPreset 根据邮箱地址的域名查找预设配置。
func LookupPreset(email string) (Preset, bool) {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return Preset{}, false
	}
	domain := strings.ToLower(email[at+1:])
	preset, ok := presetsByDomain[domain]
	return preset, ok
}

// oauthProviderByDomain 列出支持 OAuth2 登录的邮箱域名。国内邮箱（QQ/163/126等）不支持
// OAuth，只能用密码/授权码，所以不在此表中。
var oauthProviderByDomain = map[string]string{
	"gmail.com":   "gmail",
	"outlook.com": "outlook",
	"hotmail.com": "outlook",
}

// SupportsOAuth 返回该邮箱域名对应的 OAuth provider 名称（"gmail"/"outlook"）。
func SupportsOAuth(email string) (string, bool) {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "", false
	}
	domain := strings.ToLower(email[at+1:])
	provider, ok := oauthProviderByDomain[domain]
	return provider, ok
}
