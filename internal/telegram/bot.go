package telegram

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/i18n"
)

const (
	sendTimeout = 60 * time.Second

	callbackAnswerMaxLen = 200
)

type ActionHandler func(ctx context.Context, action string, userID int64) error

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

func truncateAnswer(s string) string {
	runes := []rune(s)
	if len(runes) <= callbackAnswerMaxLen {
		return s
	}
	return string(runes[:callbackAnswerMaxLen-1]) + "…"
}

type Bot struct {
	api      *telego.Bot
	chatID   int64
	threadID int64
	adminIDs map[int64]bool
	logger   *logrus.Logger
	onAction ActionHandler
	onStats  StatsHandler

	sendMu sync.Mutex

	settings  SettingsProvider
	pendingMu sync.Mutex
	pending   map[int64]pendingInput
}

func NewBot(token string, chatID, threadID int64, adminIDs []int64, proxyURL string, logger *logrus.Logger) (*Bot, error) {
	var caller ta.Caller = ta.DefaultFastHTTPCaller
	if proxyURL != "" {
		client, err := buildProxyHTTPClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("TELEGRAM_PROXY: %w", err)
		}
		caller = ta.HTTPCaller{Client: client}
		logger.WithField("proxy", maskProxyURL(proxyURL)).Info("Telegram бот: используется прокси")
	}

	opts := []telego.BotOption{
		telego.WithLogger(newBotLogger(logger, token)),
		telego.WithAPICaller(&ta.RetryCaller{
			Caller:       caller,
			MaxAttempts:  4,
			ExponentBase: 2,
			StartDelay:   time.Second,
			MaxDelay:     20 * time.Second,
			RateLimit:    ta.RetryRateLimitWaitOrAbort,
		}),
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

func (b *Bot) sendMsg(ctx context.Context, text string, keyboard *telego.InlineKeyboardMarkup) error {
	msg := tu.Message(tu.ID(b.chatID), text).
		WithParseMode(telego.ModeHTML).
		WithLinkPreviewOptions(&telego.LinkPreviewOptions{IsDisabled: true})

	if b.threadID != 0 {
		msg = msg.WithMessageThreadID(int(b.threadID))
	}

	if keyboard != nil {
		msg = msg.WithReplyMarkup(keyboard)
	}

	b.sendMu.Lock()
	defer b.sendMu.Unlock()

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	if _, err := b.api.SendMessage(sendCtx, msg); err != nil {
		return fmt.Errorf("не удалось отправить сообщение: %w", err)
	}
	return nil
}

func (b *Bot) SendManualAlert(ctx context.Context, text string, userID int64, disableDuration int, ignoreDuration int) error {
	rows := [][]telego.InlineKeyboardButton{
		{
			tu.InlineKeyboardButton(i18n.T("button.drop")).WithCallbackData(fmt.Sprintf("drop:%d", userID)),
			tu.InlineKeyboardButton(i18n.T("button.disable_forever")).WithCallbackData(fmt.Sprintf("disable:%d", userID)),
		},
	}

	if disableDuration > 0 {
		tempLabel := fmt.Sprintf("%s %s", i18n.T("button.disable_for"), FormatDuration(disableDuration))
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(tempLabel).WithCallbackData(fmt.Sprintf("disable_temp:%d", userID)),
		})
	}

	if ignoreDuration > 0 {
		ignoreLabel := fmt.Sprintf("%s %s", i18n.T("button.ignore_for"), FormatDuration(ignoreDuration))
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(ignoreLabel).WithCallbackData(fmt.Sprintf("ignore_temp:%d", userID)),
		})
	} else {
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(i18n.T("button.ignore")).WithCallbackData(fmt.Sprintf("ignore:%d", userID)),
		})
	}

	keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
	return b.sendMsg(ctx, text, keyboard)
}

func (b *Bot) SendAutoAlert(ctx context.Context, text string, userID int64) error {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(i18n.T("button.enable")).WithCallbackData(fmt.Sprintf("enable:%d", userID)),
		),
	)
	return b.sendMsg(ctx, text, keyboard)
}

func (b *Bot) SendMessage(ctx context.Context, text string) error {
	return b.sendMsg(ctx, text, nil)
}

func (b *Bot) SendStartupMessage(ctx context.Context, text string) error {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(i18n.T("settings.open")).WithCallbackData("cfg:menu"),
		),
	)
	return b.sendMsg(ctx, text, keyboard)
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

func (b *Bot) answerCallback(ctx context.Context, callbackID, text string) {
	if err := b.api.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            truncateAnswer(text),
	}); err != nil {
		b.logger.WithError(err).Debug("Telegram бот: не удалось ответить на callback")
	}
}

func (b *Bot) handleCallback(ctx context.Context, callback *telego.CallbackQuery) {
	callerID := callback.From.ID

	if !b.adminIDs[callerID] {
		b.logger.WithFields(logrus.Fields{
			"from": callerID,
			"data": callback.Data,
		}).Warn("Telegram бот: попытка нажать кнопку без прав администратора")
		b.answerCallback(ctx, callback.ID, i18n.T("callback.no_access"))
		return
	}

	if strings.HasPrefix(callback.Data, "cfg:") {
		b.handleSettingsCallback(ctx, callback)
		return
	}

	parts := strings.SplitN(callback.Data, ":", 2)
	if len(parts) != 2 {
		b.logger.WithField("data", callback.Data).Warn("Telegram бот: неверный формат callback data")
		b.answerCallback(ctx, callback.ID, i18n.T("callback.stale"))
		return
	}

	action := parts[0]
	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.logger.WithField("data", callback.Data).Warn("Telegram бот: устаревший callback data, кнопка недействительна")
		b.answerCallback(ctx, callback.ID, i18n.T("callback.stale"))
		return
	}

	adminName := callback.From.FirstName
	if callback.From.LastName != "" {
		adminName += " " + callback.From.LastName
	}
	if callback.From.Username != "" {
		adminName = "@" + callback.From.Username
	}

	if b.onAction != nil {
		if err := b.onAction(ctx, action, userID); err != nil {
			b.logger.WithError(err).WithFields(logrus.Fields{
				"action": action,
				"userID": userID,
				"admin":  callerID,
			}).Error("Telegram бот: ошибка выполнения действия")

			b.answerCallback(ctx, callback.ID, i18n.T("callback.error")+": "+err.Error())
			return
		}
	}

	b.logger.WithFields(logrus.Fields{
		"action": action,
		"userID": userID,
		"admin":  callerID,
	}).Info("Telegram бот: действие выполнено администратором")

	msg, _ := callback.Message.(*telego.Message)
	if msg != nil {
		originalText := msg.Text
		if originalText == "" {
			originalText = msg.Caption
		}
		newText := escapeHTML(originalText) + FormatActionResult(action, adminName)

		if _, err := b.api.EditMessageText(ctx, &telego.EditMessageTextParams{
			ChatID:             tu.ID(msg.Chat.ID),
			MessageID:          msg.MessageID,
			Text:               newText,
			ParseMode:          telego.ModeHTML,
			LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
			ReplyMarkup:        &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{}},
		}); err != nil {
			b.logger.WithError(err).Error("Telegram бот: ошибка редактирования сообщения")
		}
	}

	b.answerCallback(ctx, callback.ID, i18n.T("callback.done"))
}
