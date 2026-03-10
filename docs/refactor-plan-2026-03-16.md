# 重构计划 - 2026-03-16

## 目标

1. 删除"配置管理"和"运维中心"功能（前后端）
2. 精简后端 Go 代码
3. 重构 Checker 服务为自实现、可配置的网络检测
4. 前端状态页增加代理状态显示和 Checker 配置
5. 前端增加 CHNROUTE 同步按钮

---

## Phase 1: 删除不需要的功能

### 1.1 前端删除

**删除文件：**
- `portal/src/features/config/ConfigPage.tsx`
- `portal/src/features/config/ConfigPage.css`
- `portal/src/features/config/config.test.tsx`
- `portal/src/features/ops/OpsPage.tsx`
- `portal/src/features/ops/ops.test.tsx`

**修改文件：**
- `portal/src/App.tsx` - 移除 config 和 ops 路由
- `portal/src/lib/api/client.ts` - 移除 config/audit/health/rollback 相关 API

### 1.2 后端删除

**删除文件：**
- `server/config_service.go` - 配置服务
- `server/audit_service.go` - 审计服务
- `server/health_service.go` - 健康检查服务
- `server/api_router.go` 中的 config/apply/audit/rollback handlers
- `server/openwrt_service.go` 中的 overlay/rollback 相关代码

**保留文件：**
- `server/nft_service.go` - 规则管理
- `server/checker_service.go` - 重构为自实现
- `server/app.go` - 简化
- `server/api_rules.go` - 规则 API
- `server/api_status.go` - 状态 API

---

## Phase 2: 重构 Checker 服务

### 2.1 新的 Checker 配置结构

```yaml
# config.yaml
checker:
  enabled: true                    # 是否启用检测
  method: "GET"                    # GET 或 HEAD
  url: "https://www.google.com"    # 检测目标 URL
  host: ""                         # 可选，覆盖 Host 头
  timeout: "5s"                    # 超时时间
  failureThreshold: 3              # 连续失败几次视为断网
  checkInterval: "30s"             # 检测间隔
  onFailure: "/etc/transparent-proxy/disable.sh"   # 断网时执行
  onSuccess: "/etc/transparent-proxy/enable.sh"    # 恢复时执行
```

### 2.2 新的 Checker 实现

**文件：** `server/checker.go`

```go
type CheckerConfig struct {
    Enabled          bool          `yaml:"enabled"`
    Method           string        `yaml:"method"`           // GET or HEAD
    URL              string        `yaml:"url"`
    Host             string        `yaml:"host"`             // Override Host header
    Timeout          time.Duration `yaml:"timeout"`
    FailureThreshold int           `yaml:"failureThreshold"`
    CheckInterval    time.Duration `yaml:"checkInterval"`
    OnFailure        string        `yaml:"onFailure"`        // Script to run on failure
    OnSuccess        string        `yaml:"onSuccess"`        // Script to run on recovery
}

type Checker struct {
    config    CheckerConfig
    runtime   Runtime
    status    int32          // 0 = down, 1 = up
    failures  int32          // consecutive failures
    mu        sync.RWMutex
    stopCh    chan struct{}
}

func (c *Checker) Start(ctx context.Context)
func (c *Checker) Stop()
func (c *Checker) Status() int          // 0 = down, 1 = up
func (c *Checker) UpdateConfig(cfg CheckerConfig) error
```

### 2.3 状态变化逻辑

```
初始状态: 检测中
检测成功 → status = up, failures = 0
检测失败 → failures++
failures >= threshold → status = down, 执行 onFailure

状态为 down 时：
检测成功 → status = up, failures = 0, 执行 onSuccess
检测失败 → 继续失败计数（不变）
```

---

## Phase 3: API 简化

### 3.1 保留的 API

| API | 用途 |
|-----|------|
| `GET /api/status` | 获取规则状态 + Checker 状态 |
| `GET /api/ip` | 获取客户端 IP |
| `GET /api/rules` | 获取所有规则 |
| `POST /api/rules/add` | 添加规则 |
| `POST /api/rules/remove` | 删除规则 |
| `POST /api/rules/sync` | 同步规则到文件 |
| `POST /api/refresh-route` | 刷新 CHNROUTE |
| `GET /api/checker` | 获取 Checker 配置和状态 |
| `PUT /api/checker` | 更新 Checker 配置 |

