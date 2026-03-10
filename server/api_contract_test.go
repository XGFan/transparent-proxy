package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type contractRunner struct {
	output []byte
	err    error
}

func (r *contractRunner) Run(name string, args ...string) ([]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	if name == "nft" && len(args) >= 5 && args[0] == "list" && args[1] == "chain" && args[2] == "inet" && args[3] == "fw4" {
		switch args[4] {
		case "mangle_prerouting":
			return []byte("chain mangle_prerouting {\n\tiifname \"br-lan\" jump transparent_proxy\n}\n"), nil
		case "mangle_output":
			return []byte("chain mangle_output {\n\tjump transparent_proxy_mask\n}\n"), nil
		}
	}
	return r.output, nil
}

func (r *contractRunner) ProxyEnabledFromNft() (bool, error) {
	return true, nil
}

type contractActionRunner struct {
	mu       sync.Mutex
	commands []string
	proxyOn  bool
}

func (r *contractActionRunner) Run(name string, args ...string) ([]byte, error) {
	command := commandLine(name, args...)
	proxyOn := false
	r.mu.Lock()
	r.commands = append(r.commands, command)
	if name == "nft" {
		if len(args) >= 5 && args[0] == "flush" && args[1] == "chain" && args[2] == "inet" && args[3] == "fw4" {
			r.proxyOn = false
		}
		if len(args) >= 2 && args[0] == "-f" && args[1] == transparentNftFullPath {
			r.proxyOn = true
		}
	}
	proxyOn = r.proxyOn
	r.mu.Unlock()

	if name == "nft" {
		if len(args) >= 5 && args[0] == "list" && args[1] == "chain" && args[2] == "inet" && args[3] == "fw4" {
			switch args[4] {
			case "mangle_prerouting":
				if proxyOn {
					return []byte("chain mangle_prerouting {\n\tiifname \"br-lan\" jump transparent_proxy\n}\n"), nil
				}
				return []byte("chain mangle_prerouting {\n}\n"), nil
			case "mangle_output":
				if proxyOn {
					return []byte("chain mangle_output {\n\tjump transparent_proxy_mask\n}\n"), nil
				}
				return []byte("chain mangle_output {\n}\n"), nil
			}
		}
		return []byte(`{
			"nftables": [
				{"set": {"type": "ipv4_addr", "elem": ["1.1.1.1"]}}
			]
		}`), nil
	}
	return []byte("ok\n"), nil
}

func (r *contractActionRunner) ProxyEnabledFromNft() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proxyOn, nil
}

type contractProxyRunner struct {
	mu       sync.Mutex
	enabled  bool
	commands []string
}

func (r *contractProxyRunner) Run(name string, args ...string) ([]byte, error) {
	command := commandLine(name, args...)

	r.mu.Lock()
	r.commands = append(r.commands, command)
	if name == "nft" {
		if len(args) >= 2 && args[0] == "-f" && args[1] == transparentNftFullPath {
			r.enabled = true
		}
		if len(args) >= 5 && args[0] == "flush" && args[1] == "chain" && args[2] == "inet" && args[3] == "fw4" {
			r.enabled = false
		}
	}
	enabled := r.enabled
	r.mu.Unlock()

	if name != "nft" {
		return []byte("ok\n"), nil
	}

	if len(args) >= 5 && args[0] == "list" && args[1] == "chain" && args[2] == "inet" && args[3] == "fw4" {
		switch args[4] {
		case "mangle_prerouting":
			if enabled {
				return []byte("chain mangle_prerouting {\n\tiifname \"br-lan\" jump transparent_proxy\n}\n"), nil
			}
			return []byte("chain mangle_prerouting {\n}\n"), nil
		case "mangle_output":
			if enabled {
				return []byte("chain mangle_output {\n\tjump transparent_proxy_mask\n}\n"), nil
			}
			return []byte("chain mangle_output {\n}\n"), nil
		}
	}

	if len(args) >= 6 && args[0] == "-j" && args[1] == "list" && args[2] == "set" {
		return []byte(`{
			"nftables": [
				{"set": {"type": "ipv4_addr", "elem": ["1.1.1.1"]}}
			]
		}`), nil
	}

	return []byte("ok\n"), nil
}

func (r *contractProxyRunner) ProxyEnabledFromNft() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled, nil
}

