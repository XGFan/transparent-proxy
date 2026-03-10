package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type checkerTestCommandRunner struct {
	mu         sync.Mutex
	commands   []string
	failures   map[string]error
	proxyState bool
}

type mockProxyStateReader struct {
	mu    sync.Mutex
	state bool
	err   error
}

func (m *mockProxyStateReader) ProxyEnabledFromNft() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, m.err
}

func (m *mockProxyStateReader) setState(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = enabled
}

func (r *checkerTestCommandRunner) setProxyState(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.proxyState = enabled
}

type checkerTestFiles struct {
	mu       sync.Mutex
	contents map[string][]byte
	removed  []string
	writes   map[string][]byte
	perms    map[string]os.FileMode
}

func newCheckerTestFiles(contents map[string][]byte) *checkerTestFiles {
	cloned := make(map[string][]byte, len(contents))
	for path, data := range contents {
		cloned[path] = append([]byte(nil), data...)
	}
	return &checkerTestFiles{
		contents: cloned,
		writes:   map[string][]byte{},
		perms:    map[string]os.FileMode{},
	}
}

func (f *checkerTestFiles) WriteFile(name string, data []byte, perm os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[name] = append([]byte(nil), data...)
	f.perms[name] = perm
	return nil
}

func (f *checkerTestFiles) ReadFile(name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.contents[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (f *checkerTestFiles) Remove(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, name)
	return nil
}

func (f *checkerTestFiles) removedCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, removedPath := range f.removed {
		if removedPath == path {
			count++
		}
	}
	return count
}

func (f *checkerTestFiles) write(path string) ([]byte, os.FileMode, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content, ok := f.writes[path]
	if !ok {
		return nil, 0, false
	}
	return append([]byte(nil), content...), f.perms[path], true
}

func (r *checkerTestCommandRunner) Run(name string, args ...string) ([]byte, error) {
	command := commandLine(name, args...)
	r.mu.Lock()
	r.commands = append(r.commands, command)
	err := r.failures[command]
	proxyEnabled := r.proxyState
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}

	if name == "nft" && len(args) >= 5 && args[0] == "list" && args[1] == "chain" && args[2] == "inet" && args[3] == "fw4" {
		switch args[4] {
		case "mangle_prerouting":
			if proxyEnabled {
				return []byte("chain mangle_prerouting {\n\tiifname \"br-lan\" jump transparent_proxy\n}\n"), nil
			}
			return []byte("chain mangle_prerouting {\n}\n"), nil
		case "mangle_output":
			if proxyEnabled {
				return []byte("chain mangle_output {\n\tjump transparent_proxy_mask\n}\n"), nil
			}
			return []byte("chain mangle_output {\n}\n"), nil
		}
	}

	if name == "nft" && len(args) >= 5 && args[0] == "flush" && args[1] == "chain" {
		r.setProxyState(false)
	}

	if name == "nft" && len(args) >= 2 && args[0] == "-f" && args[1] == transparentNftFullPath {
		r.setProxyState(true)
	}

	return []byte("ok\n"), nil
}

func (r *checkerTestCommandRunner) ProxyEnabledFromNft() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proxyState, nil
}

func (r *checkerTestCommandRunner) count(target string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, command := range r.commands {
		if command == target {
			count++
		}
	}
	return count
}

