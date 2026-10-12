# AGENTS.md

## 项目概述

OpenWrt 路由器上的透明代理管理工具，管理 nftables 规则：
- **后端** `server/`：Go（版本见 `server/go.mod`）+ Gin，管理 nftables set、出口健康检查、chnroute、配置；构建时嵌入前端
- **前端** `portal/`：Preact + TypeScript + Vite，Web Components 面板（`<tp-card>` / `<tp-panel>`），lib 模式构建成单文件 `server/web/panel.js`

相关文档：`docs/local-development.md`、`docs/vm-testing.md`、`docs/build-scripts.md`、`docs/system-architecture.md`。

## 构建与测试

### 后端（`server/`）
```bash
go build -o transparent-proxy .              # 需先构建前端
sudo ./transparent-proxy -c ../config.yaml   # 需 root；默认配置路径 /etc/transparent-proxy/config.yaml
go test ./...
go test -run ^TestName$ .
gofmt -w . && go vet ./...
```

### 前端（`portal/`）
```bash
npm install
npm start                   # dev server 127.0.0.1:3000，/api 代理到 :1444
npm run build               # 类型检查 + 构建 + build:verify → ../server/web/
npm run test:component      # Vitest 组件测试
npm run lint                # ESLint 9，--max-warnings 0

# 直连已开启鉴权的真实后端（dev 代理注入 X-Api-Key）
PORTAL_API_TARGET=http://192.168.2.1:1444 \
PORTAL_API_KEY=$(../../infra/ops/net-console-dev-env.sh transparent-proxy) \
npm run dev
```

**构建顺序**：前端先构建到 `server/web/`（`panel.js` 不入库），Go 再 `//go:embed web` 嵌入。

### Mac 本地开发（无需 root/VM）
```bash
cd server && DEV_MODE=1 go run . -c ../config.yaml   # 终端 1
cd portal && npm start                                # 终端 2，访问 http://localhost:3000
```
`DEV_MODE=1` 用内存 mock 替代 nft 命令和文件读写；chnroute 拉取仍走真实 HTTP。

### OpenWrt VM 测试
```bash
bash scripts/openwrt-vm/test-all.sh   # go test + VM 平台集成（hotplug、ipk 安装/升级/卸载、LuCI 探针）
bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready   # 单独拉起并部署 VM
```
VM 地址等契约变量见 `scripts/openwrt-vm/env.sh`（`TP_API_BASE_URL`、`TP_UI_BASE_URL` 等）。

### 打包
```bash
bash scripts/build-openwrt-ipk.sh --version v1.0.0 [--upx] [--package transparent-proxy]   # 服务 + luci-app ipk，含前端构建
bash scripts/build-release.sh --version v1.0.0     # linux/arm64 单二进制
```
`build-openwrt-ipk.sh` 默认架构 aarch64_generic，路由器是 x86_64，需设 `OPENWRT_PACKAGE_ARCH=x86_64`，否则 opkg 拒装。

## CI / 部署

Woodpecker（`.woodpecker.yaml`，push 到 `master`）：构建 x86_64 ipk（含 UPX）→ 只把服务 ipk 和 `deploy/openwrt/ci-deploy.sh` 传到路由器执行 → Bark 通知。CI 不跑测试。
- 不装 luci-app（net-console ADR-0005：LuCI 入口退役），但仍构建防烂。
- 部署逻辑必须放在 `ci-deploy.sh` 里：内联一行 ssh 要穿过 CI sh 与 busybox ash 两层引号，实测会崩。
- 传包前清空 `/tmp/tp-deploy`，否则 opkg 一次收到多个 ipk 会段错误。
- 版本号是 `0.0.0-<sha8>`，必须 `--force-downgrade`，否则 opkg 以"已是最新"拒装且返回 0。
- 验证通过才存为 lastgood（`/mnt/ext` 上），失败自动装回。

## 架构

