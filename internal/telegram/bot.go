package telegram

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/i18n"
)

const (
	sendTimeout = 60 * time.Second

	updateTimeout = 90 * time.Second
	answerTimeout = 10 * time.Second

	pollTimeout = 31 * time.Second
	httpTimeout = pollTimeout + 15*time.Second

	callbackAnswerMaxLen = 200
)

type Action struct {
	Name    string
	UserID  int64
	Minutes int
}

type ActionHandler func(ctx context.Context, a Action) error

var ErrStaleAction = errors.New("кнопка устарела")

func actionData(action string, userID int64) string {
	return fmt.Sprintf("%s:%d", action, userID)
}

func timedActionData(action string, userID int64, minutes int) string {
	return fmt.Sprintf("%s:%d:%d", action, userID, minutes)
}

func parseActionData(data string) (Action, bool) {
	parts := strings.Split(data, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" {
		return Action{}, false
	}
	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Action{}, false
	}
	a := Action{Name: parts[0], UserID: userID}
	if len(parts) == 3 {
		minutes, err := strconv.Atoi(parts[2])
		if err != nil || minutes <= 0 {
			return Action{}, false
		}
		a.Minutes = minutes
	}
	return a, true
}

type StatsHandler func(ctx context.Context) (string, error)

func buildHTTPClient(proxyURL string) (*http.Client, error) {
	var proxy func(*http.Request) (*url.URL, error)
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("невозможно разобрать URL %q: %w", proxyURL, err)
		}
		proxy = http.ProxyURL(u)
	}
	transport := &http.Transport{
		Proxy: proxy,
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
	return &http.Client{Transport: transport, Timeout: httpTimeout}, nil
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
	api      *tgbot.Bot
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
	return newBot(token, chatID, threadID, adminIDs, proxyURL, logger)
}

func newBot(token string, chatID, threadID int64, adminIDs []int64, proxyURL string, logger *logrus.Logger, extra ...tgbot.Option) (*Bot, error) {
	if !validToken(token) {
		return nil, fmt.Errorf("не удалось создать Telegram бота: неверный формат токена")
	}

	client, err := buildHTTPClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("TELEGRAM_PROXY: %w", err)
	}
	if proxyURL != "" {
		logger.WithField("proxy", maskProxyURL(proxyURL)).Info(i18n.T("log.telegram_proxy"))
	}

	admins := make(map[int64]bool, len(adminIDs))
	for _, id := range adminIDs {
		admins[id] = true
	}

	b := &Bot{
		chatID:   chatID,
		threadID: threadID,
		adminIDs: admins,
		logger:   logger,
		pending:  make(map[int64]pendingInput),
	}

	bl := newBotLogger(logger, token)
	opts := []tgbot.Option{
		tgbot.WithSkipGetMe(),
		tgbot.WithHTTPClient(pollTimeout, &retryClient{
			client:      client,
			maxAttempts: 4,
			startDelay:  time.Second,
			maxDelay:    20 * time.Second,
		}),
		tgbot.WithErrorsHandler(func(err error) { bl.Errorf("%v", err) }),
		tgbot.WithDebugHandler(bl.Debugf),
		tgbot.WithDefaultHandler(b.handleUpdate),
		tgbot.WithNotAsyncHandlers(),
		// Telegram запоминает allowed_updates от прошлых getUpdates/webhook и без
		// явного списка не присылает отсутствующие в нём типы. Общий или ранее
		// использованный бот мог остаться без callback_query — тогда кнопки
		// настроек и действий молча не работают. Запрашиваем нужные типы явно.
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "callback_query", "channel_post", "my_chat_member"}),
	}
	api, err := tgbot.New(token, append(opts, extra...)...)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать Telegram бота: %w", err)
	}
	b.api = api
	return b, nil
}

func validToken(token string) bool {
	id, secret, ok := strings.Cut(token, ":")
	if !ok || len(secret) < 30 {
		return false
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return false
	}
	return !strings.ContainsAny(secret, " /?#")
}

func (b *Bot) SetActionHandler(handler ActionHandler) {
	b.onAction = handler
}

