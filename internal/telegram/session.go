package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Step 表示添加账号多轮问答的当前阶段。
type Step int

const (
	StepEmail Step = iota
	StepAuthMethod
	StepHost
	StepPort
	StepPassword
	StepSMTPOptional
	StepSMTPHost
	StepSMTPPort
	StepOAuthPending
	StepConfirm
)

// Draft 保存添加账号过程中收集到的字段。
type Draft struct {
	Protocol string // "imap"（默认）| "pop3"，由 SessionStore.Start 在会话开始时固定
	Email    string
	Host     string
	Port     int
	Password string
	Hint     string
	SMTPHost string // 空字符串表示未配置发信
	SMTPPort int

	AuthType      string // "password"（默认）| "oauth"
	OAuthProvider string // AuthType="oauth" 时的 "gmail" | "outlook"
	// OAuthAccessToken/OAuthRefreshToken/OAuthTokenExpiry 由 handlers.go 里异步完成 device flow
	// 后填入，Advance 本身保持同步纯函数，不做网络 IO。
	OAuthAccessToken  string
	OAuthRefreshToken string
	OAuthTokenExpiry  time.Time

	fromPreset bool // 命中预设域名时跳过 StepSMTPOptional，SMTP信息随预设一起填好
}

// Session 是单个 Telegram 用户正在进行的 /addaccount 会话状态。
type Session struct {
	Step  Step
	Draft Draft
	// availableOAuthProviders 是已配置了 Client ID 的 provider 集合（"gmail"/"outlook"），
	// 由 SessionStore.Start 传入。域名支持 OAuth 但对应 provider 不在此集合中时，
	// 该选项不会出现在问答流程里，等同于完全没有实现 OAuth。
	availableOAuthProviders map[string]bool
}

