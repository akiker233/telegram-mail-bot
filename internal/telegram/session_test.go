package telegram

import (
	"testing"
	"time"
)

func TestSessionPresetFlowSkipsHostAndPort(t *testing.T) {
	s := &Session{Step: StepEmail}

	reply, finished, cancelled := s.Advance("user@gmail.com")
	if finished || cancelled {
		t.Fatalf("unexpected finished=%v cancelled=%v after email step", finished, cancelled)
	}
	if s.Step != StepPassword {
		t.Fatalf("expected StepPassword after preset match, got %v", s.Step)
	}
	if s.Draft.Host != "imap.gmail.com" || s.Draft.Port != 993 {
		t.Fatalf("expected preset host/port to be filled, got %+v", s.Draft)
	}
	if reply == "" {
		t.Fatal("expected a hint reply for preset match")
	}

	reply, finished, cancelled = s.Advance("app-specific-password")
	if finished || cancelled {
		t.Fatalf("unexpected finished=%v cancelled=%v after password step", finished, cancelled)
	}
	if s.Step != StepConfirm {
		t.Fatalf("expected StepConfirm after password step, got %v", s.Step)
	}
	if s.Draft.Password != "app-specific-password" {
		t.Errorf("unexpected password stored: %q", s.Draft.Password)
	}
	if reply == "" {
		t.Fatal("expected confirmation prompt")
	}

	_, finished, cancelled = s.Advance("确认")
	if !finished || cancelled {
		t.Fatalf("expected finished=true cancelled=false, got finished=%v cancelled=%v", finished, cancelled)
	}
}

func TestSessionUnknownDomainAsksHostAndPort(t *testing.T) {
	s := &Session{Step: StepEmail}

	_, _, _ = s.Advance("user@unknown-provider.example")
	if s.Step != StepHost {
		t.Fatalf("expected StepHost for unknown domain, got %v", s.Step)
	}

	_, _, _ = s.Advance("imap.unknown-provider.example")
	if s.Step != StepPort {
		t.Fatalf("expected StepPort after host input, got %v", s.Step)
	}
	if s.Draft.Host != "imap.unknown-provider.example" {
		t.Errorf("unexpected host: %q", s.Draft.Host)
	}

	_, _, _ = s.Advance("993")
	if s.Step != StepPassword {
		t.Fatalf("expected StepPassword after port input, got %v", s.Step)
	}
	if s.Draft.Port != 993 {
		t.Errorf("unexpected port: %d", s.Draft.Port)
	}
}

func TestSessionPresetFlowSkipsSMTPOptionalStep(t *testing.T) {
	s := &Session{Step: StepEmail}
	_, _, _ = s.Advance("user@gmail.com")
	_, _, _ = s.Advance("app-specific-password")

	if s.Step != StepConfirm {
		t.Fatalf("expected preset flow to skip StepSMTPOptional and land on StepConfirm, got %v", s.Step)
	}
	if s.Draft.SMTPHost != "smtp.gmail.com" || s.Draft.SMTPPort != 587 {
		t.Fatalf("expected preset SMTP info to be filled, got %+v", s.Draft)
	}
}

func TestSessionManualFlowSMTPOptionalDeclined(t *testing.T) {
	s := &Session{Step: StepEmail}
	_, _, _ = s.Advance("user@unknown-provider.example")
	_, _, _ = s.Advance("imap.unknown-provider.example")
	_, _, _ = s.Advance("993")

	if s.Step != StepPassword {
		t.Fatalf("expected StepPassword, got %v", s.Step)
	}
	_, _, _ = s.Advance("secret")

	if s.Step != StepSMTPOptional {
		t.Fatalf("expected StepSMTPOptional for manual flow, got %v", s.Step)
	}

	_, finished, cancelled := s.Advance("否")
	if finished || cancelled {
		t.Fatal("declining SMTP setup should not finish or cancel the session")
	}
	if s.Step != StepConfirm {
		t.Fatalf("expected StepConfirm after declining SMTP setup, got %v", s.Step)
	}
	if s.Draft.SMTPHost != "" {
		t.Errorf("expected SMTPHost to remain empty when declined, got %q", s.Draft.SMTPHost)
	}
}