func (b *Bot) SetStatsHandler(handler StatsHandler) {
	b.onStats = handler
}

func (b *Bot) sendRich(ctx context.Context, chatID int64, threadID int, m richMsg) (int, error) {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	var reply *models.ReplyParameters
	if m.ReplyTo != 0 {
		reply = &models.ReplyParameters{MessageID: m.ReplyTo, AllowSendingWithoutReply: true}
	}

	sent, err := b.api.SendRichMessage(sendCtx, &tgbot.SendRichMessageParams{
		ChatID:          chatID,
		MessageThreadID: threadID,
		RichMessage:     models.InputRichMessage{HTML: m.render(), SkipEntityDetection: true},
		ReplyParameters: reply,
	})
	if err == nil {
		return sent.ID, nil
	}
	if !errors.Is(err, tgbot.ErrorBadRequest) {
		return 0, err
	}

	b.logger.WithError(err).Warn(i18n.T("log.telegram_rich_rejected"))
	params := &tgbot.SendMessageParams{
		ChatID:             chatID,
		MessageThreadID:    threadID,
		Text:               plainText(m.HTML),
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()},
		ReplyParameters:    reply,
	}
	if len(m.Rows) > 0 {
		params.ReplyMarkup = m.keyboard()
	}
	sent, err = b.api.SendMessage(sendCtx, params)
	if err != nil {
		return 0, err
	}
	return sent.ID, nil
}

func (b *Bot) editRich(ctx context.Context, chatID int64, messageID int, m richMsg) error {
	if chatID == 0 || messageID == 0 {
		return nil
	}
	editCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	_, err := b.api.EditMessageText(editCtx, &tgbot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		RichMessage: &models.InputRichMessage{HTML: m.render(), SkipEntityDetection: true},
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}},
	})
	if err == nil || isNotModified(err) {
		return nil
	}
	if !errors.Is(err, tgbot.ErrorBadRequest) {
		return err
	}

	b.logger.WithError(err).Warn(i18n.T("log.telegram_rich_edit_rejected"))
	_, err = b.api.EditMessageText(editCtx, &tgbot.EditMessageTextParams{
		ChatID:             chatID,
		MessageID:          messageID,
		Text:               plainText(m.HTML),
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()},
		ReplyMarkup:        m.keyboard(),
	})
	if err != nil && !isNotModified(err) {
		return err
	}
	return nil
}

func isNotModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

func (b *Bot) sendMsg(ctx context.Context, m richMsg) error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()

	if _, err := b.sendRich(ctx, b.chatID, int(b.threadID), m); err != nil {
		return fmt.Errorf("не удалось отправить сообщение: %w", err)
	}
	return nil
}

func (b *Bot) SendManualAlert(ctx context.Context, text string, userID int64, disableDuration int, ignoreDuration int) error {
	rows := [][]richButton{
		{cbButton(i18n.T("button.drop"), actionData("drop", userID), stylePrimary)},
	}

	disableRow := []richButton{
		cbButton(i18n.T("button.disable_forever"), actionData("disable", userID), styleDanger),
	}
	if disableDuration > 0 {
		tempLabel := fmt.Sprintf("%s %s", i18n.T("button.disable_for"), FormatDuration(disableDuration))
		disableRow = append(disableRow, cbButton(tempLabel, timedActionData("disable_temp", userID, disableDuration), styleDanger))
	}
	rows = append(rows, disableRow)
	rows = append(rows, []richButton{ignoreButton(userID, ignoreDuration)})

	return b.sendMsg(ctx, richMsg{HTML: text, Rows: rows})
}

func (b *Bot) SendAutoAlert(ctx context.Context, text string, userID int64) error {
	rows := [][]richButton{
		{cbButton(i18n.T("button.enable"), actionData("enable", userID), styleSuccess)},
	}
	return b.sendMsg(ctx, richMsg{HTML: text, Rows: rows})
}

