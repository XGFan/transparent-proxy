package main

import "embed"

//go:embed web web/index.html web/assets/*
var assetData embed.FS
