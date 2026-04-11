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
| 前端 | Preact 10 + Vite + TypeScript | 管理界面、规则配置、状态监控 |
| 防火墙 | nftables (fw4) | 流量拦截、TPROXY 透明代理重定向 |

## 核心功能

- **多级流量决策** — 通过 4 个 nft set（proxy_src/dst, direct_src/dst）+ chnroute 实现分层路由
- **健康检查** — 自动检测代理可用性，支持 SOCKS5 代理探测、Bark 推送通知
- **Web 管理** — 实时增删 IP 规则，配置编辑，无需重启
- **OpenWrt 原生集成** — fw4 自定义链注入，procd 服务管理，IPK 包分发

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
