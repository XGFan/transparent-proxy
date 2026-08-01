# 系统架构文档：transparent-proxy

## 概述

本项目是一个 OpenWrt 透明代理管理工具，采用**前后端分离架构**，通过管理 Linux **nftables sets** 实现透明代理规则的动态配置。

### 核心组件

| 组件 | 技术栈 | 职责 |
|------|--------|------|
| 后端 | Go 1.25 + Gin | nftables 规则管理、健康检查、Web API |
| 前端 | Preact 10 + TypeScript | 管理界面、规则配置、状态监控 |
| 防火墙 | nftables (fw4) | 流量拦截、透明代理重定向 |

### 代码量统计

| 模块 | 生产代码 | 测试代码 | 总计 |
|------|----------|----------|------|
| Go 后端 | ~1,700 行（9 文件） | ~1,350 行（4 文件） | ~3,050 行 |
| 前端 TSX/TS | ~740 行 | — | ~740 行 |
| 前端 CSS | ~860 行 | — | ~860 行 |
| **合计** | **~3,300 行** | **~1,350 行** | **~4,650 行** |

---

## 透明代理原理

### 核心技术：TPROXY

本项目使用 **TPROXY**（透明代理）实现透明代理，这是 nftables 提供的一种机制。

#### TPROXY 与 REDIRECT 对比

| 特性 | TPROXY | REDIRECT |
|------|--------|----------|
| 原理 | 不修改 IP 头，直接投递到本地 socket | DNAT 的特殊形式，将目标 IP 改为本地 |
| 协议支持 | TCP + UDP 完整支持 | 主要 TCP，UDP 处理复杂 |
| 原始目的地 | 直接通过 `getsockname()` 获取 | 需要 `getsockopt(SO_ORIGINAL_DST)` |
| 依赖项 | 策略路由 + fwmark | 无额外依赖 |

**选择 TPROXY 的原因**：
1. 完整的 UDP 支持（现代代理协议如 QUIC 所需）
2. 保留原始目的 IP，代理逻辑更简洁
3. 更符合现代透明代理架构

### 流量拦截流程

```
+---------------------------------------------------------------------+
|                        OpenWrt 路由器                               |
+---------------------------------------------------------------------+
|                                                                     |
|  br-lan (LAN 接口)                                                  |
|       |                                                             |
|       v                                                             |
|  +---------------------+                                            |
|  | mangle_prerouting   |  <-- PREROUTING hook（mangle 优先级）      |
|  | chain transparent_proxy                                          |
|  +----------+----------+                                            |
|             |                                                       |
|    +--------+--------+                                              |
|    |                 |                                              |
|    v                 v                                              |
|  匹配规则          匹配规则                                          |
|  （按优先级）      （按优先级）                                       |
|    |                 |                                              |
|  +-+-+             +-+-+                                            |
|  |   |             |   |                                            |
|  v   v             v   v                                            |
|RETURN TPROXY    RETURN TPROXY                                       |
|（直连）（代理）  （直连）（代理）                                      |
|  |     |           |     |                                          |
|  v     v           v     v                                          |
|正常    代理       正常    代理                                        |
|路由    进程       路由    进程                                        |
|      (1081/1082)       (1081/1082)                                  |
|                                                                     |
|  +-----------------------------------------------------------+      |
|  |         策略路由（fwmark=1 -> table 100）                  |      |
|  |   ip rule add fwmark 1 table 100                          |      |
|  |   ip route add local 0.0.0.0/0 dev lo table 100           |      |
|  +-----------------------------------------------------------+      |
|                                                                     |
+---------------------------------------------------------------------+
```

### nftables 规则链详解

#### 核心规则链（`proxy.nft`）