// Advance 用用户的下一条消息推进会话状态机。
// finished=true 表示 Draft 已收集完整，可以入库；cancelled=true 表示用户取消了流程。
func (s *Session) Advance(text string) (reply string, finished bool, cancelled bool) {
	text = strings.TrimSpace(text)

	switch s.Step {
	case StepEmail:
		if !strings.Contains(text, "@") {
			return "⚠️ 邮箱地址格式不对，请重新输入", false, false
		}
		s.Draft.Email = text

		// POP3 没有 OAuth 场景（Gmail/Outlook 的 OAuth 授权范围是围绕 IMAP/SMTP 设计的），
		// 只有 IMAP 协议才提供 OAuth 选项。
		if s.Draft.Protocol != "pop3" {
			if provider, ok := SupportsOAuth(text); ok && s.availableOAuthProviders[provider] {
				s.Draft.OAuthProvider = provider
				s.Step = StepAuthMethod
				return "该邮箱支持 OAuth 登录，回复\"oauth\"使用 OAuth 授权，或回复\"密码\"使用密码/应用专用密码", false, false
			}
		}

		if preset, ok := LookupPreset(text); ok {
			s.applyPreset(preset)
			s.Draft.fromPreset = true
			s.Step = StepPassword
			return preset.Hint, false, false
		}
		s.Step = StepHost
		return s.hostPrompt(), false, false

	case StepAuthMethod:
		switch strings.ToLower(text) {
		case "oauth":
			s.Draft.AuthType = "oauth"
			if preset, ok := LookupPreset(s.Draft.Email); ok {
				s.Draft.Host = preset.Host
				s.Draft.Port = preset.Port
				s.Draft.SMTPHost = preset.SMTPHost
				s.Draft.SMTPPort = preset.SMTPPort
			}
			s.Step = StepOAuthPending
			return "", false, false // 具体的授权链接由 handlers.go 发起 device flow 后发送
		case "密码", "password":
			s.Draft.AuthType = "password"
			if preset, ok := LookupPreset(s.Draft.Email); ok {
				s.applyPreset(preset)
				s.Draft.fromPreset = true
				s.Step = StepPassword
				return preset.Hint, false, false
			}
			s.Step = StepHost
			return s.hostPrompt(), false, false
		default:
			return "⚠️ 请回复\"oauth\"或\"密码\"", false, false
		}

	case StepHost:
		if text == "" {
			return "⚠️ 服务器地址不能为空，请重新输入", false, false
		}
		s.Draft.Host = text
		s.Step = StepPort
		return s.portPrompt(), false, false

	case StepPort:
		port, err := strconv.Atoi(text)
		if err != nil || port <= 0 || port > 65535 {
			return "⚠️ 端口必须是 1-65535 之间的数字，请重新输入", false, false
		}
		s.Draft.Port = port
		s.Step = StepPassword
		return "请输入密码或授权码", false, false

	case StepPassword:
		if text == "" {
			return "⚠️ 密码不能为空，请重新输入", false, false
		}
		s.Draft.Password = text
		if s.Draft.Protocol == "pop3" {
			// POP3 账号不支持发信配置（发信走 SMTP，与 POP3 收信是两个独立的协议/端口）。
			s.Step = StepConfirm
			return s.confirmPrompt(), false, false
		}
		if s.Draft.fromPreset {
			// 预设域名已经带有 SMTP 信息，跳过额外问答直接进入确认。
			s.Step = StepConfirm
			return s.confirmPrompt(), false, false
		}
		s.Step = StepSMTPOptional
		return "是否需要配置发信（SMTP）？回复\"是\"配置，回复\"否\"跳过（可通过 /listaccounts 查看状态，跳过后 /send 将不可用该账号）", false, false

	case StepSMTPOptional:
		switch text {
		case "是", "yes", "y", "Y":
			s.Step = StepSMTPHost
			return "请输入 SMTP 服务器地址（例如 smtp.example.com）", false, false
		case "否", "no", "n", "N":
			s.Step = StepConfirm
			return s.confirmPrompt(), false, false
		default:
			return "⚠️ 请回复\"是\"或\"否\"", false, false
		}

	case StepSMTPHost:
		if text == "" {
			return "⚠️ 服务器地址不能为空，请重新输入", false, false
		}
		s.Draft.SMTPHost = text
		s.Step = StepSMTPPort
		return "请输入 SMTP 端口（通常是 587 或 465）", false, false

	case StepSMTPPort:
		port, err := strconv.Atoi(text)
		if err != nil || port <= 0 || port > 65535 {
			return "⚠️ 端口必须是 1-65535 之间的数字，请重新输入", false, false
		}
		s.Draft.SMTPPort = port
		s.Step = StepConfirm
		return s.confirmPrompt(), false, false

	case StepOAuthPending:
		// device flow 的轮询在 handlers.go 里异步进行，完成后由 handlers.go 直接把
		// Step 推进到 StepConfirm 并调用 Send 主动通知用户，不经过 Advance。
		// 用户在等待期间发消息只会走到这里，提示其耐心等待即可。
		return "🔐 正在等待浏览器授权完成，请稍候（或发送 /cancel 取消）", false, false

	case StepConfirm:
		switch text {
		case "确认", "yes", "y", "Y":
			return "✅ 账号已添加", true, false
		case "取消", "no", "n", "N":
			return "🚫 已取消添加账号", false, true
		default:
			return "⚠️ 请回复\"确认\"保存账号，或\"取消\"放弃", false, false
		}
	}

	return "⚠️ 内部状态异常，请重新执行 /addaccount", false, true
}

// CompleteOAuth 在 device flow 轮询成功后填入 token 并把状态推进到 StepConfirm，
// 返回确认提示文本。调用方（handlers.go）负责在轮询期间校验会话是否还有效
// （SessionStore.Get(userID) == 原 session 指针），避免把结果写进一个已经被
// /cancel 或新 /addaccount 替换掉的会话里。
func (s *Session) CompleteOAuth(accessToken, refreshToken string, expiry time.Time) string {
	s.Draft.OAuthAccessToken = accessToken
	s.Draft.OAuthRefreshToken = refreshToken
	s.Draft.OAuthTokenExpiry = expiry
	s.Step = StepConfirm
	return s.confirmPrompt()
}

