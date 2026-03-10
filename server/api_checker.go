package main

import (
	"strings"

	"github.com/gin-gonic/gin"
)

type CheckerConfigRequest struct {
	Enabled          bool   `json:"enabled"`
	Method           string `json:"method"`
	URL              string `json:"url"`
	Host             string `json:"host"`
	Timeout          string `json:"timeout"`
	FailureThreshold int    `json:"failureThreshold"`
	CheckInterval    string `json:"checkInterval"`
}

type CheckerStatusResponse struct {
	Enabled             bool   `json:"enabled"`
	Method              string `json:"method,omitempty"`
	URL                 string `json:"url,omitempty"`
	Host                string `json:"host,omitempty"`
	Timeout             string `json:"timeout,omitempty"`
	FailureThreshold    int    `json:"failureThreshold,omitempty"`
	CheckInterval       string `json:"checkInterval,omitempty"`
	Running             bool   `json:"running"`
	Status              string `json:"status"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	LastCheck           string `json:"lastCheck"`
	LastError           string `json:"lastError"`
}

func (server apiServer) handleCheckerGet(c *gin.Context) {
	apiOK(c, server.currentCheckerResponse())
}

func (server apiServer) handleCheckerUpdate(c *gin.Context) {
	var request CheckerConfigRequest
	if err := decodeJSONBodyStrict(c.Request, &request); err != nil {
		apiInvalidRequest(c, "invalid checker config payload", gin.H{"error": err.Error()})
		return
	}

	configPath := strings.TrimSpace(server.effectiveConfigPath())
	if configPath == "" {
		apiInternalError(c, "checker config path not configured", nil)
		return
	}

	currentConfig := server.effectiveConfig()
	if currentConfig == nil {
		apiInternalError(c, "checker config not loaded", nil)
		return
	}

	updatedConfig := *currentConfig
	updatedConfig.Checker = CheckerConfig{
		Enabled:          request.Enabled,
		Method:           strings.ToUpper(strings.TrimSpace(request.Method)),
		URL:              strings.TrimSpace(request.URL),
		Host:             strings.TrimSpace(request.Host),
		Timeout:          strings.TrimSpace(request.Timeout),
		FailureThreshold: request.FailureThreshold,
		CheckInterval:    strings.TrimSpace(request.CheckInterval),
	}
	updatedConfig.ApplyDefaults()
	if err := updatedConfig.Validate(configPath); err != nil {
		apiInvalidRequest(c, "invalid checker config payload", gin.H{"error": err.Error()})
		return
	}

	persistedConfig, err := SaveConfig(configPath, &updatedConfig)
	if err != nil {
		apiInternalError(c, "save checker config fail", err)
		return
	}

	if server.app != nil {
		server.app.UpdateConfig(persistedConfig)
	} else {
		server.config = persistedConfig
	}

	apiOK(c, server.currentCheckerResponse())
}

func (server apiServer) currentCheckerResponse() CheckerStatusResponse {
	config := server.effectiveConfig()
	checkerConfig := CheckerConfig{}
	if config != nil {
		checkerConfig = config.Checker
	}

	service := server.checkerService()
	return CheckerStatusResponse{
		Enabled:             checkerConfig.Enabled,
		Method:              checkerConfig.Method,
		URL:                 checkerConfig.URL,
		Host:                checkerConfig.Host,
		Timeout:             checkerConfig.Timeout,
		FailureThreshold:    checkerConfig.FailureThreshold,
		CheckInterval:       checkerConfig.CheckInterval,
		Running:             service.IsRunning(),
		Status:              checkerStatusLabel(service.Status()),
		ConsecutiveFailures: service.ConsecutiveFailures(),
		LastCheck:           service.LastCheck(),
		LastError:           service.LastError(),
	}
}

func checkerStatusLabel(status int) string {
	switch status {
	case 1:
		return "up"
	case -1:
		return "down"
	default:
		return "unknown"
	}
}
