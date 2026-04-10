# 需求文档：transparent-proxy

## 1. 项目定位

OpenWrt 路由器上的**透明代理管理工具**。路由器上已运行成熟的代理软件（v2ray/clash 等），本工具通过 nftables + tproxy 控制哪些流量走透明代理，并提供 Web 管理界面。

**不负责**：代理软件本身的安装、配置、启停。

## 2. 核心概念

### 2.1 流量决策模型

通过 nftables set 实现分层决策，优先级从高到低：

| 优先级 | 匹配条件 | 动作 | 说明 |
|--------|----------|------|------|
| 1 | mark=0xff | 直连 | 代理软件自身流量，防环 |
| 2 | 目标 IP ∈ reserved_ip | 直连 | RFC 保留地址（私网/环回等） |
| 3 | 源 IP ∈ proxy_src | 代理 → :1082 | 强制走代理的设备 |
| 4 | 源 IP ∈ direct_src | 直连 | 强制直连的设备（IoT 等） |
| 5 | 目标 IP ∈ proxy_dst | 代理 → :1082 | 强制走代理的目标 |
| 6 | 目标 IP ∈ direct_dst | 直连 | 强制直连的目标 |
| 7 | 目标 IP ∈ chnroute | 直连 | 中国大陆 IP |
| 8 | 其余流量 | 代理 → :1081 | 默认走代理 |

**两个代理端口**：
- `:1081` — 默认代理（代理软件内部再做分流）
- `:1082` — 直出代理（proxy_src/proxy_dst 命中的流量，不再分流）

### 2.2 可管理的 nft sets

**用户管理的 sets**（通过 Web 界面增删，运行时持久化到 .nft 文件）：

| Set 名称 | 元素类型 | 语义 | 典型用途 |
|-----------|----------|------|----------|
| proxy_src | ipv4_addr | 按源 IP 强制代理 | 指定设备全部流量走代理 |
| direct_src | ipv4_addr | 按源 IP 强制直连 | IoT/服务器绕过代理 |
| proxy_dst | ipv4_addr | 按目标 IP 强制代理 | 特定网站 IP 走代理 |
| direct_dst | ipv4_addr | 按目标 IP 强制直连 | 已知安全服务直连 |
| allow_v6_mac | ether_addr | IPv6 白名单设备 | 允许使用 IPv6 的 LAN 设备 |

**自动管理的 sets**（程序生成，不通过 Web 编辑）：

| Set 名称 | 语义 | 生成方式 |
|-----------|------|----------|
| chnroute | 中国大陆 IP 段 | APNIC 数据自动拉取 |

**静态 sets**（随包安装，不可修改）：

| Set 名称 | 语义 |
|-----------|------|
| reserved_ip | RFC 保留地址（私网/环回等） |

### 2.3 IPv6 控制（v6block）

基于 MAC 地址白名单控制 LAN 设备的 IPv6 互联网访问：
- **forward 链**：非白名单设备的 IPv6 流量禁止转发（无法访问 IPv6 互联网）
- **input 链**：非白名单设备的 DHCPv6 请求被阻止（不分配有状态 IPv6 地址）
- 链路本地 IPv6（NDP、mDNS 等）不受影响，所有设备的局域网 IPv6 功能正常
- `allow_v6_mac` 通过 Web 界面管理，与 IP set 管理方式一致

## 3. 功能需求

### F1: 透明代理开关

- 启用：加载 tproxy 规则到 nft mangle chains，设置持久化
- 禁用：flush mangle chains，删除持久化文件
- 通过 Web 界面或 API 手动切换
- 状态可查询（读取 nft chain 内容判断）

### F2: 健康检查（Checker）

- 定期通过 HTTP(S) 请求探测代理是否可用
- 支持 GET/HEAD 方法，可自定义 URL、Host header、超时
- 可配置通过 SOCKS5 代理发起探测
- 连续失败达到阈值后自动禁用透明代理
- 恢复后自动重新启用

**可选行为**（用户可配置）：
- **模式 A（默认）**：代理不可用时禁用透明代理（流量回退直连）
- **模式 B**：代理不可用时仍保持透明代理启用（宁可断网也走代理）

### F3: IP Set 管理

- 增删 IP 地址/CIDR/IP 范围到指定 set
- 查看各 set 当前内容
- **每次增删操作后自动将 set 内容持久化到 nft 状态文件**（写入 `state_path/<set_name>.nft`）
- OpenWrt 重启后，fw4 自动加载 `state_path/` 下的 .nft 文件，set 内容恢复

### F4: 中国路由表（chnroute）

- 从 APNIC 拉取最新中国 IP 段，生成 nft set 文件
- 首次启动时若不存在则自动拉取
- 之后定期自动更新（可配置间隔）

### F5: IPv6 MAC 白名单

- 通过 Web 界面增删 `allow_v6_mac` set 中的 MAC 地址
- 每次增删后自动持久化到 `state_path/allow_v6_mac.nft`
- 控制哪些 LAN 设备允许使用 IPv6

### F6: Web 管理界面

- 显示代理状态（启用/禁用/健康检查结果）
- 开关透明代理
- 配置健康检查参数
- 管理 IP set（增删查）
- 触发 chnroute 刷新
- 单页面应用，嵌入 Go 二进制