### 后端
- `main.go` 组装依赖：`NftExecutor` / `FileStore` / `RemoteFetcher` 三个接口，生产实现 `ExecNftRunner` / `OSFileStore` / `HTTPFetcher`，DEV_MODE 与测试用 `mock.go` 的 `MemoryNft` / `MemoryFileStore` / `MemoryFetcher`
- `app.go`：`App` 生命周期 bootstrap → run（checker + chnroute + HTTP）
- `nft.go`：`NftManager` 负责 set 增删、代理开关、模板渲染（`templates/*.tmpl`，`//go:embed`）
- `checker.go`：周期健康检查并切换代理状态；`on_failure: disable|keep`，探测可走 SOCKS5，状态变化推 Bark（`bark_server` 为空则用官方 api.day.app）
- `chnroute.go`：拉 APNIC 数据生成 chnroute.nft 并定期刷新
- `api.go`：全部路由在 `/api/` 下；响应信封 `{"code", "message", "data"}`，`code` 为 `ok` 或错误码（`invalid_request`、`unauthorized`、`internal_error` 等）
- `auth.go`：
  - `X-Api-Key` 中间件作用于全部 `/api/*`，`api_key` 为空则不鉴权
  - `writeGuard`：写方法要求 `Content-Type: application/json`（415，**配了 key 也校验**——net-console 反代会代注 key，挡不住经壳打来的跨站简单请求）；未配 key 时另校验 Origin/Referer 同源（403）
- `web.go`：`/panel.js` 单列为 `Cache-Control: no-cache` 路由，必须注册在静态中间件之前，否则拿不到该头

### nft 文件
- 静态（ipk 安装到 `/etc/nftables.d/`）：`reserved_ip.nft`（保留地址 set）、`v6block.nft`（IPv6 DHCPv6 input/forward 过滤，引用 `@allow_v6_mac`）
- 运行时由模板渲染：`proxy.nft`（tproxy 链，用配置的端口/mark）、`transparent.nft`（mangle 挂钩，用配置的 LAN 接口）、`set.nft`（set 定义）

### 前端（`portal/src/`）
按面板契约（net-console 仓库 `docs/panel-contract.md`）产出单文件自包含 ES module。
- `panel.tsx` 注册 `<tp-card>`（只读摘要，不放代理开关——误碰代价大）与 `<tp-panel>`（代理开关、规则同步、设置、规则集）
- `wc/define.ts`：custom element 基类，open shadow DOM、挂载时读 `api-base`、防重复注册
- `styles/shadow.css` 用 `@layer pico, joy` 分层引入 Pico conditional + tokens + panel.css，经 `wc/styles.ts` 以 `?inline` 注入 shadow。**必须分层**：Pico 主题块选择器 `:host(:not([data-theme=dark]))`（0,2,0）压过 tokens 的裸 `:root,:host`（0,1,0），只调引入顺序无效
- `styles/tokens.css` vendor 自 net-console `assets/tokens.css`，权威版在那边，勿在此改
- `lib/api/client.ts` 自动带 `X-Api-Key`（`localStorage['tp.apiKey']`）；`ApiProvider` 在 401/403 时切到 `ApiKeyForm`，提交后重挂载重试
- 产物必须是 `server/web/panel.js` 单文件 + `index.html` 宿主页，无额外 chunk、无独立 CSS（`build:verify` 会卡住违规）

### 配置（`config.yaml`，`version: 1`）
结构见 `server/config.go`（`AppConfig`，含校验与原子保存）：`listen`（默认 `:1444`）、`api_key`、`proxy`（`lan_interface`、`default_port` 1081、`forced_port` 1082 供 proxy_src/proxy_dst、`self_mark` 255 用于排除代理自身流量）、`checker`、`nft`（`state_path`、`sets`）、`chnroute`。

## 代码约定
- Go：`fmt.Errorf("context: %w", err)` 包装错误，不忽略错误；测试用内存 mock，不碰真实 nft/文件系统
- TypeScript：`strict`、`noUnusedLocals/Parameters`，禁止 `as any`；hooks 从 `preact/hooks` 引入，回调用 `useCallback`；`@/` 别名指向 `src/`