func (r *contractActionRunner) commandCount(target string) int {
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

type contractEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func TestStatusContract(t *testing.T) {
	router := newContractRouter(Runtime{Runner: &contractRunner{output: []byte(`{
		"nftables": [
			{"set": {"type": "ipv4_addr", "elem": ["1.1.1.1"]}}
		]
	}`)}})

	req := httptest.NewRequest(http.MethodGet, "/api/status?ip=8.8.8.8", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}

	env := decodeContractEnvelope(t, recorder)
	if env.Code != APICodeOK {
		t.Fatalf("response code = %q, want %q", env.Code, APICodeOK)
	}
	if env.Message != "ok" {
		t.Fatalf("response message = %q, want %q", env.Message, "ok")
	}

	var data struct {
		Proxy struct {
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
		} `json:"proxy"`
		Checker struct {
			Enabled bool   `json:"enabled"`
			Running bool   `json:"running"`
			Status  string `json:"status"`
		} `json:"checker"`
		Rules struct {
			Sets  []string `json:"sets"`
			Rules []struct {
				Name  string   `json:"name"`
				Type  string   `json:"type"`
				Elems []string `json:"elems"`
			} `json:"rules"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data error = %v", err)
	}

	if !data.Proxy.Enabled {
		t.Fatalf("proxy.enabled = %v, want true", data.Proxy.Enabled)
	}
	if data.Proxy.Status != "running" {
		t.Fatalf("proxy.status = %q, want %q", data.Proxy.Status, "running")
	}
	if len(data.Rules.Sets) != 1 {
		t.Fatalf("rules.sets len = %d, want 1", len(data.Rules.Sets))
	}
	if data.Rules.Sets[0] != "proxy_src" {
		t.Fatalf("rules.sets[0] = %q, want %q", data.Rules.Sets[0], "proxy_src")
	}
	if len(data.Rules.Rules) != 1 {
		t.Fatalf("rules.rules len = %d, want 1", len(data.Rules.Rules))
	}
	if data.Rules.Rules[0].Name != "proxy_src" {
		t.Fatalf("rules.rules[0].name = %q, want %q", data.Rules.Rules[0].Name, "proxy_src")
	}
	if data.Rules.Rules[0].Type != "ipv4_addr" {
		t.Fatalf("rules.rules[0].type = %q, want %q", data.Rules.Rules[0].Type, "ipv4_addr")
	}
	if !reflect.DeepEqual(data.Rules.Rules[0].Elems, []string{"1.1.1.1"}) {
		t.Fatalf("rules.rules[0].elems = %#v, want %#v", data.Rules.Rules[0].Elems, []string{"1.1.1.1"})
	}
}

func TestRulesContract(t *testing.T) {
	router := newContractRouter(Runtime{})

	req := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}

	env := decodeContractEnvelope(t, recorder)
	if env.Code != APICodeOK {
		t.Fatalf("response code = %q, want %q", env.Code, APICodeOK)
	}

	var data struct {
		Sets []string `json:"sets"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data error = %v", err)
	}
	if !reflect.DeepEqual(data.Sets, []string{"proxy_src"}) {
		t.Fatalf("sets = %#v, want %#v", data.Sets, []string{"proxy_src"})
	}
}

func TestProxyToggleContractDependsOnNftState(t *testing.T) {
	runner := &contractProxyRunner{enabled: true}
	files := newCheckerTestFiles(map[string][]byte{
		transparentNftPartialPath: []byte("chain mangle_output { jump transparent_proxy_mask }\n"),
	})
	app := NewApp(&AppConfig{Nft: NftConfig{StatePath: "/tmp", Sets: []string{"proxy_src"}}}, Runtime{Runner: runner, Files: files})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAPIRoutes(router.Group("/api"), apiServer{app: app})

	disableRecorder := performJSONRequest(router, http.MethodPut, "/api/proxy", `{"enabled":false}`)
	if disableRecorder.Code != http.StatusOK {
		t.Fatalf("PUT /api/proxy disable status code = %d, want %d, body = %s", disableRecorder.Code, http.StatusOK, disableRecorder.Body.String())
	}
	disableEnv := decodeContractEnvelope(t, disableRecorder)
	var disabledData struct {
		Enabled bool   `json:"enabled"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(disableEnv.Data, &disabledData); err != nil {
		t.Fatalf("unmarshal disable response error = %v", err)
	}
	if disabledData.Enabled {
		t.Fatal("PUT /api/proxy disable enabled = true, want false")
	}
	if disabledData.Status != "stopped" {
		t.Fatalf("PUT /api/proxy disable status = %q, want %q", disabledData.Status, "stopped")
	}

	statusRecorder := performJSONRequest(router, http.MethodGet, "/api/status", "")
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("GET /api/status after disable status code = %d, want %d", statusRecorder.Code, http.StatusOK)
	}
	statusEnv := decodeContractEnvelope(t, statusRecorder)
	var statusData struct {
		Proxy struct {
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
		} `json:"proxy"`
	}
	if err := json.Unmarshal(statusEnv.Data, &statusData); err != nil {
		t.Fatalf("unmarshal status data error = %v", err)
	}
	if statusData.Proxy.Enabled {
		t.Fatal("GET /api/status proxy.enabled = true, want false")
	}
	if statusData.Proxy.Status != "stopped" {
		t.Fatalf("GET /api/status proxy.status = %q, want %q", statusData.Proxy.Status, "stopped")
	}

	enableRecorder := performJSONRequest(router, http.MethodPut, "/api/proxy", `{"enabled":true}`)
	if enableRecorder.Code != http.StatusOK {
		t.Fatalf("PUT /api/proxy enable status code = %d, want %d, body = %s", enableRecorder.Code, http.StatusOK, enableRecorder.Body.String())
	}
	enableEnv := decodeContractEnvelope(t, enableRecorder)
	var enabledData struct {
		Enabled bool   `json:"enabled"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(enableEnv.Data, &enabledData); err != nil {
		t.Fatalf("unmarshal enable response error = %v", err)
	}
	if !enabledData.Enabled {
		t.Fatal("PUT /api/proxy enable enabled = false, want true")
	}
	if enabledData.Status != "running" {
		t.Fatalf("PUT /api/proxy enable status = %q, want %q", enabledData.Status, "running")
	}
}

func TestAddRuleRejectsInvalidPayload(t *testing.T) {
	router := newContractRouter(Runtime{})

	req := httptest.NewRequest(http.MethodPost, "/api/rules/add", bytes.NewBufferString(`{"set":"proxy_src"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	env := decodeContractEnvelope(t, recorder)
	if env.Code != APICodeInvalidRequest {
		t.Fatalf("response code = %q, want %q", env.Code, APICodeInvalidRequest)
	}

	var data map[string]string
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data error = %v", err)
	}
	if !strings.Contains(data["error"], "required") {
		t.Fatalf("error message = %q, want contains %q", data["error"], "required")
	}
}

func TestCheckerUpdatePersistsConfigAndUpdatesRuntimeView(t *testing.T) {
	var healthy atomic.Bool
	checkerTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer checkerTarget.Close()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	initialConfig := []byte("version: v3\n" +
		"checker:\n" +
		"  enabled: true\n" +
		"  method: GET\n" +
		"  url: " + checkerTarget.URL + "\n" +
		"  host: status.example.com\n" +
		"  timeout: 150ms\n" +
		"  failureThreshold: 2\n" +
		"  checkInterval: 20ms\n" +
		"nft:\n" +
		"  sets:\n" +
		"    - proxy_src\n")
	if err := os.WriteFile(configPath, initialConfig, 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", configPath, err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(%s) error = %v", configPath, err)
	}

	runner := &contractActionRunner{proxyOn: true}
	files := newCheckerTestFiles(map[string][]byte{
		transparentNftPartialPath: []byte("table inet fw4 {}\n"),
	})
	app := NewApp(config, Runtime{Runner: runner, Files: files})
	app.SetConfigPath(configPath)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.checkerService.Start(runCtx)

	waitForCondition(t, 2*time.Second, func() bool {
		return !app.checkerService.ProxyEnabled()
	}, "checker disables proxy after startup failures")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAPIRoutes(router.Group("/api"), apiServer{app: app})

	checkerRecorder := performJSONRequest(router, http.MethodGet, "/api/checker", "")
	if checkerRecorder.Code != http.StatusOK {
		t.Fatalf("GET /api/checker status code = %d, want %d", checkerRecorder.Code, http.StatusOK)
	}
	checkerEnv := decodeContractEnvelope(t, checkerRecorder)
	var checkerData CheckerStatusResponse
	if err := json.Unmarshal(checkerEnv.Data, &checkerData); err != nil {
		t.Fatalf("unmarshal checker data error = %v", err)
	}
	if !checkerData.Running {
		t.Fatal("GET /api/checker checker.running = false, want true")
	}
	if checkerData.Status != "down" {
		t.Fatalf("GET /api/checker checker.status = %q, want %q", checkerData.Status, "down")
	}
	if checkerData.ConsecutiveFailures < 2 {
		t.Fatalf("GET /api/checker checker.consecutiveFailures = %d, want >= 2", checkerData.ConsecutiveFailures)
	}
	if checkerData.LastCheck == "" {
		t.Fatal("GET /api/checker checker.lastCheck is empty")
	}
	if checkerData.LastError == "" {
		t.Fatal("GET /api/checker checker.lastError is empty")
	}

	statusRecorder := performJSONRequest(router, http.MethodGet, "/api/status", "")
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("GET /api/status status code = %d, want %d", statusRecorder.Code, http.StatusOK)
	}
	statusEnv := decodeContractEnvelope(t, statusRecorder)
	var statusData struct {
		Proxy struct {
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
		} `json:"proxy"`
		Checker struct {
			Status string `json:"status"`
		} `json:"checker"`
	}
	if err := json.Unmarshal(statusEnv.Data, &statusData); err != nil {
		t.Fatalf("unmarshal status data error = %v", err)
	}
	if statusData.Proxy.Enabled {
		t.Fatal("GET /api/status proxy.enabled = true, want false")
	}
	if statusData.Proxy.Status != "stopped" {
		t.Fatalf("GET /api/status proxy.status = %q, want %q", statusData.Proxy.Status, "stopped")
	}
	if statusData.Checker.Status != "down" {
		t.Fatalf("GET /api/status checker.status = %q, want %q", statusData.Checker.Status, "down")
	}

	healthy.Store(true)
	waitForCondition(t, 2*time.Second, func() bool {
		return app.checkerService.ProxyEnabled()
	}, "checker recovery re-enables proxy")

	recoveredStatusRecorder := performJSONRequest(router, http.MethodGet, "/api/status", "")
	if recoveredStatusRecorder.Code != http.StatusOK {
		t.Fatalf("recovered GET /api/status status code = %d, want %d", recoveredStatusRecorder.Code, http.StatusOK)
	}
	recoveredStatusEnv := decodeContractEnvelope(t, recoveredStatusRecorder)
	var recoveredStatusData struct {
		Proxy struct {
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
		} `json:"proxy"`
		Checker struct {
			Status string `json:"status"`
		} `json:"checker"`
	}
	if err := json.Unmarshal(recoveredStatusEnv.Data, &recoveredStatusData); err != nil {
		t.Fatalf("unmarshal recovered status data error = %v", err)
	}
	if !recoveredStatusData.Proxy.Enabled {
		t.Fatal("recovered GET /api/status proxy.enabled = false, want true")
	}
	if recoveredStatusData.Proxy.Status != "running" {
		t.Fatalf("recovered GET /api/status proxy.status = %q, want %q", recoveredStatusData.Proxy.Status, "running")
	}
	if recoveredStatusData.Checker.Status != "up" {
		t.Fatalf("recovered GET /api/status checker.status = %q, want %q", recoveredStatusData.Checker.Status, "up")
	}

	updatePayload := `{"enabled":true,"method":"GET","url":"` + checkerTarget.URL + `/healthz","host":"status.example.com","timeout":"3s","failureThreshold":5,"checkInterval":"45s"}`
	updateRecorder := performJSONRequest(router, http.MethodPut, "/api/checker", updatePayload)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("PUT /api/checker status code = %d, want %d, body = %s", updateRecorder.Code, http.StatusOK, updateRecorder.Body.String())
	}
	updateEnv := decodeContractEnvelope(t, updateRecorder)
	var updateData CheckerStatusResponse
	if err := json.Unmarshal(updateEnv.Data, &updateData); err != nil {
		t.Fatalf("unmarshal update data error = %v", err)
	}
	if !updateData.Enabled {
		t.Fatal("update response checker.enabled = false, want true")
	}
	if !updateData.Running {
		t.Fatal("update response checker.running = false, want true")
	}
	if updateData.Host != "status.example.com" {
		t.Fatalf("update response checker.host = %q, want %q", updateData.Host, "status.example.com")
	}

	persistedConfig, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(%s) after PUT error = %v", configPath, err)
	}
	if !persistedConfig.Checker.Enabled {
		t.Fatal("persisted checker.enabled = false, want true")
	}
	if persistedConfig.Checker.URL != checkerTarget.URL+"/healthz" {
		t.Fatalf("persisted checker.url = %q, want %q", persistedConfig.Checker.URL, checkerTarget.URL+"/healthz")
	}
	if persistedConfig.Checker.CheckInterval != "45s" {
		t.Fatalf("persisted checker.checkInterval = %q, want %q", persistedConfig.Checker.CheckInterval, "45s")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", configPath, err)
	}
	if !strings.Contains(string(raw), "failureThreshold: 5") {
		t.Fatalf("persisted config missing updated checker settings: %q", string(raw))
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool, description string) {
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

func newContractRouter(runtime Runtime) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerAPIRoutes(r.Group("/api"), apiServer{
		runtime: runtime,
		config: &AppConfig{Nft: NftConfig{
			StatePath: "/tmp",
			Sets:      []string{"proxy_src"},
		}},
		checkerStatus: func() int { return 1 },
	})
	return r
}

func decodeContractEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) contractEnvelope {
	t.Helper()

	var env contractEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope error = %v, body = %s", err, recorder.Body.String())
	}
	if env.Code == "" {
		t.Fatalf("response missing code field: %s", recorder.Body.String())
	}
	if env.Message == "" {
		t.Fatalf("response missing message field: %s", recorder.Body.String())
	}
	if len(env.Data) == 0 {
		t.Fatalf("response missing data field: %s", recorder.Body.String())
	}
	return env
}
