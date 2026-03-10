package main

import (
	"github.com/gin-gonic/gin"
)

type apiServer struct {
	app *App

	runtime       Runtime
	config        *AppConfig
	configPath    string
	checkerStatus func() int

	nftSvc     *NftService
	checkerSvc *CheckerService
}

func registerAPIRoutes(routes gin.IRoutes, server apiServer) {
	routes.GET("/status", server.handleStatus)
	routes.GET("/ip", server.handleIP)
	routes.GET("/checker", server.handleCheckerGet)
	routes.PUT("/checker", server.handleCheckerUpdate)
	routes.PUT("/proxy", server.handleProxyUpdate)

	routes.GET("/rules", server.handleRules)
	routes.POST("/rules/add", server.handleRuleAdd)
	routes.POST("/rules/remove", server.handleRuleRemove)
	routes.POST("/rules/sync", server.handleRuleSync)
	routes.POST("/refresh-route", server.handleRefreshRoute)
}

func (server apiServer) effectiveRuntime() Runtime {
	if server.app != nil {
		return server.app.runtime
	}
	return server.runtime
}

func (server apiServer) effectiveConfig() *AppConfig {
	if server.app != nil {
		return server.app.Config()
	}
	return server.config
}

func (server apiServer) effectiveConfigPath() string {
	if server.app != nil {
		return server.app.ConfigPath()
	}
	return server.configPath
}

func (server apiServer) nftService() *NftService {
	if server.nftSvc != nil {
		return server.nftSvc
	}
	if server.app != nil && server.app.nftService != nil {
		return server.app.nftService
	}
	return NewNftService(server.effectiveRuntime())
}

func (server apiServer) checkerService() *CheckerService {
	if server.checkerSvc != nil {
		return server.checkerSvc
	}
	if server.app != nil && server.app.checkerService != nil {
		return server.app.checkerService
	}
	return NewCheckerServiceWithStatus(server.checkerStatus)
}

func (server apiServer) currentStatus() int {
	return server.checkerService().Status()
}
