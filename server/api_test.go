package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func testApp(t *testing.T) *App {
	t.Helper()
	config := BuildDefaultConfig()
	exec := NewMemoryNft()
	files := NewMemoryFileStore()
	nft := NewNftManager(exec, files, config.Proxy, config.Nft.StatePath)
	checker := NewChecker(config.Checker, nft)
	fetcher := &MemoryFetcher{Data: map[string][]byte{}}
	chnRoute := NewChnRouteManager(fetcher, files, config.Nft.StatePath, config.ChnRoute)
	app := NewApp(config, nft, checker, chnRoute)
	app.configPath = filepath.Join(t.TempDir(), "config.yaml")
	// Save initial config so checker update can read it
	raw, _ := yaml.Marshal(config)
	os.WriteFile(app.configPath, raw, 0644)
	return app
}

func testRouter(app *App) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerAPIRoutes(r.Group("/api"), app)
	return r
}

func doRequest(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func parseResponse(t *testing.T, w *httptest.ResponseRecorder) apiResponse {
	t.Helper()
	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v\nbody: %s", err, w.Body.String())
	}
	return resp
}

// --- Status ---

func TestAPI_GetStatus(t *testing.T) {
	app := testApp(t)
	// Bootstrap to create sets
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequest(router, "GET", "/api/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := parseResponse(t, w)
	if resp.Code != "ok" {
		t.Errorf("code = %q, want ok", resp.Code)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatal("data is not an object")
	}
	if _, ok := data["proxy"]; !ok {
		t.Error("response missing proxy field")
	}
	if _, ok := data["checker"]; !ok {
		t.Error("response missing checker field")
	}
	if _, ok := data["rules"]; !ok {
		t.Error("response missing rules field")
	}
}

// --- Proxy Toggle ---

func TestAPI_PutProxy_Enable(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "PUT", "/api/proxy", `{"enabled": true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	data := resp.Data.(map[string]any)
	if data["enabled"] != true {
		t.Errorf("enabled = %v, want true", data["enabled"])
	}
	if data["status"] != "running" {
		t.Errorf("status = %v, want running", data["status"])
	}
}

func TestAPI_PutProxy_Disable(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	// Enable first
	doRequest(router, "PUT", "/api/proxy", `{"enabled": true}`)

	// Then disable
	w := doRequest(router, "PUT", "/api/proxy", `{"enabled": false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := parseResponse(t, w)
	data := resp.Data.(map[string]any)
	if data["enabled"] != false {
		t.Errorf("enabled = %v, want false", data["enabled"])
	}
}

func TestAPI_PutProxy_MissingEnabled(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "PUT", "/api/proxy", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

func TestAPI_PutProxy_BadBody(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "PUT", "/api/proxy", `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

// --- Rules ---

func TestAPI_GetRules(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequest(router, "GET", "/api/rules", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := parseResponse(t, w)
	data := resp.Data.(map[string]any)
	sets, ok := data["sets"].([]any)
	if !ok {
		t.Fatal("sets is not an array")
	}
	if len(sets) != len(app.Config().Nft.Sets) {
		t.Errorf("sets length = %d, want %d", len(sets), len(app.Config().Nft.Sets))
	}
}

func TestAPI_RuleAdd_Valid(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequest(router, "POST", "/api/rules/add", `{"ip": "1.2.3.4", "set": "direct_dst"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp.Code != "ok" {
		t.Errorf("code = %q, want ok", resp.Code)
	}
}

func TestAPI_RuleAdd_CIDR(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequest(router, "POST", "/api/rules/add", `{"ip": "10.0.0.0/8", "set": "direct_dst"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
}

func TestAPI_RuleRemove_Valid(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	// Add first, then remove
	doRequest(router, "POST", "/api/rules/add", `{"ip": "1.2.3.4", "set": "direct_dst"}`)
	w := doRequest(router, "POST", "/api/rules/remove", `{"ip": "1.2.3.4", "set": "direct_dst"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
}

func TestAPI_RuleAdd_BadIP(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequest(router, "POST", "/api/rules/add", `{"ip": "not-an-ip", "set": "direct_dst"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

func TestAPI_RuleAdd_UnknownSet(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "POST", "/api/rules/add", `{"ip": "1.2.3.4", "set": "nonexistent"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Message, "invalid") {
		t.Errorf("message = %q, want mention of invalid", resp.Message)
	}
}

func TestAPI_RuleAdd_MissingFields(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	tests := []struct {
		name string
		body string
	}{
		{"empty_ip", `{"ip": "", "set": "direct_dst"}`},
		{"empty_set", `{"ip": "1.2.3.4", "set": ""}`},
		{"no_body", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doRequest(router, "POST", "/api/rules/add", tt.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status code = %d, want 400\nbody: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAPI_RuleSync(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequest(router, "POST", "/api/rules/sync", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp.Code != "ok" {
		t.Errorf("code = %q, want ok", resp.Code)
	}
}

// --- Checker API ---

func TestAPI_GetChecker(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "GET", "/api/checker", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := parseResponse(t, w)
	data := resp.Data.(map[string]any)
	if data["method"] != "HEAD" {
		t.Errorf("method = %v, want HEAD", data["method"])
	}
	if data["enabled"] != true {
		t.Errorf("enabled = %v, want true", data["enabled"])
	}
}

func TestAPI_PutChecker_ValidUpdate(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	body := `{
		"enabled": true,
		"method": "GET",
		"url": "http://example.com",
		"host": "example.com",
		"timeout": "5s",
		"failure_threshold": 5,
		"interval": "60s"
	}`
	w := doRequest(router, "PUT", "/api/checker", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	data := resp.Data.(map[string]any)
	if data["method"] != "GET" {
		t.Errorf("method = %v, want GET", data["method"])
	}
	if data["url"] != "http://example.com" {
		t.Errorf("url = %v, want http://example.com", data["url"])
	}
	// Verify threshold is a number
	threshold, ok := data["failure_threshold"].(float64)
	if !ok || threshold != 5 {
		t.Errorf("failure_threshold = %v, want 5", data["failure_threshold"])
	}
}

func TestAPI_PutChecker_InvalidMethod(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	body := `{
		"enabled": true,
		"method": "DELETE",
		"url": "http://example.com",
		"host": "example.com",
		"timeout": "5s",
		"failure_threshold": 3,
		"interval": "30s"
	}`
	w := doRequest(router, "PUT", "/api/checker", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

func TestAPI_PutChecker_InvalidURL(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	body := `{
		"enabled": true,
		"method": "HEAD",
		"url": "ftp://bad",
		"host": "bad",
		"timeout": "5s",
		"failure_threshold": 3,
		"interval": "30s"
	}`
	w := doRequest(router, "PUT", "/api/checker", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

func TestAPI_PutChecker_BadJSON(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "PUT", "/api/checker", `{bad json}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

// --- IP ---

func TestAPI_GetIP(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "GET", "/api/ip?ip=1.2.3.4", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := parseResponse(t, w)
	data := resp.Data.(map[string]any)
	if data["ip"] != "1.2.3.4" {
		t.Errorf("ip = %v, want 1.2.3.4", data["ip"])
	}
}

// --- Validation edge cases ---

func TestAPI_RuleAdd_Range(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequest(router, "POST", "/api/rules/add", `{"ip": "10.0.0.1-10.0.0.255", "set": "direct_dst"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
}

func TestAPI_RuleAdd_InvalidRange(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	// Reversed range: start > end
	w := doRequest(router, "POST", "/api/rules/add", `{"ip": "10.0.0.255-10.0.0.1", "set": "direct_dst"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400 for reversed range", w.Code)
	}
}
