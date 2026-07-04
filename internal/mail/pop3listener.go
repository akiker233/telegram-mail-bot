package mail

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	pop3 "github.com/knadh/go-pop3"
)

const (
	pop3DialTimeout = 10 * time.Second
	pruneInterval   = 24 * time.Hour
	seenUIDRetain   = 30 * 24 * time.Hour
)

// POP3AccountConfig 是监听单个 POP3 账号所需的连接信息。
// 单独定义而不是往 AccountConfig 里塞可选字段，避免一个 struct 同时表达两套协议的语义。
type POP3AccountConfig struct {
	AccountID int64
	Host      string
	Port      int
	Username  string
	Password  string
}

// POP3StateStore 抽象了"已处理过的 UIDL 集合"的读写，避免 mail 包直接依赖 db 包。
// 与 IMAP 的 StateStore 语义完全不同（UID 游标 vs 已见集合），所以是独立的接口。
type POP3StateStore interface {
	HasSeenUID(accountID int64, uidl string) (bool, error)
	MarkSeenUID(accountID int64, uidl string) error
	PruneSeenUIDs(accountID int64, olderThan time.Time) error
}

// ErrUIDLUnsupported 表示服务器不支持 UIDL 命令，无法安全地做增量同步。
// POP3 账号在这种情况下应该直接放弃监听并提示用户改用 IMAP，而不是用"假设邮件只追加不重排"
// 这种不可靠的假设来弱保证增量——一旦假设被打破就会导致重复推送或漏推送。
var ErrUIDLUnsupported = fmt.Errorf("mail: pop3 server does not support UIDL")

// ListenPOP3 持续轮询一个 POP3 账号的收件箱，直到 ctx 被取消。
// POP3 没有 IDLE 概念，每轮都是新建连接、拉取增量、断开，不长期占用连接。
// 遇到认证错误时不会永久停止，而是以 authErrorBackoff 间隔持续重试，
// 用户重新授权后监听会自动恢复。
func ListenPOP3(ctx context.Context, cfg POP3AccountConfig, store POP3StateStore, notify NotifyFunc, onAuthError AuthErrorFunc) {
	backoff := minBackoff
	lastPrune := time.Time{}
	hasAuthErr := false

	for {
		if ctx.Err() != nil {
			return
		}

		err := pop3SyncOnce(cfg, store, notify)

		if err == ErrUIDLUnsupported {
			slog.Warn("mail: pop3 server does not support UIDL, giving up", "account_id", cfg.AccountID)
			if onAuthError != nil {
				onAuthError(cfg.AccountID, err)
			}
			return
		}
		if authErr, ok := err.(*AuthError); ok {
			slog.Warn("mail: pop3 authentication failed, will retry", "account_id", cfg.AccountID, "error", authErr)
			if onAuthError != nil && !hasAuthErr {
				onAuthError(cfg.AccountID, authErr.Err)
				hasAuthErr = true
			}
			wait := authErrorBackoff
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}

		hasAuthErr = false
		wait := pollInterval
		if err != nil {
			slog.Warn("mail: pop3 session error", "account_id", cfg.AccountID, "error", err)
			wait = backoff
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = minBackoff
			if time.Since(lastPrune) > pruneInterval {
				if pruneErr := store.PruneSeenUIDs(cfg.AccountID, time.Now().Add(-seenUIDRetain)); pruneErr != nil {
					slog.Warn("mail: prune pop3 seen uids", "account_id", cfg.AccountID, "error", pruneErr)
				}
				lastPrune = time.Now()
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func pop3SyncOnce(cfg POP3AccountConfig, store POP3StateStore, notify NotifyFunc) error {
	client := pop3.New(pop3.Opt{
		Host:        cfg.Host,
		Port:        cfg.Port,
		DialTimeout: pop3DialTimeout,
		TLSEnabled:  true,
	})

	conn, err := client.NewConn()
	if err != nil {
		return err
	}
	defer conn.Quit()

	if err := conn.Auth(cfg.Username, cfg.Password); err != nil {
		return &AuthError{Err: err}
	}

	all, err := conn.Uidl(0)
	if err != nil {
		return ErrUIDLUnsupported
	}

	pending, err := filterUnseen(all, func(uidl string) (bool, error) {
		return store.HasSeenUID(cfg.AccountID, uidl)
	})
	if err != nil {
		return err
	}

	for _, msg := range pending {
		raw, err := conn.RetrRaw(msg.ID)
		if err != nil {
			slog.Warn("mail: retr message", "account_id", cfg.AccountID, "msg_id", msg.ID, "error", err)
			continue
		}
		summary, err := BuildSummary(bytes.NewReader(raw.Bytes()))
		if err != nil {
			slog.Warn("mail: parse message", "account_id", cfg.AccountID, "msg_id", msg.ID, "error", err)
			continue
		}
		notify(cfg.AccountID, summary)
		if err := store.MarkSeenUID(cfg.AccountID, msg.UID); err != nil {
			return err
		}
	}

	return nil
}

// filterUnseen 从 all 中挑出 store 判断为未见过的消息，是核心去重逻辑的纯函数部分，
// 便于在不连接真实 POP3 服务器的情况下单独测试。
func filterUnseen(all []pop3.MessageID, hasSeen func(uidl string) (bool, error)) ([]pop3.MessageID, error) {
	var pending []pop3.MessageID
	for _, msg := range all {
		seen, err := hasSeen(msg.UID)
		if err != nil {
			return nil, err
		}
		if !seen {
			pending = append(pending, msg)
		}
	}
	return pending, nil
}
