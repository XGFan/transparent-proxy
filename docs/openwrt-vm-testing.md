# OpenWrt VM 三层测试手册

## 目标

这份手册只描述当前已经落地的三层测试体系、PR 阻塞运行方式、长时回归运行方式，以及未来接入 CI 的建议路线。

当前唯一真值环境是 **Mac + QEMU + OpenWrt ARM VM**。UTM 只用于人工调试，不作为验收结果来源。若要上 CI，需要 **自托管 Apple Silicon runner**，不能假设云 CI 自带可用执行环境。

## 运行前提

执行任一 VM-backed 测试前，需要满足以下前提：

1. 宿主机是 macOS，且可运行 QEMU。
2. 已具备 OpenWrt ARM VM 所需依赖与本地权限。
3. 仓库中的 OpenWrt VM 脚本可正常执行，尤其是 `scripts/openwrt-vm/test-common.sh --ensure-vm-ready`。
4. portal 侧请求目标遵守 `PORTAL_API_TARGET` 契约，默认应指向当前 VM API，例如 `http://127.0.0.1:1444`。
5. `/api/refresh-route` 必须走 fixture seam，不能把公网 APNIC 当成测试依赖。

其中，`bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready` 是当前 VM readiness 与 deploy 真值入口。它会串行完成 boot、wait-ready、单二进制 deploy、首启自举，以及 canonical service wiring 断言。

## 三层测试边界

当前测试体系分三层，边界固定，不要混用。

### 第 1 层，快速层（后端 Go 测试 + 前端组件测试）

这层不依赖真实浏览器，不依赖真实 OpenWrt VM 交互路径，目标是快速验证后端 API 处理逻辑、前端 React 组件、状态流转、反馈文案和交互禁用语义。

入口命令：

```sh
# 后端快速层
cd server && go test ./...

# 前端快速层
cd portal && npm run test:component
```

适用场景：

- PR 快速阻塞
- 后端 API 处理逻辑回归
- 前端状态机与组件反馈回归
- 在 VM 还没启动前先发现明显后端/前端逻辑问题

### 第 2 层，VM-backed API / 集成契约测试

这层通过真实 OpenWrt VM 验证 portal 背后的 API 契约和 guest 副作用，但不经过真实浏览器交互。它覆盖 `/api/status` (envelope-first)、`/api/rules/add`、`/api/rules/remove`、`/api/rules/sync` 以及核心负例矩阵。

此外，为了兼容旧版脚本和特定工具，以下端点作为 **保留兼容性端点 (Retained Compatibility Endpoints)** 继续存在：
- `/api/ip`：获取当前客户端 IP。
- `/api/refresh-route`：触发路由刷新（需配合 fixture seam）。

入口命令：

```sh
bash scripts/openwrt-vm/test-api-contract.sh --suite=blocking
bash scripts/openwrt-vm/test-api-contract.sh --suite=regression
```

blocking 关注 happy path 和核心负例，regression 关注幂等、缺失 fixture、错误 fixture、缺失 set 等回归点。

### 第 3 层，浏览器驱动的真实 UI E2E

这层通过 Playwright 驱动真实浏览器，经由 portal UI 调真实后端，再落到真实 OpenWrt VM。它是最终的端到端真值层。

入口命令：

```sh
cd portal
npm run test:e2e:blocking
npm run test:e2e:regression

bash scripts/openwrt-vm/test-ui-e2e.sh --suite=blocking
bash scripts/openwrt-vm/test-ui-e2e.sh --suite=regression
```

说明：

- `npm run test:e2e:blocking` 和 `npm run test:e2e:regression` 是 portal 侧便捷入口。
- `bash scripts/openwrt-vm/test-ui-e2e.sh --suite=...` 是 wrapper 真值入口，负责统一 VM readiness、deploy、Playwright artifact 目录和失败留痕。

## VM readiness 与 deploy 真值

当前 VM 环路的真值不是预置 tar 包、预置目录树，也不是手工把 config/init/files 整包拷进 guest。唯一真值是：

```sh
bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready
```

该入口当前会执行以下步骤：

1. 若 VM 未运行，则先执行 `boot-vm.sh`。
2. 执行 `wait-ready.sh`，确认 SSH/ubus 可用。
3. 调用 `scripts/openwrt-vm/deploy.sh`，把 host 侧刚编译的单个 `linux/arm64` 二进制上传到 guest 临时目录。
4. deploy 只把这个单二进制安装到 `/etc/transparent-proxy/server`，不会额外上传 tar.gz、配置目录树或托管文件目录。
5. deploy 直接触发首启自举，由 `/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml` 负责生成 canonical `config.yaml`、`/etc/init.d/transparent-proxy` 与所需托管资产。
6. `test-common.sh` 最后断言 canonical layout 和 service wiring 已成立，其中 init 脚本必须包含 `/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml`。

