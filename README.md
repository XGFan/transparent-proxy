# transparent-proxy

OpenWrt 路由器透明代理管理工具。通过 nftables + TPROXY 控制哪些流量走透明代理，提供 Web 管理界面。

**不负责**代理软件本身的安装、配置、启停 — 仅管理流量路由规则。

## 快速开始

### 本地开发（Mac，无需 root/VM）

```bash
# 后端（mock 模式）
cd server && DEV_MODE=1 go run . -c ../config.yaml

# 前端
cd portal && npm install && npm start

# 浏览 http://localhost:3000
```

### 构建

```bash
cd portal && npm run build          # 前端 → server/web/
cd server && go build -o transparent-proxy .  # 嵌入前端的单二进制
```

构建顺序：先前端，后后端。

## 架构

| 组件 | 技术栈 | 职责 |
|------|--------|------|
| 后端 | Go 1.25 + Gin | nftables 规则管理、健康检查、Web API |
| 前端 | Preact 10 + Vite + TypeScript | Web Components 面板（`<tp-card>` / `<tp-panel>`） |
| 防火墙 | nftables (fw4) | 流量拦截、TPROXY 透明代理重定向 |

## 核心功能

- **多级流量决策** — 通过 4 个 nft set（proxy_src/dst, direct_src/dst）+ chnroute 实现分层路由
- **健康检查** — 自动检测代理可用性，支持 SOCKS5 代理探测、Bark 推送通知
- **Web 管理** — 实时增删 IP 规则，配置编辑，无需重启
- **OpenWrt 原生集成** — fw4 自定义链注入，procd 服务管理，IPK 包分发

## API 鉴权

`config.yaml` 的 `api_key` 非空时，所有 `/api/*`（含 GET）都要求请求头 `X-Api-Key` 匹配，否则返回 401 与
`{"code":"unauthorized",...}`；留空（默认）则不鉴权，兼容旧配置。静态资源与 `/panel.js` 始终不鉴权。

```yaml
api_key: "填了才启用鉴权"
```

面板会自动带上 `localStorage['tp.apiKey']`，遇到 401/403 时在面板内弹出 key 输入框，提交后保存并重试。
未配 `api_key` 时服务启动会打醒目告警日志。

写方法（POST/PUT/PATCH/DELETE）另有两道 CSRF 兜底，挡浏览器跨站「简单请求」：

- `Content-Type` 必须是 `application/json`，否则 415 —— **配了 key 也一样校验**（net-console 反代会代为注入 key，
  光有 key 挡不住经壳打来的跨站请求）。`/api/rules/sync`、`/api/refresh-route` 虽无 body，调用方也要带这个头。
- 未配 `api_key` 时，额外比对 `Origin`/`Referer` 与 `Host`，跨站返回 403；两个头都没有则放行（curl 等）。

配置文件权限：新建用 0600（内含 `api_key`）；已存在的文件保存时沿用其现有权限，不会被 UI 保存改宽。

## Web Components 面板

前端按面板契约（net-console 仓库 `docs/panel-contract.md`）构建成单文件自包含 ES module：

- `GET /panel.js` — 注册 `<tp-card>`（只读摘要 + 代理开关）和 `<tp-panel>`（完整管理页），响应 `Cache-Control: no-cache`
- `GET /` — standalone 宿主页，加载 `/panel.js` 并挂载 `<tp-panel api-base="/api">`
- 两个元素都用 open shadow DOM，样式（Pico v2 conditional + joy tokens）全部内联进 shadow，可被 net-console 直接嵌入：

```html
<script type="module" src="/tp/panel.js"></script>
<tp-card api-base="/tp/api"></tp-card>
```

## 文档

| 文档 | 内容 |
|------|------|
| [系统架构](docs/system-architecture.md) | TPROXY 原理、流量决策、后端/前端架构、API、配置、nft 文件策略 |
| [产品需求](docs/product-requirements.md) | 功能需求、流量决策模型、IP set 设计 |
| [本地开发](docs/local-development.md) | Mac DEV_MODE 开发指南、Mock 机制 |
| [VM 测试](docs/vm-testing.md) | OpenWrt VM 三层测试手册 |
| [构建脚本](docs/build-scripts.md) | release 构建、IPK 打包、脚本说明 |

## 分发

- **单文件自举** — `scripts/build-release.sh` 构建 linux/arm64 二进制
- **IPK 包** — `scripts/build-openwrt-ipk.sh` 构建 OpenWrt 标准包
