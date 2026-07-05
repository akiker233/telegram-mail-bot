package telegram

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// handleCallback 处理按钮点击（inline keyboard callback query）。
// 按钮对应的文字会喂给 Advance() 推进状态机。
// 下一步如果还有按钮，则直接编辑原消息原地更新；如果需要文字输入，则清除原按钮后发送新消息。
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
		b.handleAddAccountButton(chatID, messageID, userID, prefix, value)
		return
	case "sendacc", "sendconfirm":
		b.handleSendButton(chatID, messageID, userID, prefix, value)
		return
	}
}

// handleAddAccountButton 处理 /addaccount 流程中的按钮点击（认证方式、SMTP选项、确认）。
func (b *Bot) handleAddAccountButton(chatID int64, messageID int, userID int64, prefix, value string) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		b.clearKeyboard(chatID, messageID, "⚠️ 该操作已过期，请重新开始")
		return
	}

	reply, finished, cancelled := sess.Advance(buttonText(prefix, value))

	if cancelled {
		b.sessions.Clear(userID)
		b.clearKeyboard(chatID, messageID, reply)
		return
	}

	if finished {
		b.sessions.Clear(userID)
		b.clearKeyboard(chatID, messageID, reply)
		if err := b.saveAccount(userID, sess.Draft); err != nil {
			b.reply(chatID, "❌ 保存账号失败: "+err.Error())
		}
		return
	}

	if sess.Step == StepOAuthPending {
		b.clearKeyboard(chatID, messageID, buttonLabel(prefix, value))
		b.startOAuthFlow(chatID, userID, sess)
		return
	}

	b.sessions.Persist(userID)
	b.sendTransitionReply(chatID, messageID, sess.Step, reply)
}

// handleSendButton 处理 /send 流程中的按钮点击（选择账号、确认）。
func (b *Bot) handleSendButton(chatID int64, messageID int, userID int64, prefix, value string) {
	sess := b.sendSessions.Get(userID)
	if sess == nil {
		b.clearKeyboard(chatID, messageID, "⚠️ 该操作已过期，请重新开始")
		return
	}

	reply, finished, cancelled := sess.Advance(buttonText(prefix, value))

	if cancelled {
		b.sendSessions.Clear(userID)
		b.clearKeyboard(chatID, messageID, reply)
		return
	}

	if finished {
		b.sendSessions.Clear(userID)
		b.clearKeyboard(chatID, messageID, reply)
		if err := b.sendMail(userID, sess.Draft); err != nil {
			b.reply(chatID, "❌ 发信失败: "+err.Error())
			return
		}
		b.reply(chatID, "✅ 邮件已发送")
		return
	}

	b.sendSessions.Persist(userID)
	b.sendTransitionReplyForSend(chatID, messageID, sess.Step, reply)
}

// sendTransitionReply 在按钮点击后：如果下一步有按钮则原地编辑原消息，否则清除原按钮后发新消息。
func (b *Bot) sendTransitionReply(chatID int64, messageID int, step Step, reply string) {
	kb := keyboardForStep(step)
	if kb != nil {
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, reply, *kb)
		if _, err := b.api.Request(edit); err != nil {
			slog.Warn("telegram: edit message failed on button transition", "message_id", messageID, "error", err)
			// 编辑失败时降级为发新消息
			b.replyWithKeyboard(chatID, reply, *kb)
		}
		return
	}
	b.clearKeyboard(chatID, messageID, "")
	b.reply(chatID, reply)
}

// sendTransitionReply 在按钮点击后：如果下一步有按钮则原地编辑原消息，否则清除原按钮后发新消息。
func (b *Bot) sendTransitionReplyForSend(chatID int64, messageID int, step SendStep, reply string) {
	kb := keyboardForSendStep(step)
	if kb != nil {
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, reply, *kb)
		if _, err := b.api.Request(edit); err != nil {
			slog.Warn("telegram: edit message failed on send button transition", "message_id", messageID, "error", err)
			b.replyWithKeyboard(chatID, reply, *kb)
		}
		return
	}
	b.clearKeyboard(chatID, messageID, "")
	b.reply(chatID, reply)
}

// keyboardForSendStep 返回 /send 流程中各步骤对应的 inline keyboard，无按钮的步骤返回 nil。
func keyboardForSendStep(step SendStep) *tgbotapi.InlineKeyboardMarkup {
	if step == SendStepConfirm {
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ 确认", "sendconfirm:yes"),
				tgbotapi.NewInlineKeyboardButtonData("🚫 取消", "sendconfirm:no"),
			),
		)
		return &kb
	}
	return nil
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