因此，后续 blocking 与 regression 链路看到的 guest 状态，都是“复制单个二进制并触发首启自举”后的结果，而不是历史上的预装资产模型。

## LuCI/uhttpd 同源承载可行性探针

在推进 LuCI 入口方案前，先运行专用探针判断“是否能在当前 VM 镜像上通过 LuCI/uhttpd 暴露受保护同源子路径给 `transparent-proxy`”。

入口命令：

```sh
bash scripts/openwrt-vm/test-luci-proxy-probe.sh
bash scripts/openwrt-vm/test-luci-proxy-probe.sh --force-negative
```

行为约定：

- 脚本会先调用 `bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready` 作为 readiness 真值入口。
- 输出二值结果：`SAME_ORIGIN_SUPPORTED=1` 或 `SAME_ORIGIN_SUPPORTED=0`。
- **当前状态**：在当前 OpenWrt VM 镜像上，探针结果已锁定为 `SAME_ORIGIN_SUPPORTED=0`。这意味着 LuCI 插件目前仅支持 **fallback-only** 模式，即 LuCI 界面仅作为跳转到独立管理页面的入口。
- 退出码契约：`0=支持`，`20=不支持（探针已完成并写出证据）`，`1=脚本执行失败`。
- 机器可读结果写入：`.tmp/openwrt-vm/luci-probe.env`。
- 证据写入：
  - `.tmp/openwrt-vm/luci-probe-summary.txt`
  - `.sisyphus/evidence/task-1-luci-proxy-probe.txt`
  - 负路径时额外写 `.sisyphus/evidence/task-1-luci-proxy-probe-error.txt`
- `--force-negative` 用于强制演练失败分支，并显式打出 Task 9 fallback marker。

## ipk 分发与 LuCI 回归测试

随着 ipk 分发路径的引入，测试体系新增了针对 `opkg` 生命周期和 LuCI 入口的回归验证。

### opkg install 与 LuCI fallback 回归

验证 `opkg install` 流程以及在 `SAME_ORIGIN_SUPPORTED=0` 模式下的 LuCI 降级表现。

入口命令：

```sh
bash scripts/openwrt-vm/test-ipk-install.sh --service-only
bash scripts/openwrt-vm/test-luci-entry.sh
```

行为约定：

- 两个脚本都会先调用 `bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready`。
- `test-ipk-install.sh --service-only`：
  - 本地构建 `transparent-proxy` ipk；
  - 在 VM 上执行 `opkg install`；
  - 验证 canonical service 资产、`/api/status` 健康检查，以及“未安装 LuCI 包时 LuCI 路由保持 404”；
  - 证据写入 `.sisyphus/evidence/task-10-service-only.txt`。
- `test-luci-entry.sh`：
  - 本地构建 `transparent-proxy` 与 `luci-app-transparent-proxy` ipk；
  - 在 VM 上安装 service + LuCI 包；
  - 验证 `/api/status` 健康、LuCI route 存在、fallbackNotice/fallbackTarget 已注入，以及交付的 LuCI view JS 继续锁定 fallback-only 语义；
  - 证据写入 `.sisyphus/evidence/task-10-luci-entry.txt`。

### 升级、卸载与缓存失效回归

验证 `opkg upgrade` 和 `opkg remove` 流程，确保配置持久化和 LuCI 缓存清理。

入口命令：

```sh
bash scripts/openwrt-vm/test-ipk-upgrade-uninstall.sh
```

行为约定：

- 脚本会先调用 `bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready`。
- 之后会分别构建 old/new 两组 `transparent-proxy` 与 `luci-app-transparent-proxy` ipk，并在 VM 中固定验证以下回归点：
  1. **配置持久化**：升级 `transparent-proxy` 时，用户编辑过的 `config.yaml` 保持字节级不变。
  2. **缓存清理**：升级 `luci-app-transparent-proxy` 时，自动清理 `/tmp/luci-indexcache.*` 与 `/tmp/luci-modulecache/`，确保 UI 不会出现白屏或旧版本残留。
  3. **卸载回归**：卸载 `luci-app-transparent-proxy` 后，服务仍可通过 `http://127.0.0.1:1444` 独立访问，且 LuCI 路由回到 `404`。
  4. **部分卸载**：卸载 `transparent-proxy` 后，LuCI 路由应消失或返回降级说明页，不得出现 5xx 错误。
- 证据写入：
  - `.sisyphus/evidence/task-11-upgrade-cache.txt`
  - `.sisyphus/evidence/task-11-upgrade-cache.png`
  - `.sisyphus/evidence/task-11-uninstall.txt`