// applyPreset 把预设域名的连接信息填入 Draft，按当前协议选择 IMAP 或 POP3 的 host/port。
func (s *Session) applyPreset(preset Preset) {
	if s.Draft.Protocol == "pop3" {
		s.Draft.Host = preset.POP3Host
		s.Draft.Port = preset.POP3Port
	} else {
		s.Draft.Host = preset.Host
		s.Draft.Port = preset.Port
	}
	s.Draft.Hint = preset.Hint
	s.Draft.SMTPHost = preset.SMTPHost
	s.Draft.SMTPPort = preset.SMTPPort
}

func (s *Session) hostPrompt() string {
	if s.Draft.Protocol == "pop3" {
		return "未识别到该邮箱的预设配置，请输入 POP3 服务器地址（例如 pop.example.com）"
	}
	return "未识别到该邮箱的预设配置，请输入 IMAP 服务器地址（例如 imap.example.com）"
}

func (s *Session) portPrompt() string {
	if s.Draft.Protocol == "pop3" {
		return "请输入 POP3 端口（通常是 995）"
	}
	return "请输入 IMAP 端口（通常是 993）"
}

func (s *Session) confirmPrompt() string {
	smtpInfo := "未配置（/send 将不可用该账号）"
	if s.Draft.SMTPHost != "" {
		smtpInfo = fmt.Sprintf("%s:%d", s.Draft.SMTPHost, s.Draft.SMTPPort)
	}
	authInfo := "密码/授权码"
	if s.Draft.AuthType == "oauth" {
		authInfo = "OAuth（" + s.Draft.OAuthProvider + "）"
	}
	protocolLabel := "IMAP"
	if s.Draft.Protocol == "pop3" {
		protocolLabel = "POP3"
	}
	return fmt.Sprintf(
		"📋 请确认账号信息：\n邮箱: %s\n认证方式: %s\n%s: %s:%d\nSMTP: %s\n\n回复\"确认\"保存，回复\"取消\"放弃",
		s.Draft.Email, authInfo, protocolLabel, s.Draft.Host, s.Draft.Port, smtpInfo,
	)
}

// SessionStore 是按 Telegram 用户 ID 索引的内存会话表。
// 会话是短期交互状态，进程重启后用户重新走一遍 /addaccount 即可，不做持久化。
type SessionStore struct {
	mu sync.Mutex
	m  map[int64]*Session
}

// NewSessionStore 创建一个空的会话表。
func NewSessionStore() *SessionStore {
	return &SessionStore{m: make(map[int64]*Session)}
}

// Get 返回指定用户当前的会话，不存在返回 nil。
func (s *SessionStore) Get(userID int64) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[userID]
}

// Start 为用户开始一个新的会话，覆盖任何已存在的会话。
// availableOAuthProviders 是已配置了 Client ID 的 provider 集合，未配置的 provider
// 即使邮箱域名支持 OAuth 也不会在问答流程中出现该选项。
// protocol 是 "imap"（默认）或 "pop3"，由 /addaccount 命令参数决定，在会话开始时固定，
// 不再追加一轮问答——这样不带参数的 /addaccount 行为与之前完全一致，不影响已有用户习惯。
func (s *SessionStore) Start(userID int64, availableOAuthProviders map[string]bool, protocol string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if protocol == "" {
		protocol = "imap"
	}
	sess := &Session{Step: StepEmail, availableOAuthProviders: availableOAuthProviders, Draft: Draft{Protocol: protocol}}
	s.m[userID] = sess
	return sess
}

// Clear 结束用户当前的会话。
func (s *SessionStore) Clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, userID)
}
