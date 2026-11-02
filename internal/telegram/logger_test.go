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

func TestBotLogger_PollingFailureIsVisible(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	bl.Errorf("error get updates, unauthorized, Unauthorized")

	if !strings.Contains(buf.String(), "error get updates") {
		t.Errorf("ошибка polling должна попадать в лог, получено: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "level=warning") {
		t.Errorf("ожидался уровень warning, получено: %s", buf.String())
	}
}

func TestBotLogger_RepeatsSuppressed(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	for i := 0; i < 20; i++ {
		bl.Errorf("error get updates, unauthorized, Unauthorized")
	}

	if got := strings.Count(buf.String(), "level=warning"); got != 1 {
		t.Errorf("ожидалось 1 предупреждение на серию повторов, получено %d", got)
	}
}

func TestBotLogger_DistinctMessagesNotSuppressed(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	bl.Errorf("error get updates, ошибка A")
	bl.Errorf("error get updates, ошибка B")

	if got := strings.Count(buf.String(), "level=warning"); got != 2 {
		t.Errorf("разные сообщения не должны подавлять друг друга, получено %d", got)
	}
}

func TestBotLogger_RedactsToken(t *testing.T) {
	bl, buf := testBotLogger(logrus.DebugLevel)

	bl.Debugf("GET https://api.telegram.org/bot%s/getMe", "123456:SECRET-TOKEN")
	bl.Errorf("error get updates, Post https://api.telegram.org/bot%s/getUpdates", "123456:SECRET-TOKEN")

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

func TestBotLogger_TrackedMessagesBounded(t *testing.T) {
	bl, _ := testBotLogger(logrus.InfoLevel)

	for i := 0; i < maxTrackedMessages*3; i++ {
		bl.Errorf("error get updates, ошибка %d", i)
	}

	bl.mu.Lock()
	size := len(bl.lastSeen)
	bl.mu.Unlock()

	if size > maxTrackedMessages {
		t.Errorf("размер карты %d превысил лимит %d", size, maxTrackedMessages)
	}
}