func TestSessionManualFlowSMTPOptionalAccepted(t *testing.T) {
	s := &Session{Step: StepSMTPOptional, Draft: Draft{Email: "a@b.com", Host: "imap.b.com", Port: 993, Password: "secret"}}

	_, _, _ = s.Advance("是")
	if s.Step != StepSMTPHost {
		t.Fatalf("expected StepSMTPHost after accepting SMTP setup, got %v", s.Step)
	}

	_, _, _ = s.Advance("smtp.b.com")
	if s.Step != StepSMTPPort {
		t.Fatalf("expected StepSMTPPort after SMTP host input, got %v", s.Step)
	}
	if s.Draft.SMTPHost != "smtp.b.com" {
		t.Errorf("unexpected SMTPHost: %q", s.Draft.SMTPHost)
	}

	_, _, _ = s.Advance("587")
	if s.Step != StepConfirm {
		t.Fatalf("expected StepConfirm after SMTP port input, got %v", s.Step)
	}
	if s.Draft.SMTPPort != 587 {
		t.Errorf("unexpected SMTPPort: %d", s.Draft.SMTPPort)
	}
}

func TestSessionSMTPOptionalInvalidReplyStaysOnStep(t *testing.T) {
	s := &Session{Step: StepSMTPOptional, Draft: Draft{Email: "a@b.com", Host: "imap.b.com", Port: 993}}
	_, finished, cancelled := s.Advance("maybe")
	if finished || cancelled {
		t.Fatal("unrecognized reply should not finish or cancel")
	}
	if s.Step != StepSMTPOptional {
		t.Fatalf("expected to remain on StepSMTPOptional, got %v", s.Step)
	}
}

func TestSessionSMTPPortInvalidStaysOnStep(t *testing.T) {
	s := &Session{Step: StepSMTPPort, Draft: Draft{Email: "a@b.com", Host: "imap.b.com", Port: 993, SMTPHost: "smtp.b.com"}}
	_, finished, cancelled := s.Advance("not-a-port")
	if finished || cancelled {
		t.Fatal("invalid SMTP port should not finish or cancel")
	}
	if s.Step != StepSMTPPort {
		t.Fatalf("expected to remain on StepSMTPPort, got %v", s.Step)
	}
}

func TestSessionOAuthProviderNotConfiguredSkipsAuthMethodStep(t *testing.T) {
	// gmail.com 支持 OAuth，但 availableOAuthProviders 里没有 "gmail"（未配置 Client ID），
	// 所以应该完全走密码流程，等同于该邮箱不支持 OAuth 一样。
	s := &Session{Step: StepEmail, availableOAuthProviders: map[string]bool{}}
	_, _, _ = s.Advance("user@gmail.com")
	if s.Step != StepPassword {
		t.Fatalf("expected StepPassword (OAuth not offered), got %v", s.Step)
	}
}

func TestSessionOAuthProviderConfiguredOffersAuthMethodStep(t *testing.T) {
	s := &Session{Step: StepEmail, availableOAuthProviders: map[string]bool{"gmail": true}}
	_, _, _ = s.Advance("user@gmail.com")
	if s.Step != StepAuthMethod {
		t.Fatalf("expected StepAuthMethod, got %v", s.Step)
	}
	if s.Draft.OAuthProvider != "gmail" {
		t.Errorf("expected OAuthProvider=gmail, got %q", s.Draft.OAuthProvider)
	}
}

