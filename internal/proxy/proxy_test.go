package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
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
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
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
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	u, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy returned error: %v", err)
	}
	if u.User == nil {
		t.Fatal("expected proxy URL user info to be set")
	}
	username := u.User.Username()
	password, ok := u.User.Password()
	if !ok {
		t.Fatal("expected proxy URL password to be set")
	}
	if username != "user" || password != "pass" {
		t.Fatalf("expected user:pass, got %s:%s", username, password)
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

func TestNewClientSOCKS5h(t *testing.T) {
	c, err := NewClient("socks5h://127.0.0.1:1080")
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

// fakeSOCKS5Server 是一个最小伪 SOCKS5 服务器，仅用于验证认证握手。
type fakeSOCKS5Server struct {
	listener net.Listener
	addr     string
	user     string
	pass     string
	gotUser  string
	gotPass  string
	done     chan struct{}
}

func startFakeSOCKS5(t *testing.T, user, pass string) *fakeSOCKS5Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	s := &fakeSOCKS5Server{
		listener: l,
		addr:     l.Addr().String(),
		user:     user,
		pass:     pass,
		done:     make(chan struct{}),
	}
	go s.serve(t)
	return s
}

func (s *fakeSOCKS5Server) Close() {
	s.listener.Close()
	<-s.done
}

func (s *fakeSOCKS5Server) serve(t *testing.T) {
	defer close(s.done)
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	// 读取客户端问候并协商用户名/密码认证。
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if header[0] != 5 {
		return
	}
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	hasAuth := false
	for _, m := range methods {
		if m == 2 {
			hasAuth = true
			break
		}
	}
	if !hasAuth {
		conn.Write([]byte{5, 0xff})
		return
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return
	}

	// 读取用户名/密码认证请求。
	authHeader := make([]byte, 2)
	if _, err := io.ReadFull(conn, authHeader); err != nil {
		return
	}
	if authHeader[0] != 1 {
		return
	}
	userBuf := make([]byte, authHeader[1])
	if _, err := io.ReadFull(conn, userBuf); err != nil {
		return
	}
	plenBuf := make([]byte, 1)
	if _, err := io.ReadFull(conn, plenBuf); err != nil {
		return
	}
	passBuf := make([]byte, plenBuf[0])
	if _, err := io.ReadFull(conn, passBuf); err != nil {
		return
	}
	s.gotUser = string(userBuf)
	s.gotPass = string(passBuf)

	// 回复认证成功。
	if _, err := conn.Write([]byte{1, 0}); err != nil {
		return
	}

	// 读取 CONNECT 请求后返回连接被拒绝，让拨号流程正常结束。
	reqHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, reqHeader); err != nil {
		return
	}
	if reqHeader[0] != 5 || reqHeader[1] != 1 {
		return
	}
	var addrLen int
	switch reqHeader[3] {
	case 1:
		addrLen = 4
	case 4:
		addrLen = 16
	case 3:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return
		}
		addrLen = int(lenBuf[0])
	default:
		return
	}
	rest := make([]byte, addrLen+2)
	if _, err := io.ReadFull(conn, rest); err != nil {
		return
	}
	// 0x05 = connection refused
	conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
}

func TestNewClientSOCKS5WithAuth(t *testing.T) {
	srv := startFakeSOCKS5(t, "user", "pass")
	defer srv.Close()

	c, err := NewClient("socks5://user:pass@" + srv.addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = transport.DialContext(ctx, "tcp", "example.com:80")
	if err == nil {
		t.Fatal("expected dial error from fake server")
	}

	if srv.gotUser != "user" || srv.gotPass != "pass" {
		t.Fatalf("expected auth user:pass, got %s:%s", srv.gotUser, srv.gotPass)
	}
}

func TestNewClientUnsupportedScheme(t *testing.T) {
	_, err := NewClient("ftp://127.0.0.1:1080")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if !strings.Contains(err.Error(), "unsupported proxy scheme") {
		t.Fatalf("expected unsupported proxy scheme error, got %v", err)
	}
}

func TestNewClientInvalidURL(t *testing.T) {
	_, err := NewClient("http://[::1]:namedport")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "proxy: invalid proxy URL") {
		t.Fatalf("expected invalid proxy URL error, got %v", err)
	}
}
