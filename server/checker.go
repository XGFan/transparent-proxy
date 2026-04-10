package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Checker performs periodic health checks and toggles proxy state.
type Checker struct {
	nft *NftManager

	config  CheckerConfig
	rootCtx context.Context
	cancel  context.CancelFunc
	started bool
	running bool

	status              int // 1=up, -1=down, 0=unknown
	consecutiveFailures int
	lastCheck           time.Time
	lastError           string

	proxyMu sync.Mutex   // serializes proxy state transitions
	mu      sync.RWMutex // protects status fields
}

func NewChecker(config CheckerConfig, nft *NftManager) *Checker {
	return &Checker{
		nft:    nft,
		config: config,
	}
}

// Start launches the health check loop in the background.
func (c *Checker) Start(ctx context.Context) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.rootCtx = ctx
	config := c.config
	c.mu.Unlock()

	c.startLoop(ctx, config)
}

// Status returns 1 (up), -1 (down), or 0 (unknown).
func (c *Checker) Status() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Checker) IsRunning() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

func (c *Checker) ConsecutiveFailures() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.consecutiveFailures
}

func (c *Checker) LastCheck() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.lastCheck.IsZero() {
		return ""
	}
	return c.lastCheck.Format(time.RFC3339)
}

func (c *Checker) LastError() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastError
}

// SetProxyEnabled manually toggles the proxy state.
func (c *Checker) SetProxyEnabled(enabled bool) error {
	if c == nil {
		return errors.New("checker is nil")
	}

	c.proxyMu.Lock()
	defer c.proxyMu.Unlock()

	current, err := c.nft.ProxyEnabled()
	if err != nil {
		return fmt.Errorf("read proxy state: %w", err)
	}
	if current == enabled {
		return nil
	}

	if enabled {
		return c.nft.EnableProxy()
	}
	return c.nft.DisableProxy()
}

// ProxyEnabled reads current proxy state from nft.
func (c *Checker) ProxyEnabled() (bool, error) {
	if c == nil || c.nft == nil {
		return false, nil
	}
	return c.nft.ProxyEnabled()
}

// ProxyStatus returns "running", "stopped", or "unknown".
func (c *Checker) ProxyStatus() string {
	enabled, err := c.ProxyEnabled()
	if err != nil {
		return "unknown"
	}
	if enabled {
		return "running"
	}
	return "stopped"
}

// UpdateConfig restarts the health check loop with new config.
func (c *Checker) UpdateConfig(config CheckerConfig) {
	if c == nil {
		return
	}

	c.mu.Lock()
	oldCancel := c.cancel
	started := c.started
	rootCtx := c.rootCtx
	c.config = config
	c.cancel = nil
	c.running = false
	c.status = 0
	c.consecutiveFailures = 0
	c.lastCheck = time.Time{}
	c.lastError = ""
	c.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if started {
		c.startLoop(rootCtx, config)
	}
}

func (c *Checker) startLoop(ctx context.Context, config CheckerConfig) {
	if ctx == nil || !config.Enabled || strings.TrimSpace(config.URL) == "" {
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.running = true
	c.mu.Unlock()

	timeout := parseDuration(config.Timeout, 10*time.Second)
	client := &http.Client{Timeout: timeout}

	go func() {
		defer func() {
			c.mu.Lock()
			c.running = false
			c.cancel = nil
			c.mu.Unlock()
		}()

		interval := parseDuration(config.Interval, 30*time.Second)
		for {
			if runCtx.Err() != nil {
				return
			}
			c.checkOnce(runCtx, config, client)
			select {
			case <-runCtx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}

func (c *Checker) checkOnce(ctx context.Context, config CheckerConfig, client *http.Client) {
	now := time.Now().UTC()
	success, err := doCheckerRequest(ctx, config, client)
	if ctx.Err() != nil {
		return
	}

	if success {
		c.mu.Lock()
		c.status = 1
		c.consecutiveFailures = 0
		c.lastCheck = now
		c.lastError = ""
		c.mu.Unlock()

		if actionErr := c.tryEnableProxy(); actionErr != nil {
			log.Printf("checker: enable proxy failed: %v", actionErr)
			c.mu.Lock()
			c.lastError = actionErr.Error()
			c.mu.Unlock()
		}
		return
	}

	lastError := "checker failed"
	if err != nil {
		lastError = err.Error()
	}

	c.mu.Lock()
	c.status = -1
	c.consecutiveFailures++
	c.lastCheck = now
	c.lastError = lastError
	threshold := config.FailureThreshold
	if threshold < 1 {
		threshold = 1
	}
	thresholdReached := c.consecutiveFailures >= threshold
	onFailure := config.OnFailure
	c.mu.Unlock()

	if !thresholdReached {
		return
	}

	// on_failure: "keep" means don't disable proxy even on failure
	if onFailure == "keep" {
		return
	}

	if actionErr := c.tryDisableProxy(); actionErr != nil {
		log.Printf("checker: disable proxy failed: %v", actionErr)
		c.mu.Lock()
		c.lastError = fmt.Sprintf("%s; %s", lastError, actionErr.Error())
		c.mu.Unlock()
	}
}

func (c *Checker) tryEnableProxy() error {
	c.proxyMu.Lock()
	defer c.proxyMu.Unlock()

	enabled, err := c.nft.ProxyEnabled()
	if err != nil {
		return err
	}
	if !enabled {
		return c.nft.EnableProxy()
	}
	return nil
}

func (c *Checker) tryDisableProxy() error {
	c.proxyMu.Lock()
	defer c.proxyMu.Unlock()

	enabled, err := c.nft.ProxyEnabled()
	if err != nil {
		return err
	}
	if enabled {
		return c.nft.DisableProxy()
	}
	return nil
}

func doCheckerRequest(ctx context.Context, config CheckerConfig, client *http.Client) (bool, error) {
	method := strings.ToUpper(strings.TrimSpace(config.Method))
	if method != http.MethodGet {
		method = http.MethodHead
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSpace(config.URL), nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	if host := strings.TrimSpace(config.Host); host != "" {
		req.Host = host
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, err
		}
		return false, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, nil
	}
	return false, fmt.Errorf("status %d", resp.StatusCode)
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