func TestSessionAuthMethodChoosePassword(t *testing.T) {
	s := &Session{Step: StepAuthMethod, availableOAuthProviders: map[string]bool{"gmail": true}, Draft: Draft{Email: "user@gmail.com", OAuthProvider: "gmail"}}
	_, _, _ = s.Advance("密码")
	if s.Step != StepPassword {
		t.Fatalf("expected StepPassword, got %v", s.Step)
	}
	if s.Draft.AuthType != "password" {
		t.Errorf("expected AuthType=password, got %q", s.Draft.AuthType)
	}
	if s.Draft.Host != "imap.gmail.com" {
		t.Errorf("expected preset host to be filled, got %q", s.Draft.Host)
	}
}

func TestSessionAuthMethodChooseOAuthEntersPendingStep(t *testing.T) {
	s := &Session{Step: StepAuthMethod, availableOAuthProviders: map[string]bool{"gmail": true}, Draft: Draft{Email: "user@gmail.com", OAuthProvider: "gmail"}}
	_, finished, cancelled := s.Advance("oauth")
	if finished || cancelled {
		t.Fatal("choosing oauth should not finish or cancel the session")
	}
	if s.Step != StepOAuthPending {
		t.Fatalf("expected StepOAuthPending, got %v", s.Step)
	}
	if s.Draft.AuthType != "oauth" {
		t.Errorf("expected AuthType=oauth, got %q", s.Draft.AuthType)
	}
}

func TestSessionAuthMethodInvalidReplyStaysOnStep(t *testing.T) {
	s := &Session{Step: StepAuthMethod, Draft: Draft{Email: "user@gmail.com", OAuthProvider: "gmail"}}
	_, finished, cancelled := s.Advance("maybe")
	if finished || cancelled {
		t.Fatal("unrecognized reply should not finish or cancel")
	}
	if s.Step != StepAuthMethod {
		t.Fatalf("expected to remain on StepAuthMethod, got %v", s.Step)
	}
}

func TestSessionCompleteOAuthAdvancesToConfirm(t *testing.T) {
	s := &Session{Step: StepOAuthPending, Draft: Draft{Email: "user@gmail.com", AuthType: "oauth", OAuthProvider: "gmail", Host: "imap.gmail.com", Port: 993}}
	reply := s.CompleteOAuth("access-token", "refresh-token", time.Now().Add(time.Hour))
	if s.Step != StepConfirm {
		t.Fatalf("expected StepConfirm after CompleteOAuth, got %v", s.Step)
	}
	if s.Draft.OAuthAccessToken != "access-token" || s.Draft.OAuthRefreshToken != "refresh-token" {
		t.Errorf("expected tokens to be stored, got %+v", s.Draft)
	}
	if reply == "" {
		t.Fatal("expected non-empty confirm prompt")
	}
}

func TestSessionInvalidEmailStaysOnStep(t *testing.T) {
	s := &Session{Step: StepEmail}
	_, finished, cancelled := s.Advance("not-an-email")
	if finished || cancelled {
		t.Fatal("invalid email should not finish or cancel the session")
	}
	if s.Step != StepEmail {
		t.Fatalf("expected to remain on StepEmail, got %v", s.Step)
	}
}

func TestSessionInvalidPortStaysOnStep(t *testing.T) {
	s := &Session{Step: StepPort, Draft: Draft{Email: "a@b.com", Host: "imap.b.com"}}
	_, finished, cancelled := s.Advance("not-a-port")
	if finished || cancelled {
		t.Fatal("invalid port should not finish or cancel the session")
	}
	if s.Step != StepPort {
		t.Fatalf("expected to remain on StepPort, got %v", s.Step)
	}
}

func TestSessionPOP3PresetUsesPOP3HostPort(t *testing.T) {
	s := &Session{Step: StepEmail, Draft: Draft{Protocol: "pop3"}}

	_, _, _ = s.Advance("user@gmail.com")
	if s.Step != StepPassword {
		t.Fatalf("expected StepPassword after preset match, got %v", s.Step)
	}
	if s.Draft.Host != "pop.gmail.com" || s.Draft.Port != 995 {
		t.Fatalf("expected POP3 preset host/port to be filled, got %+v", s.Draft)
	}
}