```nftables
chain transparent_proxy {
    mark 0xff return                                    # 排除代理自身
    ip daddr @reserved_ip return                        # 保留地址直连
    meta l4proto {tcp, udp} ip saddr @proxy_src         # 按源 IP 强制代理
        mark set 1 tproxy ip to 127.0.0.1:1082 accept
    ip saddr @direct_src return                         # 按源 IP 强制直连
    meta l4proto {tcp, udp} ip daddr @proxy_dst         # 按目的 IP 强制代理
        mark set 1 tproxy ip to 127.0.0.1:1082 accept
    ip daddr @direct_dst return                         # 按目的 IP 强制直连
    ip daddr @chnroute return                           # 国内 IP 直连
    meta l4proto {tcp, udp}                             # 默认代理
        mark set 1 tproxy ip to 127.0.0.1:1081 accept
}

chain transparent_proxy_mask {
    mark 0xff return                                    # 排除代理自身
    oifname "lo" return                                 # 排除回环接口
    ip daddr @reserved_ip return                        # 保留地址直连
    ip daddr @direct_dst return                         # 按目的 IP 直连
    ip daddr @chnroute return                           # 国内 IP 直连
    meta l4proto {tcp, udp} mark set 1 accept           # 标记待代理流量
}
```

#### 规则优先级（从高到低）

```
优先级  规则                        动作
========================================================================
  1     mark=0xff（代理自身）    -> RETURN（直连）
  2     目的 IP 在 reserved_ip  -> RETURN（直连）
  3     源 IP 在 proxy_src      -> TPROXY -> 代理
  4     源 IP 在 direct_src     -> RETURN（直连）
  5     目的 IP 在 proxy_dst    -> TPROXY -> 代理
  6     目的 IP 在 direct_dst   -> RETURN（直连）
  7     目的 IP 在 chnroute     -> RETURN（国内直连）
  8     其他所有流量             -> TPROXY -> 代理（默认）
```

### 四个 IP Sets

本项目使用 **nftables sets**（非 ipset）进行高效 IP 匹配：

| Set 名称 | 用途 | 示例 |
|----------|------|------|
| `proxy_src` | 按源 IP 强制代理 | 特定 LAN 设备 |
| `direct_src` | 按源 IP 强制直连 | 服务器、IoT 设备 |
| `proxy_dst` | 按目的 IP 强制代理 | 被封锁网站的 IP |
| `direct_dst` | 按目的 IP 强制直连 | 特定服务 IP |

**Sets 定义示例**（`proxy_dst.nft`）：
```nftables
set proxy_dst {
    type ipv4_addr
    flags interval      # 支持 CIDR 范围
    auto-merge          # 自动合并相邻范围
}
```

### 策略路由配置

TPROXY 必须配合策略路由使用：

```bash
# WAN 接口上线时自动配置（80-ifup-wan）
ip rule add fwmark 1 table 100
ip route add local 0.0.0.0/0 dev lo table 100
```

**工作原理**：
1. `nftables` 将需要代理的流量标记为 `mark=1`
2. `ip rule` 匹配 `fwmark=1` 的数据包，使用路由表 100
3. 路由表 100 将所有流量路由到 `lo`（回环接口）
4. TPROXY 在 `mangle_prerouting` 链将流量投递到代理进程

---

## 流量路由策略

### 路由决策逻辑图

```
                    +-------------------+
                    |  LAN 流量进入     |
                    |    (br-lan)       |
                    +--------+----------+
                             |
                             v
                    +-------------------+
                    |   mark=0xff?      |
                    | （代理自身）       |
                    +--------+----------+
                             |
               +-------------+-------------+
               | YES                       | NO
               v                           v
          +---------+              +-------------+
          |  RETURN |              | 目的 IP 在  |
          | （直连） |              | reserved_ip?|
          +---------+              +------+------+
                                          |
                             +------------+------------+
                             | YES                      | NO
                             v                          v
                        +---------+            +-------------+
                        | RETURN  |            | 源 IP 在    |
                        | （直连） |            | proxy_src?  |
                        +---------+            +------+------+
                                                     |
                                   +-----------------+-----------------+
                                   | YES                               | NO
                                   v                                   v
                              +----------+                     +-------------+
                              |  TPROXY  |                     | 源 IP 在    |
                              | -> 代理  |                     | direct_src? |
                              |  :1082   |                     +------+------+
                              +----------+                            |
                                                     +----------------+-----------------+
                                                     | YES                              | NO
                                                     v                                  v
                                                +---------+                      +-------------+
                                                | RETURN  |                      | 目的 IP 在  |
                                                | （直连） |                      | proxy_dst?  |
                                                +---------+                      +------+------+
                                                                                      |
                                                                    +-----------------+-----------------+
                                                                    | YES                               | NO
                                                                    v                                   v
                                                               +----------+                      +-------------+
                                                               |  TPROXY  |                      | 目的 IP 在  |
                                                               | -> 代理  |                      | direct_dst? |
                                                               |  :1082   |                      +------+------+
                                                               +----------+                             |
                                                                                     +----------------+-----------------+
                                                                                     | YES                              | NO
                                                                                     v                                  v
                                                                                +---------+                     +-------------+
                                                                                | RETURN  |                     | 目的 IP 在  |
                                                                                | （直连） |                     | chnroute?   |
                                                                                +---------+                     +------+------+
                                                                                                                     |
                                                                                          +--------------------------+--------------------------+
                                                                                          | YES                                                 | NO
                                                                                          v                                                     v
                                                                                     +---------+                                          +----------+
                                                                                     | RETURN  |                                          |  TPROXY  |
                                                                                     | （国内  |                                          | -> 代理  |
                                                                                     |  直连） |                                          |  :1081   |
                                                                                     +---------+                                          +----------+
```

