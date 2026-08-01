package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

const gracefulShutdownTimeout = 5 * time.Second

// App is the main application container.
type App struct {
	config     *AppConfig
	configPath string

	nft      *NftManager
	checker  *Checker
	chnRoute *ChnRouteManager

	mu sync.RWMutex
}

// NewApp creates a new App with the given dependencies.
func NewApp(config *AppConfig, nft *NftManager, checker *Checker, chnRoute *ChnRouteManager) *App {
	return &App{
		config:   config,
		nft:      nft,
		checker:  checker,
		chnRoute: chnRoute,
	}
}

// Bootstrap ensures nft sets exist, syncs them to files, and renders proxy rules.
func (app *App) Bootstrap() error {
	if err := app.nft.EnsureSetsExist(app.config.Nft.Sets); err != nil {
		return fmt.Errorf("ensure nft sets: %w", err)
	}
	// Ensure infrastructure sets referenced by proxy.nft exist (may be empty until
	// fw4 loads reserved_ip.nft or ChnRouteManager populates chnroute.nft).
	if err := app.nft.EnsureSetsExist([]string{"reserved_ip", "chnroute"}); err != nil {
		return fmt.Errorf("ensure infrastructure sets: %w", err)
	}
	if err := app.nft.SyncAllSets(app.config.Nft.Sets); err != nil {
		return fmt.Errorf("sync nft sets: %w", err)
	}
	if err := app.chnRoute.EnsureExists(); err != nil {
		log.Printf("chnroute init: %v", err)
	}
	if err := app.nft.RenderAndLoadProxyRules(); err != nil {
		log.Printf("WARNING: proxy rules not loaded (tproxy module may be unavailable): %v", err)
		log.Printf("proxy rules written to disk and will take effect on next fw4 restart")
	}
	return nil
}

// Run starts the checker, chnroute manager, and HTTP server.
func (app *App) Run(ctx context.Context) error {
	// Start health checker
	app.checker.Start(ctx)

	// Start periodic chnroute refresh (initial data loaded in Bootstrap)
	app.chnRoute.StartPeriodicRefresh(ctx)

	if app.Config().ApiKey == "" {
		log.Printf("**********************************************************************")
		log.Printf("WARNING: api_key 未配置 —— /api/* 全部无鉴权。")
		log.Printf("         同一网络里的任何人都能读写代理规则、改配置、开关代理。")
		log.Printf("         在 %s 里设置 api_key 即可启用鉴权。", app.configPath)
		log.Printf("**********************************************************************")
	}

	return serveHTTP(ctx, buildRouter(app), app.config.Listen)
}

// buildRouter 组装完整的 HTTP 路由（静态资源 + /panel.js + /api）。
// 与测试共用，保证「静态开放、API 关闭」这条边界是被实际路由树验证的，而不是各拼一份。
func buildRouter(app *App) *gin.Engine {
	router := gin.Default()
	// servePanelJS 必须在静态中间件之前，否则 /panel.js 会被静态中间件先行返回、拿不到 no-cache
	router.Use(servePanelJS())
	router.Use(static.Serve("/", static.EmbedFolder(assetData, "web")))
	registerAPIRoutes(router.Group("/api"), app)
	return router
}

// Config returns a thread-safe copy of the current config.
func (app *App) Config() *AppConfig {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.config
}

// UpdateConfig updates the config and restarts the checker.
func (app *App) UpdateConfig(config *AppConfig) {
	app.mu.Lock()
	app.config = config
	app.mu.Unlock()
	app.checker.UpdateConfig(config.Checker)
}

func serveHTTP(ctx context.Context, handler http.Handler, addr string) error {
	srv := &http.Server{Addr: addr, Handler: handler}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Printf("shutting down (timeout %s)...", gracefulShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
