package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/remnawave/limiter/internal/api"
)

func TestFormatSharedHWIDAlert(t *testing.T) {
	at := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	out := FormatSharedHWIDAlert(SharedHWIDAlert{
		User:    &api.CachedUser{UserID: 16205, Username: "new<trial>", Squads: []string{"Trial"}},
		Status:  "ACTIVE",
		Created: at.Add(-6 * time.Hour),
		Device:  "iPhone15,2 · iOS 18.1",
		App:     "Happ",
		HWID:    "0123456789abcdef0123456789abcdef",
		Accounts: []SharedAccount{
			{UserID: 16165, Username: "old&paid", Status: "EXPIRED", Squads: []string{"Cluster1"}, Created: time.Date(2025, 12, 1, 9, 0, 0, 0, time.UTC), Seen: time.Date(2026, 11, 19, 17, 5, 0, 0, time.UTC)},
			{UserID: 900, Deleted: true, Seen: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)},
			{UserID: 901, Username: "kept", Status: "WEIRD", DeviceRemoved: true},
		},
		Hidden: 1,
		At:     at,
	}, time.UTC)

	for _, want := range []string{
		"<h3>👥 Один HWID у нескольких аккаунтов</h3>",
		row("👤 Пользователь", "<code>new&lt;trial&gt;</code>"),
		row("👥 Сквад", "Trial"),
		row("📋 Статус", "активна"),
		row("📱 Устройство", "iPhone15,2 · iOS 18.1 · Happ"),
		row("🔑 HWID", "<code>0123456789abcdef01234567…</code>"),
		row("🔁 Других аккаунтов", "<mark>3</mark>"),
		"<summary>🔁 Аккаунты с этим HWID (3)</summary>",
		"<td><code>old&amp;paid</code></td><td>истекла · Cluster1</td><td>01.12.25 09:00</td><td>19.11.26 17:05</td>",
		"<td><code>900</code></td><td>🗑 удалён из панели</td><td>—</td><td>01.10.26 08:00</td>",
		"<td><code>kept</code></td><td>WEIRD · устройство удалено</td>",
		"<blockquote>Аккаунты с тем же Telegram ID не показываются",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в алерте нет %q:\n%s", want, out)
		}
	}
	assertBalancedHTML(t, out)
}

func TestFormatSharedHWIDAlert_Minimal(t *testing.T) {
	out := FormatSharedHWIDAlert(SharedHWIDAlert{
		User:     &api.CachedUser{UserID: 1, Username: "u"},
		HWID:     "short-hwid",
		Accounts: []SharedAccount{{UserID: 2}},
		At:       time.Now(),
	}, time.UTC)
	for _, unwanted := range []string{"📋", "🆕", "📱", "<blockquote>"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("без данных строка %q не нужна:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "<code>short-hwid</code>") || !strings.Contains(out, "<td><code>2</code></td><td>—</td>") {
		t.Errorf("короткий HWID показывается целиком, а пустое состояние прочерком:\n%s", out)
	}
	assertBalancedHTML(t, out)
}

func TestFormatSharedHWIDAlert_ManyAccounts(t *testing.T) {
	accounts := make([]SharedAccount, maxSharedAccounts+3)
	for i := range accounts {
		accounts[i] = SharedAccount{UserID: int64(i + 1)}
	}
	out := FormatSharedHWIDAlert(SharedHWIDAlert{User: &api.CachedUser{UserID: 99, Username: "u"}, HWID: "h", Accounts: accounts, At: time.Now()}, time.UTC)
	if strings.Count(out, "<tr><td><code>") != maxSharedAccounts || !strings.Contains(out, "… и ещё 3") {
		t.Errorf("в таблице не больше %d аккаунтов и строка об остальных:\n%s", maxSharedAccounts, out)
	}
}

func TestKeptRows_TrafficRestoreSurvivesOtherActions(t *testing.T) {
	data := actionData(ActionTrafficRestore, 7)
	rich := &models.Message{RichMessage: &models.RichMessage{Blocks: []models.RichBlock{
		{Type: models.RichBlockTypeButtons, RichBlockButtons: &models.RichBlockButtons{Buttons: []models.RichMessageButton{{CallbackData: data}}}},
	}}}
	plain := &models.Message{ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{CallbackData: data}}}}}
	for _, msg := range []*models.Message{rich, plain} {
		if !messageHasButton(msg, data) {
			t.Errorf("кнопка возврата трафика должна находиться в сообщении: %+v", msg)
		}
	}
	if messageHasButton(&models.Message{}, data) || messageHasButton(nil, data) {
		t.Error("в сообщении без кнопок её нет")
	}

	rows := keptRows("disable", 7, true)
	if len(rows) != 2 || rows[0][0].Data != data || rows[1][0].Data != actionData("enable", 7) {
		t.Errorf("после отключения остаются «Вернуть трафик» и «Включить»: %+v", rows)
	}
	if rows := keptRows(ActionTrafficRestore, 7, true); len(rows) != 0 {
		t.Errorf("после возврата трафика кнопка пропадает: %+v", rows)
	}
	if rows := keptRows("ignore", 7, false); len(rows) != 0 {
		t.Errorf("без кнопки в алерте ничего не добавляется: %+v", rows)
	}
}

func TestFormatSharedHWIDAlert_TrafficRow(t *testing.T) {
	base := SharedHWIDAlert{User: &api.CachedUser{UserID: 2, Username: "u"}, HWID: "h", At: time.Now()}
	cases := []struct {
		cut  TrafficCut
		want string
	}{
		{TrafficCut{State: TrafficLimited, Limit: 100 << 20, Previous: 0, Used: 300 << 20}, "ограничен до 100 МБ (было без лимита, израсходовано 300 МБ)"},
		{TrafficCut{State: TrafficAlreadyLower, Previous: 50 << 20}, "лимит уже 50 МБ"},
		{TrafficCut{State: TrafficCutFailed}, "не удалось ограничить"},
	}
	for _, tc := range cases {
		a := base
		a.Traffic = tc.cut
		if text := FormatSharedHWIDAlert(a, time.UTC); !strings.Contains(text, tc.want) {
			t.Errorf("нет %q:\n%s", tc.want, text)
		}
	}
	if text := FormatSharedHWIDAlert(base, time.UTC); strings.Contains(text, "Трафик") {
		t.Errorf("без урезания строки о трафике нет:\n%s", text)
	}
}