### 代理端口说明

| 端口 | 用途 |
|------|------|
| `1081`（default_port） | 默认代理端口（处理非国内流量） |
| `1082`（forced_port） | 强制代理端口（处理 proxy_src/proxy_dst 流量） |

**双端口设计目的**：
- 区分"规则匹配代理"与"强制代理"流量
- 支持不同的代理策略（如不同出口节点）

---

## 系统架构

### 后端架构

```
+------------------------------------------------------------------+
|                      App（app.go）                                |
|                         :1444                                    |
+------------------------------------------------------------------+
|                                                                  |
|  +----------------+  +-----------+  +--------------------+       |
|  | NftManager     |  | Checker   |  | ChnRouteManager    |       |
|  |                |  |           |  |                    |       |
|  | - GetSet       |  | - Start() |  | - EnsureExists()   |       |
|  | - AddToSet     |  | - Status()|  | - StartPeriodic    |       |
|  | - RemoveFromSet|  | - SetProxy|  |   Refresh()        |       |
|  | - SyncAllSets  |  |   Enabled |  |                    |       |
|  | - EnableProxy  |  |           |  |                    |       |
|  | - DisableProxy |  |           |  |                    |       |
|  +------+---------+  +-----+-----+  +--------+-----------+       |
|         |                  |                 |                   |
|         v                  |                 v                   |
|  +----------------+        |        +-------------------+        |
|  | NftExecutor    |        |        | RemoteFetcher     |        |
|  | （接口）        |        |        | （接口）           |        |
|  | ExecNftRunner  |        |        | HTTPFetcher       |        |
|  | MemoryNft      |        |        | MemoryFetcher     |        |
|  +----------------+        |        +-------------------+        |
|  +----------------+        |                                     |
|  | FileStore      |<-------+                                     |
|  | （接口）        |                                              |
|  | OSFileStore    |                                              |
|  | MemoryFileStore|                                              |
|  +----------------+                                              |
|                                                                  |
+------------------------------------------------------------------+
```

`Checker` 驱动代理状态变更。健康检查成功时调用 `NftManager.EnableProxy()`；连续失败达到阈值时，若 `on_failure` 为 `"disable"` 则调用 `NftManager.DisableProxy()`。代理启用状态通过读取 nft 链内容确定（检查 `mangle_prerouting` / `mangle_output` 是否包含跳转规则），而非硬编码标志。

### 文件结构

