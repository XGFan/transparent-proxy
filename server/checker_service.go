package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
)

type proxyStateReader interface {
	ProxyEnabledFromNft() (bool, error)
}

type CheckerService struct {
	runtime Runtime

	statusOverride func() int
	config         CheckerConfig
	proxyState     proxyStateReader

	rootCtx context.Context
	cancel  context.CancelFunc
	started bool
	runID   uint64
	running bool

	status              int
	consecutiveFailures int
	lastCheck           time.Time
	lastError           string

	mu sync.RWMutex
}

func NewCheckerService(config CheckerConfig, runtime Runtime, proxyState proxyStateReader) *CheckerService {
	config.ApplyDefaults()
	return &CheckerService{
		runtime:    runtime,
		config:     config,
		proxyState: proxyState,
	}
}

func NewCheckerServiceWithStatus(statusFn func() int) *CheckerService {
	return &CheckerService{statusOverride: statusFn}
}

func (service *CheckerService) Start(ctx context.Context) {
	if service == nil {
		return
	}

	service.mu.Lock()
	if service.started {
		service.mu.Unlock()
		return
	}
	config := service.config
	service.started = true
	service.rootCtx = ctx
	service.mu.Unlock()

	service.startRuntime(ctx, config)
}

func (service *CheckerService) Status() int {
	if service == nil {
		return 0
	}
	service.mu.RLock()
	statusOverride := service.statusOverride
	status := service.status
	service.mu.RUnlock()
	if statusOverride != nil {
		return statusOverride()
	}
	return status
}

func (service *CheckerService) ProxyEnabled() bool {
	if service == nil {
		return false
	}
	if service.proxyState == nil {
		return false
	}
	enabled, err := service.proxyState.ProxyEnabledFromNft()
	if err != nil {
		log.Printf("ProxyEnabled: read nft state fail: %v", err)
		return false
	}
	return enabled
}

func (service *CheckerService) ProxyStatus() string {
	if service == nil {
		return "unknown"
	}
	if service.proxyState == nil {
		return "unknown"
	}
	enabled, err := service.proxyState.ProxyEnabledFromNft()
	if err != nil {
		return "unknown"
	}
	if enabled {
		return "running"
	}
	return "stopped"
}

func (service *CheckerService) SetProxyEnabled(enabled bool) error {
	if service == nil {
		return errors.New("checker service is nil")
	}
	if service.proxyState == nil {
		return errors.New("proxy state reader is nil")
	}

	current, err := service.proxyState.ProxyEnabledFromNft()
	if err != nil {
		return fmt.Errorf("read current proxy state fail: %w", err)
	}

	if current == enabled {
		return nil
	}

	var actionErr error
	if enabled {
		actionErr = service.handleSuccessAction()
	} else {
		actionErr = service.handleFailureAction()
	}
	if actionErr != nil {
		service.mu.Lock()
		service.lastError = actionErr.Error()
		service.mu.Unlock()
		return actionErr
	}

	return nil
}

