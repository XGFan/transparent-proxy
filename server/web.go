package main

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed web
var assetData embed.FS

// panelAssetPath 是面板契约要求的单文件自包含 ES module（portal 构建产物）。
const panelAssetPath = "web/panel.js"

// servePanelJS 拦截 GET/HEAD /panel.js，按面板契约返回 no-cache 的 ES module。
// 必须注册在静态文件中间件之前：否则静态中间件会先命中该文件并直接返回，拿不到 no-cache 头。
func servePanelJS() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path != "/panel.js" ||
			(c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			c.Next()
			return
		}

		data, err := assetData.ReadFile(panelAssetPath)
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/javascript; charset=utf-8", data)
		c.Abort()
	}
}