```
server/
├── main.go          # 68 行  — CLI 入口、信号处理、DEV_MODE 注入
├── app.go           # 109 行 — App 生命周期：bootstrap → run
├── config.go        # 319 行 — YAML 配置解析、验证、原子保存
├── nft.go           # 478 行 — NftManager：set 管理、代理开关、模板渲染
├── checker.go       # 415 行 — 健康检查、代理联动、Bark 通知、SOCKS5 支持
├── chnroute.go      # 136 行 — APNIC 数据拉取、chnroute.nft 生成、定时刷新
├── api.go           # 492 行 — 所有 HTTP 路由和 handler（单文件）
├── auth.go          # 32 行  — X-Api-Key 中间件（constant-time 比较）
├── mock.go          # 268 行 — MemoryNft + MemoryFileStore + MemoryFetcher
├── web.go           # 35 行  — 前端 embed.FS + /panel.js（no-cache）
├── config_test.go   # 234 行
├── nft_test.go      # 350 行
├── checker_test.go  # 364 行
├── api_test.go      # 435 行
├── auth_test.go     # 130 行
└── templates/
    ├── proxy.nft.tmpl        # 引用 {{.SelfMark}}, {{.ForcedPort}}, {{.DefaultPort}}
    ├── transparent.nft.tmpl  # 引用 {{.LanInterface}}
    └── set.nft.tmpl          # set 定义模板
```

### 核心组件

#### NftManager（nft.go）

管理所有 nftables 操作，依赖两个可 mock 接口：

```go
type NftExecutor interface {
    Run(args ...string) ([]byte, error)
}

type FileStore interface {
    WriteFile(path string, data []byte, perm os.FileMode) error
    ReadFile(path string) ([]byte, error)
    RemoveFile(path string) error
}
```

主要操作：
- `EnsureSetsExist` -- 检查并创建缺失的 nft set
- `AddToSet` / `RemoveFromSet` -- 增删元素，每次操作后自动 `syncSetToFile`
- `SyncAllSets` -- 将所有 set 状态持久化到 `state_path/*.nft`
- `EnableProxy` -- 渲染 `transparent.nft.tmpl`，包裹为 table 声明后 `nft -f` 加载，将 partial 写入 table-post 持久化
- `DisableProxy` -- flush mangle 链，删除 table-post 文件
- `ProxyEnabled` -- 读取 `mangle_prerouting` / `mangle_output` 链内容判断状态
- `RenderAndLoadProxyRules` -- 渲染 `proxy.nft.tmpl`，写入 `state_path/proxy.nft` 并 `nft -f` 加载

JSON 解析使用 `gjson`（tidwall），避免手写 nft JSON 结构体。

生产实现：`ExecNftRunner`（exec.Command）+ `OSFileStore`（os 调用）

#### Checker（checker.go）

定期向配置的 URL 发起健康检查，联动控制代理开关。

- 支持 SOCKS5 代理（`checker.proxy` 字段），通过 `golang.org/x/net/proxy` 构造 Dialer
- 支持 Bark 推送通知（`checker.bark_token` 字段）：故障达阈值时发送"禁用/保持代理"通知，恢复时发送"恢复"通知，通知去重（`notifiedDown` 标记）
- `on_failure: disable` -- 连续失败达阈值后禁用代理
- `on_failure: keep` -- 达阈值后发通知但不改变代理状态
- `UpdateConfig` -- 重启检查循环（原子替换 cancel 函数）
- `SetProxyEnabled` -- 手动切换代理状态（API 调用路径）

```go
type CheckerConfig struct {
    Enabled          bool   // 是否启用
    Method           string // GET 或 HEAD
    URL              string // 检查目标 URL
    Host             string // 可选 Host 头覆盖
    Timeout          string // 单次请求超时，如 "10s"
    Interval         string // 检查间隔，如 "30s"
    FailureThreshold int    // 触发动作所需连续失败次数
    OnFailure        string // "disable"（禁用代理）或 "keep"（保持代理）
    Proxy            string // 可选 SOCKS5 代理地址，如 "127.0.0.1:1080"
    BarkToken        string // 可选 Bark 推送 token
}
```

**检查逻辑**：
- 成功时：重置失败计数，若代理未启用则调用 `EnableProxy()`，若之前已发过失败通知则发送 Bark 恢复通知
- 失败时：递增失败计数；达到阈值后发送 Bark 失败通知（仅一次）；若 `on_failure == "disable"` 则调用 `DisableProxy()`

#### App（app.go）

```
App.Bootstrap()
  → EnsureSetsExist → SyncAllSets → RenderAndLoadProxyRules

App.Run(ctx)
  → Checker.Start(ctx)
  → ChnRouteManager.EnsureExists() + StartPeriodicRefresh(ctx)
  → gin HTTP server（前端静态文件 + /api/ 路由）
```

