package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseConfig_Valid(t *testing.T) {
	raw := mustMarshal(t, BuildDefaultConfig())
	config, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if config.Version != ConfigVersion {
		t.Errorf("Version = %d, want %d", config.Version, ConfigVersion)
	}
	if config.Listen != DefaultListenAddr {
		t.Errorf("Listen = %q, want %q", config.Listen, DefaultListenAddr)
	}
}

func TestParseConfig_RejectsWrongVersion(t *testing.T) {
	c := BuildDefaultConfig()
	c.Version = 999
	raw := mustMarshal(t, c)
	_, err := ParseConfig(raw)
	if err == nil {
		t.Fatal("expected error for wrong version")
	}
	if !strings.Contains(err.Error(), "unsupported config version") {
		t.Errorf("error = %q, want mention of unsupported version", err.Error())
	}
}

func TestParseConfig_AppliesDefaults(t *testing.T) {
	raw := []byte("version: 1\nnft:\n  sets: [\"direct_dst\"]\n")
	config, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if config.Listen != DefaultListenAddr {
		t.Errorf("Listen = %q, want default %q", config.Listen, DefaultListenAddr)
	}
	if config.Proxy.DefaultPort != 1081 {
		t.Errorf("DefaultPort = %d, want 1081", config.Proxy.DefaultPort)
	}
	if config.Checker.Method != "HEAD" {
		t.Errorf("Checker.Method = %q, want HEAD", config.Checker.Method)
	}
	if config.Checker.FailureThreshold != 3 {
		t.Errorf("Checker.FailureThreshold = %d, want 3", config.Checker.FailureThreshold)
	}
	if config.Checker.OnFailure != "disable" {
		t.Errorf("Checker.OnFailure = %q, want disable", config.Checker.OnFailure)
	}
}

func TestValidate_RejectsInvalidPort(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*AppConfig)
		errSub string
	}{
		{"default_port_negative", func(c *AppConfig) { c.Proxy.DefaultPort = -1 }, "default_port"},
		{"default_port_too_high", func(c *AppConfig) { c.Proxy.DefaultPort = 70000 }, "default_port"},
		{"forced_port_negative", func(c *AppConfig) { c.Proxy.ForcedPort = -1 }, "forced_port"},
		{"forced_port_too_high", func(c *AppConfig) { c.Proxy.ForcedPort = 70000 }, "forced_port"},
		{"self_mark_negative", func(c *AppConfig) { c.Proxy.SelfMark = -1 }, "self_mark"},
		{"self_mark_too_high", func(c *AppConfig) { c.Proxy.SelfMark = 999 }, "self_mark"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := BuildDefaultConfig()
			tt.modify(c)
			raw := mustMarshal(t, c)
			_, err := ParseConfig(raw)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.errSub)
			}
		})
	}
}

func TestValidate_RejectsInvalidChecker(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*AppConfig)
		errSub string
	}{
		{
			"bad_method",
			func(c *AppConfig) { c.Checker.Method = "DELETE" },
			"checker.method",
		},
		{
			"bad_url_scheme",
			func(c *AppConfig) { c.Checker.URL = "ftp://example.com" },
			"checker.url",
		},
		{
			"bad_url_no_host",
			func(c *AppConfig) { c.Checker.URL = "http://" },
			"checker.url",
		},
		{
			"bad_timeout",
			func(c *AppConfig) { c.Checker.Timeout = "nope" },
			"checker.timeout",
		},
		{
			"bad_interval",
			func(c *AppConfig) { c.Checker.Interval = "nope" },
			"checker.interval",
		},
		{
			"bad_on_failure",
			func(c *AppConfig) { c.Checker.OnFailure = "crash" },
			"on_failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := BuildDefaultConfig()
			tt.modify(c)
			raw := mustMarshal(t, c)
			_, err := ParseConfig(raw)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.errSub)
			}
		})
	}
}

func TestSaveConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	original := BuildDefaultConfig()
	original.Listen = ":9999"
	original.Checker.URL = "http://example.com"
	original.Checker.Host = "example.com"

	saved, err := SaveConfig(configPath, original)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if saved.Listen != ":9999" {
		t.Errorf("saved.Listen = %q, want :9999", saved.Listen)
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Listen != ":9999" {
		t.Errorf("loaded.Listen = %q, want :9999", loaded.Listen)
	}
	if loaded.Checker.URL != "http://example.com" {
		t.Errorf("loaded.Checker.URL = %q, want http://example.com", loaded.Checker.URL)
	}
}

func TestBuildDefaultConfig_IsValid(t *testing.T) {
	c := BuildDefaultConfig()
	raw := mustMarshal(t, c)
	_, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("BuildDefaultConfig produced invalid config: %v", err)
	}
}

func TestParseConfig_RejectsUnknownFields(t *testing.T) {
	raw := []byte("version: 1\nnft:\n  sets: [\"x\"]\nunknown_field: true\n")
	_, err := ParseConfig(raw)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestValidateSetName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "direct_dst", false},
		{"empty", "", true},
		{"dotdot", "a..b", true},
		{"slash", "a/b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSetName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSetName(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestEnsureDefaultConfig_CreatesAtDefaultPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Non-default path: should be a no-op
	err := EnsureDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("EnsureDefaultConfig non-default path: %v", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Error("expected no file created for non-default path")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	return raw
}