func (b *Bot) SendSoftAlert(ctx context.Context, text string, userID int64, ignoreDuration int) error {
	row := []richButton{
		cbButton(i18n.T("button.drop"), actionData("drop", userID), stylePrimary),
		ignoreButton(userID, ignoreDuration),
	}
	return b.sendMsg(ctx, richMsg{HTML: text, Rows: [][]richButton{row}})
}

func ignoreButton(userID int64, ignoreDuration int) richButton {
	if ignoreDuration > 0 {
		label := fmt.Sprintf("%s %s", i18n.T("button.ignore_for"), FormatDuration(ignoreDuration))
		return cbButton(label, timedActionData("ignore_temp", userID, ignoreDuration), styleNone)
	}
	return cbButton(i18n.T("button.ignore"), actionData("ignore", userID), styleNone)
}

func undoRows(action string, userID int64) [][]richButton {
	if action != "disable" && action != "disable_temp" {
		return nil
	}
	return [][]richButton{{cbButton(i18n.T("button.enable"), actionData("enable", userID), styleSuccess)}}
}

func (b *Bot) SendMessage(ctx context.Context, text string) error {
	return b.sendMsg(ctx, richMsg{HTML: "<p>" + text + "</p>"})
}

func (b *Bot) SendStats(ctx context.Context, text string) error {
	return b.sendMsg(ctx, richMsg{HTML: text, Rows: statsRows()})
}

func (b *Bot) SendStartupMessage(ctx context.Context, text string) error {
	rows := [][]richButton{{
		cbButton(i18n.T("settings.open"), "cfg:menu", stylePrimary),
		cbButton(i18n.T("button.stats"), "ui:stats", styleNone),
	}}
	return b.sendMsg(ctx, richMsg{HTML: text, Rows: rows})
}

func statsRows() [][]richButton {
	return [][]richButton{{
		cbButton(i18n.T("button.refresh"), "ui:stats", stylePrimary),
		cbButton(i18n.T("settings.open"), "cfg:menu", styleNone),
	}}
}

func botCommands() []models.BotCommand {
	return []models.BotCommand{
		{Command: "settings", Description: i18n.T("command.settings")},
		{Command: "stats", Description: i18n.T("command.stats")},
	}
}

func (b *Bot) RegisterCommands(ctx context.Context) {
	cmds := botCommands()

	for adminID := range b.adminIDs {
		if _, err := b.api.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
			Commands: cmds,
			Scope:    &models.BotCommandScopeChat{ChatID: adminID},
		}); err != nil {
			b.logger.WithError(err).WithField("admin", adminID).Warn(i18n.T("log.telegram_commands_admin_failed"))
		}
	}

	if b.chatID < 0 {
		if _, err := b.api.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
			Commands: cmds,
			Scope:    &models.BotCommandScopeChatAdministrators{ChatID: b.chatID},
		}); err != nil {
			b.logger.WithError(err).Warn(i18n.T("log.telegram_commands_group_failed"))
		}
	}
}

func (b *Bot) StartPolling(ctx context.Context) {
	b.RegisterCommands(ctx)

	b.logger.Info(i18n.T("log.telegram_polling_started"))

	b.api.Start(ctx)

	b.logger.Info(i18n.T("log.telegram_polling_stopped"))
}

