package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigPath   = "/etc/transparent-proxy/config.yaml"
	DefaultListenAddr   = ":1444"
	DefaultNftStatePath = "/etc/nftables.d"
	ConfigVersion       = 1
)

// AppConfig is the top-level configuration.
type AppConfig struct {
	Version  int            `yaml:"version"`
	Listen   string         `yaml:"listen,omitempty"`
	Proxy    ProxyConfig    `yaml:"proxy,omitempty"`
	Checker  CheckerConfig  `yaml:"checker,omitempty"`
	Nft      NftConfig      `yaml:"nft,omitempty"`
	ChnRoute ChnRouteConfig `yaml:"chnroute,omitempty"`
}

type ProxyConfig struct {
	LanInterface string `yaml:"lan_interface,omitempty" json:"lan_interface"`
	DefaultPort  int    `yaml:"default_port,omitempty" json:"default_port"`
	ForcedPort   int    `yaml:"forced_port,omitempty" json:"forced_port"`
	SelfMark     int    `yaml:"self_mark,omitempty" json:"self_mark"`
}

type CheckerConfig struct {
	Enabled          bool   `yaml:"enabled,omitempty" json:"enabled"`
	Method           string `yaml:"method,omitempty" json:"method"`
	URL              string `yaml:"url,omitempty" json:"url"`
	Host             string `yaml:"host,omitempty" json:"host"`
	Timeout          string `yaml:"timeout,omitempty" json:"timeout"`
	Interval         string `yaml:"interval,omitempty" json:"interval"`
	FailureThreshold int    `yaml:"failure_threshold,omitempty" json:"failure_threshold"`
	OnFailure        string `yaml:"on_failure,omitempty" json:"on_failure"`
	Proxy            string `yaml:"proxy,omitempty" json:"proxy,omitempty"`
	BarkToken        string `yaml:"bark_token,omitempty" json:"bark_token,omitempty"`
}

type NftConfig struct {
	StatePath string   `yaml:"state_path,omitempty" json:"state_path"`
	Sets      []string `yaml:"sets,omitempty" json:"sets"`
}

type ChnRouteConfig struct {
	AutoRefresh     bool   `yaml:"auto_refresh,omitempty" json:"auto_refresh"`
	RefreshInterval string `yaml:"refresh_interval,omitempty" json:"refresh_interval"`
}

// LoadConfig reads and parses a YAML config file.
func LoadConfig(configPath string) (*AppConfig, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", configPath, err)
	}
	return ParseConfig(raw)
}

// ParseConfig parses raw YAML bytes into an AppConfig.
func ParseConfig(raw []byte) (*AppConfig, error) {
	config := new(AppConfig)
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	config.applyDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// SaveConfig validates, normalizes and atomically writes config to disk.
func SaveConfig(configPath string, config *AppConfig) (*AppConfig, error) {
	if config == nil {
		return nil, errors.New("config must not be nil")
	}

	raw, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	normalized, err := ParseConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	raw, err = yaml.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized config: %w", err)
	}

	if err := writeFileAtomically(configPath, raw, 0644); err != nil {
		return nil, err
	}
	return normalized, nil
}

// BuildDefaultConfig returns a default configuration.
func BuildDefaultConfig() *AppConfig {
	c := &AppConfig{
		Version: ConfigVersion,
		Listen:  DefaultListenAddr,
		Proxy: ProxyConfig{
			LanInterface: "br-lan",
			DefaultPort:  1081,
			ForcedPort:   1082,
			SelfMark:     255,
		},
		Checker: CheckerConfig{
			Enabled:          true,
			Method:           "HEAD",
			URL:              "http://www.google.com",
			Host:             "www.google.com",
			Timeout:          "10s",
			Interval:         "30s",
			FailureThreshold: 3,
			OnFailure:        "disable",
		},
		Nft: NftConfig{
			StatePath: DefaultNftStatePath,
			Sets:      []string{"direct_src", "direct_dst", "proxy_src", "proxy_dst", "allow_v6_mac"},
		},
		ChnRoute: ChnRouteConfig{
			AutoRefresh:     true,
			RefreshInterval: "168h",
		},
	}
	return c
}

