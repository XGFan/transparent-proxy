package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// --- Response Helpers ---

type apiResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func apiOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, apiResponse{Code: "ok", Message: "ok", Data: nonNil(data)})
}

func apiError(c *gin.Context, status int, code, message string, err error) {
	data := gin.H{}
	if err != nil {
		data["error"] = err.Error()
	}
	c.JSON(status, apiResponse{Code: code, Message: message, Data: data})
}

func apiBadRequest(c *gin.Context, message string, err error) {
	apiError(c, http.StatusBadRequest, "invalid_request", message, err)
}

func apiServerError(c *gin.Context, message string, err error) {
	apiError(c, http.StatusInternalServerError, "internal_error", message, err)
}

func nonNil(v any) any {
	if v == nil {
		return gin.H{}
	}
	return v
}

func decodeJSON(req *http.Request, out any) error {
	if req == nil || req.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return err
	}
	return nil
}

// --- Route Registration ---

func registerAPIRoutes(r *gin.RouterGroup, app *App) {
	r.GET("/status", func(c *gin.Context) { handleStatus(c, app) })
	r.GET("/ip", handleIP)
	r.GET("/config", func(c *gin.Context) { handleConfigGet(c, app) })
	r.PUT("/config", func(c *gin.Context) { handleConfigUpdate(c, app) })
	r.GET("/checker", func(c *gin.Context) { handleCheckerGet(c, app) })
	r.PUT("/checker", func(c *gin.Context) { handleCheckerUpdate(c, app) })
	r.PUT("/proxy", func(c *gin.Context) { handleProxyUpdate(c, app) })
	r.GET("/rules", func(c *gin.Context) { handleRules(c, app) })
	r.POST("/rules/add", func(c *gin.Context) { handleRuleAdd(c, app) })
	r.POST("/rules/remove", func(c *gin.Context) { handleRuleRemove(c, app) })
	r.POST("/rules/sync", func(c *gin.Context) { handleRuleSync(c, app) })
	r.POST("/refresh-route", func(c *gin.Context) { handleRefreshRoute(c, app) })
}

// --- Handlers ---

func handleIP(c *gin.Context) {
	ip := c.Query("ip")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(c.Request.RemoteAddr)
	}
	apiOK(c, gin.H{"ip": ip})
}

func handleStatus(c *gin.Context, app *App) {
	config := app.Config()
	rules := listRuleSets(app)

	proxyEnabled, proxyStatus := currentProxyState(app)
	checker := app.checker

	apiOK(c, gin.H{
		"proxy": gin.H{
			"enabled": proxyEnabled,
			"status":  proxyStatus,
		},
		"checker": gin.H{
			"enabled":             config.Checker.Enabled,
			"running":             checker.IsRunning(),
			"status":              statusLabel(checker.Status()),
			"consecutiveFailures": checker.ConsecutiveFailures(),
			"lastCheck":           checker.LastCheck(),
			"lastError":           checker.LastError(),
		},
		"rules": gin.H{
			"sets":  config.Nft.Sets,
			"rules": rules,
		},
	})
}

// --- Config API (exposes all editable config except nft) ---

type editableConfig struct {
	Listen   string         `json:"listen"`
	Proxy    ProxyConfig    `json:"proxy"`
	Checker  CheckerConfig  `json:"checker"`
	ChnRoute ChnRouteConfig `json:"chnroute"`
}

func handleConfigGet(c *gin.Context, app *App) {
	cfg := app.Config()
	apiOK(c, editableConfig{
		Listen:   cfg.Listen,
		Proxy:    cfg.Proxy,
		Checker:  cfg.Checker,
		ChnRoute: cfg.ChnRoute,
	})
}

func handleConfigUpdate(c *gin.Context, app *App) {
	var req editableConfig
	if err := decodeJSON(c.Request, &req); err != nil {
		apiBadRequest(c, "invalid config", err)
		return
	}

	configPath := app.configPath
	if configPath == "" {
		apiServerError(c, "config path not set", nil)
		return
	}

	// Merge editable fields into current config, preserving nft section
	config := *app.Config()
	config.Listen = req.Listen
	config.Proxy = req.Proxy
	config.Checker = req.Checker
	config.ChnRoute = req.ChnRoute

	config.applyDefaults()
	if err := config.validate(); err != nil {
		apiBadRequest(c, "invalid config", err)
		return
	}

	saved, err := SaveConfig(configPath, &config)
	if err != nil {
		apiServerError(c, "save config failed", err)
		return
	}

	// Apply changes
	app.UpdateConfig(saved)

	// Re-render proxy rules if proxy config changed
	if err := app.nft.RenderAndLoadProxyRules(); err != nil {
		log.Printf("re-render proxy rules after config update: %v", err)
	}

	apiOK(c, editableConfig{
		Listen:   saved.Listen,
		Proxy:    saved.Proxy,
		Checker:  saved.Checker,
		ChnRoute: saved.ChnRoute,
	})
}

