package logger

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

type ViolationFormatter struct{}

func (f *ViolationFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b bytes.Buffer

	timestamp := entry.Time.Format("2006/01/02 15:04:05")
	b.WriteString(timestamp)
	b.WriteString(" ")
	b.WriteString(entry.Message)
	b.WriteString("\n")

	return b.Bytes(), nil
}

func SetupLogger(logPath string) (*logrus.Logger, *os.File, error) {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, nil, err
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}

	mw := io.MultiWriter(os.Stdout, logFile)
	logger.SetOutput(mw)

	return logger, logFile, nil
}

func SetupViolationLogger(logPath string) (*logrus.Logger, *os.File, error) {
	logger := logrus.New()

	logger.SetFormatter(&ViolationFormatter{})

	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, nil, err
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}

	logger.SetOutput(logFile)

	return logger, logFile, nil
}
