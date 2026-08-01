package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// doRequestWithKey 与 doRequest 相同，额外带上 X-Api-Key 头（key 为空则不带）。
func doRequestWithKey(router *gin.Engine, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(apiKeyHeader, key)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestAPIKeyAuth_NotConfigured_AllowsAll(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequest(router, "GET", "/api/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (api_key 未配置应放行)\nbody: %s", w.Code, w.Body.String())
	}
}

func TestAPIKeyAuth_MissingKey_Rejected(t *testing.T) {
	app := testApp(t)
	app.config.ApiKey = "s3cret"
	router := testRouter(app)

	w := doRequest(router, "GET", "/api/status", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want 401\nbody: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp.Code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", resp.Code)
	}
}

func TestAPIKeyAuth_WrongKey_Rejected(t *testing.T) {
	app := testApp(t)
	app.config.ApiKey = "s3cret"
	router := testRouter(app)

	w := doRequestWithKey(router, "GET", "/api/status", "", "wrong")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want 401\nbody: %s", w.Code, w.Body.String())
	}
}

func TestAPIKeyAuth_CorrectKey_Allowed(t *testing.T) {
	app := testApp(t)
	app.config.ApiKey = "s3cret"
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequestWithKey(router, "GET", "/api/status", "", "s3cret")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	if resp := parseResponse(t, w); resp.Code != "ok" {
		t.Errorf("code = %q, want ok", resp.Code)
	}
}