func TestSessionPOP3SkipsOAuthOffer(t *testing.T) {
	// gmail.com 支持 OAuth，但 POP3 协议不提供 OAuth 选项。
	s := &Session{Step: StepEmail, Draft: Draft{Protocol: "pop3"}, availableOAuthProviders: map[string]bool{"gmail": true}}
	_, _, _ = s.Advance("user@gmail.com")
	if s.Step != StepPassword {
		t.Fatalf("expected StepPassword (no OAuth offer for pop3), got %v", s.Step)
	}
}

func TestSessionPOP3SkipsSMTPOptionalStep(t *testing.T) {
	s := &Session{Step: StepEmail, Draft: Draft{Protocol: "pop3"}}
	_, _, _ = s.Advance("user@gmail.com")
	_, _, _ = s.Advance("app-specific-password")
	if s.Step != StepConfirm {
		t.Fatalf("expected StepConfirm (pop3 skips SMTP setup), got %v", s.Step)
	}
	if s.Draft.SMTPHost == "" {
		t.Error("expected preset SMTP info to still be filled even though pop3 doesn't ask about it")
	}
}

func TestSessionPOP3UnknownDomainAsksPOP3Host(t *testing.T) {
	s := &Session{Step: StepEmail, Draft: Draft{Protocol: "pop3"}}
	reply, _, _ := s.Advance("user@unknown-provider.example")
	if s.Step != StepHost {
		t.Fatalf("expected StepHost, got %v", s.Step)
	}
	if reply == "" {
		t.Fatal("expected a non-empty prompt")
	}

	_, _, _ = s.Advance("pop.unknown-provider.example")
	if s.Step != StepPort {
		t.Fatalf("expected StepPort, got %v", s.Step)
	}
}

func TestSessionConfirmCancel(t *testing.T) {
	s := &Session{Step: StepConfirm, Draft: Draft{Email: "a@b.com", Host: "imap.b.com", Port: 993}}
	_, finished, cancelled := s.Advance("取消")
	if finished {
		t.Fatal("cancel should not report finished")
	}
	if !cancelled {
		t.Fatal("expected cancelled=true")
	}
}

func TestSessionConfirmUnrecognizedReplyStaysOnStep(t *testing.T) {
	s := &Session{Step: StepConfirm, Draft: Draft{Email: "a@b.com", Host: "imap.b.com", Port: 993}}
	_, finished, cancelled := s.Advance("maybe")
	if finished || cancelled {
		t.Fatal("unrecognized confirm reply should not finish or cancel")
	}
	if s.Step != StepConfirm {
		t.Fatalf("expected to remain on StepConfirm, got %v", s.Step)
	}
}

func TestSessionStoreStartGetClear(t *testing.T) {
	store := NewSessionStore()

	if store.Get(1) != nil {
		t.Fatal("expected no session before Start")
	}

	sess := store.Start(1, nil, "")
	if sess.Step != StepEmail {
		t.Fatalf("expected new session to start at StepEmail, got %v", sess.Step)
	}
	if store.Get(1) != sess {
		t.Fatal("expected Get to return the same session instance")
	}

	store.Clear(1)
	if store.Get(1) != nil {
		t.Fatal("expected session to be removed after Clear")
	}
}

func TestSessionStoreStartDefaultsProtocolToIMAP(t *testing.T) {
	store := NewSessionStore()
	sess := store.Start(1, nil, "")
	if sess.Draft.Protocol != "imap" {
		t.Errorf("expected default Protocol=imap, got %q", sess.Draft.Protocol)
	}
}

func TestSessionStoreStartWithPOP3Protocol(t *testing.T) {
	store := NewSessionStore()
	sess := store.Start(1, nil, "pop3")
	if sess.Draft.Protocol != "pop3" {
		t.Errorf("expected Protocol=pop3, got %q", sess.Draft.Protocol)
	}
}
