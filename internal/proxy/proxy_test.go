package proxy

import (
	"net/http"
	"testing"
)

func TestNewClientEmptyReturnsNil(t *testing.T) {
	c, err := NewClient("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != nil {
		t.Fatalf("expected nil client for empty proxy URL, got %v", c)
	}
}

func TestNewClientHTTP(t *testing.T) {
	c, err := NewClient("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("expected Proxy to be set")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	u, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy returned error: %v", err)
	}
	if u.Host != "127.0.0.1:8080" {
		t.Fatalf("expected proxy host 127.0.0.1:8080, got %s", u.Host)
	}
}

func TestNewClientHTTPSWithAuth(t *testing.T) {
	c, err := NewClient("https://user:pass@proxy.example.com:8443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("expected Proxy to be set")
	}
}

func TestNewClientSOCKS5(t *testing.T) {
	c, err := NewClient("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("expected DialContext to be set")
	}
}

func TestNewClientSOCKS5WithAuth(t *testing.T) {
	c, err := NewClient("socks5://user:pass@127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientUnsupportedScheme(t *testing.T) {
	_, err := NewClient("ftp://127.0.0.1:1080")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}