func TestCheckerServiceFailureRecoveryWithoutActionSpam(t *testing.T) {
	var healthy atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer target.Close()

	runner := &checkerTestCommandRunner{proxyState: true}
	files := newCheckerTestFiles(map[string][]byte{
		transparentNftPartialPath: []byte("table inet fw4 {}\n"),
	})
	service := NewCheckerService(CheckerConfig{
		Enabled:          true,
		Method:           "GET",
		URL:              target.URL,
		Host:             "status.example.com",
		Timeout:          "120ms",
		FailureThreshold: 2,
		CheckInterval:    "20ms",
	}, Runtime{Runner: runner, Files: files}, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)

	waitForCheckerCondition(t, 2*time.Second, func() bool {
		return !service.ProxyEnabled() && service.Status() == -1
	}, "checker disables proxy after threshold failures")

	if service.ConsecutiveFailures() < 2 {
		t.Fatalf("consecutiveFailures = %d, want >= 2", service.ConsecutiveFailures())
	}
	if service.LastCheck() == "" {
		t.Fatal("lastCheck is empty, want recent check timestamp")
	}
	if service.LastError() == "" {
		t.Fatal("lastError is empty, want failure reason")
	}

	healthy.Store(true)
	waitForCheckerCondition(t, 2*time.Second, func() bool {
		return service.ProxyEnabled() && service.Status() == 1
	}, "checker recovery re-enables proxy")
	if service.ConsecutiveFailures() != 0 {
		t.Fatalf("consecutiveFailures after recovery = %d, want 0", service.ConsecutiveFailures())
	}
	if service.LastError() != "" {
		t.Fatalf("lastError after recovery = %q, want empty", service.LastError())
	}
	if got := runner.count("nft flush chain inet fw4 mangle_prerouting"); got != 2 {
		t.Fatalf("flush prerouting call count = %d, want 2", got)
	}
	if got := runner.count("nft flush chain inet fw4 mangle_output"); got != 2 {
		t.Fatalf("flush output call count = %d, want 2", got)
	}
	if got := runner.count("nft -f /etc/transparent-proxy/transparent_full.nft"); got != 1 {
		t.Fatalf("load full nft call count = %d, want 1", got)
	}
	if got := files.removedCount(transparentNftTargetPath); got != 2 {
		t.Fatalf("remove target call count = %d, want 2", got)
	}
	written, perm, ok := files.write(transparentNftTargetPath)
	if !ok {
		t.Fatalf("target %s not written", transparentNftTargetPath)
	}
	if string(written) != "table inet fw4 {}\n" {
		t.Fatalf("target content = %q, want %q", string(written), "table inet fw4 {}\n")
	}
	if perm != 0644 {
		t.Fatalf("target mode = %v, want %v", perm, os.FileMode(0644))
	}
}

func TestCheckerServiceActionsDontRunScripts(t *testing.T) {
	runner := &checkerTestCommandRunner{}
	files := newCheckerTestFiles(map[string][]byte{
		transparentNftPartialPath: []byte("rules\n"),
	})
	service := NewCheckerService(CheckerConfig{}, Runtime{Runner: runner, Files: files}, nil)

	if err := service.handleFailureAction(); err != nil {
		t.Fatalf("handleFailureAction() error = %v", err)
	}
	if err := service.handleSuccessAction(); err != nil {
		t.Fatalf("handleSuccessAction() error = %v", err)
	}

	for _, cmd := range runner.commands {
		if !strings.HasPrefix(cmd, "nft ") {
			t.Fatalf("unexpected non-nft command executed: %q", cmd)
		}
	}
}

func TestCheckerServiceRecoveryActionPartialSuccessReportsLiveNftState(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	runner := &checkerTestCommandRunner{proxyState: false}
	service := NewCheckerService(CheckerConfig{
		Enabled:          true,
		Method:           "GET",
		URL:              target.URL,
		Timeout:          "120ms",
		FailureThreshold: 1,
		CheckInterval:    "20ms",
	}, Runtime{Runner: runner, Files: newCheckerTestFiles(nil)}, runner)

	service.checkOnce(context.Background(), service.config)

	// After partial success (nft rules loaded but file copy failed), ProxyEnabled reports live nft state
	if !service.ProxyEnabled() {
		t.Fatal("proxyEnabled = false, want true (nft rules were loaded)")
	}
	if service.Status() != 1 {
		t.Fatalf("status = %d, want 1", service.Status())
	}
	if service.LastError() == "" {
		t.Fatal("lastError is empty, want recovery action failure")
	}
	if !strings.Contains(service.LastError(), "read file /etc/transparent-proxy/transparent.nft fail") {
		t.Fatalf("lastError = %q, want read file failure", service.LastError())
	}
}

