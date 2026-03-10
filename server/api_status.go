package main

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
)

func currentIP(req *http.Request) string {
	ip := req.URL.Query().Get("ip")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(req.RemoteAddr)
	}
	return ip
}

func (server apiServer) handleIP(c *gin.Context) {
	apiOK(c, gin.H{"ip": currentIP(c.Request)})
}

func (server apiServer) handleStatus(c *gin.Context) {
	config := server.effectiveConfig()
	sets := make([]map[string]any, 0, len(config.Nft.Sets))
	rules := make([]map[string]any, 0, len(config.Nft.Sets))

	for _, setName := range config.Nft.Sets {
		typ, elems, err := server.nftService().GetSetJSON(setName)
		if err != nil {
			rules = append(rules, map[string]any{
				"name":  setName,
				"type":  "",
				"elems": []string{},
				"error": err.Error(),
			})
			continue
		}
		sets = append(sets, map[string]any{
			"name":  setName,
			"type":  typ,
			"elems": elems,
		})
		rules = append(rules, map[string]any{
			"name":  setName,
			"type":  typ,
			"elems": elems,
		})
	}

	checkerService := server.checkerService()
	checkerStatusStr := checkerStatusLabel(checkerService.Status())
	proxyEnabled, proxyStatus := server.currentProxyState()

	apiOK(c, gin.H{
		"proxy": gin.H{
			"enabled": proxyEnabled,
			"status":  proxyStatus,
		},
		"checker": gin.H{
			"enabled":             config.Checker.Enabled,
			"running":             checkerService.IsRunning(),
			"status":              checkerStatusStr,
			"consecutiveFailures": checkerService.ConsecutiveFailures(),
			"lastCheck":           checkerService.LastCheck(),
			"lastError":           checkerService.LastError(),
		},
		"rules": gin.H{
			"sets":  config.Nft.Sets,
			"rules": rules,
		},
	})
}

func (server apiServer) currentProxyState() (bool, string) {
	enabled, err := server.nftService().ProxyEnabledFromNft()
	if err != nil {
		return false, "unknown"
	}
	if enabled {
		return true, "running"
	}
	return false, "stopped"
}
