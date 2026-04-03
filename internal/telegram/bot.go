package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/i18n"
)

type ActionHandler func(ctx context.Context, action, userUUID, userID string) error

type Bot struct {
	api      *telego.Bot
	chatID   int64
	threadID int64
	adminIDs map[int64]bool
	logger   *logrus.Logger
	onAction ActionHandler
}

func NewBot(token string, chatID, threadID int64, adminIDs []int64, logger *logrus.Logger) (*Bot, error) {
	bot, err := telego.NewBot(token)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать Telegram бота: %w", err)
	}

	admins := make(map[int64]bool, len(adminIDs))
	for _, id := range adminIDs {
		admins[id] = true
	}

	return &Bot{
		api:      bot,
		chatID:   chatID,
		threadID: threadID,
		adminIDs: admins,
		logger:   logger,
	}, nil
}

func (b *Bot) SetActionHandler(handler ActionHandler) {
	b.onAction = handler
}

func (b *Bot) sendMsg(text string, keyboard *telego.InlineKeyboardMarkup) error {
	msg := tu.Message(tu.ID(b.chatID), text).
		WithParseMode(telego.ModeHTML).
		WithLinkPreviewOptions(&telego.LinkPreviewOptions{IsDisabled: true})

	if b.threadID != 0 {
		msg = msg.WithMessageThreadID(int(b.threadID))
	}

	if keyboard != nil {
		msg = msg.WithReplyMarkup(keyboard)
	}

	_, err := b.api.SendMessage(context.Background(), msg)
	if err != nil {
		return fmt.Errorf("не удалось отправить сообщение: %w", err)
	}
	return nil
}

func (b *Bot) SendManualAlert(text string, userUUID string, userID string, disableDuration int) error {
	rows := [][]telego.InlineKeyboardButton{
		{
			tu.InlineKeyboardButton(i18n.T("button.drop")).WithCallbackData(fmt.Sprintf("drop:%s:%s", userUUID, userID)),
			tu.InlineKeyboardButton(i18n.T("button.disable_forever")).WithCallbackData(fmt.Sprintf("disable:%s:%s", userUUID, userID)),
		},
	}

	if disableDuration > 0 {
		tempLabel := fmt.Sprintf("%s %s", i18n.T("button.disable_for"), FormatDuration(disableDuration))
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(tempLabel).WithCallbackData(fmt.Sprintf("disable_temp:%s:%s", userUUID, userID)),
		})
	}

	rows = append(rows, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton(i18n.T("button.ignore")).WithCallbackData(fmt.Sprintf("ignore:%s:%s", userUUID, userID)),
	})

	keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
	return b.sendMsg(text, keyboard)
}

func (b *Bot) SendAutoAlert(text string, userUUID string) error {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(i18n.T("button.enable")).WithCallbackData(fmt.Sprintf("enable:%s:", userUUID)),
		),
	)
	return b.sendMsg(text, keyboard)
}

func (b *Bot) SendMessage(text string) error {
	return b.sendMsg(text, nil)
}

func (b *Bot) StartPolling(ctx context.Context) {
	updates, err := b.api.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 30,
	})
	if err != nil {
		b.logger.WithError(err).Error("Telegram бот: ошибка запуска polling")
		return
	}

	b.logger.Info("Telegram бот: запущен polling")

	for update := range updates {
		if update.CallbackQuery == nil {
			continue
		}
		b.handleCallback(ctx, update.CallbackQuery)
	}

	b.logger.Info("Telegram бот: polling остановлен")
}

func (b *Bot) handleCallback(ctx context.Context, callback *telego.CallbackQuery) {
	callerID := callback.From.ID

	if !b.adminIDs[callerID] {
		_ = b.api.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            i18n.T("callback.no_access"),
		})
		return
	}

	parts := strings.SplitN(callback.Data, ":", 3)
	if len(parts) < 3 {
		b.logger.WithField("data", callback.Data).Warn("Telegram бот: неверный формат callback data")
		return
	}

	action := parts[0]
	userUUID := parts[1]
	userID := parts[2]

	adminName := callback.From.FirstName
	if callback.From.LastName != "" {
		adminName += " " + callback.From.LastName
	}
	if callback.From.Username != "" {
		adminName = "@" + callback.From.Username
	}

	if b.onAction != nil {
		if err := b.onAction(ctx, action, userUUID, userID); err != nil {
			b.logger.WithError(err).WithFields(logrus.Fields{
				"action":   action,
				"userUUID": userUUID,
				"userID":   userID,
			}).Error("Telegram бот: ошибка выполнения действия")

			_ = b.api.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
				CallbackQueryID: callback.ID,
				Text:            i18n.T("callback.error") + ": " + err.Error(),
			})
			return
		}
	}

	username := userUUID

	actionResult := FormatActionResult(action, adminName, username)

	originalText := ""
	if callback.Message != nil {
		if msg, ok := callback.Message.(*telego.Message); ok {
			originalText = msg.Text
			if originalText == "" {
				originalText = msg.Caption
			}
		}
	}

	newText := originalText + actionResult
	emptyMarkup := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{},
	}

	if callback.Message != nil {
		if msg, ok := callback.Message.(*telego.Message); ok {
			_, err := b.api.EditMessageText(ctx, &telego.EditMessageTextParams{
				ChatID:                tu.ID(b.chatID),
				MessageID:             msg.MessageID,
				Text:                  newText,
				ParseMode:             telego.ModeHTML,
				LinkPreviewOptions:    &telego.LinkPreviewOptions{IsDisabled: true},
				ReplyMarkup:           emptyMarkup,
			})
			if err != nil {
				b.logger.WithError(err).Error("Telegram бот: ошибка редактирования сообщения")
			}
		}
	}

	_ = b.api.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
		CallbackQueryID: callback.ID,
		Text:            i18n.T("callback.done"),
	})
}
