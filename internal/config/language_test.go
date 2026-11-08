package config

import "testing"

func TestLoadConfig_ChineseLanguage(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	t.Setenv("LANGUAGE", "zh")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LANGUAGE=zh should be accepted: %v", err)
	}
	if cfg.Language != "zh" {
		t.Errorf("Language = %q, want zh", cfg.Language)
	}
}
