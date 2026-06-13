package telegram

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/i18n"
)

type ActionHandler func(ctx context.Context, action, userUUID, userID string) error

type StatsHandler func(ctx context.Context) (string, error)

func buildProxyHTTPClient(proxyURL string) (*http.Client, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("невозможно разобрать URL %q: %w", proxyURL, err)
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(u),
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport}, nil
}

func maskProxyURL(proxyURL string) string {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}

type Bot struct {
	api      *telego.Bot
	chatID   int64
	threadID int64
	adminIDs map[int64]bool
	logger   *logrus.Logger
	onAction ActionHandler
	onStats  StatsHandler

	settings  SettingsProvider
	pendingMu sync.Mutex
	pending   map[int64]pendingInput
}

func NewBot(token string, chatID, threadID int64, adminIDs []int64, proxyURL string, logger *logrus.Logger) (*Bot, error) {
	opts := []telego.BotOption{}
	if proxyURL != "" {
		client, err := buildProxyHTTPClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("TELEGRAM_PROXY: %w", err)
		}
		opts = append(opts, telego.WithHTTPClient(client))
		logger.WithField("proxy", maskProxyURL(proxyURL)).Info("Telegram бот: используется прокси")
	}

	bot, err := telego.NewBot(token, opts...)
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
		pending:  make(map[int64]pendingInput),
	}, nil
}

func (b *Bot) SetActionHandler(handler ActionHandler) {
	b.onAction = handler
}

func (b *Bot) SetStatsHandler(handler StatsHandler) {
	b.onStats = handler
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

func (b *Bot) SendManualAlert(text string, userUUID string, userID string, disableDuration int, ignoreDuration int) error {
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

	if ignoreDuration > 0 {
		ignoreLabel := fmt.Sprintf("%s %s", i18n.T("button.ignore_for"), FormatDuration(ignoreDuration))
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(ignoreLabel).WithCallbackData(fmt.Sprintf("ignore_temp:%s:%s", userUUID, userID)),
		})
	} else {
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(i18n.T("button.ignore")).WithCallbackData(fmt.Sprintf("ignore:%s:%s", userUUID, userID)),
		})
	}

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

func (b *Bot) SendStartupMessage(text string) error {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(i18n.T("settings.open")).WithCallbackData("cfg:menu"),
		),
	)
	return b.sendMsg(text, keyboard)
}

func botCommands() []telego.BotCommand {
	return []telego.BotCommand{
		{Command: "settings", Description: i18n.T("command.settings")},
		{Command: "stats", Description: i18n.T("command.stats")},
	}
}

func (b *Bot) RegisterCommands(ctx context.Context) {
	cmds := botCommands()

	for adminID := range b.adminIDs {
		if err := b.api.SetMyCommands(ctx, &telego.SetMyCommandsParams{
			Commands: cmds,
			Scope:    &telego.BotCommandScopeChat{Type: telego.ScopeTypeChat, ChatID: tu.ID(adminID)},
		}); err != nil {
			b.logger.WithError(err).WithField("admin", adminID).Warn("Telegram бот: не удалось зарегистрировать команды для админа")
		}
	}

	if b.chatID < 0 {
		if err := b.api.SetMyCommands(ctx, &telego.SetMyCommandsParams{
			Commands: cmds,
			Scope:    &telego.BotCommandScopeChatAdministrators{Type: telego.ScopeTypeChatAdministrators, ChatID: tu.ID(b.chatID)},
		}); err != nil {
			b.logger.WithError(err).Warn("Telegram бот: не удалось зарегистрировать команды для админов группы")
		}
	}
}

func (b *Bot) StartPolling(ctx context.Context) {
	b.RegisterCommands(ctx)

	updates, err := b.api.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 30,
	})
	if err != nil {
		b.logger.WithError(err).Error("Telegram бот: ошибка запуска polling")
		return
	}

	b.logger.Info("Telegram бот: запущен polling")

	for update := range updates {
		switch {
		case update.CallbackQuery != nil:
			b.handleCallback(ctx, update.CallbackQuery)
		case update.Message != nil && update.Message.From != nil:
			b.handleMessage(ctx, update.Message)
		}
	}

	b.logger.Info("Telegram бот: polling остановлен")
}

func (b *Bot) handleMessage(ctx context.Context, msg *telego.Message) {
	if !b.adminIDs[msg.From.ID] {
		return
	}

	text := strings.TrimSpace(msg.Text)

	switch strings.SplitN(text, "@", 2)[0] {
	case "/settings":
		b.handleSettingsCommand(ctx, msg)
		return
	case "/stats":
		b.handleStatsCommand(ctx, msg)
		return
	}

	b.handlePendingInput(ctx, msg)
}

func (b *Bot) handleStatsCommand(ctx context.Context, msg *telego.Message) {
	if b.onStats == nil {
		return
	}
	text, err := b.onStats(ctx)
	if err != nil {
		b.logger.WithError(err).Error("Telegram бот: ошибка получения статистики")
		b.replyText(ctx, msg.Chat.ID, i18n.T("stats.error"))
		return
	}
	b.replyText(ctx, msg.Chat.ID, text)
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

	if strings.HasPrefix(callback.Data, "cfg:") {
		b.handleSettingsCallback(ctx, callback)
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
				ChatID:             tu.ID(b.chatID),
				MessageID:          msg.MessageID,
				Text:               newText,
				ParseMode:          telego.ModeHTML,
				LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
				ReplyMarkup:        emptyMarkup,
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
