package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sirupsen/logrus"
)

const testToken = "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"

type apiCall struct {
	method string
	fields map[string]string
}

type fakeAPI struct {
	mu         sync.Mutex
	calls      []apiCall
	rejectRich bool
	rejectEdit bool
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	fields := map[string]string{}
	if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil {
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			v, _ := io.ReadAll(p)
			fields[p.FormName()] = string(v)
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, apiCall{method: method, fields: fields})
	f.mu.Unlock()

	if f.rejectEdit && method == "editMessageText" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`))
		return
	}
	if f.rejectRich && (method == "sendRichMessage" || (method == "editMessageText" && fields["rich_message"] != "")) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: can't parse rich message"}`))
		return
	}

	switch method {
	case "sendMessage", "sendRichMessage":
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":900,"date":1,"chat":{"id":1,"type":"private"}}}`))
	case "editMessageText":
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":1,"type":"private"}}}`))
	default:
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}
}

func (f *fakeAPI) find(method string) []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []apiCall
	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func newTestBot(t *testing.T, chatID, threadID int64) (*Bot, *fakeAPI) {
	t.Helper()
	fake := &fakeAPI{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	b, err := newBot(testToken, chatID, threadID, []int64{42}, "", logger, tgbot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("newBot: %v", err)
	}
	return b, fake
}

func TestNewBot_RejectsMalformedToken(t *testing.T) {
	if _, err := NewBot("not-a-token", 1, 0, nil, "", logrus.New()); err == nil {
		t.Fatal("ожидалась ошибка для токена неверного формата")
	}
}

func richHTML(t *testing.T, fields map[string]string) string {
	t.Helper()
	var rm models.InputRichMessage
	if err := json.Unmarshal([]byte(fields["rich_message"]), &rm); err != nil {
		t.Fatalf("rich_message: %v (%q)", err, fields["rich_message"])
	}
	return rm.HTML
}

var reCallbackData = regexp.MustCompile(`data="([^"]+)"`)

func buttonData(html string) string {
	var out []string
	for _, m := range reCallbackData.FindAllStringSubmatch(html, -1) {
		out = append(out, m[1])
	}
	return strings.Join(out, ",")
}

func TestSendManualAlert_SendsRichMessageWithButtonsAndThread(t *testing.T) {
	b, fake := newTestBot(t, -100500, 7)

	if err := b.SendManualAlert(context.Background(), "<h3>alert</h3>", 99, 30, 0); err != nil {
		t.Fatalf("SendManualAlert: %v", err)
	}

	if n := len(fake.find("sendMessage")); n != 0 {
		t.Fatalf("алерт должен уходить через sendRichMessage, sendMessage вызван %d раз", n)
	}
	calls := fake.find("sendRichMessage")
	if len(calls) != 1 {
		t.Fatalf("sendRichMessage вызван %d раз", len(calls))
	}
	f := calls[0].fields
	if f["chat_id"] != "-100500" || f["message_thread_id"] != "7" {
		t.Errorf("неверные поля сообщения: %v", f)
	}
	html := richHTML(t, f)
	if !strings.HasPrefix(html, "<h3>alert</h3><tg-button-row>") {
		t.Errorf("кнопки должны идти после тела алерта: %s", html)
	}
	want := "drop:99,disable:99,disable_temp:99:30,ignore:99"
	if got := buttonData(html); got != want {
		t.Errorf("callback data %q, ожидалось %q", got, want)
	}
	if !strings.Contains(html, `style="danger" data="disable:99"`) {
		t.Errorf("кнопка отключения должна быть красной: %s", html)
	}
	assertBalancedHTML(t, html)
}

func TestSendRich_FallsBackToPlainText(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	fake.rejectRich = true

	if err := b.SendAutoAlert(context.Background(), "<h3>Отключено</h3><p>a &amp; b</p>", 5); err != nil {
		t.Fatalf("SendAutoAlert: %v", err)
	}

	calls := fake.find("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("ожидался запасной sendMessage, вызван %d раз", len(calls))
	}
	f := calls[0].fields
	if f["text"] != "Отключено\na & b" {
		t.Errorf("текст запасного сообщения: %q", f["text"])
	}
	if f["parse_mode"] != "" {
		t.Errorf("запасное сообщение отправляется без разметки, parse_mode=%q", f["parse_mode"])
	}
	if !strings.Contains(f["reply_markup"], `"callback_data":"enable:5"`) {
		t.Errorf("кнопки должны перейти в inline-клавиатуру: %s", f["reply_markup"])
	}
}

func TestSendMessage_NoThreadOmitsField(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)

	if err := b.SendMessage(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	calls := fake.find("sendRichMessage")
	if len(calls) != 1 {
		t.Fatalf("sendRichMessage вызван %d раз", len(calls))
	}
	if _, ok := calls[0].fields["message_thread_id"]; ok {
		t.Error("message_thread_id не должен отправляться без темы")
	}
	if got := richHTML(t, calls[0].fields); got != "<p>hi</p>" {
		t.Errorf("html = %q", got)
	}
}

func callbackUpdate(fromID int64, data string) *models.Update {
	return &models.Update{
		ID: 1,
		CallbackQuery: &models.CallbackQuery{
			ID:   "cb1",
			From: models.User{ID: fromID, Username: "admin"},
			Data: data,
			Message: models.MaybeInaccessibleMessage{
				Type: models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{
					ID:   5,
					Chat: models.Chat{ID: 100},
					Text: "Нарушение <user>",
				},
			},
		},
	}
}

func TestHandleCallback_LegacyTextAlertEditsMessage(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)

	var got Action
	b.SetActionHandler(func(_ context.Context, a Action) error {
		got = a
		return nil
	})

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "disable:77"))

	if got != (Action{Name: "disable", UserID: 77}) {
		t.Fatalf("обработчик получил %+v", got)
	}

	edits := fake.find("editMessageText")
	if len(edits) != 1 {
		t.Fatalf("editMessageText вызван %d раз", len(edits))
	}
	f := edits[0].fields
	if f["message_id"] != "5" || f["chat_id"] != "100" {
		t.Errorf("редактируется не то сообщение: %v", f)
	}
	if !strings.HasPrefix(f["text"], "Нарушение &lt;user&gt;") {
		t.Errorf("исходный текст не экранирован: %q", f["text"])
	}
	if f["reply_markup"] != `{"inline_keyboard":[[{"text":"🔓 Включить подписку","style":"success","callback_data":"enable:77"}]]}` {
		t.Errorf("должна остаться только кнопка включения, reply_markup = %s", f["reply_markup"])
	}
	if len(fake.find("answerCallbackQuery")) != 1 {
		t.Error("callback должен получить ответ")
	}
}

