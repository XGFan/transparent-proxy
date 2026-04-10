# 实现文档：transparent-proxy

## 1. 现状分析

### 1.1 代码量统计

| 模块 | 生产代码 | 测试代码 | 总计 |
|------|----------|----------|------|
| Go 后端 | ~3,170 行 (17 文件) | ~2,240 行 (7 文件) | 5,412 行 |
| 前端 TS/TSX | ~730 行 | ~140 行 | 870 行 |
| 前端 CSS | 800 行 | - | 800 行 |
| **合计** | **~4,700 行** | **~2,380 行** | **~7,080 行** |

### 1.2 膨胀点识别

**后端（核心问题：间接层过多）**

| 文件 | 行数 | 问题 |
|------|------|------|
| `runtime.go` | 396 | 5 个接口 + 4 个实现，包含 enableProxy/disableProxy/flushChains 等业务逻辑混入 DI 层 |
| `runtime_mock.go` | 302 | DevMockRunner 模拟整套 nft 命令解析，本质是重新实现了 NftService |
| `runtime_test.go` | 542 | 为 DI 层写的测试，但测的是 nft 命令拼接，不是业务逻辑 |
| `config.go` | 477 | v2→v3 legacy 迁移、netguard 兼容，已无必要 |
| `api_contract_test.go` | 590 | 用又一套 contractRunner mock 测 API |
| `api_response.go` | 98 | 响应封装过度抽象 |

**问题总结**：
1. **DI 层（Runtime）承担了业务逻辑**：`enableProxy()`、`disableProxy()` 是核心功能，却藏在 Runtime 里作为"基础设施"
2. **Mock 分裂**：`runtime_mock.go`（DevMode）、`runtime_test.go` 里的 fakeRunner、`api_contract_test.go` 里的 contractRunner — 三套 mock 做类似的事
3. **Config legacy 包袱**：v2 迁移代码占 config.go 近 1/3
4. **API 文件碎片化**：7 个 api_*.go 文件，多数不到 60 行
5. **依赖过重**：`gin` (HTTP 框架) + `ajson` (JSON path) + `go-utils` + `netguard` + `ipset`，其中后三个几乎没用到核心功能

**前端**：
1. `StatusPage.tsx` 490 行单文件，混合了代理控制、checker 配置、rules 管理
2. `StatusPage.css` 718 行手写 CSS
3. React 18 对单页面状态面板过重

### 1.3 设计优点（应保留）

- nft 规则设计本身合理：分层决策、双端口、reserved_ip/chnroute
- DEV_MODE mock 开发的思路正确
- 前端嵌入 Go 二进制的部署模式好
- 测试覆盖面广

## 2. 方案选择：重构

**不选重写的原因**：核心 nft 规则设计和业务逻辑是正确的，问题在于代码组织和间接层过多，不需要从零开始。

**不选仅优化的原因**：DI 架构和 mock 策略需要根本性调整，小修小补无法解决结构问题。

**重构策略**：保留业务逻辑，重新组织代码结构，削减间接层，换掉过重的依赖。

## 3. 目标架构

### 3.1 后端（Go）

```
server/
├── main.go              # 入口、信号处理、DEV_MODE
├── config.go            # YAML 配置（去掉 legacy 迁移）
├── nft.go               # nft 命令执行 + set/chain 管理（合并 NftService + Runtime 的 nft 部分）
├── checker.go           # 健康检查 + 代理开关联动
├── chnroute.go          # APNIC 数据拉取 + nft set 生成
├── api.go               # 所有 HTTP 路由和 handler（合并 7 个 api_*.go）
├── mock.go              # 统一的 mock 实现（DEV_MODE + 测试共用）
├── app.go               # App 生命周期（精简）
├── web.go               # 前端静态文件嵌入
├── *_test.go            # 测试
└── web/                 # 嵌入的前端产物
```

**关键变化**：

#### (a) 消除 Runtime 间接层

现在：
```
App → Runtime{Runner, Fetcher, Files} → NftService → nft commands
App → Runtime.enableProxy() / disableProxy()
```

目标：
```
App → NftManager → nft commands / enable / disable
App → Checker → NftManager.SetProxyEnabled()
```

只保留两个接口用于 mock：

