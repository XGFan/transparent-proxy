package main

import "fmt"

func bootstrapApp(configPath string) (*App, error) {
	return bootstrapAppWithRuntime(configPath, appRuntime, nil)
}

func bootstrapAppWithRuntime(configPath string, runtime Runtime, prepare func(*App)) (*App, error) {
	if err := ensureBootstrapConfigExists(configPath); err != nil {
		return nil, fmt.Errorf("bootstrap config fail: %w", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config fail: %w", err)
	}

	app := NewApp(config, runtime)
	app.SetConfigPath(configPath)
	if prepare != nil {
		prepare(app)
	}
	if err := app.Bootstrap(); err != nil {
		return nil, fmt.Errorf("bootstrap app fail: %w", err)
	}
	return app, nil
}