`App` 直接持有 `*NftManager`、`*Checker`、`*ChnRouteManager`，无 Runtime 间接层。

#### ChnRouteManager（chnroute.go）

从 APNIC 拉取数据生成 `chnroute.nft`，写入 `state_path/`。支持定时自动刷新（`chnroute.refresh_interval`，默认 168h）。

#### Mock（mock.go）

一套 mock 服务 DEV_MODE 和全部测试：

- `MemoryNft` -- 内存中模拟 nft 命令（set 增删查、chain list/flush、`-f` 文件加载）
- `MemoryFileStore` -- 内存文件读写，提供 `GetFile` 用于测试断言
- `MemoryFetcher` -- 模拟 HTTP 拉取（用于 chnroute 测试）

### 依赖（go.mod）

| 包 | 用途 |
|----|------|
| `github.com/gin-gonic/gin` | HTTP 框架，路由 + JSON 绑定 |
| `github.com/gin-contrib/static` | 前端静态文件服务 |
| `github.com/tidwall/gjson` | nft JSON 输出解析 |
| `golang.org/x/net` | SOCKS5 proxy dialer |
| `gopkg.in/yaml.v3` | 配置文件解析 |

---

## API

所有路由注册在 `/api/` 下，响应体统一使用信封格式：

```json
{"code": "ok"|"invalid_request"|"unauthorized"|"internal_error", "message": "...", "data": {...}}
```

### 鉴权（auth.go）

`config.api_key` 非空时，`apiKeyAuth` 中间件对**全部** `/api/*`（含 GET）校验请求头 `X-Api-Key`，
用 `crypto/subtle.ConstantTimeCompare` 比较，不匹配返回 401 + `code: unauthorized`。
`api_key` 为空（默认）时中间件直接放行，兼容旧配置。静态资源与 `/panel.js` 不经过该中间件。

`api_key` 不在 `GET/PUT /api/config` 的 `editableConfig` 里，既不会被读出，也不会被配置更新清空。
`api_key` 为空时，服务启动会在日志里打一段醒目告警。

### 写方法的 CSRF 兜底（auth.go `writeGuard`）

POST/PUT/PATCH/DELETE 额外过两道（形状对齐 dns-switchy 的 `guardWrite`）：

1. **`Content-Type` 必须是 `application/json`**（忽略 `charset` 等参数），否则 415 + `code: unsupported_media_type`。
   **始终生效，配了 `api_key` 也不跳过** —— 跨站「简单请求」只能把 Content-Type 设成
   `text/plain` / `application/x-www-form-urlencoded` / `multipart/form-data`，要求 JSON 即可逼出预检，
   而本服务不回 CORS 头。这条不能省：net-console 反代会**代为注入 api-key**，
   所以「配了 key」挡不住经壳打过来的跨站简单请求。
2. **未配 `api_key` 时**再比对 `Origin`（优先）/`Referer` 的 host 与请求 `Host`，不一致返回 403 + `code: forbidden`。
   两个头都没有时放行（curl 等非浏览器客户端）。配了 key 之后不再校验同源：浏览器跨站请求
   带不上自定义头 `X-Api-Key`，鉴权本身是更强的防护，继续卡同源反而会挡掉反代宿主。

实际能被简单请求打到的只有 4 条 POST（`/rules/add`、`/rules/remove`、`/rules/sync`、`/refresh-route`；
PUT 必定预检），但校验按方法统一加，不针对具体路径。注意 `/rules/sync` 与 `/refresh-route` 没有 body，
调用方也必须带上 `Content-Type: application/json`。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/status` | 代理状态 + checker 状态 + 所有 set 内容 |
| GET | `/api/ip` | 返回请求方 IP |
| GET/PUT | `/api/config` | 读写全局配置（proxy + checker + chnroute） |
| GET/PUT | `/api/checker` | 读写 checker 配置 |
| PUT | `/api/proxy` | 切换代理开关（`{"enabled": true/false}`） |
| GET | `/api/rules` | 列出所有 set 内容 |
| POST | `/api/rules/add` | 向 set 添加元素 |
| POST | `/api/rules/remove` | 从 set 删除元素 |
| POST | `/api/rules/sync` | 将所有 set 持久化到文件 |
| POST | `/api/refresh-route` | 立即刷新 chnroute |

非 `/api/` 的两个入口：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/panel.js` | 面板 bundle，`Cache-Control: no-cache`，不鉴权（net-console 用它探活） |
| GET | `/` | standalone 宿主页，加载 `/panel.js` 并挂 `<tp-panel api-base="/api">` |

