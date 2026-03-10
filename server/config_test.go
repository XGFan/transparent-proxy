package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigRejectsV2(t *testing.T) {
	t.Run("rejects legacy config without explicit version", func(t *testing.T) {
		legacyYAML := []byte(`checker: {}
nft:
  sets:
    - proxy_src
`)

		_, err := ParseConfig(legacyYAML, UserConfigPath)
		if err == nil {
			t.Fatal("ParseConfig() expected failure for legacy config")
		}
		if !strings.Contains(err.Error(), "config version must be v3") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects explicit v2 config", func(t *testing.T) {
		v2YAML := []byte(`version: v2
nft:
  sets:
    - proxy_src
`)

		_, err := ParseConfig(v2YAML, UserConfigPath)
		if err == nil {
			t.Fatal("ParseConfig() expected failure for v2 config")
		}
		if !strings.Contains(err.Error(), `unsupported config version "v2": only v3 is supported`) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestParseConfigAcceptsV3(t *testing.T) {
	v3YAML := []byte(`version: v3
nft:
  sets:
    - proxy_src
    - proxy_dst
    - direct_src
    - direct_dst
`)

	config, err := ParseConfig(v3YAML, UserConfigPath)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if config.Version != CurrentConfigSchema {
		t.Fatalf("config version = %s, want %s", config.Version, CurrentConfigSchema)
	}
	if config.Server.ListenAddress != DefaultListenAddress {
		t.Fatalf("server.listenAddress = %s, want %s", config.Server.ListenAddress, DefaultListenAddress)
	}
	if config.Nft.StatePath != DefaultNftStatePath {
		t.Fatalf("nft.statePath = %s, want %s", config.Nft.StatePath, DefaultNftStatePath)
	}
	if len(config.Nft.Sets) != 4 {
		t.Fatalf("nft.sets len = %d, want 4", len(config.Nft.Sets))
	}
}

func TestParseConfigHonorsExplicitListenAddress(t *testing.T) {
	v3YAML := []byte(`version: v3
server:
  listenAddress: 127.0.0.1:1444
nft:
  sets:
    - proxy_src
`)

	config, err := ParseConfig(v3YAML, UserConfigPath)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if config.Server.ListenAddress != "127.0.0.1:1444" {
		t.Fatalf("server.listenAddress = %s, want %s", config.Server.ListenAddress, "127.0.0.1:1444")
	}
}

func TestParseConfigNormalizesLegacyCheckerFields(t *testing.T) {
	legacyYAML := []byte(`version: v3
checker:
  name: default
  targets:
    - host: www.google.com
  threshold: 4
  postUp: /etc/transparent-proxy/enable.sh
  postDown: /etc/transparent-proxy/disable.sh
nft:
  sets:
    - proxy_src
`)

	config, err := ParseConfig(legacyYAML, UserConfigPath)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if !config.Checker.Enabled {
		t.Fatal("checker.enabled = false, want true")
	}
	if config.Checker.URL != "http://www.google.com" {
		t.Fatalf("checker.url = %q, want %q", config.Checker.URL, "http://www.google.com")
	}
	if config.Checker.Host != "www.google.com" {
		t.Fatalf("checker.host = %q, want %q", config.Checker.Host, "www.google.com")
	}
	if config.Checker.FailureThreshold != 4 {
		t.Fatalf("checker.failureThreshold = %d, want 4", config.Checker.FailureThreshold)
	}
	if config.Checker.Timeout != "10s" {
		t.Fatalf("checker.timeout = %q, want %q", config.Checker.Timeout, "10s")
	}
	if config.Checker.CheckInterval != "30s" {
		t.Fatalf("checker.checkInterval = %q, want %q", config.Checker.CheckInterval, "30s")
	}
}

func TestSaveConfigWritesNormalizedCheckerFields(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := &AppConfig{
		Version: CurrentConfigSchema,
		Checker: CheckerConfig{
			Enabled:          true,
			Method:           "GET",
			URL:              "https://example.com/healthz",
			Host:             "status.example.com",
			Timeout:          "15s",
			FailureThreshold: 5,
			CheckInterval:    "45s",
		},
		Nft: NftConfig{Sets: []string{"proxy_src"}},
	}

	savedConfig, err := SaveConfig(configPath, config)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", configPath, err)
	}
	content := string(raw)
	for _, want := range []string{
		"enabled: true",
		"method: GET",
		"url: https://example.com/healthz",
		"host: status.example.com",
		"timeout: 15s",
		"failureThreshold: 5",
		"checkInterval: 45s",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("saved config missing %q in %q", want, content)
		}
	}
	if strings.Contains(content, "name:") || strings.Contains(content, "targets:") {
		t.Fatalf("saved config should not contain legacy checker fields: %q", content)
	}
	if savedConfig.Checker.URL != "https://example.com/healthz" {
		t.Fatalf("saved checker.url = %q, want %q", savedConfig.Checker.URL, "https://example.com/healthz")
	}
}

func TestLoadConfigRejectsInvalidExistingConfigWithoutOverwrite(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	invalidConfig := []byte(`version: v3
nft:
  sets: []
`)
	if err := os.WriteFile(configPath, invalidConfig, 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", configPath, err)
	}

	beforeBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) before LoadConfig error = %v", configPath, err)
	}

	_, err = LoadConfig(configPath)
	if err == nil {
		t.Fatal("LoadConfig() should reject invalid existing config instead of mutating it")
	}
	if !strings.Contains(err.Error(), "nft.sets must not be empty") {
		t.Fatalf("LoadConfig() error = %q, want nft.sets validation failure", err)
	}

	afterBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) after LoadConfig error = %v", configPath, err)
	}
	if string(afterBytes) != string(beforeBytes) {
		t.Fatalf("LoadConfig() must not overwrite invalid existing config bytes; got %q, want %q", string(afterBytes), string(beforeBytes))
	}
}
