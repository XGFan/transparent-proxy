package main

import (
	"context"
	"flag"

	"github.com/XGFan/go-utils"
)

func main() {
	configFile := flag.String("c", DefaultConfigPath, "config location")
	flag.Parse()

	var app *App
	var err error

	devConfig := buildDevModeConfig()
	if devConfig != nil {
		app, err = bootstrapAppWithRuntime(*configFile, devConfig.Runtime, nil)
	} else {
		app, err = bootstrapApp(*configFile)
	}

	utils.PanicIfErr(err)

	err = app.Run(context.Background())
	utils.PanicIfErr(err)
}
