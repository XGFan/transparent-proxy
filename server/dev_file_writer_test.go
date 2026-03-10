package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDevFileWriterReadFileFallsBackToRepoAssets(t *testing.T) {
	overlayRoot := t.TempDir()
	fallbackRoot := t.TempDir()

	assetPath := filepath.Join(fallbackRoot, "etc", "transparent-proxy", "transparent_full.nft")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0755); err != nil {
		t.Fatalf("mkdir fallback asset directory error = %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("full-rules\n"), 0644); err != nil {
		t.Fatalf("write fallback asset error = %v", err)
	}

	writer := DevFileWriter{root: overlayRoot, fallbackRoot: fallbackRoot}
	content, err := writer.ReadFile(transparentNftFullPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "full-rules\n" {
		t.Fatalf("ReadFile() content = %q, want %q", string(content), "full-rules\n")
	}
}

func TestDevFileWriterReadFilePrefersOverlayContent(t *testing.T) {
	overlayRoot := t.TempDir()
	fallbackRoot := t.TempDir()

	writer := DevFileWriter{root: overlayRoot, fallbackRoot: fallbackRoot}
	overlayPath := writer.resolvePath(transparentNftPartialPath)
	if err := os.MkdirAll(filepath.Dir(overlayPath), 0755); err != nil {
		t.Fatalf("mkdir overlay directory error = %v", err)
	}
	if err := os.WriteFile(overlayPath, []byte("overlay-rules\n"), 0644); err != nil {
		t.Fatalf("write overlay file error = %v", err)
	}

	fallbackPath := filepath.Join(fallbackRoot, "etc", "transparent-proxy", "transparent.nft")
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0755); err != nil {
		t.Fatalf("mkdir fallback directory error = %v", err)
	}
	if err := os.WriteFile(fallbackPath, []byte("fallback-rules\n"), 0644); err != nil {
		t.Fatalf("write fallback file error = %v", err)
	}

	content, err := writer.ReadFile(transparentNftPartialPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "overlay-rules\n" {
		t.Fatalf("ReadFile() content = %q, want %q", string(content), "overlay-rules\n")
	}
}
