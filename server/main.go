package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configFile := flag.String("c", DefaultConfigPath, "config file path")
	flag.Parse()

	app, err := bootstrap(*configFile)
	if err != nil {
		log.Fatalf("bootstrap failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		log.Printf("app exited: %v", err)
	}
}

func bootstrap(configPath string) (*App, error) {
	// Ensure default config exists
	if err := EnsureDefaultConfig(configPath); err != nil {
		return nil, err
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	// Create dependencies
	var executor NftExecutor
	var files FileStore
	var fetcher RemoteFetcher

	if os.Getenv("DEV_MODE") == "1" {
		log.Println("DEV_MODE enabled: using in-memory mocks")
		executor = NewMemoryNft()
		files = NewMemoryFileStore()
		fetcher = &HTTPFetcher{Timeout: 30 * 1e9} // still use real HTTP for chnroute
	} else {
		executor = NewExecNftRunner(10 * 1e9)
		files = OSFileStore{}
		fetcher = &HTTPFetcher{Timeout: 30 * 1e9}
	}

	nft := NewNftManager(executor, files, config.Proxy, config.Nft.StatePath)
	checker := NewChecker(config.Checker, nft)
	chnRoute := NewChnRouteManager(fetcher, files, config.Nft.StatePath, config.ChnRoute)

	app := NewApp(config, nft, checker, chnRoute)
	app.configPath = configPath

	if err := app.Bootstrap(); err != nil {
		return nil, err
	}

	return app, nil
}
