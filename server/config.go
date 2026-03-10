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

	"github.com/XGFan/netguard"
	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigPath    = "/etc/transparent-proxy/config.yaml"
	DefaultListenAddress = ":1444"
	DefaultNftStatePath  = "/etc/nftables.d"
	UserConfigPath       = "/etc/transparent-proxy/config.yaml"
	CurrentConfigSchema  = "v3"
)

type AppConfig struct {
	Version string        `yaml:"version,omitempty"`
	Server  ServerConfig  `yaml:"server,omitempty"`
	Checker CheckerConfig `yaml:"checker,omitempty"`
	Nft     NftConfig     `yaml:"nft,omitempty"`
}

type ServerConfig struct {
	ListenAddress string `yaml:"listenAddress,omitempty"`
}

type NftConfig struct {
	StatePath string   `yaml:"statePath,omitempty"`
	Sets      []string `yaml:"sets,omitempty"`
}

type bootstrapCheckerConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Method           string `yaml:"method"`
	URL              string `yaml:"url"`
	Host             string `yaml:"host"`
	Timeout          string `yaml:"timeout"`
	FailureThreshold int    `yaml:"failureThreshold"`
	CheckInterval    string `yaml:"checkInterval"`
}

type bootstrapNftConfig struct {
	Sets []string `yaml:"sets,omitempty"`
}

type bootstrapConfigDocument struct {
	Version string                 `yaml:"version,omitempty"`
	Checker bootstrapCheckerConfig `yaml:"checker,omitempty"`
	Nft     bootstrapNftConfig     `yaml:"nft,omitempty"`
}

type CheckerConfig struct {
	Enabled          bool   `yaml:"enabled,omitempty"`
	Method           string `yaml:"method,omitempty"`
	URL              string `yaml:"url,omitempty"`
	Host             string `yaml:"host,omitempty"`
	Timeout          string `yaml:"timeout,omitempty"`
	FailureThreshold int    `yaml:"failureThreshold,omitempty"`
	CheckInterval    string `yaml:"checkInterval,omitempty"`

	Name      string            `yaml:"name,omitempty"`
	Targets   []netguard.Target `yaml:"targets,omitempty"`
	Proxy     string            `yaml:"proxy,omitempty"`
	Threshold int               `yaml:"threshold,omitempty"`
	PostUp    string            `yaml:"postUp,omitempty"`
	PostDown  string            `yaml:"postDown,omitempty"`
}

type checkerConfigYAML struct {
	Enabled          bool   `yaml:"enabled"`
	Method           string `yaml:"method"`
	URL              string `yaml:"url"`
	Host             string `yaml:"host"`
	Timeout          string `yaml:"timeout"`
	FailureThreshold int    `yaml:"failureThreshold"`
	CheckInterval    string `yaml:"checkInterval"`
	Proxy            string `yaml:"proxy,omitempty"`
}

func LoadConfig(configPath string) (*AppConfig, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config %s fail: %w", configPath, err)
	}
	return ParseConfig(raw, configPath)
}

func BuildDefaultBootstrapConfigYAML() ([]byte, error) {
	document := bootstrapConfigDocument{
		Version: CurrentConfigSchema,
		Checker: bootstrapCheckerConfig{
			Enabled:          true,
			Method:           "HEAD",
			URL:              "http://www.google.com",
			Host:             "www.google.com",
			Timeout:          "10s",
			FailureThreshold: 3,
			CheckInterval:    "30s",
		},
		Nft: bootstrapNftConfig{Sets: []string{"direct_src", "direct_dst", "proxy_src", "proxy_dst"}},
	}

	raw, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("marshal default bootstrap config fail: %w", err)
	}
	return raw, nil
}

func ensureBootstrapConfigExists(configPath string) error {
	if !shouldAutoBootstrapConfig(configPath) {
		return nil
	}

	if _, err := os.Stat(configPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config %s fail: %w", configPath, err)
	}

	raw, err := BuildDefaultBootstrapConfigYAML()
	if err != nil {
		return err
	}

	return writeBootstrapConfigAtomically(configPath, raw)
}

func shouldAutoBootstrapConfig(configPath string) bool {
	return isUserConfigPath(configPath)
}

