package main

import (
	"context"
	"fmt"
	"log"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const chnRouteURL = "http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest"

// ChnRouteManager handles downloading and refreshing the China IP route table.
type ChnRouteManager struct {
	fetcher   RemoteFetcher
	files     FileStore
	statePath string
	config    ChnRouteConfig
}

func NewChnRouteManager(fetcher RemoteFetcher, files FileStore, statePath string, config ChnRouteConfig) *ChnRouteManager {
	return &ChnRouteManager{
		fetcher:   fetcher,
		files:     files,
		statePath: statePath,
		config:    config,
	}
}

// EnsureExists checks if chnroute.nft exists; if not, fetches and generates it.
func (m *ChnRouteManager) EnsureExists() error {
	targetPath := filepath.Join(m.statePath, "chnroute.nft")
	if _, err := m.files.ReadFile(targetPath); err == nil {
		log.Printf("chnroute.nft exists, skipping initial fetch")
		return nil
	}
	log.Printf("chnroute.nft not found, fetching from APNIC...")
	return m.Refresh()
}

// Refresh fetches the latest APNIC data and writes chnroute.nft.
func (m *ChnRouteManager) Refresh() error {
	ips, err := m.fetchChinaIPs()
	if err != nil {
		return fmt.Errorf("fetch China IPs: %w", err)
	}

	content := renderChnRouteSet(ips)
	targetPath := filepath.Join(m.statePath, "chnroute.nft")
	if err := m.files.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write chnroute.nft: %w", err)
	}
	log.Printf("chnroute.nft updated with %d entries", len(ips))
	return nil
}

// StartPeriodicRefresh runs a background goroutine that refreshes chnroute periodically.
func (m *ChnRouteManager) StartPeriodicRefresh(ctx context.Context) {
	if !m.config.AutoRefresh {
		return
	}
	interval := parseDuration(m.config.RefreshInterval, 168*time.Hour)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.Refresh(); err != nil {
					log.Printf("chnroute periodic refresh failed: %v", err)
				}
			}
		}
	}()
}

func (m *ChnRouteManager) fetchChinaIPs() ([]string, error) {
	data, err := m.fetcher.Fetch(chnRouteURL)
	if err != nil {
		return nil, err
	}

	var ips []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 6 || parts[1] != "CN" || parts[2] != "ipv4" {
			continue
		}
		count, err := strconv.ParseUint(parts[4], 10, 32)
		if err != nil {
			continue
		}
		mask := 32 - (bits.Len64(count) - 1)
		ips = append(ips, fmt.Sprintf("%s/%d", parts[3], mask))
	}
	return ips, nil
}

func renderChnRouteSet(ips []string) string {
	var sb strings.Builder
	sb.WriteString("set chnroute {\n")
	sb.WriteString("    type ipv4_addr\n")
	sb.WriteString("    flags interval\n")
	sb.WriteString("    auto-merge\n")
	if len(ips) > 0 {
		sb.WriteString("    elements = {\n")
		for i, ip := range ips {
			if i < len(ips)-1 {
				sb.WriteString(fmt.Sprintf("        %s,\n", ip))
			} else {
				sb.WriteString(fmt.Sprintf("        %s\n", ip))
			}
		}
		sb.WriteString("    }\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// resolveCHNRouteFixturePath checks env vars for test fixture paths.
func resolveCHNRouteFixturePath() string {
	if p := strings.TrimSpace(os.Getenv("TP_CHNROUTE_FIXTURE_PATH")); p != "" {
		return p
	}
	return strings.TrimSpace(os.Getenv("TP_REFRESH_ROUTE_FIXTURE"))
}
