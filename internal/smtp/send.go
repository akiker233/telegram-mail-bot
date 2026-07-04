package smtp

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"

	"github.com/emersion/go-message/mail"
)

const dialTimeout = 10 * time.Second

// Config 是通过一个账号身份发信所需的连接信息。
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// Auth 为 nil 时使用 Password 做 PLAIN 认证；非 nil 时直接用该 Auth
	// 实例做认证（用于 XOAUTH2 等非密码方案），忽略 Password 字段。
	Auth smtp.Auth
}

// Message 是要发送的邮件内容。
type Message struct {
	To      string
	Subject string
	Body    string
}

// Send 通过 cfg 描述的 SMTP 服务器发送一封邮件。
// 端口 465 走隐式 TLS（连接建立后直接是加密的），其他端口（通常是587）走明文连接后 STARTTLS 升级，
// 因为 net/smtp.SendMail 高层封装不支持隐式 TLS，这两种模式都需要手动管理连接建立。
func Send(cfg Config, msg Message) error {
	raw, err := buildMessage(cfg.From, msg)
	if err != nil {
		return fmt.Errorf("smtp: build message: %w", err)
	}

	client, err := dial(cfg.Host, cfg.Port)
	if err != nil {
		return fmt.Errorf("smtp: dial: %w", err)
	}
	defer client.Close()

	if !isImplicitTLS(cfg.Port) {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				return fmt.Errorf("smtp: starttls: %w", err)
			}
		}
	}

	if cfg.Auth != nil {
		if err := client.Auth(cfg.Auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	} else {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("smtp: mail from: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp: rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		w.Close()
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: close data writer: %w", err)
	}

	return client.Quit()
}

// isImplicitTLS 判断该端口应该在连接建立时就直接走 TLS（465），还是先建立明文连接
// 再通过 STARTTLS 升级（587 等其他端口）。
func isImplicitTLS(port int) bool {
	return port == 465
}

func dial(host string, port int) (*smtp.Client, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: dialTimeout}

	if isImplicitTLS(port) {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
		if err != nil {
			return nil, err
		}
		return smtp.NewClient(conn, host)
	}

	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return smtp.NewClient(conn, host)
}

// buildMessage 用 go-message/mail 构造一封 RFC 5322 邮件（From/To/Subject头 + 纯文本正文）。
func buildMessage(from string, msg Message) ([]byte, error) {
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, err
	}
	toAddr, err := mail.ParseAddress(msg.To)
	if err != nil {
		return nil, err
	}

	var header mail.Header
	header.SetAddressList("From", []*mail.Address{fromAddr})
	header.SetAddressList("To", []*mail.Address{toAddr})
	header.SetSubject(msg.Subject)
	header.SetDate(time.Now())

	var buf bytes.Buffer
	w, err := mail.CreateSingleInlineWriter(&buf, header)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(msg.Body)); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