```go
// NftExecutor 执行 nft 命令
type NftExecutor interface {
    Run(args ...string) ([]byte, error)
}

// FileStore 读写持久化文件（nft set 状态、代理开关持久化）
type FileStore interface {
    WriteFile(path string, data []byte, perm os.FileMode) error
    ReadFile(path string) ([]byte, error)
    RemoveFile(path string) error
}

// NftManager 包含所有 nft 操作（set 管理 + 代理开关 + 持久化）
type NftManager struct {
    exec  NftExecutor
    files FileStore
    mu    sync.Mutex
}
```

生产环境：`NftExecutor` = exec.Command("nft", ...)，`FileStore` = os 调用
DEV_MODE / 测试：两者都用内存 mock

**Set 操作的持久化流程**：
```
AddToSet("proxy_src", "1.2.3.4/32")
  → nft add element inet fw4 proxy_src {1.2.3.4/32}
  → nft list set inet fw4 proxy_src
  → files.WriteFile("state_path/proxy_src.nft", formatted_output)
```
每次增删都自动写文件，不再需要手动 sync。fw4 重启时加载 `state_path/*.nft` 恢复。

#### (b) 简化配置

```yaml
version: 1
listen: ":1444"

proxy:
  lan_interface: br-lan         # LAN 桥接接口名
  default_port: 1081            # 默认代理端口（代理软件内部再分流）
  forced_port: 1082             # 强制代理端口（proxy_src/proxy_dst 命中的流量）
  self_mark: 255                # 代理软件自身流量的 fwmark（十进制），防环用

checker:
  enabled: true
  url: "https://www.google.com"
  method: HEAD
  timeout: 10s
  interval: 30s
  failure_threshold: 3
  on_failure: disable           # "disable" | "keep"
  proxy: ""                     # 可选 SOCKS5

nft:
  state_path: /etc/nftables.d
  sets: [direct_src, direct_dst, proxy_src, proxy_dst, allow_v6_mac]

chnroute:
  auto_refresh: true
  refresh_interval: 168h        # 7 天
```

由于端口、接口名、mark 值可配置，`proxy.nft` 和 `transparent.nft` 不再是静态文件，**改为 Go 模板渲染**：

**模板策略**：

| 文件 | 是否动态 | 处理方式 |
|------|----------|----------|
| `proxy.nft` | 是（端口、mark） | Go 模板渲染，写入 `state_path/` |
| `transparent.nft` | 是（接口名） | Go 模板渲染，enable 时包裹为 full 版本加载 + 写入 table-post |
| `reserved_ip.nft` | 否 | 静态文件，IPK 直接安装到 `state_path/` |
| `v6block.nft` | 否 | 静态文件，IPK 直接安装到 `state_path/` |

**模板来源**：源码树中保留 `.nft.tmpl` 文件（`server/templates/`），通过 `//go:embed` 编译进二进制。IPK 不安装模板文件。

```
server/templates/
├── proxy.nft.tmpl          # 引用 {{.DefaultPort}}, {{.ForcedPort}}, {{.SelfMark}}
└── transparent.nft.tmpl    # 引用 {{.LanInterface}}
```

**启动流程**：
1. 读取 config → 渲染模板 → 写入 `state_path/proxy.nft`
2. `nft -f state_path/proxy.nft` 加载规则链（确保首次启动和配置变更都生效）
3. 后续 fw4 重启时自动从 `state_path/` 加载持久化的渲染结果

去掉：legacy 迁移、netguard 兼容、server 嵌套层。
新增：`checker.on_failure`（disable/keep）、`chnroute` 配置段。

#### (c) 精简依赖

| 现有依赖 | 处理 | 替代 |
|-----------|------|------|
| gin | 替换 | net/http + 轻量 mux（或保留 gin，视实际权衡） |
| ajson (JSON path) | 移除 | encoding/json + 手写结构体 |
| go-utils | 移除 | 内联需要的极少功能 |
| netguard | 移除 | 已无使用场景 |
| ipset | 移除 | NftManager 直接管理 |
| yaml.v3 | 保留 | - |

> **gin 的取舍**：gin 的实际价值是路由、JSON 绑定、中间件。用 `net/http` 替代需要自己写 JSON 响应 helper，代码量差距不大。如果目标是最小化依赖可以换掉，如果目标是少写 boilerplate 可以保留。**建议保留 gin**，它是成熟的选择，不是膨胀的原因。

