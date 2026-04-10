package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func newTestChecker(t *testing.T, serverURL string) (*Checker, *MemoryNft) {
	t.Helper()
	exec := NewMemoryNft()
	files := NewMemoryFileStore()
	config := BuildDefaultConfig()
	nft := NewNftManager(exec, files, config.Proxy, filepath.Join(t.TempDir(), "nft"))

	checkerCfg := CheckerConfig{
		Enabled:          true,
		Method:           "HEAD",
		URL:              serverURL,
		Host:             "test",
		Timeout:          "2s",
		Interval:         "50ms",
		FailureThreshold: 2,
		OnFailure:        "disable",
	}
	checker := NewChecker(checkerCfg, nft)
	return checker, exec
}

func TestChecker_DetectsSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	checker, exec := newTestChecker(t, ts.URL)

	// Enable proxy so checker can verify it stays enabled
	exec.mu.Lock()
	exec.proxyEnabled = false
	exec.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// Wait for at least one check cycle
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for checker to report up")
		default:
		}
		if checker.Status() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if checker.ConsecutiveFailures() != 0 {
		t.Errorf("consecutiveFailures = %d, want 0", checker.ConsecutiveFailures())
	}
}

func TestChecker_DisablesProxyAfterThreshold(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	checker, exec := newTestChecker(t, ts.URL)

	// Enable proxy first
	exec.mu.Lock()
	exec.proxyEnabled = true
	exec.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// Wait for proxy to be disabled (threshold = 2, interval = 50ms)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for proxy to be disabled")
		default:
		}
		exec.mu.Lock()
		disabled := !exec.proxyEnabled
		exec.mu.Unlock()
		if disabled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if checker.Status() != -1 {
		t.Errorf("status = %d, want -1 (down)", checker.Status())
	}
}

func TestChecker_OnFailureKeep_DoesNotDisable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	exec := NewMemoryNft()
	files := NewMemoryFileStore()
	config := BuildDefaultConfig()
	nft := NewNftManager(exec, files, config.Proxy, filepath.Join(t.TempDir(), "nft"))

	checkerCfg := CheckerConfig{
		Enabled:          true,
		Method:           "HEAD",
		URL:              ts.URL,
		Host:             "test",
		Timeout:          "2s",
		Interval:         "50ms",
		FailureThreshold: 1,
		OnFailure:        "keep",
	}
	checker := NewChecker(checkerCfg, nft)

	// Enable proxy
	exec.mu.Lock()
	exec.proxyEnabled = true
	exec.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// Wait for failures to accumulate past threshold
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for failure detection")
		default:
		}
		if checker.ConsecutiveFailures() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Proxy should still be enabled despite failures
	exec.mu.Lock()
	stillEnabled := exec.proxyEnabled
	exec.mu.Unlock()
	if !stillEnabled {
		t.Error("proxy should remain enabled with on_failure=keep")
	}
}

func TestChecker_ReenablesOnRecovery(t *testing.T) {
	failing := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	checker, exec := newTestChecker(t, ts.URL)

	// Start with proxy disabled (simulating previous failure)
	exec.mu.Lock()
	exec.proxyEnabled = false
	exec.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// Wait for failure detection
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for failure detection")
		default:
		}
		if checker.Status() == -1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Switch to success
	failing = false

	// Wait for recovery (proxy re-enabled)
	deadline = time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for proxy to be re-enabled")
		default:
		}
		exec.mu.Lock()
		enabled := exec.proxyEnabled
		exec.mu.Unlock()
		if enabled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if checker.Status() != 1 {
		t.Errorf("status = %d, want 1 (up) after recovery", checker.Status())
	}
}

func TestChecker_SetProxyEnabled(t *testing.T) {
	exec := NewMemoryNft()
	files := NewMemoryFileStore()
	config := BuildDefaultConfig()
	nft := NewNftManager(exec, files, config.Proxy, filepath.Join(t.TempDir(), "nft"))
	checker := NewChecker(config.Checker, nft)

	// Initially disabled
	enabled, err := checker.ProxyEnabled()
	if err != nil {
		t.Fatalf("ProxyEnabled: %v", err)
	}
	if enabled {
		t.Error("proxy should be disabled initially")
	}

	// Enable
	if err := checker.SetProxyEnabled(true); err != nil {
		t.Fatalf("SetProxyEnabled(true): %v", err)
	}
	enabled, err = checker.ProxyEnabled()
	if err != nil {
		t.Fatalf("ProxyEnabled after enable: %v", err)
	}
	if !enabled {
		t.Error("proxy should be enabled")
	}

	// Disable
	if err := checker.SetProxyEnabled(false); err != nil {
		t.Fatalf("SetProxyEnabled(false): %v", err)
	}
	enabled, err = checker.ProxyEnabled()
	if err != nil {
		t.Fatalf("ProxyEnabled after disable: %v", err)
	}
	if enabled {
		t.Error("proxy should be disabled")
	}

	// Idempotent: disable again
	if err := checker.SetProxyEnabled(false); err != nil {
		t.Fatalf("SetProxyEnabled(false) idempotent: %v", err)
	}
}

func TestChecker_UpdateConfig_RestartsLoop(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	checker, _ := newTestChecker(t, ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// Wait for first check
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first check")
		default:
		}
		if checker.Status() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Update config (disable checker)
	newCfg := CheckerConfig{
		Enabled:          false,
		Method:           "HEAD",
		URL:              ts.URL,
		Host:             "test",
		Timeout:          "2s",
		Interval:         "50ms",
		FailureThreshold: 2,
		OnFailure:        "disable",
	}
	checker.UpdateConfig(newCfg)

	// After update with enabled=false, the loop should stop
	time.Sleep(200 * time.Millisecond)
	if checker.IsRunning() {
		t.Error("checker should not be running after disable")
	}
	if checker.Status() != 0 {
		t.Errorf("status = %d, want 0 (unknown) after config reset", checker.Status())
	}
}

func TestChecker_ProxyStatus(t *testing.T) {
	exec := NewMemoryNft()
	files := NewMemoryFileStore()
	config := BuildDefaultConfig()
	nft := NewNftManager(exec, files, config.Proxy, filepath.Join(t.TempDir(), "nft"))
	checker := NewChecker(config.Checker, nft)

	status := checker.ProxyStatus()
	if status != "stopped" {
		t.Errorf("ProxyStatus = %q, want stopped", status)
	}

	exec.mu.Lock()
	exec.proxyEnabled = true
	exec.mu.Unlock()

	status = checker.ProxyStatus()
	if status != "running" {
		t.Errorf("ProxyStatus = %q, want running", status)
	}
}

func TestChecker_NilSafety(t *testing.T) {
	var checker *Checker

	if checker.Status() != 0 {
		t.Error("nil checker Status should return 0")
	}
	if checker.IsRunning() {
		t.Error("nil checker IsRunning should return false")
	}
	if checker.ConsecutiveFailures() != 0 {
		t.Error("nil checker ConsecutiveFailures should return 0")
	}
	if checker.LastCheck() != "" {
		t.Error("nil checker LastCheck should return empty")
	}
	if checker.LastError() != "" {
		t.Error("nil checker LastError should return empty")
	}
	// Start should not panic
	checker.Start(context.Background())
	checker.UpdateConfig(CheckerConfig{})
}