func (b *Bot) handleUpdate(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	switch {
	case update.CallbackQuery != nil:
		b.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil && update.Message.From != nil:
		b.handleMessage(ctx, update.Message)
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *models.Message) {
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

func (b *Bot) statsMessage(ctx context.Context) (richMsg, error) {
	text, err := b.onStats(ctx)
	if err != nil {
		return richMsg{}, err
	}
	return richMsg{HTML: text, Rows: statsRows()}, nil
}

func (b *Bot) handleStatsCommand(ctx context.Context, msg *models.Message) {
	if b.onStats == nil {
		return
	}
	m, err := b.statsMessage(ctx)
	if err != nil {
		b.logger.WithError(err).Error(i18n.T("log.telegram_stats_failed"))
		m = richMsg{HTML: "<p>" + escapeHTML(i18n.T("stats.error")) + "</p>"}
	}
	if _, err := b.sendRich(ctx, msg.Chat.ID, b.threadFor(msg.Chat.ID), m); err != nil {
		b.logger.WithError(err).Error(i18n.T("log.telegram_stats_send_failed"))
	}
}

func (b *Bot) handleStatsCallback(ctx context.Context, callback *models.CallbackQuery) {
	if b.onStats == nil {
		b.answerCallback(ctx, callback.ID, "")
		return
	}
	m, err := b.statsMessage(ctx)
	if err != nil {
		b.logger.WithError(err).Error(i18n.T("log.telegram_stats_failed"))
		b.answerCallback(ctx, callback.ID, i18n.T("stats.error"))
		return
	}
	chatID, messageID, _ := callbackMessage(callback)
	b.showScreen(ctx, chatID, messageID, m)
	b.answerCallback(ctx, callback.ID, i18n.T("stats.refreshed"))
}

func (b *Bot) answerCallback(ctx context.Context, callbackID, text string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), answerTimeout)
	defer cancel()

	if _, err := b.api.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            truncateAnswer(text),
	}); err != nil {
		b.logger.WithError(err).Debug(i18n.T("log.telegram_callback_failed"))
	}
}

func (b *Bot) handleCallback(ctx context.Context, callback *models.CallbackQuery) {
	callerID := callback.From.ID

	if !b.adminIDs[callerID] {
		b.logger.WithFields(logrus.Fields{
			"from": callerID,
			"data": callback.Data,
		}).Warn(i18n.T("log.telegram_unauthorized"))
		b.answerCallback(ctx, callback.ID, i18n.T("callback.no_access"))
		return
	}

	switch {
	case strings.HasPrefix(callback.Data, "cfg:"):
		b.handleSettingsCallback(ctx, callback)
		return
	case callback.Data == "ui:stats":
		b.handleStatsCallback(ctx, callback)
		return
	}

	a, ok := parseActionData(callback.Data)
	if !ok {
		b.logger.WithField("data", callback.Data).Warn(i18n.T("log.telegram_callback_stale"))
		b.answerCallback(ctx, callback.ID, i18n.T("callback.stale"))
		return
	}
	fields := logrus.Fields{
		"action": a.Name,
		"userID": a.UserID,
		"admin":  callerID,
	}
	if a.Minutes > 0 {
		fields["minutes"] = a.Minutes
	}

	adminName := callback.From.FirstName
	if callback.From.LastName != "" {
		adminName += " " + callback.From.LastName
	}
	if callback.From.Username != "" {
		adminName = "@" + callback.From.Username
	}

	if b.onAction != nil {
		if err := b.onAction(ctx, a); err != nil {
			if errors.Is(err, ErrStaleAction) {
				b.logger.WithError(err).WithFields(fields).Warn(i18n.T("log.telegram_action_stale"))
				b.answerCallback(ctx, callback.ID, i18n.T("callback.stale"))
				return
			}
			b.logger.WithError(err).WithFields(fields).Error(i18n.T("log.telegram_action_failed"))
			b.answerCallback(ctx, callback.ID, i18n.T("callback.error")+": "+err.Error())
			return
		}
	}

	b.logger.WithFields(fields).Info(i18n.T("log.telegram_action_done"))

	chatID, messageID, msg := callbackMessage(callback)
	if err := b.markActionDone(ctx, chatID, messageID, msg, a.Name, a.UserID, adminName); err != nil {
		b.logger.WithError(err).Error(i18n.T("log.telegram_action_mark_failed"))
	}

	b.answerCallback(ctx, callback.ID, i18n.T("callback.done"))
}

func callbackMessage(cb *models.CallbackQuery) (chatID int64, messageID int, msg *models.Message) {
	switch {
	case cb.Message.Message != nil:
		m := cb.Message.Message
		return m.Chat.ID, m.ID, m
	case cb.Message.InaccessibleMessage != nil:
		m := cb.Message.InaccessibleMessage
		return m.Chat.ID, m.MessageID, nil
	}
	return 0, 0, nil
}