#### (d) Mock 统一

一套 mock 服务三个场景：

```go
// MemoryNft 模拟 nft 命令执行（DEV_MODE + 测试共用）
type MemoryNft struct {
    sets         map[string]map[string]struct{}
    proxyEnabled bool
}

func (m *MemoryNft) Run(args ...string) ([]byte, error) {
    // 解析 nft 子命令，操作内存状态
}

// MemoryFileStore 模拟文件读写（DEV_MODE + 测试共用）
type MemoryFileStore struct {
    files map[string][]byte
}
```

- `DEV_MODE`：`main.go` 中创建 `MemoryNft` + `MemoryFileStore` 注入
- 单元测试：测试中创建同样的 mock 注入
- 不再需要 fakeRunner、contractRunner、recordingRunner、DevFileWriter 多套 mock

### 3.2 前端（Preact + Vite）

```
portal/
├── src/
│   ├── main.tsx
│   ├── app.tsx               # 布局
│   ├── api.ts                # API client（精简）
│   ├── components/
│   │   ├── ProxyToggle.tsx   # 代理开关
│   │   ├── CheckerCard.tsx   # 健康检查状态 + 配置
│   │   ├── RuleSets.tsx      # IP set 管理
│   │   └── StatusBadge.tsx   # 状态指示器
│   └── app.css               # 样式（精简）
├── index.html
├── vite.config.ts
├── tsconfig.json
└── package.json
```

**关键变化**：
- React → **Preact**（3KB，API 兼容）
- 490 行 StatusPage 拆成 3-4 个组件
- 718 行 CSS 精简（考虑用 CSS 变量减少重复）

### 3.3 文件结构总览

```
transparent-proxy/
├── server/               # Go 后端（目标 ~2000 行生产代码）
├── portal/               # Preact 前端
├── files/                # 部署资产（IPK 直接安装到 OpenWrt 对应路径）
│   └── etc/
│       ├── nftables.d/
│       │   ├── proxy.nft          # 核心 tproxy 规则链（引用所有 set）
│       │   ├── reserved_ip.nft    # RFC 保留地址 set（静态）
│       │   └── v6block.nft        # IPv6 过滤规则链（引用 @allow_v6_mac，不含 set 定义）
│       ├── transparent-proxy/
│       │   ├── transparent.nft    # 唯一规则源：持久化 + Go 动态包裹为 full 版本加载
│       │   └── config.yaml        # 默认配置（conffile，升级保留）
│       ├── init.d/
│       │   └── transparent-proxy  # procd 服务脚本
│       └── hotplug.d/iface/
│           └── 80-ifup-wan        # WAN 策略路由 hotplug
├── openwrt/              # OpenWrt 打包定义
│   ├── transparent-proxy/        # 主服务包（二进制 + files/）
│   │   ├── Makefile
│   │   └── files/                # postinst, postrm, conffiles
│   └── luci-app-transparent-proxy/  # LuCI 菜单入口包
│       ├── Makefile
│       ├── htdocs/               # LuCI 前端 JS
│       └── root/                 # 菜单 + ACL JSON
├── scripts/
│   └── build-openwrt-ipk.sh     # 本地 IPK 打包脚本
├── config.yaml           # 开发用配置模板
└── docs/                 # 文档
```

**files/ 目录说明**：这些文件由 IPK 直接安装到 OpenWrt 对应路径。Go 程序运行时读取/操作这些文件（加载 nft 规则、写 set 状态），但不负责安装它们。

**nft 文件改进**：

1. **v6block.nft 重写**：改为 forward 链过滤（阻止非白名单设备的 IPv6 转发）+ input 链阻止 DHCPv6，不再阻止 ICMPv6（保留链路本地 IPv6 功能）
   - `v6block.nft`（managed）只包含规则链定义，引用 `@allow_v6_mac` set
   - `allow_v6_mac.nft`（运行时生成）由 Go 程序管理 set 定义和元素，与 `proxy_src.nft` 等同等待遇

2. **删除 `transparent_full.nft`**：Go 在 enable 时读取 `transparent.nft`，动态包裹 `table inet fw4 { ... }` 生成临时文件用 `nft -f` 加载，消除两份文件的同步维护