### 3.2 删除的 API

| API | 原用途 |
|-----|--------|
| `GET /api/health` | 健康检查（合并到 /api/status） |
| `GET /api/configs` | 配置列表 |
| `GET /api/configs/:name` | 配置详情 |
| `PUT /api/configs/:name` | 保存配置 |
| `POST /api/configs/:name/validate` | 验证配置 |
| `POST /api/apply/preview` | 预览变更 |
| `POST /api/apply/execute` | 应用变更 |
| `GET /api/audit` | 审计日志 |
| `POST /api/rollback` | 回滚 |

---

## Phase 4: 前端重构

### 4.1 新的状态页面结构

```
┌─────────────────────────────────────────────────────────────────────┐
│  透明代理管理                                                       │
├─────────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ 代理状态                                                     │   │
│  │   状态: ● 运行中 / ○ 已禁用                                  │   │
│  │   检测器: ● 正常 / ○ 异常 / ○ 已禁用                         │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ 网络检测配置                                                 │   │
│  │   [✓] 启用网络检测                                          │   │
│  │   检测方法: [GET ▼]  URL: [https://www.google.com        ]  │   │
│  │   Host 头: [                    ]  (可选)                   │   │
│  │   超时时间: [5   ] 秒    失败阈值: [3  ] 次                  │   │
│  │   检测间隔: [30  ] 秒                                        │   │
│  │   断网时执行: [/etc/transparent-proxy/disable.sh         ]  │   │
│  │   恢复时执行: [/etc/transparent-proxy/enable.sh          ]  │   │
│  │                                             [保存配置]       │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ 规则管理                              [同步CHNROUTE]        │   │
│  │   proxy_src: [添加 IP] [IP列表...]                          │   │
│  │   proxy_dst: [添加 IP] [IP列表...]                          │   │
│  │   direct_src: [添加 IP] [IP列表...]                         │   │
│  │   direct_dst: [添加 IP] [IP列表...]                         │   │
│  │                                             [同步到文件]     │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.2 页面结构

```
portal/src/
├── App.tsx                 # 简化为单页
├── app/
│   └── AppShell.tsx        # 简化导航
├── features/
│   ├── rules/
│   │   ├── RulesPage.tsx   # 规则管理
│   │   └── RulesPage.css
│   └── status/
│       ├── StatusPage.tsx  # 合并状态 + Checker 配置
│       └── StatusPage.css
└── lib/
    └── api/
        └── client.ts       # 简化 API
```

---

## Phase 5: 后端文件精简

### 5.1 删除文件

```
server/config_service.go      # 配置服务
server/audit_service.go       # 审计服务
server/health_service.go      # 健康服务
server/openwrt_manifest.go    # Manifest 管理
server/managed_ledger.go      # 账本
server/managed_assets_embedded.go
server/managed_assets_generated.go
server/generate_embedded.go
server/api_router.go          # 大量 handler
```

### 5.2 保留并简化的文件

```
server/main.go               # 入口
server/bootstrap.go          # 启动
server/app.go                # 应用核心
server/config.go             # 配置结构（简化）
server/runtime.go            # 运行时
server/dev_mode.go           # 开发模式
server/dev_file_writer.go    # 开发写入
server/errors.go             # 错误类型
server/nft_service.go        # nft 规则
server/checker.go            # 新的 Checker 实现
server/api_handlers.go       # 合并所有 handler
server/api_response.go       # 响应工具
server/web.go                # 前端资源
```

---

## 执行顺序

1. ✅ 创建此文档
2. 删除前端 config/ops 页面
3. 删除后端 config/audit/health 服务
4. 重构 Checker 服务
5. 更新前端状态页
6. 精简后端代码
7. 运行测试
8. 更新文档

---

## 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 删除过多导致编译失败 | 高 | 分步删除，每步验证编译 |
| 前端路由断裂 | 中 | 同时更新路由配置 |
| 配置格式不兼容 | 高 | 保持 config.yaml 向后兼容 |
| Checker 逻辑错误 | 高 | 充分测试各种状态转换 |

---

**开始时间:** 2026-03-16 23:45
**预计完成:** 2026-03-17 06:00