func (c *AppConfig) applyDefaults() {
	if c.Listen == "" {
		c.Listen = DefaultListenAddr
	}
	if c.Proxy.LanInterface == "" {
		c.Proxy.LanInterface = "br-lan"
	}
	if c.Proxy.DefaultPort == 0 {
		c.Proxy.DefaultPort = 1081
	}
	if c.Proxy.ForcedPort == 0 {
		c.Proxy.ForcedPort = 1082
	}
	if c.Proxy.SelfMark == 0 {
		c.Proxy.SelfMark = 255
	}
	if c.Checker.Method == "" {
		c.Checker.Method = "HEAD"
	}
	if c.Checker.Timeout == "" {
		c.Checker.Timeout = "10s"
	}
	if c.Checker.Interval == "" {
		c.Checker.Interval = "30s"
	}
	if c.Checker.FailureThreshold <= 0 {
		c.Checker.FailureThreshold = 3
	}
	if c.Checker.OnFailure == "" {
		c.Checker.OnFailure = "disable"
	}
	if c.Checker.Enabled && c.Checker.Host == "" {
		c.Checker.Host = hostFromURL(c.Checker.URL)
	}
	if c.Nft.StatePath == "" {
		c.Nft.StatePath = DefaultNftStatePath
	}
	if c.ChnRoute.RefreshInterval == "" {
		c.ChnRoute.RefreshInterval = "168h"
	}
}

func (c *AppConfig) validate() error {
	if c.Version != ConfigVersion {
		return fmt.Errorf("unsupported config version %d: only %d is supported", c.Version, ConfigVersion)
	}
	if len(c.Nft.Sets) == 0 {
		return fmt.Errorf("nft.sets must not be empty")
	}
	for _, name := range c.Nft.Sets {
		if err := validateSetName(name); err != nil {
			return fmt.Errorf("nft.sets: %w", err)
		}
	}
	if c.Proxy.DefaultPort < 1 || c.Proxy.DefaultPort > 65535 {
		return fmt.Errorf("proxy.default_port must be 1-65535")
	}
	if c.Proxy.ForcedPort < 1 || c.Proxy.ForcedPort > 65535 {
		return fmt.Errorf("proxy.forced_port must be 1-65535")
	}
	if c.Proxy.SelfMark < 1 || c.Proxy.SelfMark > 255 {
		return fmt.Errorf("proxy.self_mark must be 1-255")
	}
	if err := c.Checker.validate(); err != nil {
		return err
	}
	if c.ChnRoute.RefreshInterval != "" {
		if _, err := time.ParseDuration(c.ChnRoute.RefreshInterval); err != nil {
			return fmt.Errorf("chnroute.refresh_interval: %w", err)
		}
	}
	if c.Checker.OnFailure != "disable" && c.Checker.OnFailure != "keep" {
		return fmt.Errorf("checker.on_failure must be \"disable\" or \"keep\"")
	}
	return nil
}

func (c *CheckerConfig) validate() error {
	method := strings.ToUpper(strings.TrimSpace(c.Method))
	if method != "GET" && method != "HEAD" {
		return fmt.Errorf("checker.method must be GET or HEAD")
	}
	if _, err := time.ParseDuration(c.Timeout); err != nil {
		return fmt.Errorf("checker.timeout: %w", err)
	}
	if _, err := time.ParseDuration(c.Interval); err != nil {
		return fmt.Errorf("checker.interval: %w", err)
	}
	if c.FailureThreshold < 1 {
		return fmt.Errorf("checker.failure_threshold must be at least 1")
	}
	if !c.Enabled {
		return nil
	}
	parsedURL, err := url.Parse(c.URL)
	if err != nil || parsedURL.Scheme == "" {
		return fmt.Errorf("checker.url must be a valid absolute URL")
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("checker.url scheme must be http or https")
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("checker.url must include a host")
	}
	return nil
}

func validateSetName(name string) error {
	if name == "" {
		return fmt.Errorf("set name must not be empty")
	}
	if strings.ContainsAny(name, "/\\\x00") || strings.Contains(name, "..") {
		return fmt.Errorf("set name %q contains invalid characters", name)
	}
	if name != filepath.Base(name) {
		return fmt.Errorf("set name %q must be a plain filename", name)
	}
	return nil
}

func hostFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Host
}

// writeFileAtomically writes data to a temp file, then renames to target path.
func writeFileAtomically(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(targetPath)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", targetPath, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpPath, targetPath, err)
	}
	cleanup = false
	return nil
}

// EnsureDefaultConfig creates a default config file if it doesn't exist at the default path.
func EnsureDefaultConfig(configPath string) error {
	abs, _ := filepath.Abs(configPath)
	defaultAbs, _ := filepath.Abs(DefaultConfigPath)
	if abs != defaultAbs {
		return nil
	}
	if _, err := os.Stat(configPath); err == nil {
		return nil
	}
	raw, err := yaml.Marshal(BuildDefaultConfig())
	if err != nil {
		return fmt.Errorf("marshal default config: %w", err)
	}
	return writeFileAtomically(configPath, raw, 0644)
}
