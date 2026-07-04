package telegram

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"telegram-mail-bot/internal/db"
)

// SendStep 表示 /send 多轮问答的当前阶段。
type SendStep int

const (
	SendStepChooseAccount SendStep = iota
	SendStepTo
	SendStepSubject
	SendStepBody
	SendStepConfirm
)

// SendDraft 保存 /send 过程中收集到的字段。
type SendDraft struct {
	AccountID int64
	To        string
	Subject   string
	Body      string
}

// SendableAccount 是 /send 选择账号步骤展示给用户的候选账号。
type SendableAccount struct {
	ID    int64
	Email string
}

// SendSession 是单个 Telegram 用户正在进行的 /send 会话状态。
type SendSession struct {
	Step     SendStep
	Draft    SendDraft
	Accounts []SendableAccount // 供 StepChooseAccount 按编号校验用户输入
}

// Advance 用用户的下一条消息推进会话状态机。
func (s *SendSession) Advance(text string) (reply string, finished bool, cancelled bool) {
	text = strings.TrimSpace(text)

	switch s.Step {
	case SendStepChooseAccount:
		idx, err := strconv.Atoi(text)
		if err != nil || idx < 1 || idx > len(s.Accounts) {
			return "⚠️ 请输入有效的编号", false, false
		}
		s.Draft.AccountID = s.Accounts[idx-1].ID
		s.Step = SendStepTo
		return "请输入收件人邮箱地址", false, false

	case SendStepTo:
		if !strings.Contains(text, "@") {
			return "⚠️ 邮箱地址格式不对，请重新输入", false, false
		}
		s.Draft.To = text
		s.Step = SendStepSubject
		return "请输入邮件主题", false, false

	case SendStepSubject:
		s.Draft.Subject = text
		s.Step = SendStepBody
		return "请输入邮件正文", false, false

	case SendStepBody:
		s.Draft.Body = text
		s.Step = SendStepConfirm
		return s.confirmPrompt(), false, false

	case SendStepConfirm:
		switch text {
		case "确认", "yes", "y", "Y":
			return "✅ 正在发送...", true, false
		case "取消", "no", "n", "N":
			return "🚫 已取消发信", false, true
		default:
			return "⚠️ 请回复\"确认\"发送，或\"取消\"放弃", false, false
		}
	}

	return "⚠️ 内部状态异常，请重新执行 /send", false, true
}

func (s *SendSession) confirmPrompt() string {
	return fmt.Sprintf(
		"📋 请确认邮件信息：\n收件人: %s\n主题: %s\n正文: %s\n\n回复\"确认\"发送，回复\"取消\"放弃",
		s.Draft.To, s.Draft.Subject, s.Draft.Body,
	)
}

// SendSessionStore 是按 Telegram 用户 ID 索引的内存会话表，与 SessionStore 结构相同但
// 类型不同（两套 FSM 状态不共享），独立存储让两种会话互斥的判断逻辑更清晰。
type SendSessionStore struct {
	mu sync.Mutex
	m  map[int64]*SendSession
	db *sql.DB
}

// NewSendSessionStore 创建一个空的会话表。database 用于持久化，nil 时仅使用内存存储。
func NewSendSessionStore(database *sql.DB) *SendSessionStore {
	return &SendSessionStore{m: make(map[int64]*SendSession), db: database}
}

// Get 返回指定用户当前的会话，不存在返回 nil。
func (s *SendSessionStore) Get(userID int64) *SendSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[userID]
}

// Start 为用户开始一个新的会话，覆盖任何已存在的会话。
func (s *SendSessionStore) Start(userID int64, accounts []SendableAccount) *SendSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := &SendSession{Step: SendStepChooseAccount, Accounts: accounts}
	s.m[userID] = sess
	s.persistLocked(userID, sess)
	return sess
}

// Clear 结束用户当前的会话。
func (s *SendSessionStore) Clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, userID)
	if s.db != nil {
		if err := db.DeleteSession(s.db, userID, "send"); err != nil {
			slog.Warn("telegram: delete send session", "user_id", userID, "error", err)
		}
	}
}

// Persist 在每次 Advance 推进状态后调用，将当前会话持久化到数据库。
func (s *SendSessionStore) Persist(userID int64) {
	s.mu.Lock()
	sess := s.m[userID]
	s.mu.Unlock()
	if sess != nil {
		s.persistLocked(userID, sess)
	}
}

// persistLocked 在持有锁的情况下持久化会话到数据库。调用方必须持有 s.mu。
func (s *SendSessionStore) persistLocked(userID int64, sess *SendSession) {
	if s.db == nil {
		return
	}
	draftJSON, err := json.Marshal(sess.Draft)
	if err != nil {
		slog.Warn("telegram: marshal send session", "user_id", userID, "error", err)
		return
	}
	if err := db.UpsertSession(s.db, &db.StoredSession{
		UserID:      userID,
		SessionType: "send",
		Step:        int(sess.Step),
		DraftJSON:   string(draftJSON),
	}); err != nil {
		slog.Warn("telegram: persist send session", "user_id", userID, "error", err)
	}
}