func TestHandleCallback_LegacyTextAlertDropRemovesButtons(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	b.SetActionHandler(func(context.Context, Action) error { return nil })

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "drop:77"))

	if f := fake.find("editMessageText")[0].fields; f["reply_markup"] != `{"inline_keyboard":[]}` {
		t.Errorf("после сброса подключений кнопок быть не должно, reply_markup = %s", f["reply_markup"])
	}
}

func richCallbackUpdate(fromID int64, data string) *models.Update {
	var blocks []models.RichBlock
	_ = json.Unmarshal([]byte(`[
		{"type":"heading","text":"Alert","size":3},
		{"type":"paragraph","text":{"type":"code","text":"bob"}},
		{"type":"buttons","buttons":[{"text":"Drop","callback_data":"drop:77"},{"text":"Disable","callback_data":"disable:77"}]}
	]`), &blocks)
	u := callbackUpdate(fromID, data)
	msg := u.CallbackQuery.Message.Message
	msg.Text = ""
	msg.RichMessage = &models.RichMessage{Blocks: blocks}
	return u
}

func TestHandleCallback_RichAlertKeepsContentAndOffersUndo(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	b.SetActionHandler(func(context.Context, Action) error { return nil })

	b.handleUpdate(context.Background(), nil, richCallbackUpdate(42, "disable:77"))

	edits := fake.find("editMessageText")
	if len(edits) != 1 {
		t.Fatalf("editMessageText вызван %d раз", len(edits))
	}
	var rm models.InputRichMessage
	if err := json.Unmarshal([]byte(edits[0].fields["rich_message"]), &rm); err != nil {
		t.Fatalf("rich_message: %v", err)
	}
	if rm.HTML != "" || len(rm.Blocks) != 4 {
		t.Fatalf("ожидались 4 блока (заголовок, абзац, итог, кнопка), получено %d: %s", len(rm.Blocks), edits[0].fields["rich_message"])
	}
	raw := edits[0].fields["rich_message"]
	if strings.Contains(raw, "drop:77") {
		t.Errorf("старые кнопки алерта должны исчезнуть: %s", raw)
	}
	for _, want := range []string{`"type":"heading"`, `"bob"`, "Подписка отключена навсегда", "@admin", `"type":"date_time"`, `"callback_data":"enable:77"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("rich_message не содержит %s: %s", want, raw)
		}
	}
}

func TestHandleCallback_RichAlertDropHasNoUndo(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	b.SetActionHandler(func(context.Context, Action) error { return nil })

	b.handleUpdate(context.Background(), nil, richCallbackUpdate(42, "drop:77"))

	raw := fake.find("editMessageText")[0].fields["rich_message"]
	if strings.Contains(raw, `"type":"buttons"`) {
		t.Errorf("после сброса подключений кнопок быть не должно: %s", raw)
	}
}

func TestHandleCallback_StatsRefresh(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	b.SetStatsHandler(func(context.Context) (string, error) { return "<h3>stats</h3>", nil })

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "ui:stats"))

	edits := fake.find("editMessageText")
	if len(edits) != 1 {
		t.Fatalf("editMessageText вызван %d раз", len(edits))
	}
	html := richHTML(t, edits[0].fields)
	if !strings.HasPrefix(html, "<h3>stats</h3>") || buttonData(html) != "ui:stats,cfg:menu" {
		t.Errorf("неожиданное содержимое статистики: %s", html)
	}
	if len(fake.find("answerCallbackQuery")) != 1 {
		t.Error("callback должен получить ответ")
	}
}

func TestHandleCallback_NonAdminRejected(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)

	called := false
	b.SetActionHandler(func(context.Context, Action) error {
		called = true
		return nil
	})

	b.handleUpdate(context.Background(), nil, callbackUpdate(1, "disable:77"))

	if called {
		t.Error("действие не должно выполняться для не-админа")
	}
	if len(fake.find("editMessageText")) != 0 {
		t.Error("сообщение не должно редактироваться для не-админа")
	}
	if len(fake.find("answerCallbackQuery")) != 1 {
		t.Error("не-админ должен получить ответ об отказе")
	}
}

func TestStartPolling_DeliversUpdatesAndStops(t *testing.T) {
	var mu sync.Mutex
	served := false
	acted := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			mu.Lock()
			first := !served
			served = true
			mu.Unlock()
			if first {
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":10,"callback_query":{"id":"c","from":{"id":42,"is_bot":false,"first_name":"A"},"chat_instance":"i","data":"enable:5","message":{"message_id":3,"date":1,"chat":{"id":100,"type":"private"},"text":"t"}}}]}`))
				return
			}
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer srv.Close()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	b, err := newBot(testToken, 100, 0, []int64{42}, "", logger, tgbot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	b.SetActionHandler(func(_ context.Context, a Action) error {
		acted <- a.Name
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.StartPolling(ctx)
		close(done)
	}()

	select {
	case a := <-acted:
		if a != "enable" {
			t.Errorf("действие %q, ожидалось enable", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("обновление не дошло до обработчика")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("polling не остановился после отмены контекста")
	}
}

func TestParseActionData(t *testing.T) {
	cases := []struct {
		data string
		want Action
		ok   bool
	}{
		{"disable:77", Action{Name: "disable", UserID: 77}, true},
		{"ignore_temp:77:60", Action{Name: "ignore_temp", UserID: 77, Minutes: 60}, true},
		{"disable_temp:9223372036854775807:1440", Action{Name: "disable_temp", UserID: 9223372036854775807, Minutes: 1440}, true},
		{"ignore_temp:77:0", Action{}, false},
		{"ignore_temp:77:-5", Action{}, false},
		{"ignore_temp:77:x", Action{}, false},
		{"disable:a1b2-uuid", Action{}, false},
		{"disable", Action{}, false},
		{":77", Action{}, false},
		{"a:1:2:3", Action{}, false},
	}
	for _, c := range cases {
		got, ok := parseActionData(c.data)
		if ok != c.ok || got != c.want {
			t.Errorf("parseActionData(%q) = %+v, %v; want %+v, %v", c.data, got, ok, c.want, c.ok)
		}
	}
}

func TestAlertButtons_CarryDuration(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)

	if err := b.SendManualAlert(context.Background(), "<p>a</p>", 9223372036854775807, 1440, 60); err != nil {
		t.Fatal(err)
	}
	if err := b.SendSoftAlert(context.Background(), "<p>b</p>", 5, 30); err != nil {
		t.Fatal(err)
	}
	calls := fake.find("sendRichMessage")
	manual := buttonData(richHTML(t, calls[0].fields))
	want := "drop:9223372036854775807,disable:9223372036854775807,disable_temp:9223372036854775807:1440,ignore_temp:9223372036854775807:60"
	if manual != want {
		t.Errorf("кнопки алерта %q, ожидалось %q", manual, want)
	}
	for _, data := range strings.Split(manual, ",") {
		if len(data) > 64 {
			t.Errorf("callback data длиннее 64 байт: %q", data)
		}
	}
	if soft := buttonData(richHTML(t, calls[1].fields)); soft != "drop:5,ignore_temp:5:30" {
		t.Errorf("кнопки мягкого алерта %q", soft)
	}
}

func TestHandleCallback_PassesDurationFromButton(t *testing.T) {
	b, _ := newTestBot(t, 100, 0)
	var got Action
	b.SetActionHandler(func(_ context.Context, a Action) error {
		got = a
		return nil
	})

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "ignore_temp:77:60"))

	if got != (Action{Name: "ignore_temp", UserID: 77, Minutes: 60}) {
		t.Errorf("обработчик получил %+v", got)
	}
}