func handleCheckerGet(c *gin.Context, app *App) {
	apiOK(c, checkerResponse(app))
}

func handleCheckerUpdate(c *gin.Context, app *App) {
	var req struct {
		Enabled          bool   `json:"enabled"`
		Method           string `json:"method"`
		URL              string `json:"url"`
		Host             string `json:"host"`
		Timeout          string `json:"timeout"`
		FailureThreshold int    `json:"failure_threshold"`
		Interval         string `json:"interval"`
	}
	if err := decodeJSON(c.Request, &req); err != nil {
		apiBadRequest(c, "invalid checker config", err)
		return
	}

	configPath := app.configPath
	if configPath == "" {
		apiServerError(c, "config path not set", nil)
		return
	}

	config := *app.Config()
	config.Checker = CheckerConfig{
		Enabled:          req.Enabled,
		Method:           strings.ToUpper(strings.TrimSpace(req.Method)),
		URL:              strings.TrimSpace(req.URL),
		Host:             strings.TrimSpace(req.Host),
		Timeout:          strings.TrimSpace(req.Timeout),
		Interval:         strings.TrimSpace(req.Interval),
		FailureThreshold: req.FailureThreshold,
		OnFailure:        app.Config().Checker.OnFailure, // preserve existing on_failure
	}
	config.applyDefaults()
	if err := config.validate(); err != nil {
		apiBadRequest(c, "invalid checker config", err)
		return
	}

	saved, err := SaveConfig(configPath, &config)
	if err != nil {
		apiServerError(c, "save config failed", err)
		return
	}
	app.UpdateConfig(saved)
	apiOK(c, checkerResponse(app))
}

func handleProxyUpdate(c *gin.Context, app *App) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(c.Request, &req); err != nil {
		apiBadRequest(c, "invalid proxy payload", err)
		return
	}
	if req.Enabled == nil {
		apiBadRequest(c, "enabled is required", nil)
		return
	}

	if err := app.checker.SetProxyEnabled(*req.Enabled); err != nil {
		apiServerError(c, "toggle proxy failed", err)
		return
	}

	enabled, status := currentProxyState(app)
	apiOK(c, gin.H{"enabled": enabled, "status": status})
}

func handleRules(c *gin.Context, app *App) {
	apiOK(c, gin.H{
		"sets":  app.Config().Nft.Sets,
		"rules": listRuleSets(app),
	})
}

func handleRuleAdd(c *gin.Context, app *App) {
	var req struct {
		IP  string `json:"ip"`
		Set string `json:"set"`
	}
	if err := decodeJSON(c.Request, &req); err != nil {
		apiBadRequest(c, "invalid payload", err)
		return
	}
	ip, set, err := validateRuleRequest(req.IP, req.Set, app.Config())
	if err != nil {
		apiBadRequest(c, "invalid payload", err)
		return
	}

	if err := app.nft.AddToSet(set, ip); err != nil {
		apiServerError(c, "add rule failed", err)
		return
	}
	ruleSet := getRuleSet(app, set)
	apiOK(c, gin.H{"set": set, "ip": ip, "rule": ruleSet, "operation": gin.H{"action": "add", "result": "applied"}})
}

func handleRuleRemove(c *gin.Context, app *App) {
	var req struct {
		IP  string `json:"ip"`
		Set string `json:"set"`
	}
	if err := decodeJSON(c.Request, &req); err != nil {
		apiBadRequest(c, "invalid payload", err)
		return
	}
	ip, set, err := validateRuleRequest(req.IP, req.Set, app.Config())
	if err != nil {
		apiBadRequest(c, "invalid payload", err)
		return
	}

	if err := app.nft.RemoveFromSet(set, ip); err != nil {
		apiServerError(c, "remove rule failed", err)
		return
	}
	ruleSet := getRuleSet(app, set)
	apiOK(c, gin.H{"set": set, "ip": ip, "rule": ruleSet, "operation": gin.H{"action": "remove", "result": "applied"}})
}

func handleRuleSync(c *gin.Context, app *App) {
	config := app.Config()
	if err := app.nft.SyncAllSets(config.Nft.Sets); err != nil {
		apiServerError(c, "sync rules failed", err)
		return
	}

	var results []gin.H
	for _, name := range config.Nft.Sets {
		ruleSet := getRuleSet(app, name)
		results = append(results, gin.H{
			"rule":      ruleSet,
			"operation": gin.H{"action": "sync", "result": "applied", "output": filepath.Join(config.Nft.StatePath, name+".nft")},
		})
	}
	apiOK(c, gin.H{"synced": config.Nft.Sets, "results": results})
}