func (service *CheckerService) IsRunning() bool {
	if service == nil {
		return false
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.running
}

func (service *CheckerService) ConsecutiveFailures() int {
	if service == nil {
		return 0
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.consecutiveFailures
}

func (service *CheckerService) LastCheck() string {
	if service == nil {
		return ""
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.lastCheck.IsZero() {
		return ""
	}
	return service.lastCheck.Format(time.RFC3339)
}

func (service *CheckerService) LastError() string {
	if service == nil {
		return ""
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.lastError
}

func (service *CheckerService) UpdateConfig(config CheckerConfig) {
	if service == nil {
		return
	}

	config.ApplyDefaults()

	service.mu.Lock()
	oldCancel := service.cancel
	started := service.started
	rootCtx := service.rootCtx
	service.config = config
	service.cancel = nil
	service.running = false
	service.status = 0
	service.consecutiveFailures = 0
	service.lastCheck = time.Time{}
	service.lastError = ""
	service.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if started {
		service.startRuntime(rootCtx, config)
	}
}

func (service *CheckerService) startRuntime(ctx context.Context, config CheckerConfig) {
	if service == nil || ctx == nil || !checkerConfigEnabled(config) {
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	service.mu.Lock()
	service.runID++
	runID := service.runID
	service.cancel = cancel
	service.running = true
	service.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.loop(runCtx, config)
		service.mu.Lock()
		defer service.mu.Unlock()
		if service.runID == runID {
			service.running = false
			service.cancel = nil
		}
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Millisecond):
	}
}

func (service *CheckerService) loop(ctx context.Context, config CheckerConfig) {
	interval := parseCheckerDuration(config.CheckInterval, 30*time.Second)
	for {
		if ctx.Err() != nil {
			return
		}
		service.checkOnce(ctx, config)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (service *CheckerService) checkOnce(ctx context.Context, config CheckerConfig) {
	now := time.Now().UTC()
	success, err := runCheckerRequest(ctx, config)
	if ctx.Err() != nil {
		return
	}

	if success {
		service.mu.Lock()
		service.status = 1
		service.consecutiveFailures = 0
		service.lastCheck = now
		service.lastError = ""
		service.mu.Unlock()

		if service.proxyState != nil {
			currentEnabled, stateErr := service.proxyState.ProxyEnabledFromNft()
			if stateErr != nil {
				log.Printf("checkOnce: read nft state fail: %v", stateErr)
				return
			}
			if !currentEnabled {
				actionErr := service.handleSuccessAction()
				if actionErr != nil {
					log.Printf("checker success action failed: %v", actionErr)
					service.mu.Lock()
					service.lastError = actionErr.Error()
					service.mu.Unlock()
				}
			}
		}
		return
	}

	lastError := "checker failed"
	if err != nil {
		lastError = err.Error()
	}

	service.mu.Lock()
	service.status = -1
	service.consecutiveFailures++
	service.lastCheck = now
	service.lastError = lastError
	thresholdReached := service.consecutiveFailures >= checkerFailureThreshold(config)
	service.mu.Unlock()

	if !thresholdReached {
		return
	}

	if service.proxyState == nil {
		return
	}

	currentEnabled, stateErr := service.proxyState.ProxyEnabledFromNft()
	if stateErr != nil {
		log.Printf("checkOnce: read nft state fail: %v", stateErr)
		return
	}
	if !currentEnabled {
		return
	}

	actionErr := service.handleFailureAction()
	if actionErr != nil {
		log.Printf("checker failure action failed: %v", actionErr)
		service.mu.Lock()
		service.lastError = fmt.Sprintf("%s; %s", lastError, actionErr.Error())
		service.mu.Unlock()
	}
}

func (service *CheckerService) handleFailureAction() error {
	return service.runtime.disableProxy()
}

func (service *CheckerService) handleSuccessAction() error {
	return service.runtime.enableProxy()
}

func runCheckerRequest(ctx context.Context, config CheckerConfig) (bool, error) {
	timeout := parseCheckerDuration(config.Timeout, 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, checkerMethod(config.Method), strings.TrimSpace(config.URL), nil)
	if err != nil {
		return false, fmt.Errorf("create checker request fail: %w", err)
	}
	if host := strings.TrimSpace(config.Host); host != "" {
		req.Host = host
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, err
		}
		return false, fmt.Errorf("send checker request fail: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest {
		return true, nil
	}
	return false, fmt.Errorf("checker response status %d", resp.StatusCode)
}

func checkerMethod(method string) string {
	normalized := strings.ToUpper(strings.TrimSpace(method))
	if normalized == http.MethodGet {
		return http.MethodGet
	}
	return http.MethodHead
}

func checkerFailureThreshold(config CheckerConfig) int {
	if config.FailureThreshold < 1 {
		return 1
	}
	return config.FailureThreshold
}

func parseCheckerDuration(raw string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func checkerConfigEnabled(config CheckerConfig) bool {
	return config.Enabled && strings.TrimSpace(config.URL) != ""
}

func getCHNRoute() ([]string, error) {
	return getCHNRouteWithRuntime(appRuntime)
}

func getCHNRouteWithRuntime(runtime Runtime) ([]string, error) {
	all, err := runtime.fetch(chnRouteURL)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(all), "\n")
	ipRanges := make([]string, 0)
	for _, line := range lines {
		if line != "" && !strings.HasPrefix(line, "#") {
			parts := strings.Split(line, "|")
			if len(parts) >= 6 && parts[1] == "CN" && parts[2] == "ipv4" {
				float, err := strconv.ParseFloat(parts[4], 64)
				if err != nil {
					return nil, fmt.Errorf("parse line %s fail: %w", line, err)
				}
				mask := 32 - int8(math.Log2(float))
				ipRanges = append(ipRanges, fmt.Sprintf("%s/%d", parts[3], mask))
			}
		}
	}
	return ipRanges, nil
}

func refreshCHNRoute(runtime Runtime, statePath string, tmpl *template.Template) error {
	ips, err := getCHNRouteWithRuntime(runtime)
	if err != nil {
		return err
	}
	targetPath := path.Join(statePath, "chnroute.nft")
	if err := writeTemplateFile(runtime, targetPath, 0664, tmpl, NftSet{
		Name:     "chnroute",
		Elements: ips,
	}); err != nil {
		return fmt.Errorf("refresh chnroute fail: %w", err)
	}
	return nil
}

func join(sep string, s []string) string {
	return strings.Join(s, sep)
}

type NftSet struct {
	Name     string
	Attrs    []string
	Elements []string
}