// 写操作同样受保护（鉴权不只覆盖 GET）。
func TestAPIKeyAuth_WriteEndpointProtected(t *testing.T) {
	app := testApp(t)
	app.config.ApiKey = "s3cret"
	router := testRouter(app)

	w := doRequest(router, "PUT", "/api/proxy", `{"enabled": true}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want 401\nbody: %s", w.Code, w.Body.String())
	}

	w = doRequestWithKey(router, "PUT", "/api/proxy", `{"enabled": true}`, "s3cret")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
}

// --- 写方法的 CSRF 兜底（Content-Type + 同源）---

// doRequestRaw 允许自定义 Content-Type / Origin，用来模拟跨站「简单请求」。
func doRequestRaw(router *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// 跨站简单请求只能把 Content-Type 设成 text/plain 等三种之一，必须挡掉。
// 这四条 POST 是实际能被简单请求打到的（PUT 必定预检）。
func TestWriteGuard_NonJSONContentTypeRejected(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	for _, path := range []string{"/api/rules/add", "/api/rules/remove", "/api/rules/sync", "/api/refresh-route"} {
		t.Run(path, func(t *testing.T) {
			w := doRequestRaw(router, "POST", path, `{"ip":"1.2.3.4","set":"proxy_src"}`,
				map[string]string{"Content-Type": "text/plain"})
			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status code = %d, want 415\nbody: %s", w.Code, w.Body.String())
			}
			if resp := parseResponse(t, w); resp.Code != "unsupported_media_type" {
				t.Errorf("code = %q, want unsupported_media_type", resp.Code)
			}
		})
	}
}

// 该规则集不应被上面那些被拒的请求写进去。
func TestWriteGuard_RejectedRequestDoesNotMutate(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	doRequestRaw(router, "POST", "/api/rules/add", `{"ip":"1.2.3.4","set":"proxy_src"}`,
		map[string]string{"Content-Type": "text/plain", "Origin": "https://evil.example"})

	_, elems, err := app.nft.GetSet("proxy_src")
	if err != nil {
		t.Fatalf("GetSet: %v", err)
	}
	for _, e := range elems {
		if strings.Contains(e, "1.2.3.4") {
			t.Fatalf("跨站简单请求写进了 nft set: %v", elems)
		}
	}
}

func TestWriteGuard_JSONWithCharsetAccepted(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequestRaw(router, "POST", "/api/rules/add", `{"ip":"1.2.3.4","set":"proxy_src"}`,
		map[string]string{"Content-Type": "application/json; charset=utf-8"})
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
}

// 未配 api_key 时，同源校验是唯一的 CSRF 兜底。
func TestWriteGuard_CrossOriginRejectedWhenNoKey(t *testing.T) {
	app := testApp(t)
	router := testRouter(app)

	w := doRequestRaw(router, "POST", "/api/rules/sync", "",
		map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want 403\nbody: %s", w.Code, w.Body.String())
	}
	if resp := parseResponse(t, w); resp.Code != "forbidden" {
		t.Errorf("code = %q, want forbidden", resp.Code)
	}
}

func TestWriteGuard_SameOriginAndHeaderlessAllowed(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"同源 Origin", map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"}},
		{"同源 Referer", map[string]string{"Content-Type": "application/json", "Referer": "http://example.com/panel"}},
		{"无 Origin/Referer（curl）", map[string]string{"Content-Type": "application/json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// httptest.NewRequest 的默认 Host 是 example.com
			w := doRequestRaw(router, "POST", "/api/rules/sync", "", tc.headers)
			if w.Code != http.StatusOK {
				t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
			}
		})
	}
}

// 配了 key 之后不再卡同源：浏览器跨站带不上 X-Api-Key，鉴权本身即更强的防护，
// 继续卡同源会挡掉 net-console 这类反代宿主。但 Content-Type 仍然要求 JSON。
func TestWriteGuard_WithKey_OriginIgnoredButContentTypeStillEnforced(t *testing.T) {
	app := testApp(t)
	app.config.ApiKey = "s3cret"
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequestRaw(router, "POST", "/api/rules/sync", "", map[string]string{
		"Content-Type": "application/json",
		"Origin":       "https://console.example",
		apiKeyHeader:   "s3cret",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("带 key 的跨 Origin 请求 status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}

	w = doRequestRaw(router, "POST", "/api/rules/sync", "", map[string]string{
		"Content-Type": "text/plain",
		apiKeyHeader:   "s3cret",
	})
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("带 key 但 Content-Type 不对 status = %d, want 415\nbody: %s", w.Code, w.Body.String())
	}
}

// 读方法不受写兜底影响（GET 不需要 Content-Type）。
func TestWriteGuard_ReadMethodsUnaffected(t *testing.T) {
	app := testApp(t)
	if err := app.nft.EnsureSetsExist(app.Config().Nft.Sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}
	router := testRouter(app)

	w := doRequestRaw(router, "GET", "/api/status", "", map[string]string{"Origin": "https://evil.example"})
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
}

// --- 路由边界：静态开放、API 关闭 ---

// 用 buildRouter（与 App.Run 同一份路由树）守住边界：配了 api_key 之后，
// /panel.js 与宿主页必须仍然可匿名访问（net-console 靠 /panel.js 探活），而 /api/* 必须关闭。
func TestBuildRouter_StaticOpenAPIClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := testApp(t)
	app.config.ApiKey = "s3cret"
	router := buildRouter(app)

	t.Run("GET /panel.js 200 且 no-cache", func(t *testing.T) {
		if _, err := assetData.ReadFile(panelAssetPath); err != nil {
			t.Skip("前端未构建（server/web/panel.js 缺失，构建产物不入库）：先跑 portal npm run build")
		}
		w := doRequestRaw(router, "GET", "/panel.js", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status code = %d, want 200", w.Code)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control = %q, want no-cache", got)
		}
	})

	t.Run("GET / 200", func(t *testing.T) {
		w := doRequestRaw(router, "GET", "/", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status code = %d, want 200", w.Code)
		}
	})

	t.Run("GET /api/status 401", func(t *testing.T) {
		w := doRequestRaw(router, "GET", "/api/status", "", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status code = %d, want 401\nbody: %s", w.Code, w.Body.String())
		}
	})
}

// api_key 不应出现在 /api/config 的读写契约里，避免泄露或被清空。
func TestAPIKeyAuth_NotExposedByConfigAPI(t *testing.T) {
	app := testApp(t)
	app.config.ApiKey = "s3cret"
	router := testRouter(app)

	w := doRequestWithKey(router, "GET", "/api/config", "", "s3cret")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "s3cret") {
		t.Errorf("GET /api/config 泄露了 api_key: %s", w.Body.String())
	}

	// 更新配置后 api_key 必须保留
	body := `{"listen":":1444","proxy":{"lan_interface":"br-lan","default_port":1081,"forced_port":1082,"self_mark":255},` +
		`"checker":{"enabled":false,"method":"HEAD","url":"http://www.google.com","timeout":"10s","interval":"30s","failure_threshold":3},` +
		`"chnroute":{"auto_refresh":true,"refresh_interval":"168h"}}`
	w = doRequestWithKey(router, "PUT", "/api/config", body, "s3cret")
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	if app.Config().ApiKey != "s3cret" {
		t.Errorf("api_key 被配置更新清空了: %q", app.Config().ApiKey)
	}
}
