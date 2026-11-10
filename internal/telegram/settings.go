package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

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
	key       string
	chatID    int64
	messageID int
	expires   time.Time
}

func (b *Bot) setPending(adminID int64, key string, chatID int64, messageID int) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	b.pending[adminID] = pendingInput{key: key, chatID: chatID, messageID: messageID, expires: time.Now().Add(pendingTTL)}
}

func (b *Bot) pendingFor(adminID int64) (pendingInput, bool) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	p, ok := b.pending[adminID]
	if !ok {
		return pendingInput{}, false
	}
	if time.Now().After(p.expires) {
		delete(b.pending, adminID)
		return pendingInput{}, false
	}
	return p, true
}

func (b *Bot) keepPending(adminID int64, p pendingInput) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	b.pending[adminID] = p
}

func (b *Bot) clearPending(adminID int64) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	delete(b.pending, adminID)
}

type settingsGroup struct {
	id   string
	keys []string
}

const groupOther = "other"

var settingsGroups = []settingsGroup{
	{id: "detect", keys: []string{"CHECK_INTERVAL", "ACTIVE_IP_WINDOW", "DEFAULT_DEVICE_LIMIT", "TOLERANCE", "TOLERANCE_MULTIPLIER"}},
	{id: "actions", keys: []string{"ACTION_MODE", "AUTO_DISABLE_DURATION", "IGNORE_DURATION", "AUTO_NOTIFY_SOFT"}},
	{id: "thresholds", keys: []string{"COOLDOWN", "VIOLATION_THRESHOLD", "VIOLATION_THRESHOLD_WINDOW"}},
	{id: "grouping", keys: []string{"ASN_GROUPING", "SUBNET_GROUPING", "SUBNET_PREFIX_V4"}},
	{id: "service", keys: []string{"USER_CACHE_TTL", "DAILY_REPORT", "LOG_LEVEL"}},
}

var settingPresets = map[string][]string{
	"CHECK_INTERVAL":             {"30", "60", "120", "300"},
	"ACTIVE_IP_WINDOW":           {"120", "300", "600", "900"},
	"DEFAULT_DEVICE_LIMIT":       {"0", "1", "2", "3", "5"},
	"TOLERANCE":                  {"0", "1", "2", "3"},
	"TOLERANCE_MULTIPLIER":       {"0", "0.25", "0.5", "1"},
	"AUTO_DISABLE_DURATION":      {"0", "10", "30", "60", "1440"},
	"IGNORE_DURATION":            {"0", "60", "360", "1440"},
	"COOLDOWN":                   {"300", "900", "1800", "3600"},
	"VIOLATION_THRESHOLD":        {"1", "2", "3", "5"},
	"VIOLATION_THRESHOLD_WINDOW": {"600", "1800", "3600", "7200"},
	"SUBNET_PREFIX_V4":           {"16", "20", "22", "24"},
	"USER_CACHE_TTL":             {"60", "300", "600", "1800"},
}

var minuteKeys = map[string]bool{
	"AUTO_DISABLE_DURATION": true,
	"IGNORE_DURATION":       true,
}

func groupOf(key string) string {
	for _, g := range settingsGroups {
		for _, k := range g.keys {
			if k == key {
				return g.id
			}
		}
	}
	return groupOther
}

type itemGroup struct {
	id    string
	items []SettingItem
}

func groupedItems(items []SettingItem) []itemGroup {
	byKey := make(map[string]SettingItem, len(items))
	for _, it := range items {
		byKey[it.Key] = it
	}
	var out []itemGroup
	used := make(map[string]bool, len(items))
	for _, g := range settingsGroups {
		var list []SettingItem
		for _, k := range g.keys {
			if it, ok := byKey[k]; ok {
				list = append(list, it)
				used[k] = true
			}
		}
		if len(list) > 0 {
			out = append(out, itemGroup{id: g.id, items: list})
		}
	}
	var rest []SettingItem
	for _, it := range items {
		if !used[it.Key] {
			rest = append(rest, it)
		}
	}
	if len(rest) > 0 {
		out = append(out, itemGroup{id: groupOther, items: rest})
	}
	return out
}

func groupTitle(id string) string {
	return i18n.T("settings.group." + id)
}

func optionalT(key string) string {
	if v := i18n.T(key); v != key {
		return v
	}
	return ""
}

func displayValue(it SettingItem) string {
	if it.Kind == SettingBool {
		if it.Display == "true" {
			return "✅ " + escapeHTML(i18n.T("settings.on"))
		}
		return "⬜ " + escapeHTML(i18n.T("settings.off"))
	}
	v := "<code>" + escapeHTML(it.Display) + "</code>"
	if minuteKeys[it.Key] {
		if n, err := strconv.Atoi(it.Display); err == nil {
			v += " · " + escapeHTML(FormatDuration(n))
		}
	}
	return v
}