func writeBootstrapConfigAtomically(configPath string, raw []byte) error {
	if _, err := ParseConfig(raw, configPath); err != nil {
		return fmt.Errorf("validate generated bootstrap config for %s fail: %w", configPath, err)
	}
	return writeConfigAtomically(configPath, raw, "."+filepath.Base(configPath)+".bootstrap-*")
}

func SaveConfig(configPath string, config *AppConfig) (*AppConfig, error) {
	if config == nil {
		return nil, errors.New("config must not be nil")
	}

	raw, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal config fail: %w", err)
	}

	normalized, err := ParseConfig(raw, configPath)
	if err != nil {
		return nil, fmt.Errorf("validate config fail: %w", err)
	}

	raw, err = yaml.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized config fail: %w", err)
	}

	if err := writeConfigAtomically(configPath, raw, "."+filepath.Base(configPath)+".config-*"); err != nil {
		return nil, err
	}

	return normalized, nil
}

func writeConfigAtomically(configPath string, raw []byte, tempPattern string) error {
	directory := filepath.Dir(configPath)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("create config directory %s fail: %w", directory, err)
	}

	tempFile, err := os.CreateTemp(directory, tempPattern)
	if err != nil {
		return fmt.Errorf("create temp config for %s fail: %w", configPath, err)
	}
	tempPath := tempFile.Name()
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(0644); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("chmod temp config %s fail: %w", tempPath, err)
	}
	if _, err := tempFile.Write(raw); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write temp config %s fail: %w", tempPath, err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("sync temp config %s fail: %w", tempPath, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temp config %s fail: %w", tempPath, err)
	}

	if err := os.Rename(tempPath, configPath); err != nil {
		return fmt.Errorf("rename temp config %s to %s fail: %w", tempPath, configPath, err)
	}
	cleanupTemp = false

	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync config directory %s fail: %w", directory, err)
	}

	return nil
}

func ParseConfig(raw []byte, configPath string) (*AppConfig, error) {
	config := new(AppConfig)
	if err := decodeYAMLKnown(raw, config); err != nil {
		return nil, fmt.Errorf("decode config fail: %w", err)
	}
	config.ApplyDefaults()
	if err := config.Validate(configPath); err != nil {
		return nil, err
	}
	return config, nil
}

func (c *AppConfig) ApplyDefaults() {
	c.Checker.ApplyDefaults()
	if c.Server.ListenAddress == "" {
		c.Server.ListenAddress = DefaultListenAddress
	}
	if c.Nft.StatePath == "" {
		c.Nft.StatePath = DefaultNftStatePath
	}
}

func (c *AppConfig) ListenAddress() string {
	if c == nil {
		return DefaultListenAddress
	}
	listenAddress := strings.TrimSpace(c.Server.ListenAddress)
	if listenAddress == "" {
		return DefaultListenAddress
	}
	return listenAddress
}

func (c *AppConfig) Validate(_ string) error {
	if c.Version != CurrentConfigSchema {
		if c.Version == "" {
			return fmt.Errorf("config version must be %s", CurrentConfigSchema)
		}
		return fmt.Errorf("unsupported config version %q: only %s is supported", c.Version, CurrentConfigSchema)
	}
	if len(c.Nft.Sets) == 0 {
		return fmt.Errorf("nft.sets must not be empty")
	}
	if err := c.Checker.Validate(); err != nil {
		return err
	}
	return nil
}

func (c *CheckerConfig) ApplyDefaults() {
	if c == nil {
		return
	}

	if c.EnabledFromLegacy() {
		c.Enabled = true
	}
	if c.URL == "" {
		c.URL = c.legacyURL()
	}
	if c.Host == "" {
		c.Host = c.legacyHost()
	}
	if c.FailureThreshold <= 0 && c.Threshold > 0 {
		c.FailureThreshold = c.Threshold
	}
	if strings.TrimSpace(c.Method) == "" {
		c.Method = "HEAD"
	}
	if strings.TrimSpace(c.Timeout) == "" {
		c.Timeout = "10s"
	}
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 3
	}
	if strings.TrimSpace(c.CheckInterval) == "" {
		c.CheckInterval = "30s"
	}
	if c.Enabled && strings.TrimSpace(c.Host) == "" {
		c.Host = c.hostFromURL()
	}

	c.Name = ""
	c.Targets = nil
	c.Threshold = 0
	c.PostUp = ""
	c.PostDown = ""
}