func (b *Bot) showScreen(ctx context.Context, chatID int64, messageID int, m richMsg) int {
	if chatID == 0 {
		return 0
	}
	if messageID != 0 {
		err := b.editRich(ctx, chatID, messageID, m)
		if err == nil {
			return messageID
		}
		if !errors.Is(err, tgbot.ErrorBadRequest) {
			b.logger.WithError(err).Error(i18n.T("log.telegram_message_edit_failed"))
			return messageID
		}
		b.logger.WithError(err).Warn(i18n.T("log.telegram_message_new"))
	}
	id, err := b.sendRich(ctx, chatID, b.threadFor(chatID), m)
	if err != nil {
		b.logger.WithError(err).Error(i18n.T("log.telegram_message_send_failed"))
		return 0
	}
	return id
}

func (b *Bot) markActionDone(ctx context.Context, chatID int64, messageID int, msg *models.Message, action string, userID int64, adminName string) error {
	if chatID == 0 {
		return nil
	}
	if msg != nil {
		var err error
		if msg.RichMessage == nil {
			err = b.markTextAlert(ctx, msg, action, userID, adminName)
		} else {
			err = b.markRichAlert(ctx, msg, action, userID, adminName)
		}
		if err == nil || isNotModified(err) {
			return nil
		}
		if !errors.Is(err, tgbot.ErrorBadRequest) {
			return err
		}
		b.logger.WithError(err).Warn(i18n.T("log.telegram_action_reply"))
	}
	_, err := b.sendRich(ctx, chatID, b.threadFor(chatID), actionReport(action, userID, adminName, messageID))
	return err
}

func (b *Bot) markTextAlert(ctx context.Context, msg *models.Message, action string, userID int64, adminName string) error {
	originalText := msg.Text
	if originalText == "" {
		originalText = msg.Caption
	}
	editCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	_, err := b.api.EditMessageText(editCtx, &tgbot.EditMessageTextParams{
		ChatID:             msg.Chat.ID,
		MessageID:          msg.ID,
		Text:               escapeHTML(originalText) + FormatActionResult(action, adminName),
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()},
		ReplyMarkup:        richMsg{Rows: undoRows(action, userID)}.keyboard(),
	})
	return err
}

func (b *Bot) markRichAlert(ctx context.Context, msg *models.Message, action string, userID int64, adminName string) error {
	blocks, dropped := richToInput(msg.RichMessage.Blocks)
	if dropped > 0 {
		b.logger.WithFields(logrus.Fields{
			"messageID": msg.ID,
			"dropped":   dropped,
		}).Warn(i18n.T("log.telegram_alert_content_dropped"))
	}

	now := time.Now()
	blocks = append(blocks, quoteBlock(paragraphBlock(
		boldRich(actionResultText(action)),
		plainRich(" · "+i18n.T("action.admin")+": "+adminName+" · "),
		models.RichText{
			Type: models.RichTextTypeDateTime,
			RichTextDateTime: &models.RichTextDateTime{
				Text:           plainRich(now.Format("15:04")),
				UnixTime:       int(now.Unix()),
				DateTimeFormat: "t",
			},
		},
	)))
	for _, row := range undoRows(action, userID) {
		blocks = append(blocks, buttonsBlock(row))
	}

	editCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	_, err := b.api.EditMessageText(editCtx, &tgbot.EditMessageTextParams{
		ChatID:      msg.Chat.ID,
		MessageID:   msg.ID,
		RichMessage: &models.InputRichMessage{Blocks: blocks, IsRTL: msg.RichMessage.IsRTL},
	})
	return err
}

func actionReport(action string, userID int64, adminName string, replyTo int) richMsg {
	html := "<blockquote><b>" + escapeHTML(actionResultText(action)) + "</b> · " +
		escapeHTML(i18n.T("action.admin")) + ": " + escapeHTML(adminName) + " · " +
		escapeHTML(i18n.T("alert.user")) + ": <code>" + strconv.FormatInt(userID, 10) + "</code> · " +
		tgTime(time.Now(), time.Local, "t") + "</blockquote>"
	return richMsg{HTML: html, Rows: undoRows(action, userID), ReplyTo: replyTo}
}