### F7: 配置持久化

- YAML 配置文件
- API 修改配置后自动保存
- 支持配置验证

### F8: OpenWrt 集成与打包

#### 系统集成脚本

以下脚本不由 Go 程序直接执行，但作为项目的一部分随 IPK 安装到 OpenWrt 对应路径：

| 脚本 | 路径 | 用途 |
|------|------|------|
| init.d 服务脚本 | `/etc/init.d/transparent-proxy` | procd 管理的服务启停、自动重启 |
| WAN hotplug | `/etc/hotplug.d/iface/80-ifup-wan` | WAN 上线时设置 fwmark 策略路由（`ip rule add fwmark 1 table 100`），WAN 下线时清理 |

**策略路由是 tproxy 工作的前提**：nft 的 `tproxy` action 为报文打上 `fwmark=1`，策略路由将 fwmark=1 的流量导向 loopback，代理软件才能接收。

#### IPK 打包

两个包：

| 包名 | 内容 | 架构 |
|------|------|------|
| `transparent-proxy` | Go 二进制 + nft 规则 + 系统脚本 + 默认配置 | aarch64_generic |
| `luci-app-transparent-proxy` | LuCI 菜单入口（iframe 嵌入 Web UI） | all |

**打包方式**：本地 Minimal Packager（不依赖 OpenWrt SDK），通过脚本构建确定性 IPK 文件。

#### 文件安装分类

IPK 直接安装所有文件到目标路径（不使用 bootstrap 自释放机制）：

**Managed files**（随包安装/升级覆盖/卸载删除）：

| 文件 | 说明 |
|------|------|
| `/usr/bin/transparent-proxy` | Go 二进制 |
| `/etc/init.d/transparent-proxy` | procd 服务脚本 |
| `/etc/hotplug.d/iface/80-ifup-wan` | WAN 策略路由 hotplug |
| `/etc/nftables.d/reserved_ip.nft` | RFC 保留地址 set（静态） |
| `/etc/nftables.d/v6block.nft` | IPv6 过滤规则链（静态，引用 @allow_v6_mac） |

**conffiles**（首次安装写默认值，升级时保留用户修改）：

| 文件 | 说明 |
|------|------|
| `/etc/transparent-proxy/config.yaml` | 应用配置 |

**运行时生成文件**（Go 程序模板渲染或写入，卸载时 postrm 清理）：

| 文件 | 来源 | 说明 |
|------|------|------|
| `/etc/nftables.d/proxy.nft` | 模板渲染 | 核心 tproxy 规则链（含端口、mark 配置） |
| `/etc/nftables.d/proxy_src.nft` | 用户操作 | 用户管理的 IP set |
| `/etc/nftables.d/direct_src.nft` | 用户操作 | 同上 |
| `/etc/nftables.d/proxy_dst.nft` | 用户操作 | 同上 |
| `/etc/nftables.d/direct_dst.nft` | 用户操作 | 同上 |
| `/etc/nftables.d/allow_v6_mac.nft` | 用户操作 | 用户管理的 MAC set |
| `/etc/nftables.d/chnroute.nft` | APNIC 拉取 | 中国 IP 段 |
| `/usr/share/nftables.d/table-post/transparent.nft` | 模板渲染 | 代理启用时的持久化副本（含接口名配置） |

## 4. 非功能需求

- **单二进制部署**：Go 编译嵌入前端静态文件
- **无外部依赖**：不依赖数据库，状态存 nftables + 文件
- **可 mock 开发**：DEV_MODE 支持在非 OpenWrt 环境（如 Mac）开发测试
- **交叉编译**：支持 ARM64 (aarch64) 目标
- **开源友好**：代码清晰，易于理解和贡献

## 5. 持久化策略总览

系统需要在 OpenWrt 重启后完整恢复状态，涉及多个持久化层：

| 数据 | 持久化方式 | 恢复时机 |
|------|-----------|----------|
| 代理规则链（proxy.nft） | Go 模板渲染写入 `state_path/proxy.nft` | fw4 启动时自动加载 |
| 用户管理的 IP set（proxy_src 等） | 每次增删后写 `state_path/<set>.nft` | fw4 启动时自动加载 |
| allow_v6_mac | 每次增删后写 `state_path/allow_v6_mac.nft` | fw4 启动时自动加载 |
| chnroute | 写 `state_path/chnroute.nft` | fw4 启动时自动加载 |
| reserved_ip | IPK 安装到 `state_path/reserved_ip.nft`（静态） | fw4 启动时自动加载 |
| v6block 规则链 | IPK 安装到 `state_path/v6block.nft`（静态） | fw4 启动时自动加载 |
| 透明代理开关 | 启用时：Go 渲染 transparent 模板 → 包裹为 full 版本 `nft -f` 加载 → 写入 fw4 table-post 目录 | fw4 启动时自动加载 |
| 策略路由（fwmark） | hotplug 脚本设置 | WAN 上线时自动执行 |
| 应用配置 | YAML 文件（conffile，升级保留） | 程序启动时读取 |

## 6. 不在范围内

- 代理软件（v2ray/clash）的管理
- 多用户/权限管理