`/panel.js` 由 `servePanelJS()` 中间件处理，必须注册在 `static.Serve` 之前，否则静态中间件会先命中该文件、拿不到 no-cache 头。

---

## 前端

框架：Preact 10（单一 `preact` 依赖），hooks 从 `preact/hooks` 导入。按面板契约（net-console 仓库
`docs/panel-contract.md`）用 Vite lib 模式构建成**单文件自包含 ES module** `server/web/panel.js`，
由 Go 二进制通过 `embed.FS` 嵌入。开发时 Vite dev server（:3000）将 `/api` 代理到 `:1444`。

注册两个 custom element：

| 元素 | 内容 |
|------|------|
| `<tp-card>` | 只读摘要（代理状态、checker 状态、规则条数）+ 代理开关这一个高频操作 |
| `<tp-panel>` | 全部管理能力：status / rules / checker / config |

两者都是 open shadow DOM，样式全部内联进 shadow（Pico v2 **conditional** 构建，作用域 `.pico` 容器，
无 `:root`/`body` 全局选择器；叠 vendored `tokens.css` 与组件自有 `panel.css`，用 `?inline` 导入避免产出独立 CSS）。

**样式必须分层，不能靠引入顺序**（`styles/shadow.css`）：Pico v2.1.1 把颜色变量定义在
`:host(:not([data-theme=dark]))`（特异性 0,2,0），而 `tokens.css` 的 `--pico-*` 映射写在裸 `:root,:host`（0,1,0），
无论谁先谁后都被压住，表现为主按钮/开关是 Pico 默认 azure 而非 `--joy-accent`。
解法是 `@layer pico, joy` 把 Pico 压进低层——层序优先于特异性。
不选「把映射挪到 `.pico` 容器」的原因：Pico 有 9 个变量的值是 `var(--pico-primary*)`
（`primary-border`、`switch-checked-background-color`、`form-element-focus-color` 等），
它们在 `:host` 上就完成了变量代入，只覆盖容器的话背景色变了但边框/开关/聚焦圈仍是 azure。
（见 net-console `docs/panel-contract.md`「特异性陷阱」。）
`api-base` 在 `connectedCallback` 读取一次；`customElements.get()` 守卫防重复注册。

鉴权：请求自动带 `localStorage['tp.apiKey']` 作为 `X-Api-Key`；收到 401/403 时 `ApiProvider` 改渲染
key 输入界面，提交后写 localStorage 并重挂载子树重试。console 宿主下反代在服务端注入 key，该逻辑自然休眠。

```
portal/src/
├── panel.tsx                         # 入口 — 注册 <tp-card> / <tp-panel>
├── wc/
│   ├── define.ts                     # custom element 基类（shadow + api-base + preact 挂载）
│   └── styles.ts                     # 取 shadow.css 的编译结果作为注入串
├── styles/
│   ├── shadow.css                    # @layer pico, joy —— 分层引入下面三者
│   ├── tokens.css                    # vendor 自 net-console（权威版在那边）
│   └── panel.css                     # 组件自有样式
├── components/
│   ├── TpCard.tsx                    # card 内容
│   ├── TpPanel.tsx                   # panel 内容（原 StatusPage）
│   ├── ProxyToggle.tsx               # 代理开关
│   ├── SettingsCard.tsx              # 配置编辑器
│   ├── RuleSets.tsx                  # IP set 管理
│   ├── ApiKeyForm.tsx                # 401/403 时的 key 输入界面
│   └── status.ts                     # 状态徽标文案
├── lib/
│   ├── useStatus.ts                  # /status 拉取与刷新
│   └── api/
│       ├── client.ts                 # createApiClient(apiBase)，APIError，key 存取
│       ├── context.ts                # ApiContext + useApi
│       └── ApiProvider.tsx           # 注入 client + 401 门禁
└── test/setup.ts                     # vitest：补内存版 localStorage
```

