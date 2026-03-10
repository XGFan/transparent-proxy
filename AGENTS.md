# AGENTS.md

此文件为 AI 编码代理（如 Claude Code、Cursor 等）提供项目上下文。

## 项目概述

透明代理管理工具，管理 Linux nftables 规则。前后端分离架构：
- **后端**：Go 1.22 (Gin) - nftables ipset、网络检查、OpenWrt 配置管理
- **前端**：React 18 + TypeScript + Vite - Web UI
- **测试**：三层体系（Go 单元测试 + Vitest 组件测试 + Playwright E2E）

## 构建命令

### 后端 (server/)
```bash
cd server
go build -o transparent-proxy .          # 构建（需先构建前端）
sudo ./transparent-proxy -c ../config.yaml  # 运行（需 root）
go test ./...                             # 测试所有
go test ./path/to/package -run ^TestName$ # 测试单个函数
go test -cover ./...                      # 覆盖率
go mod tidy                                # 整理依赖
```

### 前端 (portal/)
```bash
cd portal
npm install                    # 安装依赖
npm start                      # 开发服务器 http://localhost:3000
npm run build                  # 类型检查 + 构建 → ../server/web/
npm run test:component         # Vitest 组件测试
npx vitest                     # 组件测试 watch 模式
npm run lint                   # ESLint 检查
npm run test:e2e:blocking      # E2E 测试（需 OpenWrt VM）
```

### 完整测试流程
```bash
# PR 阻塞层
cd server && go test ./... && cd ../portal && npm run test:component
bash scripts/openwrt-vm/test-all.sh --tier blocking

# 完整回归层
bash scripts/openwrt-vm/test-all.sh --tier regression
```

## 代码风格指南

### Go 后端
**导入顺序**：标准库 → 空行 → 第三方库 → 空行 → 本地模块

**命名**：PascalCase 导出，camelCase 私有，接口用 -er 后缀

**错误处理**：
```go
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}
```
- 禁止忽略错误
- `log.Printf` 记录日志，`utils.PanicIfErr` 处理启动致命错误

**测试**：`*_test.go` 同目录，`Test<Name>` 命名，用 `t.Run()` 组织子测试

### TypeScript/React 前端
**导入顺序**：样式 → 第三方库 → 本地模块（`@/` 别名）

**命名**：组件 PascalCase，变量/函数 camelCase，data-testid 用小写连字符

**TypeScript 严格配置**：`strict: true`，`noUnusedLocals/Parameters: true`，禁止 `as any`

**Hooks**：必须声明依赖数组，用 `useCallback` 缓存回调

**错误处理**：
```typescript
// axios 必须有 .catch() 或 try/catch
err.response?.data?.error || err.message
```

**测试**（Vitest + Testing Library）：
```typescript
import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
```

## Lint 配置
- **前端**：ESLint 9 + TypeScript ESLint，`npm run lint`，`--max-warnings 0`
- **后端**：`gofmt -w .` 格式化，`go vet ./...` 检查

## 项目结构
```
transparent-proxy/
├── server/           # Go 后端（main.go, app.go, api_*.go, *_service.go, *_test.go）
├── portal/           # React 前端（src/app/, src/features/, src/lib/, src/test/）
├── scripts/openwrt-vm/  # OpenWrt VM 测试脚本
├── files/            # 管理文件资产
├── docs/             # 文档
├── config.yaml       # 运行配置（version: v3）
└── .drone.yml        # CI/CD 配置
```

## 运行配置
`config.yaml` 包含：`version`（v3）、`checker`（网络检查目标）、`nft`（ipset 配置）、`externalConfigs`、`managedFiles`

## 开发注意事项
1. **权限**：后端需 root 执行 nft 命令
2. **构建顺序**：前端先构建到 `server/web/`，Go 再构建嵌入
3. **类型安全**：TypeScript 严格模式，禁止 `as any`
4. **并发安全**：Go 用 `sync.RWMutex` 保护共享状态
5. **E2E 测试**：需 OpenWrt VM，详见 `docs/openwrt-vm-testing.md`

## 环境变量
| 变量 | 说明 | 默认值 |
|------|------|--------|
| `PORTAL_API_TARGET` | 前端代理目标 | `http://localhost:8080` |
| `TP_API_BASE_URL` | VM API 地址 | `http://127.0.0.1:1444` |
| `TP_UI_BASE_URL` | Portal UI | `http://127.0.0.1:3000` |
| `DEV_MODE` | 开发模式（Mac 本地调试）| 未设置 |

## Mac 本地开发

```bash
# 终端 1：后端（开发模式，无需 root/VM）
cd server
DEV_MODE=1 go run . -c ../config.yaml

# 终端 2：前端
cd portal
npm start

# 浏览器访问
open http://localhost:3000
```

开发模式特性：
- Mock `nft` 命令，内存存储 ipset
- 文件写入重定向到临时目录
- 无需 OpenWrt/VM 即可调试 UI 和 API

详见 `docs/development.md`。

## 脚本

| 脚本 | 用途 |
|------|------|
| `scripts/generate-manifest.sh` | 自动生成 `files/managed-manifest.json` |
| `scripts/build-release.sh` | 构建 OpenWrt ARM64 发布包 |
| `scripts/openwrt-vm/` | OpenWrt VM 测试脚本 |

详见 `docs/scripts.md`。
