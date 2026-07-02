package mail

import (
	"strings"
	"testing"
)

func TestBuildSummaryPlainText(t *testing.T) {
	raw := "From: Alice <alice@example.com>\r\n" +
		"To: bob@example.com\r\n" +
		"Subject: Hello there\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"This is the plain text body.\r\n"

	summary, err := BuildSummary(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("BuildSummary returned error: %v", err)
	}

	if summary.From != "Alice <alice@example.com>" {
		t.Errorf("unexpected From: %q", summary.From)
	}
	if summary.Subject != "Hello there" {
		t.Errorf("unexpected Subject: %q", summary.Subject)
	}
	if summary.Body != "This is the plain text body." {
		t.Errorf("unexpected Body: %q", summary.Body)
	}
}

func TestBuildSummaryHTMLOnlyRendersTelegramSafeSubset(t *testing.T) {
	raw := "From: bob@example.com\r\n" +
		"Subject: HTML mail\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><body><p>Hello <b>world</b></p></body></html>\r\n"

	summary, err := BuildSummary(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("BuildSummary returned error: %v", err)
	}

	if !summary.BodyIsHTML {
		t.Fatal("expected BodyIsHTML=true for HTML-only mail")
	}
	if !strings.Contains(summary.Body, "<b>world</b>") {
		t.Errorf("expected safe <b> tag to be preserved, got: %q", summary.Body)
	}
	if !strings.Contains(summary.Body, "Hello") {
		t.Errorf("expected text content preserved, got: %q", summary.Body)
	}
}

func TestBuildSummaryMultipartPrefersPlainText(t *testing.T) {
	raw := "From: bob@example.com\r\n" +
		"Subject: Multipart mail\r\n" +
		"Content-Type: multipart/alternative; boundary=BOUNDARY\r\n" +
		"\r\n" +
		"--BOUNDARY\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Plain version.\r\n" +
		"--BOUNDARY\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<p>HTML version.</p>\r\n" +
		"--BOUNDARY--\r\n"

	summary, err := BuildSummary(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("BuildSummary returned error: %v", err)
	}

	if summary.Body != "Plain version." {
		t.Errorf("expected plain text part to be preferred, got: %q", summary.Body)
	}
}

func TestBuildSummaryMissingHeadersUseDefaults(t *testing.T) {
	raw := "Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Body without From/Subject.\r\n"

	summary, err := BuildSummary(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("BuildSummary returned error: %v", err)
	}

	if summary.From != "(未知发件人)" {
		t.Errorf("unexpected From default: %q", summary.From)
	}
	if summary.Subject != "(无主题)" {
		t.Errorf("unexpected Subject default: %q", summary.Subject)
	}
}

func TestTruncateASCII(t *testing.T) {
	s := strings.Repeat("a", 600)
	got := truncate(s, summaryMaxRunes)
	runes := []rune(got)
	// 500 个原始字符 + "..." 三个字符
	if len(runes) != summaryMaxRunes+3 {
		t.Errorf("unexpected truncated length: %d", len(runes))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncated string to end with '...', got %q", got[len(got)-10:])
	}
}

func TestTruncateMultiByteDoesNotCorrupt(t *testing.T) {
	// 中文字符和 emoji 都是多字节 rune，确保按 rune 而非 byte 截断。
	s := strings.Repeat("中", 400) + strings.Repeat("😀", 400)
	got := truncate(s, summaryMaxRunes)

	if !strings_ValidUTF8(got) {
		t.Fatalf("truncated string is not valid UTF-8: %q", got)
	}

	runes := []rune(got)
	if len(runes) != summaryMaxRunes+3 {
		t.Errorf("unexpected truncated rune count: %d", len(runes))
	}
}

func TestTruncateShorterThanMaxIsUnchanged(t *testing.T) {
	s := "short body"
	if got := truncate(s, summaryMaxRunes); got != s {
		t.Errorf("expected unchanged string, got %q", got)
	}
}

func strings_ValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
