# Mac 本地开发指南

本文档说明如何在 Mac 上进行本地开发和调试，无需 OpenWrt VM 或 root 权限。

## 开发模式

通过环境变量 `DEV_MODE=1` 启用开发模式：

```bash
cd server
DEV_MODE=1 go run . -c ../config.yaml
```

### 功能特性

| 功能 | 生产模式 | 开发模式 |
|------|---------|---------|
| `nft` 命令 | 调用系统 nftables | 内存 Mock（`MemoryNft`） |
| ipset 存储 | nftables ipset | 内存 map |
| 文件写入 | 系统目录（需 root）| 内存 Mock（`MemoryFileStore`） |
| chnroute 拉取 | 真实 HTTP 请求 | 真实 HTTP 请求 |
| 网络检查 | 真实网络请求 | 真实网络请求 |

### 启动日志

开发模式启动时会输出：

```
DEV_MODE enabled: using in-memory mocks
```

所有 nft 命令和文件操作（配置写入、ipset 同步、nft 规则渲染等）均在内存中完成，不写入任何系统目录，也不调用真实的 `nft` 可执行文件。

## 前后端联调

### 终端 1：后端

```bash
cd server
DEV_MODE=1 go run . -c ../config.yaml
```

后端监听 `:1444`。

### 终端 2：前端

```bash
cd portal
npm install  # 首次
npm start
```

前端开发服务器监听 `:3000`，API 请求自动代理到后端。

### 浏览器访问

```
open http://localhost:3000
```

## API 测试

开发模式下所有 API 正常工作：

```bash
# 查看代理状态
curl http://localhost:1444/api/status

# 查看本机 IP
curl http://localhost:1444/api/ip

# 查看配置
curl http://localhost:1444/api/config

# 更新配置
curl -X PUT http://localhost:1444/api/config \
  -H "Content-Type: application/json" \
  -d '{"proxy":{"default_port":1081}}'

# 查看 checker 配置
curl http://localhost:1444/api/checker

# 更新 checker 配置
curl -X PUT http://localhost:1444/api/checker \
  -H "Content-Type: application/json" \
  -d '{"enabled":true,"interval":"30s"}'

# 切换代理开关
curl -X PUT http://localhost:1444/api/proxy \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'

# 查看 ipset 规则
curl http://localhost:1444/api/rules

# 添加规则
curl -X POST http://localhost:1444/api/rules/add \
  -H "Content-Type: application/json" \
  -d '{"set":"proxy_dst","ip":"192.168.1.1"}'

# 删除规则
curl -X POST http://localhost:1444/api/rules/remove \
  -H "Content-Type: application/json" \
  -d '{"set":"proxy_dst","ip":"192.168.1.1"}'

# 同步规则到文件
curl -X POST http://localhost:1444/api/rules/sync

# 刷新 chnroute 路由表
curl -X POST http://localhost:1444/api/refresh-route
```

## Mock 实现细节

### nft 命令 Mock

`MemoryNft` 实现 `NftExecutor` 接口，模拟以下 nft 命令：

| 命令 | 行为 |
|------|------|
| `nft -j list sets inet fw4` | 返回所有 set 的 JSON |
| `nft -j list set inet fw4 <name>` | 返回指定 set 的 JSON 格式内容 |
| `nft list set inet fw4 <name>` | 返回指定 set 的文本格式内容 |
| `nft add set inet fw4 <name> {...}` | 在内存中创建 set |
| `nft add element inet fw4 <name> {ip}` | 添加到内存集合 |
| `nft delete element inet fw4 <name> {ip}` | 从内存集合删除 |
| `nft list chain inet fw4 <chain>` | 返回模拟的 chain 内容 |
| `nft flush chain inet fw4 <chain>` | 标记代理为禁用状态 |
| `nft -f <file>` | 标记代理为启用状态 |

Set 由 `Bootstrap` 阶段按 config 中的 `nft.sets` 列表动态创建，无硬编码默认集合。

### 文件写入 Mock

`MemoryFileStore` 实现 `FileStore` 接口，将所有文件内容存储在内存 map 中：

- `WriteFile` — 写入内存，不触及磁盘
- `ReadFile` — 从内存读取，路径不存在时返回 `os.ErrNotExist`
- `RemoveFile` — 从内存删除
- `GetFile` — 供测试断言直接读取内容

### chnroute 拉取

DEV_MODE 下 chnroute 仍使用真实的 `HTTPFetcher`，会发起真实 HTTP 请求拉取 APNIC 数据。若不希望在本地触发网络请求，可在 `config.yaml` 中将 `chnroute.auto_refresh` 设为 `false`。

## 测试

开发模式不影响单元测试：

```bash
cd server
go test ./...
```

测试直接构造 `MemoryNft`、`MemoryFileStore`、`MemoryFetcher` 实例，与 DEV_MODE 共用同一套 mock 实现。

## 限制

开发模式下的限制：

1. **无真实 iptables/nftables**：规则只存在于内存中，重启后丢失
2. **无文件持久化**：配置写入和 nft 文件渲染均在内存中，不写磁盘
3. **无网络隔离验证**：无法测试实际的网络代理效果

如需完整功能测试，请使用 OpenWrt VM（见 `docs/openwrt-vm-testing.md`）。