func TestHandleCallback_StaleActionNotMarkedDone(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	b.SetActionHandler(func(context.Context, Action) error {
		return fmt.Errorf("длительность неизвестна: %w", ErrStaleAction)
	})

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "ignore_temp:77"))

	if n := len(fake.find("editMessageText")); n != 0 {
		t.Errorf("невыполненное действие не должно отмечаться в алерте, editMessageText вызван %d раз", n)
	}
	answers := fake.find("answerCallbackQuery")
	if len(answers) != 1 || answers[0].fields["text"] != "⚠️ Кнопка устарела" {
		t.Errorf("ожидался ответ «кнопка устарела», получено %v", answers)
	}
}

func inaccessibleCallbackUpdate(fromID int64, data string) *models.Update {
	u := callbackUpdate(fromID, data)
	u.CallbackQuery.Message = models.MaybeInaccessibleMessage{
		Type:                models.MaybeInaccessibleMessageTypeInaccessibleMessage,
		InaccessibleMessage: &models.InaccessibleMessage{Chat: models.Chat{ID: 100}, MessageID: 5},
	}
	return u
}

func TestHandleCallback_InaccessibleAlertGetsSeparateReport(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	b.SetActionHandler(func(context.Context, Action) error { return nil })

	b.handleUpdate(context.Background(), nil, inaccessibleCallbackUpdate(42, "disable:77"))

	sent := fake.find("sendRichMessage")
	if len(sent) != 1 {
		t.Fatalf("ожидался отдельный отчёт, sendRichMessage вызван %d раз", len(sent))
	}
	f := sent[0].fields
	if f["chat_id"] != "100" || !strings.Contains(f["reply_parameters"], `"message_id":5`) {
		t.Errorf("отчёт должен быть ответом на алерт: %v", f)
	}
	html := richHTML(t, f)
	for _, want := range []string{"Подписка отключена навсегда", "@admin", "<code>77</code>"} {
		if !strings.Contains(html, want) {
			t.Errorf("отчёт не содержит %q: %s", want, html)
		}
	}
	if buttonData(html) != "enable:77" {
		t.Errorf("после отключения в отчёте нужна кнопка включения: %s", html)
	}
	assertBalancedHTML(t, html)
}

