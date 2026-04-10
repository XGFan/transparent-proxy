# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Transparent proxy management tool for Linux nftables on OpenWrt routers. Two-part architecture:
- **Go backend** (`server/`) — manages nftables IP sets, health checks, config; embeds the frontend at build time
- **Preact frontend** (`portal/`) — status dashboard built with Vite + Preact, outputs to `server/web/`

## Build & Test Commands

### Backend (`server/`)
```bash
go build -o transparent-proxy .            # build (requires frontend built first)
go test ./...                              # all tests
go test -run ^TestName$ .                  # single test
gofmt -w .                                 # format
go vet ./...                               # lint
```

### Frontend (`portal/`)
```bash
npm install                                # install deps
npm start                                  # dev server :3000, proxies /api to :1444
npm run build                              # typecheck + build → ../server/web/
npm run lint                               # ESLint 9, --max-warnings 0
```

### Build order matters
Frontend must be built first (`portal/npm run build` → `server/web/`), then Go binary embeds it.

### Local development (Mac, no root/VM needed)
```bash
# Terminal 1: backend with mock nft commands
cd server && DEV_MODE=1 go run . -c ../config.yaml

# Terminal 2: frontend dev server
cd portal && npm start

# Browse http://localhost:3000
```

`DEV_MODE=1` uses in-memory mocks for nft commands and file operations.

## Architecture

### Backend core (`server/`)

- **`main.go`** — CLI entry, signal handling, DEV_MODE setup, dependency wiring
- **`app.go`** — `App` struct owns lifecycle: bootstrap → run (checker + chnroute + HTTP server)
- **`config.go`** — YAML config (`AppConfig`, version 1) with validation and atomic save
- **`nft.go`** — `NftManager` manages nftables: set CRUD, proxy enable/disable, template rendering. Uses `NftExecutor` interface (sole mock point for nft commands) and `FileStore` interface (file I/O)
- **`checker.go`** — `Checker` runs periodic health checks, toggles proxy state. Supports `on_failure: disable|keep`
- **`chnroute.go`** — `ChnRouteManager` fetches APNIC data, generates chnroute.nft, periodic refresh
- **`api.go`** — All Gin HTTP routes under `/api/` (status, rules CRUD, checker config, proxy toggle, chnroute refresh)
- **`mock.go`** — `MemoryNft` + `MemoryFileStore` + `MemoryFetcher` — unified mocks for DEV_MODE and tests
- **`web.go`** — Embedded frontend assets via `embed.FS`
- **`templates/`** — `proxy.nft.tmpl` and `transparent.nft.tmpl` (embedded via `//go:embed`)

API responses use envelope: `{"code": "ok"|"error", "message": "...", "data": ...}`

### Frontend (`portal/src/`)

- **`features/status/StatusPage.tsx`** — Main dashboard container
- **`components/ProxyToggle.tsx`** — Proxy on/off switch
- **`components/CheckerCard.tsx`** — Health check status + config editor
- **`components/RuleSets.tsx`** — IP set management (add/remove/list)
- **`lib/api/client.ts`** — Type-safe fetch wrapper with `APIError` class
- Vite dev server proxies `/api` → `http://localhost:1444`

### Testing approach

Tests use `MemoryNft` + `MemoryFileStore` mocks (no real nft/filesystem calls). Key test files:
- `config_test.go` — Config parsing, validation, defaults, persistence
- `nft_test.go` — Set operations, proxy toggle, template rendering, JSON parsing
- `checker_test.go` — Health check lifecycle, failure threshold, on_failure modes
- `api_test.go` — HTTP API contract tests (all endpoints)

### Config (`config.yaml`, version: 1)

```yaml
version: 1
listen: ":1444"
proxy:
  lan_interface: br-lan
  default_port: 1081      # default proxy port
  forced_port: 1082       # forced proxy port (proxy_src/proxy_dst)
  self_mark: 255           # fwmark for proxy self-traffic exclusion
checker:
  enabled: true
  url: "http://www.google.com"
  method: HEAD
  timeout: 10s
  interval: 30s
  failure_threshold: 3
  on_failure: disable      # "disable" | "keep"
nft:
  state_path: /etc/nftables.d
  sets: [direct_src, direct_dst, proxy_src, proxy_dst, allow_v6_mac]
chnroute:
  auto_refresh: true
  refresh_interval: 168h
```

### nft files

Static files (IPK-installed to `/etc/nftables.d/`):
- `reserved_ip.nft` — RFC reserved addresses set
- `v6block.nft` — IPv6 forward + input filtering chains (references `@allow_v6_mac`)

Template-rendered (Go generates at runtime):
- `proxy.nft` — Core tproxy chains (uses configured ports/marks)
- `transparent.nft` — Mangle chain hooks (uses configured LAN interface)

## Code Style

### Go
- Import order: stdlib → third-party → local (blank line separated)
- PascalCase exports, camelCase private, `-er` suffix for interfaces
- Wrap errors with `fmt.Errorf("context: %w", err)`, never ignore errors
- Two mock interfaces: `NftExecutor` (nft commands), `FileStore` (file I/O)

### TypeScript/Preact
- Import order: styles → third-party → local (`@/` alias)
- `strict: true`, no `as any`
- Hooks from `preact/hooks`; `useCallback` for callbacks

## CI

Drone CI (`.drone.yml`): portal build → server tests → release contract → IPK packaging verify → docs lint
