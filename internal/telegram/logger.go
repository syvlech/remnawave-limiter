package telegram

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const repeatWindow = 5 * time.Minute

const maxTrackedMessages = 64

var reSourceAddr = regexp.MustCompile(`\S+->`)

type botLogger struct {
	logger *logrus.Logger
	token  string

	mu       sync.Mutex
	lastSeen map[string]time.Time
}

func newBotLogger(logger *logrus.Logger, token string) *botLogger {
	return &botLogger{
		logger:   logger,
		token:    token,
		lastSeen: make(map[string]time.Time),
	}
}

func (l *botLogger) redact(s string) string {
	if l.token == "" {
		return s
	}
	return strings.ReplaceAll(s, l.token, "***")
}

func (l *botLogger) allow(msg string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if last, ok := l.lastSeen[msg]; ok && now.Sub(last) < repeatWindow {
		return false
	}

	if len(l.lastSeen) >= maxTrackedMessages {
		for k, t := range l.lastSeen {
			if now.Sub(t) >= repeatWindow {
				delete(l.lastSeen, k)
			}
		}
		if len(l.lastSeen) >= maxTrackedMessages {
			l.lastSeen = make(map[string]time.Time, maxTrackedMessages)
		}
	}

	l.lastSeen[msg] = now
	return true
}

func dedupeKey(msg string) string {
	return reSourceAddr.ReplaceAllString(msg, "->")
}

func (l *botLogger) report(err error) {
	msg := l.redact(err.Error())
	entry := l.logger.WithField("src", "tgbot")

	if !l.allow(dedupeKey(msg)) {
		entry.Debug(msg)
		return
	}
	entry.Warn(msg)
}
