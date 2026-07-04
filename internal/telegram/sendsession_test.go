package telegram

import "testing"

func newTestAccounts() []SendableAccount {
	return []SendableAccount{
		{ID: 1, Email: "one@example.com"},
		{ID: 2, Email: "two@example.com"},
	}
}

func TestSendSessionFullFlow(t *testing.T) {
	s := &SendSession{Step: SendStepChooseAccount, Accounts: newTestAccounts()}

	_, finished, cancelled := s.Advance("2")
	if finished || cancelled {
		t.Fatalf("unexpected finished=%v cancelled=%v after choosing account", finished, cancelled)
	}
	if s.Step != SendStepTo {
		t.Fatalf("expected SendStepTo, got %v", s.Step)
	}
	if s.Draft.AccountID != 2 {
		t.Fatalf("expected AccountID=2, got %d", s.Draft.AccountID)
	}

	_, _, _ = s.Advance("recipient@example.com")
	if s.Step != SendStepSubject {
		t.Fatalf("expected SendStepSubject, got %v", s.Step)
	}
	if s.Draft.To != "recipient@example.com" {
		t.Errorf("unexpected To: %q", s.Draft.To)
	}

	_, _, _ = s.Advance("Hello")
	if s.Step != SendStepBody {
		t.Fatalf("expected SendStepBody, got %v", s.Step)
	}

	_, _, _ = s.Advance("This is the body")
	if s.Step != SendStepConfirm {
		t.Fatalf("expected SendStepConfirm, got %v", s.Step)
	}

	_, finished, cancelled = s.Advance("确认")
	if !finished || cancelled {
		t.Fatalf("expected finished=true cancelled=false, got finished=%v cancelled=%v", finished, cancelled)
	}
}

func TestSendSessionChooseAccountInvalidIndexStaysOnStep(t *testing.T) {
	s := &SendSession{Step: SendStepChooseAccount, Accounts: newTestAccounts()}

	_, finished, cancelled := s.Advance("99")
	if finished || cancelled {
		t.Fatal("invalid index should not finish or cancel")
	}
	if s.Step != SendStepChooseAccount {
		t.Fatalf("expected to remain on SendStepChooseAccount, got %v", s.Step)
	}

	_, finished, cancelled = s.Advance("not-a-number")
	if finished || cancelled {
		t.Fatal("non-numeric index should not finish or cancel")
	}
	if s.Step != SendStepChooseAccount {
		t.Fatalf("expected to remain on SendStepChooseAccount, got %v", s.Step)
	}
}

func TestSendSessionInvalidRecipientStaysOnStep(t *testing.T) {
	s := &SendSession{Step: SendStepTo, Accounts: newTestAccounts(), Draft: SendDraft{AccountID: 1}}
	_, finished, cancelled := s.Advance("not-an-email")
	if finished || cancelled {
		t.Fatal("invalid recipient should not finish or cancel")
	}
	if s.Step != SendStepTo {
		t.Fatalf("expected to remain on SendStepTo, got %v", s.Step)
	}
}

func TestSendSessionConfirmCancel(t *testing.T) {
	s := &SendSession{Step: SendStepConfirm, Draft: SendDraft{AccountID: 1, To: "a@b.com", Subject: "s", Body: "b"}}
	_, finished, cancelled := s.Advance("取消")
	if finished {
		t.Fatal("cancel should not report finished")
	}
	if !cancelled {
		t.Fatal("expected cancelled=true")
	}
}

func TestSendSessionStoreStartGetClear(t *testing.T) {
	store := NewSendSessionStore(nil)
	accounts := newTestAccounts()

	if store.Get(1) != nil {
		t.Fatal("expected no session before Start")
	}

	sess := store.Start(1, accounts)
	if sess.Step != SendStepChooseAccount {
		t.Fatalf("expected new session to start at SendStepChooseAccount, got %v", sess.Step)
	}
	if len(sess.Accounts) != 2 {
		t.Fatalf("expected 2 accounts on session, got %d", len(sess.Accounts))
	}
	if store.Get(1) != sess {
		t.Fatal("expected Get to return the same session instance")
	}

	store.Clear(1)
	if store.Get(1) != nil {
		t.Fatal("expected session to be removed after Clear")
	}
}