## fixture seam，与公网解耦

`/api/refresh-route` 的测试必须显式依赖 fixture seam，而不是公网。

关键环境变量：

- `TP_REFRESH_ROUTE_FIXTURE`
- `TP_CHNROUTE_FIXTURE_PATH`

当前约定：

1. `TP_REFRESH_ROUTE_FIXTURE` 提供默认 fixture 路径。
2. wrapper 和 contract suite 会把该 seam 显式注入到 guest 进程。
3. `refresh-route` 测试不依赖公网，不应把 `ftp.apnic.net` 可用性当成阻塞条件。

也就是说，只要 fixture 正常，`/api/refresh-route` 的成功路径和失败路径都可以在离线或受限网络下稳定复现。

## 统一契约变量

以下变量是本手册里最重要的运行契约：

- `PORTAL_API_TARGET`：portal 开发代理要转发到的后端 API 目标，默认应与 VM API 一致。
- `TP_API_BASE_URL`：VM API 基础地址，默认 `http://127.0.0.1:1444`。
- `TP_UI_BASE_URL`：portal UI 地址，默认 `http://127.0.0.1:3000`。
- `TP_REFRESH_ROUTE_FIXTURE`：`refresh-route` 默认 fixture 文件。
- `TP_PLAYWRIGHT_ARTIFACT_DIR`：Playwright wrapper 的 artifact 根目录。

如果 `PORTAL_API_TARGET` 指错地址，浏览器层看到的就是错误的 backend，E2E 结果没有意义。

## PR 阻塞，与长时回归

### PR 阻塞，blocking

PR 阻塞应跑最短但仍覆盖三层真值的组合：

```sh
bash scripts/openwrt-vm/test-all.sh --tier blocking
```

等价串行步骤为：

```sh
cd server && go test ./...
cd portal && npm run test:component
bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready
bash scripts/openwrt-vm/test-api-contract.sh --suite=blocking
bash scripts/openwrt-vm/test-ui-e2e.sh --suite=blocking
```

适合放进 PR 阻塞的原因：

- 先用快速层尽早发现后端/前端逻辑回归
- 再用 `scripts/openwrt-vm/test-common.sh --ensure-vm-ready` 固定单文件 deploy 与 canonical wiring 真值
- 再验证 VM-backed API 契约
- 最后用浏览器层确认真实 UI 主路径

### 长时回归，regression

长时回归应跑完整回归分层：

```sh
bash scripts/openwrt-vm/test-all.sh --tier regression
```

等价串行步骤为：

```sh
cd server && go test ./...
cd portal && npm run test:component
bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready
bash scripts/openwrt-vm/test-api-contract.sh --suite=regression
bash scripts/openwrt-vm/test-ui-e2e.sh --suite=regression
```

适合在以下时机执行：

- 合并前人工回归
- 夜间或长时本地回归
- 自托管 runner 上的定时回归

## tier 单入口

仓库提供统一串行入口：

```sh
bash scripts/openwrt-vm/test-all.sh --tier blocking
bash scripts/openwrt-vm/test-all.sh --tier regression
```

行为约定：

- `--tier blocking`：Go 测试 -> 组件 -> VM ready / 单文件 deploy 断言 -> API contract blocking -> UI blocking
- `--tier regression`：Go 测试 -> 组件 -> VM ready / 单文件 deploy 断言 -> API contract regression -> UI regression
- 任一步失败即退出，适合本地和未来 CI 直接复用
- 该脚本只编排现有入口，不改变既有测试语义

## artifacts 位置

### Playwright artifacts

Playwright wrapper 使用 `TP_PLAYWRIGHT_ARTIFACT_DIR` 作为根目录，默认位置为：

```sh
.tmp/openwrt-vm/playwright-artifacts/
```

每次运行会再按 `时间戳 + suite` 切目录，例如：

```sh
.tmp/openwrt-vm/playwright-artifacts/20260310-120000-blocking/
```

其中至少会包含：

- `wrapper.log`
- Playwright 失败证据：
  - trace：配置为 `retain-on-failure`，失败测试必定保留 trace 文件
  - screenshot：配置为 `only-on-failure`，失败测试保留截图
  - video：配置为 `retain-on-failure`，失败测试保留录像
  - 本地与 CI 环境下，只要测试失败，以上证据均会被保留

### VM artifacts

VM 相关日志和失败留痕默认位于：

```sh
.tmp/openwrt-vm/artifacts/
.tmp/openwrt-vm/logs/
.tmp/openwrt-vm/run/
```

其中：

