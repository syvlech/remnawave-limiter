package telegram

import (
	"bytes"
	"errors"
	"fmt"
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

	bl.report(errors.New("error get updates, unauthorized, Unauthorized"))

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
		bl.report(errors.New("error get updates, unauthorized, Unauthorized"))
	}

	if got := strings.Count(buf.String(), "level=warning"); got != 1 {
		t.Errorf("ожидалось 1 предупреждение на серию повторов, получено %d", got)
	}
}

func TestBotLogger_DistinctMessagesNotSuppressed(t *testing.T) {
	bl, buf := testBotLogger(logrus.InfoLevel)

	bl.report(errors.New("error get updates, ошибка A"))
	bl.report(errors.New("error get updates, ошибка B"))

	if got := strings.Count(buf.String(), "level=warning"); got != 2 {
		t.Errorf("разные сообщения не должны подавлять друг друга, получено %d", got)
	}
}

func TestBotLogger_RedactsToken(t *testing.T) {
	bl, buf := testBotLogger(logrus.DebugLevel)

	bl.report(fmt.Errorf("error get updates, Post https://api.telegram.org/bot%s/getUpdates", "123456:SECRET-TOKEN"))

	out := buf.String()
	if strings.Contains(out, "SECRET-TOKEN") {
		t.Errorf("токен бота утёк в лог: %s", out)
	}
	if !strings.Contains(out, "***") {
		t.Errorf("ожидалась маскировка токена, получено: %s", out)
	}
}

func TestBotLogger_EphemeralPortsDoNotDefeatDedupe(t *testing.T) {
	bl, buf := testBotLogger(logrus.DebugLevel)

	for port := 48100; port < 48100+maxTrackedMessages*2; port++ {
		bl.report(fmt.Errorf("error get updates, read tcp 172.18.0.3:%d->149.154.167.220:443: read: connection reset by peer", port))
		bl.report(fmt.Errorf("error get updates, read tcp [fd00::3]:%d->[2001:67c:4e8:f004::9]:443: read: connection reset by peer", port))
		bl.report(fmt.Errorf("error get updates, lookup api.telegram.org on 127.0.0.11:53: read udp 172.18.0.3:%d->127.0.0.11:53: i/o timeout", port))
	}

	out := buf.String()
	if got := strings.Count(out, "level=warning"); got != 3 {
		t.Errorf("ожидалось по одному предупреждению на каждый вид ошибки (3), получено %d", got)
	}
	if !strings.Contains(out, "172.18.0.3:48100->149.154.167.220:443") {
		t.Errorf("в лог должен попадать исходный текст ошибки с адресами: %s", out)
	}
	if got := strings.Count(out, "level=debug"); got != maxTrackedMessages*6-3 {
		t.Errorf("повторы должны уходить на уровень debug, получено %d", got)
	}

	bl.mu.Lock()
	size := len(bl.lastSeen)
	bl.mu.Unlock()
	if size != 3 {
		t.Errorf("в карте должно быть 3 ключа, получено %d", size)
	}
}

func TestDedupeKey(t *testing.T) {
	for in, want := range map[string]string{
		"read tcp 172.18.0.3:48122->149.154.167.220:443: connection reset by peer": "read tcp ->149.154.167.220:443: connection reset by peer",
		"write tcp [fd00::3]:51000->[2001:db8::1]:443: broken pipe":                "write tcp ->[2001:db8::1]:443: broken pipe",
		"dial tcp 149.154.167.220:443: i/o timeout":                                "dial tcp 149.154.167.220:443: i/o timeout",
		"unauthorized, Unauthorized":                                               "unauthorized, Unauthorized",
	} {
		if got := dedupeKey(in); got != want {
			t.Errorf("dedupeKey(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestBotLogger_TrackedMessagesBounded(t *testing.T) {
	bl, _ := testBotLogger(logrus.InfoLevel)

	for i := 0; i < maxTrackedMessages*3; i++ {
		bl.report(fmt.Errorf("error get updates, ошибка %d", i))
	}

	bl.mu.Lock()
	size := len(bl.lastSeen)
	bl.mu.Unlock()

	if size > maxTrackedMessages {
		t.Errorf("размер карты %d превысил лимит %d", size, maxTrackedMessages)
	}
}
