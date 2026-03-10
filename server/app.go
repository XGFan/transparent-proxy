package main

import (
	"context"
	"errors"
	"sync"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

type App struct {
	runtime    Runtime
	config     *AppConfig
	state      *AppState
	configPath string

	listenAddress string

	nftService     *NftService
	checkerService *CheckerService
	serveHTTP      func(*gin.Engine, string) error
}

type AppState struct {
	mu           sync.RWMutex
	bootstrapped bool
	resourceMu   *sync.Mutex
}

func NewApp(config *AppConfig, runtime Runtime) *App {
	app := &App{
		runtime:       runtime,
		config:        config,
		state:         &AppState{resourceMu: defaultNftResourceLock()},
		listenAddress: config.ListenAddress(),
	}

	app.nftService = NewNftService(runtime, app.state.resourceLock())
	app.checkerService = NewCheckerService(config.Checker, runtime, app.nftService)
	app.serveHTTP = func(router *gin.Engine, listenAddress string) error {
		return router.Run(listenAddress)
	}

	return app
}

func (app *App) Bootstrap() error {
	if app == nil {
		return errors.New("app must not be nil")
	}
	app.state.markBootstrapped(false)
	if err := app.nftService.EnsureSetsExist(app.config.Nft.Sets); err != nil {
		return err
	}
	app.state.markBootstrapped(true)
	return nil
}

func (app *App) Run(ctx context.Context) error {
	if app == nil {
		return errors.New("app must not be nil")
	}
	if !app.IsBootstrapped() {
		return errors.New("app bootstrap must complete before run")
	}

	app.checkerService.Start(ctx)

	router := gin.Default()
	router.Use(static.Serve("/", static.EmbedFolder(assetData, "web")))

	registerAPIRoutes(router.Group("/api"), apiServer{app: app})
	return app.serveHTTP(router, app.listenAddress)
}

func (app *App) IsBootstrapped() bool {
	if app == nil {
		return false
	}
	return app.state.isBootstrapped()
}

func (app *App) Config() *AppConfig {
	if app == nil {
		return nil
	}
	app.state.mu.RLock()
	defer app.state.mu.RUnlock()
	return app.config
}

func (app *App) UpdateConfig(config *AppConfig) {
	if app == nil || config == nil {
		return
	}
	checkerConfig := config.Checker
	app.state.mu.Lock()
	app.config = config
	app.state.mu.Unlock()
	if app.checkerService != nil {
		app.checkerService.UpdateConfig(checkerConfig)
	}
}

func (app *App) ConfigPath() string {
	if app == nil {
		return ""
	}
	app.state.mu.RLock()
	defer app.state.mu.RUnlock()
	return app.configPath
}

func (app *App) SetConfigPath(configPath string) {
	if app == nil {
		return
	}
	app.state.mu.Lock()
	defer app.state.mu.Unlock()
	app.configPath = configPath
}

func (state *AppState) markBootstrapped(bootstrapped bool) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.bootstrapped = bootstrapped
}

func (state *AppState) isBootstrapped() bool {
	if state == nil {
		return false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.bootstrapped
}

func (state *AppState) resourceLock() *sync.Mutex {
	if state == nil || state.resourceMu == nil {
		return defaultNftResourceLock()
	}
	return state.resourceMu
}

func (state *AppState) withResourceLock(run func() error) error {
	if run == nil {
		return nil
	}
	lock := state.resourceLock()
	lock.Lock()
	defer lock.Unlock()
	return run()
}
