package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DevFileWriter struct {
	root         string
	fallbackRoot string
}

func (w DevFileWriter) WriteFile(name string, data []byte, perm os.FileMode) error {
	actualPath := w.resolvePath(name)
	dir := filepath.Dir(actualPath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("dev file writer: mkdir %s fail: %w", dir, err)
	}

	if err := os.WriteFile(actualPath, data, perm); err != nil {
		return fmt.Errorf("dev file writer: write %s fail: %w", actualPath, err)
	}

	return nil
}

func (w DevFileWriter) ReadFile(name string) ([]byte, error) {
	actualPath := w.resolvePath(name)
	data, err := os.ReadFile(actualPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("dev file writer: read %s fail: %w", actualPath, err)
		}

		fallbackPath, ok := w.resolveFallbackPath(name)
		if ok {
			fallbackData, fallbackErr := os.ReadFile(fallbackPath)
			if fallbackErr == nil {
				return fallbackData, nil
			}
			if !errors.Is(fallbackErr, os.ErrNotExist) {
				return nil, fmt.Errorf("dev file writer: read fallback %s fail: %w", fallbackPath, fallbackErr)
			}
		}

		return nil, fmt.Errorf("dev file writer: read %s fail: %w", actualPath, err)
	}
	return data, nil
}

func (w DevFileWriter) Remove(name string) error {
	actualPath := w.resolvePath(name)
	if err := os.Remove(actualPath); err != nil {
		return fmt.Errorf("dev file writer: remove %s fail: %w", actualPath, err)
	}
	return nil
}

func (w DevFileWriter) resolvePath(name string) string {
	cleanName := filepath.Clean(name)

	if strings.HasPrefix(cleanName, w.root) {
		return cleanName
	}

	if filepath.IsAbs(cleanName) {
		return filepath.Join(w.root, cleanName[1:])
	}

	return filepath.Join(w.root, cleanName)
}

func (w DevFileWriter) resolveFallbackPath(name string) (string, bool) {
	fallbackRoot := strings.TrimSpace(w.fallbackRoot)
	if fallbackRoot == "" {
		return "", false
	}

	cleanRoot := filepath.Clean(fallbackRoot)
	cleanName := filepath.Clean(name)

	var candidate string
	if filepath.IsAbs(cleanName) {
		candidate = filepath.Join(cleanRoot, strings.TrimPrefix(cleanName, string(filepath.Separator)))
	} else {
		candidate = filepath.Join(cleanRoot, cleanName)
	}

	rel, err := filepath.Rel(cleanRoot, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return candidate, true
}
