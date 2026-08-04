package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/config"
)

var stdout = os.Stdout

type consoleFormatter struct {
	timeFormat string
}

var levelNames = map[logrus.Level]string{
	logrus.TraceLevel: "TRACE",
	logrus.DebugLevel: "DEBUG",
	logrus.InfoLevel:  "INFO ",
	logrus.WarnLevel:  "WARN ",
	logrus.ErrorLevel: "ERROR",
	logrus.FatalLevel: "FATAL",
	logrus.PanicLevel: "PANIC",
}

func (f *consoleFormatter) Format(e *logrus.Entry) ([]byte, error) {
	var b bytes.Buffer

	b.WriteString(e.Time.Format(f.timeFormat))
	b.WriteByte(' ')

	name, ok := levelNames[e.Level]
	if !ok {
		name = strings.ToUpper(e.Level.String())
	}
	b.WriteString(name)
	b.WriteString("  ")
	b.WriteString(e.Message)

	keys := make([]string, 0, len(e.Data))
	for k := range e.Data {
		if k == logrus.ErrorKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) > 0 {
		b.WriteString("  ")
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%s=%v", k, e.Data[k])
		}
	}

	if err, ok := e.Data[logrus.ErrorKey]; ok {
		fmt.Fprintf(&b, "  error=%v", err)
	}

	b.WriteByte('\n')
	return b.Bytes(), nil
}

func newLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(stdout)
	logger.SetFormatter(&consoleFormatter{timeFormat: defaultTimeFormat})
	return logger
}

const defaultTimeFormat = "2006-01-02 15:04:05"

func applyLogSettings(logger *logrus.Logger, cfg *config.Config) {
	if level, err := logrus.ParseLevel(cfg.LogLevel); err == nil {
		logger.SetLevel(level)
	}
	if cfg.LogFormat == "json" {
		logger.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logger.SetFormatter(&consoleFormatter{timeFormat: defaultTimeFormat})
	}
}
