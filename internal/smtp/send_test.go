package smtp

import (
	"strings"
	"testing"
)

func TestIsImplicitTLS(t *testing.T) {
	if !isImplicitTLS(465) {
		t.Error("expected port 465 to use implicit TLS")
	}
	if isImplicitTLS(587) {
		t.Error("expected port 587 to use STARTTLS, not implicit TLS")
	}
	if isImplicitTLS(25) {
		t.Error("expected port 25 to use STARTTLS, not implicit TLS")
	}
}

func TestBuildMessageContainsHeadersAndBody(t *testing.T) {
	raw, err := buildMessage("sender@example.com", Message{
		To:      "recipient@example.com",
		Subject: "Test Subject",
		Body:    "Hello, this is the body.",
	})
	if err != nil {
		t.Fatalf("buildMessage returned error: %v", err)
	}

	text := string(raw)
	if !strings.Contains(text, "sender@example.com") {
		t.Errorf("expected From address in message, got: %q", text)
	}
	if !strings.Contains(text, "recipient@example.com") {
		t.Errorf("expected To address in message, got: %q", text)
	}
	if !strings.Contains(text, "Test Subject") {
		t.Errorf("expected Subject in message, got: %q", text)
	}
	if !strings.Contains(text, "Hello, this is the body.") {
		t.Errorf("expected body in message, got: %q", text)
	}
}

func TestBuildMessageInvalidFromAddressFails(t *testing.T) {
	_, err := buildMessage("not-an-email", Message{To: "recipient@example.com", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("expected error for invalid From address")
	}
}

func TestBuildMessageInvalidToAddressFails(t *testing.T) {
	_, err := buildMessage("sender@example.com", Message{To: "not-an-email", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("expected error for invalid To address")
	}
}
