package oauth

import "golang.org/x/oauth2"

// Provider 标识一个支持 OAuth2 登录的邮箱服务商。
type Provider string

const (
	Gmail   Provider = "gmail"
	Outlook Provider = "outlook"
)

// gmailEndpoint 和 microsoftEndpoint 手写而不引入 x/oauth2/google 等子包：
// Device Flow 场景下这些子包提供的帮助不大（主要是 AuthCodeURL 场景的封装），
// 手写端点更透明，也避免额外的依赖面。
var gmailEndpoint = oauth2.Endpoint{
	AuthURL:       "https://accounts.google.com/o/oauth2/auth",
	TokenURL:      "https://oauth2.googleapis.com/token",
	DeviceAuthURL: "https://oauth2.googleapis.com/device/code",
}

var microsoftEndpoint = oauth2.Endpoint{
	AuthURL:       "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
	TokenURL:      "https://login.microsoftonline.com/common/oauth2/v2.0/token",
	DeviceAuthURL: "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode",
}

// gmailScopes 需要完整的 IMAP/SMTP 访问权限。
var gmailScopes = []string{"https://mail.google.com/"}

// outlookScopes 需要 IMAP、SMTP 发信权限，以及 offline_access 才能拿到 refresh_token。
var outlookScopes = []string{
	"https://outlook.office.com/IMAP.AccessAsUser.All",
	"https://outlook.office.com/SMTP.Send",
	"offline_access",
}

// Config 返回指定 provider 的 oauth2.Config。
func Config(provider Provider, clientID, clientSecret string) oauth2.Config {
	switch provider {
	case Gmail:
		return oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: gmailEndpoint, Scopes: gmailScopes}
	case Outlook:
		return oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: microsoftEndpoint, Scopes: outlookScopes}
	default:
		return oauth2.Config{}
	}
}
