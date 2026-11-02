package telegram

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
)

const (
	styleNone    = ""
	stylePrimary = "primary"
	styleSuccess = "success"
	styleDanger  = "danger"
	styleLink    = "link"
)

type richButton struct {
	Text     string
	Data     string
	URL      string
	Style    string
	Disabled bool
}

func cbButton(text, data, style string) richButton {
	return richButton{Text: text, Data: data, Style: style}
}

func urlButton(text, url string) richButton {
	return richButton{Text: text, URL: url}
}

func disabledButton(text string) richButton {
	return richButton{Text: text, Disabled: true}
}

type richMsg struct {
	HTML    string
	Rows    [][]richButton
	ReplyTo int
}

func (m richMsg) render() string {
	if len(m.Rows) == 0 {
		return m.HTML
	}
	var b strings.Builder
	b.WriteString(m.HTML)
	for _, row := range m.Rows {
		if len(row) == 0 {
			continue
		}
		b.WriteString("<tg-button-row>")
		for _, btn := range row {
			b.WriteString(btn.html())
		}
		b.WriteString("</tg-button-row>")
	}
	return b.String()
}

type buttonKind int

const (
	kindCallback buttonKind = iota
	kindURL
	kindDisabled
)

func (btn richButton) kind() buttonKind {
	switch {
	case btn.Disabled:
		return kindDisabled
	case btn.URL != "":
		return kindURL
	}
	return kindCallback
}

func (btn richButton) richStyle() string {
	if btn.Style == styleLink && btn.kind() != kindCallback {
		return styleNone
	}
	return btn.Style
}

func (btn richButton) inlineStyle() string {
	if btn.Style == styleLink {
		return styleNone
	}
	return btn.Style
}

func (btn richButton) html() string {
	style := ""
	if s := btn.richStyle(); s != styleNone {
		style = ` style="` + s + `"`
	}
	text := escapeHTML(btn.Text)
	switch btn.kind() {
	case kindDisabled:
		return `<tg-button type="disabled"` + style + `>` + text + `</tg-button>`
	case kindURL:
		return `<tg-button type="url"` + style + ` url="` + escapeHTML(btn.URL) + `">` + text + `</tg-button>`
	default:
		return `<tg-button type="callback_data"` + style + ` data="` + escapeHTML(btn.Data) + `">` + text + `</tg-button>`
	}
}

func (m richMsg) keyboard() *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(m.Rows))
	for _, row := range m.Rows {
		out := make([]models.InlineKeyboardButton, 0, len(row))
		for _, btn := range row {
			kb := models.InlineKeyboardButton{Text: btn.Text, Style: btn.inlineStyle()}
			switch btn.kind() {
			case kindDisabled:
				kb.Disabled = &models.DisabledButton{}
			case kindURL:
				kb.URL = btn.URL
			default:
				kb.CallbackData = btn.Data
			}
			out = append(out, kb)
		}
		if len(out) > 0 {
			rows = append(rows, out)
		}
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

var (
	reBlockBreak = regexp.MustCompile(`(?i)<(br\s*/?|/p|/h[1-6]|/tr|/li|/blockquote|/footer|/summary|/caption|hr\s*/?)>`)
	reCellBreak  = regexp.MustCompile(`(?i)</t[hd]>`)
	reButtonRow  = regexp.MustCompile(`(?is)<tg-button-row[^>]*>.*?</tg-button-row>`)
	reAnyTag     = regexp.MustCompile(`<[^>]+>`)
	reManyBlank  = regexp.MustCompile(`\n{3,}`)
	reLineSpaces = regexp.MustCompile(`[ \t]+\n`)
)

func plainText(richHTML string) string {
	s := reButtonRow.ReplaceAllString(richHTML, "")
	s = reCellBreak.ReplaceAllString(s, "  ")
	s = reBlockBreak.ReplaceAllString(s, "\n")
	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reLineSpaces.ReplaceAllString(s, "\n")
	s = reManyBlank.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 4000 {
		s = string(r[:3999]) + "…"
	}
	return s
}

func tgTime(t time.Time, loc *time.Location, format string) string {
	return fmt.Sprintf(`<tg-time unix="%d" format="%s">%s</tg-time>`,
		t.Unix(), format, t.In(loc).Format("02.01.2006 15:04:05"))
}

func tableRow(header, value string) string {
	return "<tr><th align=\"left\">" + escapeHTML(header) + "</th><td>" + value + "</td></tr>"
}

func richToInput(blocks []models.RichBlock) (out []models.InputRichBlock, dropped int) {
	out = make([]models.InputRichBlock, 0, len(blocks))
	for _, blk := range blocks {
		if blk.Type == models.RichBlockTypeButtons {
			continue
		}
		raw, err := json.Marshal(blk)
		if err != nil {
			dropped++
			continue
		}
		var in models.InputRichBlock
		if err := json.Unmarshal(raw, &in); err != nil {
			dropped++
			continue
		}
		out = append(out, in)
	}
	return out, dropped
}

func plainRich(s string) models.RichText {
	return models.RichText{PlainText: s}
}

func boldRich(s string) models.RichText {
	return models.RichText{
		Type:         models.RichTextTypeBold,
		RichTextBold: &models.RichTextBold{Text: plainRich(s)},
	}
}

func paragraphBlock(parts ...models.RichText) models.InputRichBlock {
	return models.InputRichBlock{
		Type:                    models.RichBlockTypeParagraph,
		InputRichBlockParagraph: &models.InputRichBlockParagraph{Text: models.RichText{Array: parts}},
	}
}

func quoteBlock(inner ...models.InputRichBlock) models.InputRichBlock {
	return models.InputRichBlock{
		Type:                         models.RichBlockTypeBlockQuotation,
		InputRichBlockBlockQuotation: &models.InputRichBlockBlockQuotation{Blocks: inner},
	}
}

func buttonsBlock(row []richButton) models.InputRichBlock {
	buttons := make([]models.RichMessageButton, 0, len(row))
	for _, btn := range row {
		rb := models.RichMessageButton{Text: plainRich(btn.Text), Style: btn.richStyle()}
		switch btn.kind() {
		case kindDisabled:
			rb.Disabled = &models.DisabledButton{}
		case kindURL:
			rb.URL = btn.URL
		default:
			rb.CallbackData = btn.Data
		}
		buttons = append(buttons, rb)
	}
	return models.InputRichBlock{
		Type:                  models.RichBlockTypeButtons,
		InputRichBlockButtons: &models.InputRichBlockButtons{Buttons: buttons},
	}
}
