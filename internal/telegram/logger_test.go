package telegram

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

func testBotLogger(level logrus.Level) (*botLogger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := logrus.New()
	l.SetOutput(buf)
	l.SetLevel(level)
	l.SetFormatter(&logrus.TextFormatter{DisableColors: true, DisableTimestamp: true})
	return newBotLogger(l, "123456:SECRET-TOKEN"), buf
}

// Ошибки, которые вызывающий код логирует сам, не должны появляться
// в выводе дважды.
func TestBotLogger_DuplicatesDemotedToDebug(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	bl.Errorf(`Execution error sendMessage: request call: 401 "Unauthorized"`)
	bl.Errorf("Retrying getting updates in 8s...")

	if buf.Len() != 0 {
		t.Errorf("на уровне info дубли не должны печататься, получено: %s", buf.String())
	}
}

// Сбои фонового long polling наш код не видит — они обязаны быть заметны.
func TestBotLogger_PollingFailureIsVisible(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	bl.Errorf("Getting updates: telego: getUpdates: 401 Unauthorized")

	if !strings.Contains(buf.String(), "Getting updates") {
		t.Errorf("ошибка polling должна попадать в лог, получено: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "level=warning") {
		t.Errorf("ожидался уровень warning, получено: %s", buf.String())
	}
}

// Цикл polling ретраится раз в 8 секунд: без подавления повторов
// он заливает вывод.
func TestBotLogger_RepeatsSuppressed(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	for i := 0; i < 20; i++ {
		bl.Errorf("Getting updates: telego: getUpdates: 401 Unauthorized")
	}

	if got := strings.Count(buf.String(), "level=warning"); got != 1 {
		t.Errorf("ожидалось 1 предупреждение на серию повторов, получено %d", got)
	}
}

func TestBotLogger_DistinctMessagesNotSuppressed(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	bl.Errorf("Getting updates: ошибка A")
	bl.Errorf("Getting updates: ошибка B")

	if got := strings.Count(buf.String(), "level=warning"); got != 2 {
		t.Errorf("разные сообщения не должны подавлять друг друга, получено %d", got)
	}
}

// telego предупреждает, что в отладочные сообщения попадает токен бота.
func TestBotLogger_RedactsToken(t *testing.T) {
	bl, buf := testBotLogger(logrus.DebugLevel)

	bl.Debugf("GET https://api.telegram.org/bot%s/getMe", "123456:SECRET-TOKEN")
	bl.Errorf("Getting updates: https://api.telegram.org/bot%s/getUpdates", "123456:SECRET-TOKEN")

	out := buf.String()
	if strings.Contains(out, "SECRET-TOKEN") {
		t.Errorf("токен бота утёк в лог: %s", out)
	}
	if !strings.Contains(out, "***") {
		t.Errorf("ожидалась маскировка токена, получено: %s", out)
	}
}

func TestBotLogger_DebugSkippedWhenLevelDisabled(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	bl.Debugf("подробности запроса")

	if buf.Len() != 0 {
		t.Errorf("debug не должен печататься на уровне info: %s", buf.String())
	}
}

// Набор текстов ошибок у telego маленький, но карта дедупликации всё равно
// не должна расти без границы.
func TestBotLogger_TrackedMessagesBounded(t *testing.T) {
	bl, _ := testBotLogger(logrus.InfoLevel)

	for i := 0; i < maxTrackedMessages*3; i++ {
		bl.Errorf("Getting updates: ошибка %d", i)
	}

	bl.mu.Lock()
	size := len(bl.lastSeen)
	bl.mu.Unlock()

	if size > maxTrackedMessages {
		t.Errorf("размер карты %d превысил лимит %d", size, maxTrackedMessages)
	}
}
