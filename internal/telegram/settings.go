package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/remnawave/limiter/internal/i18n"
)

type SettingKind int

const (
	SettingInt SettingKind = iota
	SettingFloat
	SettingBool
	SettingEnum
)

type SettingItem struct {
	Key        string
	Title      string
	Display    string
	Kind       SettingKind
	Allowed    []string
	Overridden bool
}

type SettingsProvider interface {
	Items() []SettingItem
	Item(key string) (SettingItem, bool)

	Apply(ctx context.Context, key, raw string) (string, error)

	Reset(ctx context.Context, key string) (string, error)

	ResetAll(ctx context.Context) error
}

func (b *Bot) SetSettingsProvider(p SettingsProvider) {
	b.settings = p
}

const pendingTTL = 5 * time.Minute

type pendingInput struct {
	key     string
	chatID  int64
	expires time.Time
}

func (b *Bot) setPending(adminID int64, key string, chatID int64) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	b.pending[adminID] = pendingInput{key: key, chatID: chatID, expires: time.Now().Add(pendingTTL)}
}

func (b *Bot) takePending(adminID int64) (pendingInput, bool) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	p, ok := b.pending[adminID]
	if !ok {
		return pendingInput{}, false
	}
	delete(b.pending, adminID)
	if time.Now().After(p.expires) {
		return pendingInput{}, false
	}
	return p, true
}

func (b *Bot) clearPending(adminID int64) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	delete(b.pending, adminID)
}

func (b *Bot) buildMenuKeyboard() *telego.InlineKeyboardMarkup {
	items := b.settings.Items()
	rows := make([][]telego.InlineKeyboardButton, 0, len(items)+1)
	for _, it := range items {
		marker := ""
		if it.Overridden {
			marker = "♻️ "
		}
		label := fmt.Sprintf("%s%s: %s", marker, it.Title, it.Display)
		data := "cfg:edit:" + it.Key
		if it.Kind == SettingBool {
			data = "cfg:toggle:" + it.Key
		}
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(label).WithCallbackData(data),
		})
	}
	rows = append(rows, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton(i18n.T("settings.reset_all")).WithCallbackData("cfg:resetall"),
	})
	return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (b *Bot) buildEditKeyboard(it SettingItem) *telego.InlineKeyboardMarkup {
	rows := make([][]telego.InlineKeyboardButton, 0, 4)
	if it.Kind == SettingBool || it.Kind == SettingEnum {
		valRow := make([]telego.InlineKeyboardButton, 0, len(it.Allowed))
		for _, v := range it.Allowed {
			label := v
			if v == it.Display {
				label = "✅ " + v
			}
			valRow = append(valRow, tu.InlineKeyboardButton(label).WithCallbackData("cfg:set:"+it.Key+":"+v))
		}
		rows = append(rows, valRow)
	}
	if it.Overridden {
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(i18n.T("settings.reset_one")).WithCallbackData("cfg:reset:" + it.Key),
		})
	}
	rows = append(rows, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton(i18n.T("settings.back")).WithCallbackData("cfg:menu"),
	})
	return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (b *Bot) sendSettingsMenu(ctx context.Context, chatID int64) {
	msg := tu.Message(tu.ID(chatID), i18n.T("settings.title")).
		WithParseMode(telego.ModeHTML).
		WithReplyMarkup(b.buildMenuKeyboard())
	if b.threadID != 0 && chatID == b.chatID {
		msg = msg.WithMessageThreadID(int(b.threadID))
	}
	if _, err := b.api.SendMessage(ctx, msg); err != nil {
		b.logger.WithError(err).Error("Telegram бот: ошибка отправки меню настроек")
	}
}

func (b *Bot) handleSettingsCommand(ctx context.Context, msg *telego.Message) {
	if b.settings == nil {
		return
	}
	if !b.adminIDs[msg.From.ID] {
		return
	}
	b.clearPending(msg.From.ID)
	b.sendSettingsMenu(ctx, msg.Chat.ID)
}

func (b *Bot) handlePendingInput(ctx context.Context, msg *telego.Message) bool {
	if b.settings == nil {
		return false
	}
	pend, ok := b.takePending(msg.From.ID)
	if !ok {
		return false
	}

	b.deleteMessage(ctx, msg.Chat.ID, msg.MessageID)

	raw := strings.TrimSpace(msg.Text)
	display, err := b.settings.Apply(ctx, pend.key, raw)
	if err != nil {
		b.replyText(ctx, pend.chatID, fmt.Sprintf("%s: %s", i18n.T("settings.apply_error"), err.Error()))
		return true
	}

	title := pend.key
	if it, ok := b.settings.Item(pend.key); ok {
		title = it.Title
	}
	b.replyText(ctx, pend.chatID, fmt.Sprintf(i18n.T("settings.applied"), title, display))
	b.sendSettingsMenu(ctx, pend.chatID)
	return true
}

