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
| `nft` 命令 | 调用系统 nftables | 内存 Mock |
| ipset 存储 | nftables ipset | 内存 map |
| 文件写入 | 系统目录（需 root）| 临时目录 overlay |
| overlay 操作 | 部署到 OpenWrt | 自动跳过 |
| 网络检查 | 真实网络请求 | 真实网络请求 |

### 启动日志

开发模式启动时会输出临时目录路径：

```
DEV_MODE enabled - overlay root: /var/folders/.../transparent-proxy-dev-xxx
```

所有文件操作（配置写入、ipset 同步等）都会写入此目录。

透明代理相关的 `transparent*.nft` 资产也会先从 overlay 读取。若 overlay 中缺少这些文件，`DevFileWriter` 会受控回退到仓库 `files/` 目录下对应资产，保证本地开发时的 enable/disable 和 checker 恢复流程能继续跑通。

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

# 查看配置
curl http://localhost:1444/api/configs

# 健康检查
curl http://localhost:1444/api/health
```

## Mock 实现细节

### nft 命令 Mock

`DevMockRunner` 实现 `CommandRunner` 接口，模拟以下 nft 命令：

| 命令 | 行为 |
|------|------|
| `nft -j list set inet fw4 <name>` | 返回 JSON 格式 ipset |
| `nft list set inet fw4 <name>` | 返回文本格式 ipset |
| `nft add element inet fw4 <name> {ip}` | 添加到内存集合 |
| `nft delete element inet fw4 <name> {ip}` | 从内存集合删除 |
| `nft -f <file>` | 返回成功（no-op） |

默认创建四个 ipset：`direct_dst`、`direct_src`、`proxy_dst`、`proxy_src`。

### 文件写入 Mock

`DevFileWriter` 将绝对路径映射到临时目录：

```
/etc/transparent-proxy/config.yaml → <tmpRoot>/etc/transparent-proxy/config.yaml
```

它同时支持对 overlay 内文件的读取和删除，透明代理的 nft 片段优先走 overlay，缺失时再回退到仓库资产。

## 测试

开发模式不影响单元测试：

```bash
cd server
go test ./...
```

生产模式的 `NewRuntime()` 仍返回真实的 `NftRunner`。

## 限制

开发模式下的限制：

1. **无真实 iptables/nftables**：规则只存在于内存中
2. **无 OpenWrt overlay**：文件部署操作被跳过
3. **无网络隔离验证**：无法测试实际的网络代理效果

如需完整功能测试，请使用 OpenWrt VM（见 `docs/openwrt-vm-testing.md`）。
