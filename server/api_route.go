package main

import (
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/gin-gonic/gin"
)

const (
	chnRouteFixturePathEnv = "TP_CHNROUTE_FIXTURE_PATH"
	refreshRouteFixtureEnv = "TP_REFRESH_ROUTE_FIXTURE"
)

type fixtureRemoteFetcher struct {
	path string
}

func (fetcher fixtureRemoteFetcher) Fetch(_ string) ([]byte, error) {
	content, err := os.ReadFile(fetcher.path)
	if err != nil {
		return nil, fmt.Errorf("read chnroute fixture fail: %w", err)
	}
	return content, nil
}

func resolveCHNRouteFixturePath() string {
	fixturePath := strings.TrimSpace(os.Getenv(chnRouteFixturePathEnv))
	if fixturePath != "" {
		return fixturePath
	}
	return strings.TrimSpace(os.Getenv(refreshRouteFixtureEnv))
}

func (server apiServer) handleRefreshRoute(c *gin.Context) {
	tmpl, err := template.New("set").Funcs(template.FuncMap{"join": join}).Parse(setTmpl)
	if err != nil {
		apiInternalError(c, "parse template failed", err)
		return
	}

	runtime := server.effectiveRuntime()
	if fixturePath := resolveCHNRouteFixturePath(); fixturePath != "" {
		runtime.Fetcher = fixtureRemoteFetcher{path: fixturePath}
	}

	if err := refreshCHNRoute(runtime, server.effectiveConfig().Nft.StatePath, tmpl); err != nil {
		apiInternalError(c, "refresh route failed", err)
		return
	}

	apiOK(c, gin.H{"message": "ok"})
}
