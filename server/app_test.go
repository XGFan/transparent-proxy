package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRunUsesResolvedListenAddress(t *testing.T) {
	t.Run("uses explicit loopback listen address", func(t *testing.T) {
		assertRunUsesListenAddress(t, "127.0.0.1:1444", "127.0.0.1:1444")
	})

	t.Run("uses default listen address when omitted", func(t *testing.T) {
		assertRunUsesListenAddress(t, "", DefaultListenAddress)
	})
}

func assertRunUsesListenAddress(t *testing.T, configuredAddress, wantAddress string) {
	t.Helper()

	config, err := ParseConfig([]byte(buildRunConfigYAML(configuredAddress)), UserConfigPath)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	app := NewApp(config, Runtime{})
	app.state.markBootstrapped(true)
	app.checkerService = &CheckerService{}

	var gotListenAddress string
	app.serveHTTP = func(_ *gin.Engine, listenAddress string) error {
		gotListenAddress = listenAddress
		return nil
	}

	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
	if gotListenAddress != wantAddress {
		t.Fatalf("run listenAddress = %q, want %q", gotListenAddress, wantAddress)
	}
}

func buildRunConfigYAML(listenAddress string) string {
	base := "version: v3\n"
	if listenAddress != "" {
		base += fmt.Sprintf("server:\n  listenAddress: %s\n", listenAddress)
	}
	base += "nft:\n"
	base += "  sets:\n"
	base += "    - proxy_src\n"
	return base
}
