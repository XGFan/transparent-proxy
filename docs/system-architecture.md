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
├── mock.go          # 268 行 — MemoryNft + MemoryFileStore + MemoryFetcher
├── web.go           # 6 行   — 前端静态文件 embed.FS
├── config_test.go   # 234 行
├── nft_test.go      # 350 行
├── checker_test.go  # 364 行
├── api_test.go      # 435 行
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
{"code": "ok"|"invalid_request"|"internal_error", "message": "...", "data": {...}}
```

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

---

## 前端

框架：Preact 10（单一 `preact` 依赖），hooks 从 `preact/hooks` 导入。Vite 构建产物输出到 `server/web/`，由 Go 二进制通过 `embed.FS` 嵌入。开发时 Vite dev server（:3000）将 `/api` 代理到 `:1444`。

```
portal/src/
├── main.tsx                          # 5 行  — 挂载入口
├── App.tsx                           # 5 行  — 根组件
├── app/
│   ├── AppShell.tsx                  # 21 行 — 布局容器
│   └── AppShell.css                  # 59 行
├── features/
│   └── status/
│       ├── StatusPage.tsx            # 148 行 — 主状态页
│       └── StatusPage.css            # 793 行
├── components/
│   ├── ProxyToggle.tsx               # 31 行  — 代理开关
│   ├── SettingsCard.tsx              # 244 行 — checker 配置编辑器
│   └── RuleSets.tsx                  # 95 行  — IP set 管理
├── lib/
│   └── api/
│       └── client.ts                 # 227 行 — 类型安全 fetch 封装，APIError 类
└── index.css                         # 12 行
```

---

## 配置

版本固定为 1，字段无 legacy 兼容逻辑。

```yaml
version: 1
listen: ":1444"
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
