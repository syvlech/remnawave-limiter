package telegram

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const repeatWindow = 5 * time.Minute

const maxTrackedMessages = 64

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

func (l *botLogger) Debugf(format string, args ...any) {
	if !l.logger.IsLevelEnabled(logrus.DebugLevel) {
		return
	}
	l.logger.WithField("src", "telego").Debug(l.redact(fmt.Sprintf(format, args...)))
}

var duplicatePrefixes = []string{
	"Execution error",
	"Retrying getting updates in",
}

func (l *botLogger) Errorf(format string, args ...any) {
	msg := l.redact(fmt.Sprintf(format, args...))
	entry := l.logger.WithField("src", "telego")

	for _, p := range duplicatePrefixes {
		if strings.HasPrefix(msg, p) {
			entry.Debug(msg)
			return
		}
	}

	if !l.allow(msg) {
		entry.Debug(msg)
		return
	}
	entry.Warn(msg)
}
