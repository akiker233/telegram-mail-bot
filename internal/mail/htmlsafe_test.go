package mail

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestHTMLToTelegramHTMLPreservesSafeTags(t *testing.T) {
	got, ok := htmlToTelegramHTML("<p>Hello <b>world</b></p>", summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.Contains(got, "<b>world</b>") {
		t.Errorf("expected <b> tag preserved, got: %q", got)
	}
	if !strings.HasPrefix(got, "Hello") {
		t.Errorf("expected text content at start, got: %q", got)
	}
}

func TestHTMLToTelegramHTMLStripsScriptContent(t *testing.T) {
	got, ok := htmlToTelegramHTML("<script>alert(1)</script><p>ok</p>", summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if strings.Contains(got, "alert") {
		t.Errorf("expected script content to be discarded, got: %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("expected surrounding text preserved, got: %q", got)
	}
}

func TestHTMLToTelegramHTMLUnwrapsUnknownTags(t *testing.T) {
	got, ok := htmlToTelegramHTML(`<span>a<b>b</b>c</span>`, summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != "a<b>b</b>c" {
		t.Errorf("expected unknown tag to be unwrapped, got: %q", got)
	}
}

func TestHTMLToTelegramHTMLEscapesBareAngleBrackets(t *testing.T) {
	got, ok := htmlToTelegramHTML("<p>if (a&lt;b) { ok }</p>", summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.Contains(got, "&lt;") {
		t.Errorf("expected literal '<' to be escaped, got: %q", got)
	}
}

func TestHTMLToTelegramHTMLRejectsUnsafeHref(t *testing.T) {
	got, ok := htmlToTelegramHTML(`<a href="javascript:alert(1)">click</a>`, summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if strings.Contains(got, "href=") {
		t.Errorf("expected unsafe href to be dropped, got: %q", got)
	}
	if !strings.Contains(got, "click") {
		t.Errorf("expected link text preserved, got: %q", got)
	}
}

func TestHTMLToTelegramHTMLKeepsSafeHref(t *testing.T) {
	got, ok := htmlToTelegramHTML(`<a href="https://example.com/path?a=1&b=2">click</a>`, summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.Contains(got, `<a href="https://example.com/path?a=1&amp;b=2">click</a>`) {
		t.Errorf("expected safe href to be preserved and escaped, got: %q", got)
	}
}

func TestHTMLToTelegramHTMLTruncationClosesOpenTags(t *testing.T) {
	longBold := "<b>" + strings.Repeat("a", 1000) + "</b>"
	got, ok := htmlToTelegramHTML(longBold, summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.HasSuffix(got, "</b>...") {
		t.Errorf("expected truncated output to close open <b> tag, got suffix: %q", got[len(got)-20:])
	}

	// 截断后的结果必须依然是合法、可被解析的 HTML（没有未闭合标签）。
	if _, err := html.Parse(strings.NewReader(got)); err != nil {
		t.Fatalf("truncated HTML failed to re-parse: %v", err)
	}
}

func TestHTMLToTelegramHTMLEmptyBodyReturnsNotOK(t *testing.T) {
	_, ok := htmlToTelegramHTML("<script>only script</script>", summaryMaxRunes)
	if ok {
		t.Fatal("expected ok=false for a body with no visible content")
	}
}

func TestHTMLToTelegramHTMLCollapsesBlankLines(t *testing.T) {
	got, ok := htmlToTelegramHTML("<div>first</div><div><br></div><div></div><div><div></div></div><div>second</div>", summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("expected consecutive blank lines to be collapsed, got: %q", got)
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("expected both divs' text preserved, got: %q", got)
	}
}

func TestHTMLToTelegramHTMLStructuralTagsBecomeNewlines(t *testing.T) {
	got, ok := htmlToTelegramHTML("<div>first</div><div>second</div>", summaryMaxRunes)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("expected both divs' text preserved, got: %q", got)
	}
	if strings.Contains(got, "<div>") {
		t.Errorf("expected div tags themselves to be dropped, got: %q", got)
	}
}
