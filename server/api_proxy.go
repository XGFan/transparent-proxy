package main

import "github.com/gin-gonic/gin"

type ProxyUpdateRequest struct {
	Enabled *bool `json:"enabled"`
}

func (server apiServer) handleProxyUpdate(c *gin.Context) {
	var request ProxyUpdateRequest
	if err := decodeJSONBodyStrict(c.Request, &request); err != nil {
		apiInvalidRequest(c, "invalid proxy payload", gin.H{"error": err.Error()})
		return
	}
	if request.Enabled == nil {
		apiInvalidRequest(c, "invalid proxy payload", gin.H{"error": "enabled is required"})
		return
	}

	if err := server.checkerService().SetProxyEnabled(*request.Enabled); err != nil {
		apiInternalError(c, "toggle proxy failed", err)
		return
	}

	enabled, status := server.currentProxyState()
	apiOK(c, gin.H{
		"enabled": enabled,
		"status":  status,
	})
}