func overriddenMark(it SettingItem) string {
	if it.Overridden {
		return " ♻️"
	}
	return ""
}

type notice struct {
	text  string
	isErr bool
}

func (n notice) html() string {
	if n.text == "" {
		return ""
	}
	mark := "✅ "
	if n.isErr {
		mark = "❌ "
	}
	return "<blockquote>" + mark + escapeHTML(n.text) + "</blockquote>"
}

func (b *Bot) menuScreen(n notice) richMsg {
	var h strings.Builder
	items := b.settings.Items()

	h.WriteString("<h3>" + escapeHTML(i18n.T("settings.title")) + "</h3>")
	h.WriteString(n.html())
	h.WriteString("<p>" + escapeHTML(i18n.T("settings.intro")) + "</p>")

	overridden := 0
	h.WriteString("<table bordered striped compact>")
	for _, g := range groupedItems(items) {
		h.WriteString("<tr><th colspan=\"2\" align=\"left\">" + escapeHTML(groupTitle(g.id)) + "</th></tr>")
		for _, it := range g.items {
			if it.Overridden {
				overridden++
			}
			h.WriteString("<tr><td>" + escapeHTML(it.Title) + "</td><td>" + displayValue(it) + overriddenMark(it) + "</td></tr>")
		}
	}
	h.WriteString("</table>")

	if overridden > 0 {
		h.WriteString("<footer>" + escapeHTML(fmt.Sprintf(i18n.T("settings.overridden_count"), overridden)) + "</footer>")
	} else {
		h.WriteString("<footer>" + escapeHTML(i18n.T("settings.all_from_env")) + "</footer>")
	}

	var rows [][]richButton
	var row []richButton
	for _, g := range groupedItems(items) {
		row = append(row, cbButton(groupTitle(g.id), "cfg:sec:"+g.id, styleNone))
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	resetBtn := cbButton(i18n.T("settings.reset_all"), "cfg:resetall", styleDanger)
	if overridden == 0 {
		resetBtn = disabledButton(i18n.T("settings.reset_all"))
	}
	rows = append(rows, []richButton{
		cbButton(i18n.T("button.stats"), "ui:stats", styleNone),
		resetBtn,
	})

	return richMsg{HTML: h.String(), Rows: rows}
}

func (b *Bot) sectionScreen(groupID string, n notice) richMsg {
	var section []SettingItem
	for _, g := range groupedItems(b.settings.Items()) {
		if g.id == groupID {
			section = g.items
			break
		}
	}
	if len(section) == 0 {
		return b.menuScreen(n)
	}

	var h strings.Builder
	h.WriteString("<h3>" + escapeHTML(groupTitle(groupID)) + "</h3>")
	h.WriteString(n.html())
	if desc := optionalT("settings.group." + groupID + ".desc"); desc != "" {
		h.WriteString("<p>" + escapeHTML(desc) + "</p>")
	}

	h.WriteString("<table bordered striped compact><tr>")
	h.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("settings.col_param")) + "</th>")
	h.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("settings.col_value")) + "</th>")
	h.WriteString("</tr>")
	for _, it := range section {
		h.WriteString("<tr><td>" + escapeHTML(it.Title) + "</td><td>" + displayValue(it) + overriddenMark(it) + "</td></tr>")
	}
	h.WriteString("</table>")

	var hints strings.Builder
	for _, it := range section {
		if hint := optionalT("setting_hint." + it.Key); hint != "" {
			hints.WriteString("<li><b>" + escapeHTML(it.Title) + "</b> — " + escapeHTML(hint) + "</li>")
		}
	}
	if hints.Len() > 0 {
		h.WriteString("<details><summary>" + escapeHTML(i18n.T("settings.hints")) + "</summary><ul>" + hints.String() + "</ul></details>")
	}

	rows := make([][]richButton, 0, len(section)+1)
	for _, it := range section {
		if it.Kind == SettingBool {
			if it.Display == "true" {
				rows = append(rows, []richButton{cbButton("✅ "+it.Title, "cfg:toggle:"+it.Key, styleSuccess)})
			} else {
				rows = append(rows, []richButton{cbButton("⬜ "+it.Title, "cfg:toggle:"+it.Key, styleNone)})
			}
			continue
		}
		rows = append(rows, []richButton{cbButton("✏️ "+it.Title, "cfg:edit:"+it.Key, styleNone)})
	}
	rows = append(rows, []richButton{cbButton(i18n.T("settings.back"), "cfg:menu", styleLink)})

	return richMsg{HTML: h.String(), Rows: rows}
}