---

## 配置

版本固定为 1，字段无 legacy 兼容逻辑。

```yaml
version: 1
listen: ":1444"
api_key: ""              # 非空则所有 /api/* 需带 X-Api-Key；留空不鉴权
proxy:
  lan_interface: br-lan
  default_port: 1081
  forced_port: 1082
  self_mark: 255
checker:
  enabled: true
  url: "http://www.google.com"
  method: HEAD
  timeout: 10s
  interval: 30s
  failure_threshold: 3
  on_failure: disable    # "disable" | "keep"
  proxy: ""              # 可选，SOCKS5 地址，如 "127.0.0.1:1080"
  bark_token: ""         # 可选，Bark 推送 token
nft:
  state_path: /etc/nftables.d
  sets: [direct_src, direct_dst, proxy_src, proxy_dst, allow_v6_mac]
chnroute:
  auto_refresh: true
  refresh_interval: 168h
```

`SaveConfig` 写入前先 round-trip 验证（marshal -> parse -> validate），然后原子 rename 写文件。

文件权限：**新建**配置用 0600（配置含 `api_key`，不给 group/other）；目标文件已存在时
`writeFileAtomically` 沿用它现有的权限位 —— 管理员 `chmod 600` 之后，一次 UI 保存不能把它悄悄改回 0644。

---

## nft 文件与 OpenWrt 集成

### nft 文件策略

| 文件 | 性质 | 管理方 |
|------|------|--------|
| `proxy.nft` | 动态（端口、mark） | Go 模板渲染，写入 `state_path/` |
| `transparent.nft` | 动态（接口名） | Go 模板渲染，enable 时包裹为 full 版本 `nft -f` + 写 table-post |
| `reserved_ip.nft` | 静态 | IPK 安装到 `state_path/` |
| `v6block.nft` | 静态 | IPK 安装到 `state_path/` |
| `{set_name}.nft` | 动态 | Go 每次 set 增删后自动 `syncSetToFile` |
| `chnroute.nft` | 动态 | ChnRouteManager 生成，写入 `state_path/` |

fw4 重启时自动从 `state_path/` 加载所有 `.nft` 文件，代理状态和 set 内容均能恢复。

### 设备文件结构

```
/etc/
+-- nftables.d/                 # fw4 自动加载的规则目录
|   +-- proxy.nft               # 核心 TPROXY 规则链（Go 模板渲染生成）
|   +-- transparent.nft         # 暂不在此，见 table-post
|   +-- proxy_src.nft           # 强制代理源 IP Set
|   +-- proxy_dst.nft           # 强制代理目的 IP Set
|   +-- direct_src.nft          # 直连源 IP Set
|   +-- direct_dst.nft          # 直连目的 IP Set
|   +-- allow_v6_mac.nft        # 允许的 IPv6 MAC Set
|   +-- reserved_ip.nft         # 保留地址 Set（IPK 安装）
|   +-- v6block.nft             # IPv6 过滤规则（IPK 安装）
|   +-- chnroute.nft            # 中国 IP 路由表（Go 定期刷新）
|
+-- transparent-proxy/
|   +-- config.yaml             # 主配置文件
|   +-- transparent-proxy       # Go 后端二进制
|
+-- init.d/
|   +-- transparent-proxy       # procd 服务脚本
|
+-- hotplug.d/iface/
    +-- 80-ifup-wan             # WAN 接口监听器（策略路由）

/usr/share/nftables.d/table-post/
    +-- transparent.nft         # fw4 重启后自动加载的链挂载规则
```

### Init Script 与 Hotplug

#### Init Script（`/etc/init.d/transparent-proxy`）
```bash
#!/bin/sh /etc/rc.common
START=99
USE_PROCD=1

start_service() {
    procd_open_instance transparent-proxy
    procd_set_param command /etc/transparent-proxy/transparent-proxy -c /etc/transparent-proxy/config.yaml
    procd_set_param respawn 3600 5 5
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_close_instance
}
```

