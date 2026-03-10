# 脚本使用指南

本文档说明项目中各脚本的用途和使用方法。

## 分发模型

项目支持双分发模型，以满足不同场景下的部署需求：

1. **raw-binary (单文件自举)**：
   - **脚本**：`scripts/build-release.sh` / `scripts/test-release-contract.sh` / `scripts/openwrt-vm/deploy.sh`
   - **特点**：交付单个 `linux/arm64` 二进制，首启后自动生成配置、init 脚本和托管资产。适合快速部署和最小化依赖场景。
   - **验证**：通过 `test-release-contract.sh` 验证单文件安装契约。

2. **ipk (OpenWrt 标准包)**：
   - **脚本**：`scripts/build-openwrt-ipk.sh`
   - **特点**：交付标准的 OpenWrt `.ipk` 包，包括服务包 `transparent-proxy` 和 LuCI 插件包 `luci-app-transparent-proxy`。支持 `opkg` 生命周期管理（安装、升级、卸载）。
   - **验证**：通过 `scripts/openwrt-vm/test-ipk-install.sh` 等脚本在 VM 中验证。

## 发布脚本

### generate-manifest.sh

自动生成 `files/managed-manifest.json`。

```bash
# 生成 manifest
./scripts/generate-manifest.sh

# 仅校验（不修改文件）
./scripts/generate-manifest.sh --check

# 指定版本号
./scripts/generate-manifest.sh -m "1.0.0"

# 输出到指定路径
./scripts/generate-manifest.sh -o /tmp/manifest.json
```

**选项：**

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `-o, --output` | 输出路径 | `files/managed-manifest.json` |
| `-c, --check` | 仅校验，不修改 | false |
| `-s, --schema-version` | schema 版本 | `v1` |
| `-m, --manifest-version` | manifest 版本 | 时间戳 |
| `-h, --help` | 显示帮助 | - |

**推断规则：**

根据文件路径自动推断属性：

| 路径模式 | permission | requiresRestart | requiresReload |
|---------|-----------|-----------------|----------------|
| `/etc/init.d/transparent-proxy` | 0755 | true | false |
| `/etc/hotplug.d/*` | 0755 | true | false |
| `/etc/transparent-proxy/*.sh` | 0755 | false | false |
| `/etc/nftables.d/*` | 0644 | false | true |
| `*.nft` | 0644 | false | true |
| 其他 | 0644 | false | false |

**依赖：** `jq`, `shasum` (macOS) 或 `sha256sum` (Linux)

### build-release.sh

构建 OpenWrt ARM64 单文件发布物。

```bash
# 完整构建（自动推断版本）
./scripts/build-release.sh

# 指定版本
./scripts/build-release.sh -v v1.0.0

# 启用 UPX 压缩
./scripts/build-release.sh -v v1.0.0 --upx

# 跳过前端构建（调试用）
./scripts/build-release.sh --skip-frontend

# 指定输出目录
./scripts/build-release.sh -v v1.0.0 -o /tmp/release
```

**选项：**

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `-v, --version` | 版本号（如 v1.0.0）| git describe 或时间戳 |
| `-u, --upx` | 启用 UPX 压缩 | false |
| `-s, --skip-frontend` | 跳过前端构建 | false |
| `-o, --output-dir` | 输出目录 | `dist/` |
| `-h, --help` | 显示帮助 | - |

**输出产物：**

```
dist/
├── transparent-proxy_1.0.0_linux_arm64
└── transparent-proxy_1.0.0_linux_arm64.sha256
```

说明：

- 主产物是单个 `linux/arm64` ELF 二进制，文件名固定为 `transparent-proxy_<version>_linux_arm64`
- 校验文件为同名 sibling 文件 `transparent-proxy_<version>_linux_arm64.sha256`
- 脚本会在构建前清理同名旧二进制、旧 `.sha256`，以及历史遗留的 `.tar.gz` / `.tar.gz.sha256`
- 当前契约下不再输出 staging 目录树，也不再生成 tar.gz 发布包

**依赖：** `go`, `npm`, `jq`, `upx`（可选）

**构建流程：**

1. 调用 `generate-manifest.sh` 生成 manifest
2. 构建前端：`npm ci && npm run build`
3. 交叉编译后端：`GOOS=linux GOARCH=arm64 go build`
4. 可选 UPX 压缩
5. 直接输出 `transparent-proxy_<version>_linux_arm64`
6. 为该单文件生成 sibling `.sha256`

### build-openwrt-ipk.sh

构建 OpenWrt 标准 `.ipk` 包。

```bash
# 完整构建（需 OpenWrt SDK）
./scripts/build-openwrt-ipk.sh

# 仅构建服务包
./scripts/build-openwrt-ipk.sh --package transparent-proxy

# 仅构建 LuCI 插件包
./scripts/build-openwrt-ipk.sh --package luci-app-transparent-proxy

# 仅验证工作区和输出结构（不依赖 SDK）
./scripts/build-openwrt-ipk.sh --prepare-only
```

**选项：**

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `-p, --package` | 指定构建包（`transparent-proxy` 或 `luci-app-transparent-proxy`）| 两者都构建 |
| `--prepare-only` | 仅验证工作区和输出结构 | false |
| `-h, --help` | 显示帮助 | - |

**输出产物：**

```
dist/ipk/<version>/
└── artifacts/
    ├── transparent-proxy_<version>_aarch64_generic.ipk
    └── luci-app-transparent-proxy_<version>_all.ipk
```

**说明：**

- 脚本支持在无 OpenWrt SDK 的环境下通过 `--prepare-only` 验证打包逻辑。
- 真实构建依赖 OpenWrt SDK 环境。
- 脚本会自动清理 `dist/ipk/<version>/` 临时产物。

**依赖：** `go`, `npm`, `jq`, `tar`, `gzip`, `OpenWrt SDK`（可选）

## OpenWrt VM 测试脚本

位于 `scripts/openwrt-vm/`，用于在 OpenWrt VM 中运行测试。

详见 `docs/openwrt-vm-testing.md`。

### test-all.sh

运行完整测试套件。

```bash
# PR 阻塞层测试
bash scripts/openwrt-vm/test-all.sh --tier blocking

# 完整回归测试
bash scripts/openwrt-vm/test-all.sh --tier regression
```

### deploy.sh

部署到 OpenWrt VM，并验证单文件安装契约。

```bash
bash scripts/openwrt-vm/deploy.sh
```

当前行为：

- host 侧只临时构建并 staging 一个 `linux/arm64` 后端二进制，文件名为 `server`
- deploy 通过 `scp` 只上传这一个二进制到 guest 临时目录
- guest 侧把该二进制原子落盘到 `/etc/transparent-proxy/server`
- 随后直接执行首启自举，依赖二进制自身生成 canonical `config.yaml`、`/etc/init.d/transparent-proxy` 与托管资产
- deploy 完成前会断言 init 脚本中服务命令固定为 `/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml`

如果需要统一执行 VM readiness、deploy 与 canonical wiring 断言，应使用：

```bash
bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready
```

## CI/CD 集成

### PR 检查

```bash
# 后端测试
cd server && go test ./...

# 前端测试
cd portal && npm run test:component

# 阻塞层 E2E 测试
bash scripts/openwrt-vm/test-all.sh --tier blocking
```

### 发布构建

```bash
# 生成版本化的发布包
./scripts/build-release.sh -v v$(cat VERSION)
```
