package main

import (
	"log"
	"os"
	"path/filepath"
	goRuntime "runtime"
)

const devModeEnvKey = "DEV_MODE"

func isDevMode() bool {
	return os.Getenv(devModeEnvKey) == "1"
}

type DevModeConfig struct {
	Enabled     bool
	OverlayRoot string
	Runtime     Runtime
}

func buildDevModeConfig() *DevModeConfig {
	if !isDevMode() {
		return nil
	}

	tmpRoot, err := os.MkdirTemp("", "transparent-proxy-dev-*")
	if err != nil {
		log.Fatalf("DEV_MODE: failed to create temp dir: %v", err)
	}

	runtime := Runtime{
		Runner:  NewDevMockRunner(),
		Fetcher: NewHTTPRemoteFetcher(DefaultRuntimeTimeouts().RemoteFetch),
		Files: DevFileWriter{
			root:         tmpRoot,
			fallbackRoot: devModeAssetFallbackRoot(),
		},
		Timeouts: DefaultRuntimeTimeouts(),
	}

	log.Printf("DEV_MODE enabled - overlay root: %s", tmpRoot)

	return &DevModeConfig{
		Enabled:     true,
		OverlayRoot: tmpRoot,
		Runtime:     runtime,
	}
}

func devModeAssetFallbackRoot() string {
	_, currentFile, _, ok := goRuntime.Caller(0)
	if !ok {
		return ""
	}

	serverRoot := filepath.Dir(currentFile)
	projectRoot := filepath.Dir(serverRoot)
	return filepath.Join(projectRoot, "files")
}
