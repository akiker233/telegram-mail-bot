package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	imapid "github.com/emersion/go-imap-id"
	"github.com/emersion/go-imap/client"
)

const (
	dialTimeout    = 10 * time.Second
	commandTimeout = 30 * time.Second
	idleRestart    = 29 * time.Minute // RFC 2177 建议 IDLE 不超过 30 分钟
	pollInterval   = 60 * time.Second
	minBackoff     = 5 * time.Second
	maxBackoff     = 60 * time.Second
)

var clientID = imapid.ID{
	imapid.FieldName:    "telegram-mail-bot",
	imapid.FieldVersion: "1.0",
}

// AccountConfig 是监听单个邮箱账号所需的连接信息。
// OAuth 账号不填 Password，而是提供 TokenProvider——因为 IDLE 长连接断线重连时也需要
// 重新拿一次最新的 access token（一次性 token 字符串在重连时可能已经过期）。
type AccountConfig struct {
	AccountID     int64
	Host          string
	Port          int
	Username      string
	Password      string
	TokenProvider func() (string, error) // 非空时使用 XOAUTH2 认证，忽略 Password
}

// AuthError 包装认证类失败（凭证错误、token 失效等），与网络类瞬时错误区分开。
// Listen 遇到 AuthError 会直接停止重试并把错误传给调用方处理，
// 而不是像网络抖动一样无限指数退避重试刷日志。
type AuthError struct {
	Err error
}

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// AuthErrorFunc 在账号发生认证类永久错误（无法通过重试恢复）时被调用一次，Listen 随即返回。
type AuthErrorFunc func(accountID int64, err error)

// NotifyFunc 在新邮件到达时被调用。
type NotifyFunc func(accountID int64, summary *Summary)

// StateStore 抽象了 UID 游标的读写，避免 mail 包直接依赖 db 包。
type StateStore interface {
	GetState(accountID int64) (uidValidity, lastUID uint32, err error)
	SaveState(accountID int64, uidValidity, lastUID uint32) error
}

// Listen 持续监听一个账号的 INBOX，直到 ctx 被取消。
// 内部处理断线重连（指数退避）和 IDLE 不支持时的轮询降级。
// onAuthError 可以为 nil；非 nil 时在遇到认证类永久错误（凭证错误、OAuth token 失效等）时
// 被调用一次，随后 Listen 直接返回，不再重试——这类错误重试也不会自愈，无限重试只会刷日志。
func Listen(ctx context.Context, cfg AccountConfig, store StateStore, notify NotifyFunc, onAuthError AuthErrorFunc) {
	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return
		}

		err := runSession(ctx, cfg, store, notify)
		if ctx.Err() != nil {
			return
		}

		var authErr *AuthError
		if errors.As(err, &authErr) {
			log.Printf("mail: account %d: authentication failed, giving up: %v", cfg.AccountID, authErr)
			if onAuthError != nil {
				onAuthError(cfg.AccountID, authErr.Err)
			}
			return
		}
		if err != nil {
			log.Printf("mail: account %d: session error: %v", cfg.AccountID, err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// runSession 建立一次连接，登录，同步增量邮件，然后循环 IDLE/轮询直到出错或 ctx 结束。
func runSession(ctx context.Context, cfg AccountConfig, store StateStore, notify NotifyFunc) error {
	c, err := dial(cfg)
	if err != nil {
		return err
	}
	defer c.Logout()

	applyIDIfNeeded(c, cfg.Host)

	c.Timeout = commandTimeout
	if err := authenticate(c, cfg); err != nil {
		return err
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		if err := syncOnce(c, cfg.AccountID, store, notify); err != nil {
			return err
		}

		useIdle, err := c.Support("IDLE")
		if err != nil {
			return err
		}

		if useIdle {
			if err := idleUntilEvent(ctx, c); err != nil {
				return err
			}
		} else {
			if err := waitPoll(ctx); err != nil {
				return err
			}
		}
	}
}

func dial(cfg AccountConfig) (*client.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: dialTimeout}
	return client.DialWithDialerTLS(dialer, addr, &tls.Config{ServerName: cfg.Host})
}

// authenticate 按账号的认证方式登录。密码登录失败和 XOAUTH2 认证失败都归类为 AuthError——
// 这类错误是永久性的（凭证不对、token 被撤销），重试不会自愈，需要用户重新配置账号才能解决。
func authenticate(c *client.Client, cfg AccountConfig) error {
	if cfg.TokenProvider == nil {
		if err := c.Login(cfg.Username, cfg.Password); err != nil {
			return &AuthError{Err: err}
		}
		return nil
	}

	token, err := cfg.TokenProvider()
	if err != nil {
		return &AuthError{Err: fmt.Errorf("get oauth token: %w", err)}
	}
	if err := c.Authenticate(newXOAUTH2Client(cfg.Username, token)); err != nil {
		return &AuthError{Err: err}
	}
	return nil
}

// applyIDIfNeeded 对 163/126 等要求客户端自报身份的邮箱服务商发送 IMAP ID 命令。
// 未处理会被服务器以 "Unsafe Login" 拒绝。
func applyIDIfNeeded(c *client.Client, host string) {
	host = strings.ToLower(host)
	if !strings.Contains(host, "163.com") && !strings.Contains(host, "126.com") {
		return
	}
	idClient := imapid.NewClient(c)
	if ok, err := idClient.SupportID(); err != nil || !ok {
		return
	}
	_, _ = idClient.ID(clientID)
}

// syncOnce 拉取自上次记录的 UID 之后的新邮件并推送，同时处理 UIDVALIDITY 变化。
func syncOnce(c *client.Client, accountID int64, store StateStore, notify NotifyFunc) error {
	mbox, err := c.Select("INBOX", false)
	if err != nil {
		return err
	}

	uidValidity, lastUID, err := store.GetState(accountID)
	if err != nil {
		return err
	}

	if uidValidity != mbox.UidValidity {
		// mailbox 被服务器重建，旧 UID 失效，从当前状态开始，不回溯历史邮件。
		uidValidity = mbox.UidValidity
		lastUID = mbox.UidNext - 1
		if err := store.SaveState(accountID, uidValidity, lastUID); err != nil {
			return err
		}
		return nil
	}

	summaries, maxUID, err := FetchNewSummaries(c, lastUID)
	if err != nil {
		return err
	}
	if maxUID == lastUID {
		return nil
	}

	for _, s := range summaries {
		notify(accountID, s)
	}

	return store.SaveState(accountID, uidValidity, maxUID)
}

// idleUntilEvent 进入 IDLE，直到收到服务器更新、达到重启周期或 ctx 结束。
func idleUntilEvent(ctx context.Context, c *client.Client) error {
	updates := make(chan client.Update, 8)
	c.Updates = updates
	defer func() { c.Updates = nil }()

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.Idle(stop, &client.IdleOptions{LogoutTimeout: idleRestart})
	}()

	select {
	case <-ctx.Done():
		close(stop)
		<-done
		return nil
	case update := <-updates:
		close(stop)
		err := <-done
		if err != nil {
			return err
		}
		_ = update
		return nil
	case err := <-done:
		return err
	}
}

func waitPoll(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(pollInterval):
		return nil
	}
}
