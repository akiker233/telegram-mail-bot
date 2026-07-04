package telegram

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// handleCallback 处理按钮点击（inline keyboard callback query）。
// 按钮对应的文字会喂给现有的 Advance()/handleSessionReply()/handleSendSessionReply()，
// FSM 本身不因为按钮而改变。
func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	userID := cb.From.ID
	if !b.allowedUsers[userID] {
		return
	}

	// 消除客户端按钮上的加载动画，失败只记日志，不影响后续处理。
	if _, err := b.api.Request(tgbotapi.NewCallback(cb.ID, "")); err != nil {
		slog.Warn("telegram: answer callback query failed", "error", err)
	}

	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	messageID := cb.Message.MessageID

	prefix, value, ok := strings.Cut(cb.Data, ":")
	if !ok {
		return
	}

	switch prefix {
	case "del":
		b.handleDelCallback(chatID, messageID, userID, value)
		return
	case "addproto":
		b.clearKeyboard(chatID, messageID, "✅ 已选择协议: "+strings.ToUpper(value))
		b.startAddAccountSession(chatID, userID, value)
		return
	case "authmethod", "smtpopt", "addconfirm":
		sess := b.sessions.Get(userID)
		if sess == nil {
			b.clearKeyboard(chatID, messageID, "⚠️ 该操作已过期，请重新开始")
			return
		}
		b.clearKeyboard(chatID, messageID, "✅ 已选择: "+buttonLabel(prefix, value))
		b.handleSessionReply(chatID, userID, sess, buttonText(prefix, value))
		return
	case "sendacc", "sendconfirm":
		sess := b.sendSessions.Get(userID)
		if sess == nil {
			b.clearKeyboard(chatID, messageID, "⚠️ 该操作已过期，请重新开始")
			return
		}
		b.clearKeyboard(chatID, messageID, "✅ 已选择: "+buttonLabel(prefix, value))
		b.handleSendSessionReply(chatID, userID, sess, buttonText(prefix, value))
		return
	}
}

// handleDelCallback 处理列表里的删除按钮：删除账号后直接编辑原消息重新渲染列表。
func (b *Bot) handleDelCallback(chatID int64, messageID int, userID int64, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return
	}

	if err := b.deleteAccount(userID, id); err != nil {
		b.clearKeyboard(chatID, messageID, "❌ "+err.Error())
		return
	}

	text, keyboard, err := b.renderAccountsList(userID)
	if err != nil {
		b.clearKeyboard(chatID, messageID, "❌ 查询账号失败: "+err.Error())
		return
	}
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, text, keyboard)
	if _, err := b.api.Request(edit); err != nil {
		slog.Warn("telegram: edit message failed", "message_id", messageID, "chat_id", chatID, "error", err)
	}
}

// clearKeyboard 把原消息文字替换成 note 并去掉按钮，防止重复点击。
func (b *Bot) clearKeyboard(chatID int64, messageID int, note string) {
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, note, tgbotapi.InlineKeyboardMarkup{})
	if _, err := b.api.Request(edit); err != nil {
		slog.Warn("telegram: edit message failed", "message_id", messageID, "chat_id", chatID, "error", err)
	}
}

// buttonText 把回调数据的 value 映射成 Advance() 认识的文字输入。
func buttonText(prefix, value string) string {
	switch prefix {
	case "authmethod":
		return value // "oauth" / "password"
	case "smtpopt":
		if value == "yes" {
			return "是"
		}
		return "否"
	case "addconfirm", "sendconfirm":
		if value == "yes" {
			return "确认"
		}
		return "取消"
	case "sendacc":
		return value // 1-based 编号
	}
	return value
}

// buttonLabel 生成点击后展示在原消息里的确认文字。
func buttonLabel(prefix, value string) string {
	switch prefix {
	case "authmethod":
		if value == "oauth" {
			return "OAuth"
		}
		return "密码/授权码"
	case "smtpopt":
		if value == "yes" {
			return "是"
		}
		return "否"
	case "addconfirm", "sendconfirm":
		if value == "yes" {
			return "确认"
		}
		return "取消"
	case "sendacc":
		return fmt.Sprintf("账号 #%s", value)
	}
	return value
}
