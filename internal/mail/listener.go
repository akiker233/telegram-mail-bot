package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	imapid "github.com/emersion/go-imap-id"
	"github.com/emersion/go-imap/client"
)

const (
	dialTimeout      = 10 * time.Second
	commandTimeout   = 30 * time.Second
	idleRestart      = 29 * time.Minute // RFC 2177 建议 IDLE 不超过 30 分钟
	pollInterval     = 60 * time.Second
	minBackoff       = 5 * time.Second
	maxBackoff       = 60 * time.Second
	authErrorBackoff = 5 * time.Minute // 认证失败后的重试间隔，给用户时间重新授权
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

// AuthErrorFunc 在账号发生认证类错误时被调用一次。与之前逻辑不同，Listen 现在
// 不会就此退出——它会以 authErrorBackoff 间隔持续重试，这样用户在重新授权后
// 无需手动删除和重新添加账号就能自动恢复监听。onAuthError 每个认证错误事件只调用一次，
// 避免持续重试期间反复刷通知骚扰用户。
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
// onAuthError 可以为 nil；非 nil 时在遇到认证类错误（凭证错误、OAuth token 失效等）时
// 被调用一次——之后 Listen 不会退出，而是以 authErrorBackoff 间隔持续重试，
// 用户重新授权后监听会自动恢复，无需手动删除和重新添加账号。
func Listen(ctx context.Context, cfg AccountConfig, store StateStore, notify NotifyFunc, onAuthError AuthErrorFunc) {
	backoff := minBackoff
	hasAuthErr := false

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
			slog.Warn("mail: authentication failed, will retry", "account_id", cfg.AccountID, "error", authErr)
			if onAuthError != nil && !hasAuthErr {
				onAuthError(cfg.AccountID, authErr.Err)
				hasAuthErr = true
			}
			backoff = authErrorBackoff
		} else {
			hasAuthErr = false
			if err != nil {
				slog.Warn("mail: session error", "account_id", cfg.AccountID, "error", err)
			}

			if err == nil {
				backoff = minBackoff
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
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
// TokenProvider 失败则不一定：它自己会把不可恢复的错误（refresh token 被撤销/过期）包成
// *AuthError，其余错误（刷新时网络抖动等）原样返回，%w 保留后 errors.As 仍能识别出前者，
// 后者则会走 Listen() 的指数退避重试，而不是被误判为永久失效。
func authenticate(c *client.Client, cfg AccountConfig) error {
	if cfg.TokenProvider == nil {
		if err := c.Login(cfg.Username, cfg.Password); err != nil {
			if isConnectionClosedErr(err) {
				// 服务器在响应前断开了连接（网络抖动/服务器限流），不代表密码错误，
				// 走普通重试路径，而不是判定为永久性认证失败。
				return err
			}
			return &AuthError{Err: err}
		}
		return nil
	}

	token, err := cfg.TokenProvider()
	if err != nil {
		return fmt.Errorf("get oauth token: %w", err)
	}
	if err := c.Authenticate(newXOAUTH2Client(cfg.Username, token)); err != nil {
		if isConnectionClosedErr(err) {
			return err
		}
		return &AuthError{Err: err}
	}
	return nil
}

// isConnectionClosedErr 判断错误是否是"服务器在命令响应前关闭了连接"，这类错误与凭证
// 是否正确无关（可能是网络抖动、服务器限流或临时故障），不应被当作永久性认证失败。
func isConnectionClosedErr(err error) bool {
	return strings.Contains(err.Error(), "connection closed") || errors.Is(err, io.EOF)
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
