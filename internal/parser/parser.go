package parser

import (
	"regexp"
	"time"
)

type LogEntry struct {
	Email     string
	IP        string
	Timestamp time.Time
}

type Parser struct {
	logPattern       *regexp.Regexp
	timestampPattern *regexp.Regexp
}

func NewParser() *Parser {
	return &Parser{
		logPattern:       regexp.MustCompile(`from\s+(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}):\d+\s+accepted.*?email:\s*(\S+)`),
		timestampPattern: regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})`),
	}
}

func (p *Parser) ParseLine(line string) *LogEntry {
	timestampMatch := p.timestampPattern.FindStringSubmatch(line)
	var timestamp time.Time
	if len(timestampMatch) > 1 {
		t, err := time.ParseInLocation("2006/01/02 15:04:05", timestampMatch[1], time.Local)
		if err == nil {
			timestamp = t
		}
	}

	match := p.logPattern.FindStringSubmatch(line)
	if len(match) < 3 {
		return nil
	}

	ip := match[1]
	email := match[2]

	if ip == "127.0.0.1" || ip == "::1" {
		return nil
	}

	if timestamp.IsZero() {
		return nil
	}

	return &LogEntry{
		Email:     email,
		IP:        ip,
		Timestamp: timestamp,
	}
}
