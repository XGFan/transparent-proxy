package main

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// apiKeyHeader 面板契约约定的鉴权请求头。
const apiKeyHeader = "X-Api-Key"

// apiKeyAuth 校验 /api/* 请求的 X-Api-Key。
// config.api_key 为空时不启用鉴权（向后兼容旧配置）。
func apiKeyAuth(app *App) gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := app.Config().ApiKey
		if expected == "" {
			c.Next()
			return
		}

		provided := c.GetHeader(apiKeyHeader)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			apiError(c, http.StatusUnauthorized, "unauthorized", "invalid or missing api key", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// writeGuard 给状态变更方法加两道廉价的 CSRF 兜底（形状对齐 dns-switchy 的 guardWrite）：
//
//  1. Content-Type 必须是 application/json —— **始终生效，配了 api_key 也不跳过**。
//     跨站「简单请求」只能把 Content-Type 设成 text/plain 等三种之一，要求 JSON 即可
//     逼出预检、而我们不回 CORS 头。这条不能省：net-console 反代会代为注入 api-key，
//     所以「配了 key」并不能挡住经壳打过来的跨站简单请求。
//  2. 未配 api_key 时补同源校验 —— 那是无鉴权服务的兜底。配了 key 之后不再校验：
//     浏览器跨站请求带不上自定义头 X-Api-Key，鉴权本身就是更强的防护，
//     此时再卡同源反而会挡掉反代宿主等合法调用方。
func writeGuard(app *App) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isWriteMethod(c.Request.Method) {
			c.Next()
			return
		}

		if !isJSONContentType(c.GetHeader("Content-Type")) {
			apiError(c, http.StatusUnsupportedMediaType, "unsupported_media_type",
				"content-type must be application/json", nil)
			c.Abort()
			return
		}

		if app.Config().ApiKey == "" && !sameOriginOK(c.Request) {
			apiError(c, http.StatusForbidden, "forbidden", "cross-origin request rejected", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// isJSONContentType 只看 media type，忽略 charset 等参数。
func isJSONContentType(value string) bool {
	if i := strings.IndexByte(value, ';'); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(strings.ToLower(value)) == "application/json"
}

// sameOriginOK 用 Origin（优先）或 Referer 的 host 与请求 Host 比对来挡跨站请求。
// 两个头都没有时放行（curl 等非浏览器客户端）。
func sameOriginOK(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return originHostMatches(origin, r.Host)
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		return originHostMatches(referer, r.Host)
	}
	return true
}

func originHostMatches(rawURL, host string) bool {
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rawURL = rawURL[i+3:]
	}
	if i := strings.IndexAny(rawURL, "/?#"); i >= 0 {
		rawURL = rawURL[:i]
	}
	return rawURL == host
}