func TestCheckerServiceFailureRecoveryInDevModeUsesFallbackAssets(t *testing.T) {
	var healthy atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer target.Close()

	overlayRoot := t.TempDir()
	fallbackRoot := t.TempDir()
	partialAssetPath := filepath.Join(fallbackRoot, "etc", "transparent-proxy", "transparent.nft")
	if err := os.MkdirAll(filepath.Dir(partialAssetPath), 0755); err != nil {
		t.Fatalf("mkdir fallback asset directory error = %v", err)
	}
	if err := os.WriteFile(partialAssetPath, []byte("table inet fw4 { chain postrouting {} }\n"), 0644); err != nil {
		t.Fatalf("write fallback asset error = %v", err)
	}

	devFiles := DevFileWriter{root: overlayRoot, fallbackRoot: fallbackRoot}
	runner := &checkerTestCommandRunner{proxyState: true}
	service := NewCheckerService(CheckerConfig{
		Enabled:          true,
		Method:           "GET",
		URL:              target.URL,
		Timeout:          "120ms",
		FailureThreshold: 1,
		CheckInterval:    "20ms",
	}, Runtime{Runner: runner, Files: devFiles}, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)

	waitForCheckerCondition(t, 2*time.Second, func() bool {
		return !service.ProxyEnabled() && service.Status() == -1
	}, "checker disables proxy in dev mode")

	if _, err := os.Stat(devFiles.resolvePath(transparentNftPartialPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlay partial asset stat error = %v, want not exists", err)
	}

	healthy.Store(true)
	waitForCheckerCondition(t, 2*time.Second, func() bool {
		return service.ProxyEnabled() && service.Status() == 1
	}, "checker recovers and re-enables proxy in dev mode")

	if service.LastError() != "" {
		t.Fatalf("lastError after recovery = %q, want empty", service.LastError())
	}

	targetPath := devFiles.resolvePath(transparentNftTargetPath)
	written, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read generated target %s error = %v", targetPath, err)
	}
	if string(written) != "table inet fw4 { chain postrouting {} }\n" {
		t.Fatalf("generated target content = %q, want fallback partial nft content", string(written))
	}
}

func TestCheckerServiceFailureActionFailureKeepsProxyRunning(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer target.Close()

	runner := &checkerTestCommandRunner{
		proxyState: true,
		failures: map[string]error{
			"nft flush chain inet fw4 mangle_prerouting": errors.New("permission denied"),
		},
	}
	service := NewCheckerService(CheckerConfig{
		Enabled:          true,
		Method:           "GET",
		URL:              target.URL,
		Timeout:          "120ms",
		FailureThreshold: 1,
		CheckInterval:    "20ms",
	}, Runtime{Runner: runner, Files: newCheckerTestFiles(nil)}, runner)

	service.checkOnce(context.Background(), service.config)

	if !service.ProxyEnabled() {
		t.Fatal("proxyEnabled = false, want true when failure action fails")
	}
	if service.Status() != -1 {
		t.Fatalf("status = %d, want -1", service.Status())
	}
	if service.LastError() == "" {
		t.Fatal("lastError is empty, want failure action error")
	}
	if !strings.Contains(service.LastError(), "checker response status") {
		t.Fatalf("lastError = %q, want checker response failure", service.LastError())
	}
	if !strings.Contains(service.LastError(), "flush chain inet fw4 mangle_prerouting fail") {
		t.Fatalf("lastError = %q, want flush failure detail", service.LastError())
	}
}

func TestRunCheckerRequestSupportsMethodAndHostOverride(t *testing.T) {
	type requestMeta struct {
		method string
		host   string
	}
	received := make(chan requestMeta, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		received <- requestMeta{method: req.Method, host: req.Host}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	success, err := runCheckerRequest(context.Background(), CheckerConfig{
		Method:  "HEAD",
		URL:     target.URL,
		Host:    "override.example.com",
		Timeout: "1s",
	})
	if err != nil {
		t.Fatalf("runCheckerRequest() error = %v", err)
	}
	if !success {
		t.Fatal("runCheckerRequest() success = false, want true")
	}
	first := <-received
	if first.method != http.MethodHead {
		t.Fatalf("request method = %q, want %q", first.method, http.MethodHead)
	}
	if first.host != "override.example.com" {
		t.Fatalf("request host = %q, want %q", first.host, "override.example.com")
	}

	success, err = runCheckerRequest(context.Background(), CheckerConfig{
		Method:  "GET",
		URL:     target.URL,
		Timeout: "1s",
	})
	if err != nil {
		t.Fatalf("runCheckerRequest(GET) error = %v", err)
	}
	if !success {
		t.Fatal("runCheckerRequest(GET) success = false, want true")
	}
	second := <-received
	if second.method != http.MethodGet {
		t.Fatalf("GET request method = %q, want %q", second.method, http.MethodGet)
	}
}

func waitForCheckerCondition(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