func (b *Bot) replyText(ctx context.Context, chatID int64, text string) {
	msg := tu.Message(tu.ID(chatID), text).WithParseMode(telego.ModeHTML)
	if b.threadID != 0 && chatID == b.chatID {
		msg = msg.WithMessageThreadID(int(b.threadID))
	}
	if _, err := b.api.SendMessage(ctx, msg); err != nil {
		b.logger.WithError(err).Error("Telegram бот: ошибка отправки ответа настроек")
	}
}

func (b *Bot) handleSettingsCallback(ctx context.Context, callback *telego.CallbackQuery) {
	answer := func(text string) {
		_ = b.api.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            text,
		})
	}

	if b.settings == nil {
		answer("")
		return
	}

	b.clearPending(callback.From.ID)

	var chatID int64
	var messageID int
	if msg, ok := callback.Message.(*telego.Message); ok {
		chatID = msg.Chat.ID
		messageID = msg.MessageID
	}

	parts := strings.Split(callback.Data, ":")

	verb := ""
	if len(parts) > 1 {
		verb = parts[1]
	}

	switch verb {
	case "menu":
		b.editMarkup(ctx, chatID, messageID, i18n.T("settings.title"), b.buildMenuKeyboard())
		answer("")

	case "toggle":
		if len(parts) < 3 {
			answer("")
			return
		}
		key := parts[2]
		it, ok := b.settings.Item(key)
		if !ok {
			answer("")
			return
		}
		newVal := "true"
		if it.Display == "true" {
			newVal = "false"
		}
		display, err := b.settings.Apply(ctx, key, newVal)
		if err != nil {
			answer(i18n.T("settings.apply_error") + ": " + err.Error())
			return
		}
		b.editMarkup(ctx, chatID, messageID, i18n.T("settings.title"), b.buildMenuKeyboard())
		answer(fmt.Sprintf(i18n.T("settings.applied_toast"), it.Title+": "+display))

	case "edit":
		if len(parts) < 3 {
			answer("")
			return
		}
		key := parts[2]
		it, ok := b.settings.Item(key)
		if !ok {
			answer("")
			return
		}
		if it.Kind == SettingInt || it.Kind == SettingFloat {
			b.setPending(callback.From.ID, key, chatID)
			b.editMarkup(ctx, chatID, messageID,
				fmt.Sprintf(i18n.T("settings.prompt_input"), it.Title, it.Display),
				b.buildEditKeyboard(it))
			answer(i18n.T("settings.prompt_input_toast"))
			return
		}
		b.editMarkup(ctx, chatID, messageID,
			fmt.Sprintf(i18n.T("settings.prompt_choose"), it.Title, it.Display),
			b.buildEditKeyboard(it))
		answer("")

	case "set":
		if len(parts) < 4 {
			answer("")
			return
		}
		key, value := parts[2], parts[3]
		display, err := b.settings.Apply(ctx, key, value)
		if err != nil {
			answer(i18n.T("settings.apply_error") + ": " + err.Error())
			return
		}
		b.editMarkup(ctx, chatID, messageID, i18n.T("settings.title"), b.buildMenuKeyboard())
		answer(fmt.Sprintf(i18n.T("settings.applied_toast"), display))

	case "reset":
		if len(parts) < 3 {
			answer("")
			return
		}
		key := parts[2]
		display, err := b.settings.Reset(ctx, key)
		if err != nil {
			answer(i18n.T("settings.apply_error") + ": " + err.Error())
			return
		}
		b.editMarkup(ctx, chatID, messageID, i18n.T("settings.title"), b.buildMenuKeyboard())
		answer(fmt.Sprintf(i18n.T("settings.reset_toast"), display))

	case "resetall":
		if err := b.settings.ResetAll(ctx); err != nil {
			answer(i18n.T("settings.apply_error") + ": " + err.Error())
			return
		}
		b.editMarkup(ctx, chatID, messageID, i18n.T("settings.title"), b.buildMenuKeyboard())
		answer(i18n.T("settings.reset_all_toast"))

	default:
		answer("")
	}
}

func (b *Bot) editMarkup(ctx context.Context, chatID int64, messageID int, text string, keyboard *telego.InlineKeyboardMarkup) {
	if chatID == 0 || messageID == 0 {
		return
	}
	_, err := b.api.EditMessageText(ctx, &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ParseMode:   telego.ModeHTML,
		ReplyMarkup: keyboard,
	})
	if err != nil {
		b.logger.WithError(err).Error("Telegram бот: ошибка обновления меню настроек")
	}
}

func (b *Bot) deleteMessage(ctx context.Context, chatID int64, messageID int) {
	if chatID == 0 || messageID == 0 {
		return
	}
	if err := b.api.DeleteMessage(ctx, &telego.DeleteMessageParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
	}); err != nil {
		b.logger.WithError(err).Debug("Telegram бот: не удалось удалить сообщение с введённым значением")
	}
}