#### WAN 接口监听器（`/etc/hotplug.d/iface/80-ifup-wan`）
```bash
#!/bin/sh
[ "$ACTION" = "ifup" -a "$INTERFACE" = "wan" ] && {
    ip rule add fwmark 1 table 100
    ip route add local 0.0.0.0/0 dev lo table 100
}
```

### 代理启用/禁用机制

#### 规则注入点

```
/usr/share/nftables.d/table-post/  ->  fw4 启动时自动加载
                                    ->  规则插入 inet fw4 表
```

#### 链挂载方式

```nftables
# transparent.nft - 将自定义链挂载到 fw4 mangle 表

chain mangle_prerouting {
    iifname "br-lan" jump transparent_proxy      # LAN 流量跳转到 transparent_proxy
    mark 0x1 jump transparent_proxy              # 已标记流量也需处理（本机出口）
}

chain mangle_output {
    jump transparent_proxy_mask                   # 本机出口流量处理
}
```

#### 启用/禁用由 Go 直接驱动

代理的启用和禁用完全由 Go 代码管理，不依赖外部脚本：

**手动切换**：前端调用 `PUT /api/proxy`，Checker 的 `SetProxyEnabled()` 方法直接调用 `NftManager.EnableProxy()` 或 `NftManager.DisableProxy()`。

**自动切换**：Checker 健康检查循环：
- 检查成功且代理未启用 -> 调用 `NftManager.EnableProxy()`
- 连续失败达到阈值且 `on_failure == "disable"` -> 调用 `NftManager.DisableProxy()`
- `on_failure == "keep"` -> 只发 Bark 通知，不改变代理状态

`on_failure` 的有效值仅为 `"disable"` 或 `"keep"`，配置加载时会校验。

**EnableProxy 流程**：
1. 刷新 `inet fw4 mangle_prerouting`
2. 刷新 `inet fw4 mangle_output`
3. 从模板渲染 `transparent.nft`，包裹 `table inet fw4 {}` 后写入临时文件
4. 执行 `nft -f <tmpfile>` 加载规则
5. 将渲染内容写入 `/usr/share/nftables.d/table-post/transparent.nft`（fw4 重启后持久生效）

**DisableProxy 流程**：
1. 刷新 `inet fw4 mangle_prerouting`
2. 刷新 `inet fw4 mangle_output`
3. 删除 `/usr/share/nftables.d/table-post/transparent.nft`

**DEV_MODE 本地开发**：

`main.go` 中的 `bootstrap()` 函数根据 `DEV_MODE` 环境变量选择依赖实现：

```go
if os.Getenv("DEV_MODE") == "1" {
    executor = NewMemoryNft()        // 内存中模拟 nft 命令
    files = NewMemoryFileStore()     // 内存中模拟文件操作
    fetcher = &HTTPFetcher{...}      // chnroute 仍使用真实 HTTP
} else {
    executor = NewExecNftRunner(10 * 1e9)  // 执行真实 nft 命令
    files = OSFileStore{}                  // 真实文件系统操作
    fetcher = &HTTPFetcher{...}
}
```

---

## 总结

### 技术特性

| 特性 | 实现 |
|------|------|
| 透明代理技术 | **TPROXY**（TCP + UDP 完整支持） |
| 规则匹配 | **nftables sets**（高性能、原子更新） |
| 流量路由 | 多级规则优先级 + 四个 IP Sets |
| 策略路由 | fwmark + 路由表 100 |
| 集成方式 | fw4 自定义链注入 |
| 配置持久化 | `/etc/nftables.d/*.nft` 文件 |
| 健康检查 | 可选 SOCKS5 代理 + Bark 推送通知 |

### 架构优势

1. **无需 iptables/ipset** -- 完全使用现代 nftables
2. **动态管理** -- 通过 Web UI 实时增删 IP，无需重启
3. **持久化支持** -- 规则自动保存，重启后恢复
4. **开发友好** -- DEV_MODE 支持 Mac 本地开发，无需 root 或 VM
5. **OpenWrt 原生集成** -- 使用标准 fw4 接口注入规则
6. **故障自动响应** -- Checker 在检测到故障时自动禁用代理并推送通知

### 适用场景

- 家庭/小型办公室网络透明代理
- 需要灵活路由规则的场景
- 需要 Web UI 管理的场景
- OpenWrt 路由器部署