func (b *Bot) editScreen(it SettingItem, n notice) richMsg {
	var h strings.Builder
	h.WriteString("<h3>✏️ " + escapeHTML(it.Title) + "</h3>")
	h.WriteString(n.html())

	source := i18n.T("settings.source_env")
	if it.Overridden {
		source = i18n.T("settings.source_bot")
	}
	h.WriteString("<table bordered compact>")
	h.WriteString(tableRow(i18n.T("settings.current"), displayValue(it)))
	h.WriteString(tableRow(i18n.T("settings.source"), escapeHTML(source)))
	h.WriteString(tableRow(i18n.T("settings.env_key"), "<code>"+escapeHTML(it.Key)+"</code>"))
	h.WriteString("</table>")

	if hint := optionalT("setting_hint." + it.Key); hint != "" {
		h.WriteString("<blockquote>💡 " + escapeHTML(hint) + "</blockquote>")
	}

	var options []string
	switch it.Kind {
	case SettingInt, SettingFloat:
		h.WriteString("<p>" + escapeHTML(i18n.T("settings.prompt_input")) + "</p>")
		options = settingPresets[it.Key]
	default:
		h.WriteString("<p>" + escapeHTML(i18n.T("settings.prompt_choose")) + "</p>")
		options = it.Allowed
	}

	var rows [][]richButton
	var row []richButton
	for _, v := range options {
		label := v
		if minuteKeys[it.Key] {
			if n, err := strconv.Atoi(v); err == nil {
				label = FormatDuration(n)
			}
		}
		btn := cbButton(label, "cfg:set:"+it.Key+":"+v, styleNone)
		if v == it.Display {
			btn = cbButton("✅ "+label, "cfg:set:"+it.Key+":"+v, styleSuccess)
		}
		row = append(row, btn)
		if len(row) == 3 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	if it.Overridden {
		rows = append(rows, []richButton{cbButton(i18n.T("settings.reset_one"), "cfg:reset:"+it.Key, styleDanger)})
	}
	rows = append(rows, []richButton{
		cbButton(i18n.T("settings.back"), "cfg:sec:"+groupOf(it.Key), styleLink),
		cbButton(i18n.T("settings.to_menu"), "cfg:menu", styleLink),
	})

	return richMsg{HTML: h.String(), Rows: rows}
}

func (b *Bot) resetAllScreen() richMsg {
	var h strings.Builder
	h.WriteString("<h3>" + escapeHTML(i18n.T("settings.reset_all_title")) + "</h3>")
	h.WriteString("<p>" + escapeHTML(i18n.T("settings.reset_all_confirm")) + "</p>")

	var list strings.Builder
	for _, it := range b.settings.Items() {
		if it.Overridden {
			list.WriteString("<li><b>" + escapeHTML(it.Title) + "</b>: " + displayValue(it) + "</li>")
		}
	}
	if list.Len() > 0 {
		h.WriteString("<ul>" + list.String() + "</ul>")
	}

	rows := [][]richButton{{
		cbButton(i18n.T("settings.reset_all_yes"), "cfg:resetall:yes", styleDanger),
		cbButton(i18n.T("settings.cancel"), "cfg:menu", styleNone),
	}}
	return richMsg{HTML: h.String(), Rows: rows}
}

func (b *Bot) handleSettingsCommand(ctx context.Context, msg *models.Message) {
	if b.settings == nil {
		return
	}
	if !b.adminIDs[msg.From.ID] {
		return
	}
	b.clearPending(msg.From.ID)
	if _, err := b.sendRich(ctx, msg.Chat.ID, b.threadFor(msg.Chat.ID), b.menuScreen(notice{})); err != nil {
		b.logger.WithError(err).Error(i18n.T("log.telegram_settings_failed"))
	}
}

func (b *Bot) handlePendingInput(ctx context.Context, msg *models.Message) {
	if b.settings == nil {
		return
	}
	pend, ok := b.pendingFor(msg.From.ID)
	if !ok || msg.Chat.ID != pend.chatID {
		return
	}
	raw := strings.TrimSpace(msg.Text)
	if msg.Chat.Type != models.ChatTypePrivate && !looksNumeric(raw) {
		return
	}

	b.clearPending(msg.From.ID)
	b.deleteMessage(ctx, msg.Chat.ID, msg.ID)

	it, ok := b.settings.Item(pend.key)
	if !ok {
		return
	}

	var screen richMsg
	display, err := b.settings.Apply(ctx, pend.key, raw)
	if err != nil {
		screen = b.editScreen(it, notice{text: err.Error(), isErr: true})
	} else {
		screen = b.sectionScreen(groupOf(pend.key), notice{text: fmt.Sprintf(i18n.T("settings.applied"), it.Title, display)})
	}

	id := b.showScreen(ctx, pend.chatID, pend.messageID, screen)
	if err != nil && id != 0 {
		pend.messageID = id
		b.keepPending(msg.From.ID, pend)
	}
}

func looksNumeric(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func (b *Bot) handleSettingsCallback(ctx context.Context, callback *models.CallbackQuery) {
	answer := func(text string) {
		b.answerCallback(ctx, callback.ID, text)
	}

	if b.settings == nil {
		answer("")
		return
	}

	b.clearPending(callback.From.ID)

	chatID, messageID, _ := callbackMessage(callback)
	show := func(m richMsg) int {
		return b.showScreen(ctx, chatID, messageID, m)
	}
	applyErr := func(err error) string {
		return i18n.T("settings.apply_error") + ": " + err.Error()
	}

	parts := strings.Split(callback.Data, ":")
	verb := ""
	if len(parts) > 1 {
		verb = parts[1]
	}
	arg := ""
	if len(parts) > 2 {
		arg = parts[2]
	}

	switch verb {
	case "menu":
		show(b.menuScreen(notice{}))
		answer("")

	case "sec":
		show(b.sectionScreen(arg, notice{}))
		answer("")

	case "toggle":
		it, ok := b.settings.Item(arg)
		if !ok {
			answer(i18n.T("callback.stale"))
			return
		}
		newVal := "true"
		if it.Display == "true" {
			newVal = "false"
		}
		display, err := b.settings.Apply(ctx, arg, newVal)
		if err != nil {
			show(b.sectionScreen(groupOf(arg), notice{text: err.Error(), isErr: true}))
			answer(truncateAnswer(applyErr(err)))
			return
		}
		show(b.sectionScreen(groupOf(arg), notice{text: fmt.Sprintf(i18n.T("settings.applied"), it.Title, display)}))
		answer(fmt.Sprintf(i18n.T("settings.applied_toast"), it.Title+": "+display))

	case "edit":
		it, ok := b.settings.Item(arg)
		if !ok {
			answer(i18n.T("callback.stale"))
			return
		}
		id := show(b.editScreen(it, notice{}))
		if it.Kind == SettingInt || it.Kind == SettingFloat {
			if id != 0 {
				b.setPending(callback.From.ID, arg, chatID, id)
			}
			answer(i18n.T("settings.prompt_input_toast"))
			return
		}
		answer("")

	case "set":
		if len(parts) < 4 {
			answer(i18n.T("callback.stale"))
			return
		}
		value := strings.Join(parts[3:], ":")
		it, ok := b.settings.Item(arg)
		if !ok {
			answer(i18n.T("callback.stale"))
			return
		}
		display, err := b.settings.Apply(ctx, arg, value)
		if err != nil {
			show(b.editScreen(it, notice{text: err.Error(), isErr: true}))
			answer(truncateAnswer(applyErr(err)))
			return
		}
		show(b.sectionScreen(groupOf(arg), notice{text: fmt.Sprintf(i18n.T("settings.applied"), it.Title, display)}))
		answer(fmt.Sprintf(i18n.T("settings.applied_toast"), display))

	case "reset":
		it, ok := b.settings.Item(arg)
		if !ok {
			answer(i18n.T("callback.stale"))
			return
		}
		display, err := b.settings.Reset(ctx, arg)
		if err != nil {
			show(b.editScreen(it, notice{text: err.Error(), isErr: true}))
			answer(truncateAnswer(applyErr(err)))
			return
		}
		show(b.sectionScreen(groupOf(arg), notice{text: fmt.Sprintf(i18n.T("settings.reset_done"), it.Title, display)}))
		answer(fmt.Sprintf(i18n.T("settings.reset_toast"), display))

	case "resetall":
		if arg != "yes" {
			show(b.resetAllScreen())
			answer("")
			return
		}
		if err := b.settings.ResetAll(ctx); err != nil {
			show(b.menuScreen(notice{text: err.Error(), isErr: true}))
			answer(truncateAnswer(applyErr(err)))
			return
		}
		show(b.menuScreen(notice{text: i18n.T("settings.reset_all_toast")}))
		answer(i18n.T("settings.reset_all_toast"))

	default:
		answer(i18n.T("callback.stale"))
	}
}

func (b *Bot) deleteMessage(ctx context.Context, chatID int64, messageID int) {
	if chatID == 0 || messageID == 0 {
		return
	}
	if _, err := b.api.DeleteMessage(ctx, &tgbot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: messageID,
	}); err != nil {
		b.logger.WithError(err).Debug(i18n.T("log.telegram_input_delete_failed"))
	}
}

func (b *Bot) threadFor(chatID int64) int {
	if chatID == b.chatID {
		return int(b.threadID)
	}
	return 0
}
