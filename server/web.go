package main

import (
	"embed"
	"fmt"
	"io/fs"
)

const (
	embeddedFrontendRootDir   = "web"
	embeddedFrontendIndexPath = embeddedFrontendRootDir + "/index.html"
	embeddedFrontendAssetsDir = embeddedFrontendRootDir + "/assets"
)

//go:embed web web/index.html web/assets/*
var assetData embed.FS

//go:embed set.tmpl
var setTmpl string

func validateEmbeddedFrontendContract(assets fs.FS) error {
	if _, err := fs.Stat(assets, embeddedFrontendIndexPath); err != nil {
		return fmt.Errorf("embedded frontend missing %s: %w", embeddedFrontendIndexPath, err)
	}

	entries, err := fs.ReadDir(assets, embeddedFrontendAssetsDir)
	if err != nil {
		return fmt.Errorf("embedded frontend missing %s: %w", embeddedFrontendAssetsDir, err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("embedded frontend missing assets in %s", embeddedFrontendAssetsDir)
	}

	return nil
}