func (c CheckerConfig) Validate() error {
	method := strings.ToUpper(strings.TrimSpace(c.Method))
	if method != "GET" && method != "HEAD" {
		return fmt.Errorf("checker.method must be GET or HEAD")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(c.Timeout)); err != nil {
		return fmt.Errorf("checker.timeout must be a valid duration: %w", err)
	}
	if _, err := time.ParseDuration(strings.TrimSpace(c.CheckInterval)); err != nil {
		return fmt.Errorf("checker.checkInterval must be a valid duration: %w", err)
	}
	if c.FailureThreshold < 1 {
		return fmt.Errorf("checker.failureThreshold must be at least 1")
	}
	if !c.Enabled {
		return nil
	}
	parsedURL, err := c.parsedURL()
	if err != nil {
		return err
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("checker.url must include a host")
	}
	return nil
}

func (c CheckerConfig) MarshalYAML() (any, error) {
	return checkerConfigYAML{
		Enabled:          c.Enabled,
		Method:           strings.ToUpper(strings.TrimSpace(c.Method)),
		URL:              strings.TrimSpace(c.URL),
		Host:             strings.TrimSpace(c.Host),
		Timeout:          strings.TrimSpace(c.Timeout),
		FailureThreshold: c.FailureThreshold,
		CheckInterval:    strings.TrimSpace(c.CheckInterval),
		Proxy:            strings.TrimSpace(c.Proxy),
	}, nil
}

func (c CheckerConfig) NetguardConfig() netguard.CheckerConf {
	if !c.Enabled {
		return netguard.CheckerConf{}
	}

	timeout, err := time.ParseDuration(strings.TrimSpace(c.Timeout))
	if err != nil {
		timeout = 10 * time.Second
	}
	targetHost := c.hostFromURL()
	requestHost := strings.TrimSpace(c.Host)
	if requestHost == "" {
		requestHost = targetHost
	}

	targets := []netguard.Target{}
	if targetHost != "" {
		targets = append(targets, netguard.Target{IP: targetHost, Host: requestHost})
	}

	return netguard.CheckerConf{
		Name:      "default",
		Targets:   targets,
		Proxy:     strings.TrimSpace(c.Proxy),
		Threshold: c.FailureThreshold,
		Timeout:   timeout,
	}
}

func (c CheckerConfig) EnabledFromLegacy() bool {
	if c.Enabled {
		return true
	}
	return strings.TrimSpace(c.Name) != "" || len(c.Targets) > 0 || c.Threshold > 0 || strings.TrimSpace(c.PostUp) != "" || strings.TrimSpace(c.PostDown) != ""
}

func (c CheckerConfig) parsedURL() (*url.URL, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || parsedURL == nil || parsedURL.Scheme == "" {
		return nil, fmt.Errorf("checker.url must be a valid absolute URL")
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("checker.url scheme must be http or https")
	}
	return parsedURL, nil
}

func (c CheckerConfig) hostFromURL() string {
	parsedURL, err := c.parsedURL()
	if err != nil {
		return ""
	}
	return parsedURL.Host
}

func (c CheckerConfig) legacyURL() string {
	if len(c.Targets) == 0 {
		return ""
	}
	target := c.Targets[0]
	address := strings.TrimSpace(target.IP)
	if address == "" {
		address = strings.TrimSpace(target.Host)
	}
	if address == "" {
		return ""
	}
	return "http://" + address
}

func (c CheckerConfig) legacyHost() string {
	if len(c.Targets) == 0 {
		return ""
	}
	if host := strings.TrimSpace(c.Targets[0].Host); host != "" {
		return host
	}
	return strings.TrimSpace(c.Targets[0].IP)
}

func decodeYAMLKnown(raw []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	return decoder.Decode(out)
}

func isUserConfigPath(path string) bool {
	return normalizePath(path) == normalizePath(UserConfigPath)
}

func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