func handleRefreshRoute(c *gin.Context, app *App) {
	fetcher := app.chnRoute.fetcher
	if fixturePath := resolveCHNRouteFixturePath(); fixturePath != "" {
		fetcher = &fileFetcher{path: fixturePath}
	}

	mgr := &ChnRouteManager{
		fetcher:   fetcher,
		files:     app.nft.files,
		statePath: app.Config().Nft.StatePath,
	}
	if err := mgr.Refresh(); err != nil {
		apiServerError(c, "refresh route failed", err)
		return
	}
	apiOK(c, gin.H{"message": "ok"})
}

// --- Helpers ---

func currentProxyState(app *App) (bool, string) {
	enabled, err := app.nft.ProxyEnabled()
	if err != nil {
		return false, "unknown"
	}
	if enabled {
		return true, "running"
	}
	return false, "stopped"
}

func statusLabel(status int) string {
	switch status {
	case 1:
		return "up"
	case -1:
		return "down"
	default:
		return "unknown"
	}
}

func checkerResponse(app *App) gin.H {
	config := app.Config().Checker
	c := app.checker
	return gin.H{
		"enabled":             config.Enabled,
		"method":              config.Method,
		"url":                 config.URL,
		"host":                config.Host,
		"timeout":             config.Timeout,
		"failure_threshold":   config.FailureThreshold,
		"interval":            config.Interval,
		"running":             c.IsRunning(),
		"status":              statusLabel(c.Status()),
		"consecutiveFailures": c.ConsecutiveFailures(),
		"lastCheck":           c.LastCheck(),
		"lastError":           c.LastError(),
	}
}

type ruleSetView struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Elems []string `json:"elems"`
}

func getRuleSet(app *App, setName string) ruleSetView {
	typ, elems, err := app.nft.GetSet(setName)
	if err != nil {
		return ruleSetView{Name: setName, Elems: []string{}}
	}
	return ruleSetView{Name: setName, Type: typ, Elems: elems}
}

func listRuleSets(app *App) []gin.H {
	config := app.Config()
	var rules []gin.H
	for _, name := range config.Nft.Sets {
		typ, elems, err := app.nft.GetSet(name)
		if err != nil {
			rules = append(rules, gin.H{"name": name, "type": "", "elems": []string{}, "error": err.Error()})
			continue
		}
		rules = append(rules, gin.H{"name": name, "type": typ, "elems": elems})
	}
	return rules
}

func validateRuleRequest(rawIP, rawSet string, config *AppConfig) (string, string, error) {
	ip := strings.TrimSpace(rawIP)
	set := strings.TrimSpace(rawSet)
	if ip == "" || set == "" {
		return "", "", fmt.Errorf("ip and set are required")
	}

	found := false
	for _, s := range config.Nft.Sets {
		if s == set {
			found = true
			break
		}
	}
	if !found {
		return "", "", fmt.Errorf("set %q is not managed", set)
	}

	normalized, err := normalizeElement(ip)
	if err != nil {
		return "", "", err
	}
	return normalized, set, nil
}

func normalizeElement(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("ip is required")
	}

	if strings.Contains(v, "-") {
		parts := strings.SplitN(v, "-", 2)
		start, err := netip.ParseAddr(strings.TrimSpace(parts[0]))
		if err != nil {
			return "", fmt.Errorf("invalid range start: %q", raw)
		}
		end, err := netip.ParseAddr(strings.TrimSpace(parts[1]))
		if err != nil {
			return "", fmt.Errorf("invalid range end: %q", raw)
		}
		if start.Compare(end) > 0 {
			return "", fmt.Errorf("range start must be <= end: %q", raw)
		}
		return start.String() + "-" + end.String(), nil
	}

	if strings.Contains(v, "/") {
		prefix, err := netip.ParsePrefix(v)
		if err != nil {
			return "", fmt.Errorf("invalid cidr: %q", raw)
		}
		return prefix.String(), nil
	}

	addr, err := netip.ParseAddr(v)
	if err != nil {
		return "", fmt.Errorf("invalid ip: %q", raw)
	}
	return addr.String(), nil
}

// fileFetcher reads from a local file instead of HTTP (for testing).
type fileFetcher struct {
	path string
}

func (f *fileFetcher) Fetch(_ string) ([]byte, error) {
	return os.ReadFile(f.path)
}