- `collect-artifacts.sh` 会在 wrapper 或 suite 失败时尽力补采 guest 证据
- `api-contract-*.log` 位于 `.tmp/openwrt-vm/logs/`
- QEMU、deploy、wait-ready 等辅助信息也会落在 `.tmp/openwrt-vm/` 下
- 若失败发生在 readiness/deploy 阶段，优先把 `test-common.sh --ensure-vm-ready` 视为真值入口回放问题

## 推荐执行顺序

### 本地开发

先跑快速层：

```sh
cd server && go test ./...
cd portal && npm run test:component
```

需要完整真值验证时再跑：

```sh
bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready
bash scripts/openwrt-vm/test-all.sh --tier blocking
```

需要更长回归时再跑：

```sh
bash scripts/openwrt-vm/test-all.sh --tier regression
```

### 面向 PR 的建议

建议把 `blocking` 作为 PR 阻塞线，把 `regression` 作为人工或定时回归线。当前不要直接假设现有云 CI 能承载 OpenWrt ARM VM。

## 失败排查顺序

排查时按下面顺序走，最快也最省时间：

1. **快速层**：先确认 `cd server && go test ./...` 和 `cd portal && npm run test:component` 是否通过。这里失败通常是后端 API 处理逻辑或前端状态、渲染、交互语义问题。
2. **VM readiness / deploy 层**：再看 `bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready`，先确认单二进制上传、首启自举、canonical wiring 是否成立。
3. **API 契约层**：然后看 `bash scripts/openwrt-vm/test-api-contract.sh --suite=...`。如果这里已经失败，先不要急着看浏览器层。
4. **UI E2E 层**：只有前面几层通过后，才把问题定位到真实浏览器路径。
5. **wrapper log**：先看 Playwright 对应目录下的 `wrapper.log`，确认 `PORTAL_API_TARGET`、fixture 路径、suite 选择是否正确。
6. **Playwright artifacts**：再看 trace、截图、失败步骤，判断是 UI 选择器、等待时序，还是 backend 结果不符。
7. **VM artifacts**：最后看 `.tmp/openwrt-vm/artifacts/`、`.tmp/openwrt-vm/logs/`、guest 侧收集结果，确认是不是 VM 未就绪、单文件 deploy 未完成、首启自举失败或 OpenWrt 侧命令失败。

一句话版顺序：**Go/组件 -> ensure-vm-ready -> API -> E2E / wrapper log -> Playwright -> VM artifacts**。

## CI 路线建议

当前只写路线建议，不直接启用新的 CI pipeline。

建议分层如下：

1. **Tier 1，轻量阻塞**
   - 先在普通环境跑 `cd server && go test ./...` 和 `cd portal && npm run test:component`
   - 这层不要求 VM
2. **Tier 2，VM-backed PR 阻塞**
    - 在自托管 Apple Silicon runner 上跑 `bash scripts/openwrt-vm/test-all.sh --tier blocking`
    - 其真值前提仍是 `scripts/openwrt-vm/test-common.sh --ensure-vm-ready` 所代表的单文件安装契约
    - 这是最有价值的 PR 真值阻塞线
3. **Tier 3，定时或人工长时回归**
   - 在同类 runner 上跑 `bash scripts/openwrt-vm/test-all.sh --tier regression`
   - 适合 nightly、release candidate、合并前人工确认

不建议：

- 假设 GitHub-hosted 或其他云 runner 默认带有可用 Apple Silicon + QEMU + OpenWrt VM 能力
- 把 regression 直接塞进每个 PR 的阻塞链路，导致反馈时间过长

## 常用命令速查

```sh
# 快速层（后端 + 前端）
cd server && go test ./...
cd portal && npm run test:component

# VM readiness / 单文件 deploy 真值入口
bash scripts/openwrt-vm/test-common.sh --ensure-vm-ready

# LuCI/uhttpd 同源承载可行性探针
bash scripts/openwrt-vm/test-luci-proxy-probe.sh
bash scripts/openwrt-vm/test-luci-proxy-probe.sh --force-negative

# API contract
bash scripts/openwrt-vm/test-api-contract.sh --suite=blocking
bash scripts/openwrt-vm/test-api-contract.sh --suite=regression

# 浏览器 E2E，portal 入口
cd portal && npm run test:e2e:blocking
cd portal && npm run test:e2e:regression

# 浏览器 E2E，wrapper 入口
bash scripts/openwrt-vm/test-ui-e2e.sh --suite=blocking
bash scripts/openwrt-vm/test-ui-e2e.sh --suite=regression

# tier 单入口
bash scripts/openwrt-vm/test-all.sh --tier blocking
bash scripts/openwrt-vm/test-all.sh --tier regression
```