3. **proxy.nft mask 链加注释**：说明 proxy_dst/proxy_src 省略的原因（标记后重新进入 transparent_proxy 链做完整决策）

**删除的文件**：
- `transparent_full.nft` — Go 动态生成，不再需要独立文件
- `enable.sh` / `disable.sh` — 逻辑由 Go 代码直接执行 nft 命令完成
- `managed-manifest.json` + `generate-manifest.sh` — 不再需要，文件管理由 opkg 原生机制处理

## 4. 重构步骤

### Phase 1: 后端精简

1. **新建 `nft.go`**：合并 `nft_service.go` + `runtime.go` 中的 nft 操作，统一接口为 `NftExecutor` + `FileStore`
2. **重写 `config.go`**：删除 legacy 迁移，新增 `chnroute` 和 `checker.on_failure` 配置
3. **合并 API 文件**：7 个 `api_*.go` → 1 个 `api.go`
4. **重写 `checker.go`**：从 `checker_service.go` 精简，支持 `on_failure: keep` 模式
5. **提取 `chnroute.go`**：从 API handler 中独立出来，加入定时刷新逻辑
6. **统一 mock**：`mock.go` 中 `MemoryNft` + `MemoryFileStore`，删除所有旧 mock
7. **精简 `app.go`**：移除 Runtime 概念，直接持有 NftManager + Checker
8. **清理依赖**：移除 ajson、go-utils、netguard、ipset

### Phase 2: 前端迁移

1. React → Preact（修改 vite.config.ts + package.json）
2. 拆分 StatusPage 为 ProxyToggle / CheckerCard / RuleSets
3. 精简 CSS
4. 更新 API client 适配后端变更（新增 allow_v6_mac 管理）

### Phase 3: 测试重建

1. 基于 `MemoryNft` + `MemoryFileStore` 重写后端测试
2. 验证所有 API contract
3. 前端组件测试

### Phase 4: 打包与部署资产

1. **拆分 `v6block.nft`**：规则链留在 `v6block.nft`（managed），`allow_v6_mac` set 定义移到运行时生成
2. **删除 `enable.sh` / `disable.sh`**：逻辑已在 Go 中
3. **删除 manifest 系统**：移除 `managed-manifest.json` + `generate-manifest.sh`
4. **重写 `build-openwrt-ipk.sh`**：
   - 删除 SDK 模式，只保留本地 Minimal Packager
   - IPK 直接打包 `files/` 下所有文件到对应路径（不再依赖 bootstrap 自释放）
   - 声明 `conffiles`：`/etc/transparent-proxy/config.yaml`
5. **简化 postinst**：只需 `enable + start` 服务（不再启动二进制等 30 秒 bootstrap）
6. **简化 postrm**：只清理运行时生成的文件（`proxy_src.nft` 等用户 set、`chnroute.nft`、`table-post/transparent.nft`）
7. **保留 luci-app-transparent-proxy 包**：无变化

### Phase 5: 删除 SDK 模式相关

1. 删除 `openwrt/transparent-proxy/Makefile` 中 SDK 相关逻辑（如果有）
2. 删除 `scripts/build-release.sh` 中的 manifest 依赖
3. 清理 CI 配置（`.drone.yml`）中不再需要的步骤

## 5. 预期效果

| 指标 | 现状 | 目标 |
|------|------|------|
| Go 生产代码 | ~3,170 行 / 17 文件 | ~1,800 行 / 8 文件 |
| Go 测试代码 | ~2,240 行 / 7 文件 | ~1,200 行 / 4 文件 |
| Mock 实现 | 3 套 | 1 套 |
| Go 依赖（direct） | 7 个 | 2-3 个（gin + yaml） |
| 前端框架 | React (44KB) | Preact (3KB) |
| 前端主组件 | 490 行单文件 | 3-4 个组件，各 ~100 行 |
| 接口/抽象层 | 5 个接口 | 2 个接口 (NftExecutor + FileStore) |
| 打包 postinst | 30 行 bootstrap 逻辑 | ~5 行 enable+start |
| Manifest 系统 | generate-manifest.sh + JSON | 删除，opkg 原生管理 |
| nft 文件安装 | 二进制自释放 | IPK 直接安装 |