func TestHandleCallback_RejectedRichEditFallsBackToReport(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	fake.rejectRich = true
	b.SetActionHandler(func(context.Context, Action) error { return nil })

	b.handleUpdate(context.Background(), nil, richCallbackUpdate(42, "disable_temp:77:30"))

	plain := fake.find("sendMessage")
	if len(plain) != 1 {
		t.Fatalf("ожидался отчёт обычным текстом, sendMessage вызван %d раз", len(plain))
	}
	f := plain[0].fields
	if !strings.Contains(f["text"], "Подписка временно отключена") || !strings.Contains(f["text"], "77") ||
		!strings.Contains(f["reply_parameters"], `"message_id":5`) {
		t.Errorf("неожиданный отчёт: %v", f)
	}
	if !strings.Contains(f["reply_markup"], `"callback_data":"enable:77"`) {
		t.Errorf("в отчёте нужна кнопка включения: %s", f["reply_markup"])
	}
}

func TestAnswerCallback_SurvivesCancelledContext(t *testing.T) {
	b, fake := newTestBot(t, 100, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b.handleUpdate(ctx, nil, callbackUpdate(1, "disable:77"))

	if len(fake.find("answerCallbackQuery")) != 1 {
		t.Error("ответ на callback должен уходить и с отменённым контекстом")
	}
}

// Telegram не присылает типы обновлений, отсутствующие в сохранённом
// allowed_updates (значение переживает рестарты и остаётся от прошлого
// webhook/getUpdates). Без явного списка бот на «чужом» или ранее
// использованном токене не получает callback_query, и кнопки молча не
// работают. Бот обязан запрашивать callback_query на каждом poll.
func TestStartPolling_RequestsCallbackQueryUpdates(t *testing.T) {
	got := make(chan []string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			var allowed []string
			if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && params["boundary"] != "" {
				mr := multipart.NewReader(r.Body, params["boundary"])
				for {
					part, err := mr.NextPart()
					if err != nil {
						break
					}
					if part.FormName() == "allowed_updates" {
						v, _ := io.ReadAll(part)
						_ = json.Unmarshal(v, &allowed)
					}
				}
			} else {
				var body struct {
					AllowedUpdates []string `json:"allowed_updates"`
				}
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				allowed = body.AllowedUpdates
			}
			select {
			case got <- allowed:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	b, err := newBot(testToken, 100, 0, []int64{42}, "", logger, tgbot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		b.StartPolling(ctx)
		close(done)
	}()

	select {
	case allowed := <-got:
		has := make(map[string]bool, len(allowed))
		for _, a := range allowed {
			has[a] = true
		}
		if !has["callback_query"] {
			t.Errorf("allowed_updates=%v, должен содержать callback_query", allowed)
		}
		if !has["message"] {
			t.Errorf("allowed_updates=%v, должен содержать message", allowed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("getUpdates не был вызван")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("polling не остановился после отмены контекста")
	}
}
