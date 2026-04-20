# CensorHub 系统架构文档

## 1. 项目概述

CensorHub 是一个高性能敏感词过滤服务，基于 **Aho-Corasick 多模式匹配算法**实现敏感内容的检测、替换和高亮。项目采用 **Clean Architecture（整洁架构）** 设计，同时提供 REST API 和 gRPC 两种接入方式。

- **语言**: Go 1.25
- **架构模式**: Clean Architecture / DDD（领域驱动设计）
- **入口**: `cmd/server/main.go`

---

## 2. 系统架构总览

```
┌─────────────────────────────────────────────────────────┐
│                       客户端                             │
│              (HTTP Client / gRPC Client)                │
└──────────┬──────────────────────┬───────────────────────┘
           │ REST (8080)          │ gRPC (9090)
           ▼                      ▼
┌─────────────────────────────────────────────────────────┐
│                   Interfaces 接口层                       │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────┐  │
│  │  HTTP Router  │  │ gRPC Server  │  │  Middleware    │  │
│  │  (Gin)       │  │              │  │  Pipeline     │  │
│  └──────┬───────┘  └──────┬───────┘  └───────────────┘  │
│         │                 │                              │
│  ┌──────┴─────────────────┴───────┐                     │
│  │         Handlers               │                     │
│  │  FilterHandler / WordHandler   │                     │
│  └──────────────┬─────────────────┘                     │
└─────────────────┼───────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────┐
│                 Application 应用层                        │
│  ┌────────────────────┐  ┌────────────────────┐         │
│  │ FilterAppService   │  │  WordAppService     │         │
│  │ (过滤编排)          │  │  (词库管理)          │         │
│  └────────┬───────────┘  └─────────┬──────────┘         │
│           │  DTO / Assembler       │                    │
└───────────┼────────────────────────┼────────────────────┘
            │                        │
            ▼                        ▼
┌─────────────────────────────────────────────────────────┐
│                   Domain 领域层                           │
│  ┌──────────┐ ┌────────────┐ ┌──────────────┐           │
│  │ Entity   │ │ ValueObject│ │ Repository   │           │
│  │ 敏感词实体│ │ 类别/等级   │ │ 接口定义      │           │
│  └──────────┘ └────────────┘ └──────────────┘           │
│  ┌──────────────┐ ┌─────────────┐                       │
│  │ DomainService│ │  Event      │                       │
│  │ FilterEngine │ │  事件定义    │                       │
│  └──────────────┘ └─────────────┘                       │
└─────────────────────────────────────────────────────────┘
            │                        │
            ▼                        ▼
┌─────────────────────────────────────────────────────────┐
│                Infrastructure 基础设施层                   │
│  ┌───────────┐ ┌───────┐ ┌───────┐ ┌────────┐          │
│  │ Algorithm │ │ Cache │ │  DB   │ │  MQ    │          │
│  │ AC自动机   │ │ 多级缓存│ │ MySQL │ │ PubSub │          │
│  └───────────┘ └───────┘ └───────┘ └────────┘          │
│  ┌────────┐ ┌────────┐ ┌────────────────┐              │
│  │ Config │ │ Trace  │ │  Persistence   │              │
│  │ Viper  │ │ OTEL   │ │  GORM实现       │              │
│  └────────┘ └────────┘ └────────────────┘              │
└─────────────────────────────────────────────────────────┘
            │           │           │
            ▼           ▼           ▼
     ┌──────────┐ ┌──────────┐ ┌──────────┐
     │  MySQL   │ │  Redis   │ │  Jaeger  │
     └──────────┘ └──────────┘ └──────────┘
```

---

## 3. 目录结构说明

```
censorhub/
├── api/                          # 协议定义
│   └── proto/censor/v1/          # Protobuf/gRPC 服务定义及生成代码
│
├── cmd/                          # 应用入口
│   └── server/main.go            # 唯一入口：加载配置、初始化依赖、启动服务
│
├── configs/                      # 多环境配置文件
│   ├── config.yaml               # 基础配置
│   ├── config.dev.yaml           # 开发环境覆盖
│   ├── config.staging.yaml       # 预发布环境覆盖
│   ├── config.production.yaml    # 生产环境覆盖（支持环境变量插值）
│   ├── config.test.yaml          # 测试配置
│   └── config.docker.yaml        # Docker Compose 配置
│
├── deployments/                  # 部署配置
│   ├── docker/                   # Dockerfile + docker-compose（含 MySQL/Redis/Jaeger/Prometheus/Grafana）
│   └── kubernetes/               # K8s 部署清单（base / staging / production，Kustomize 分层）
│
├── docs/                         # 文档
│
├── internal/                     # 核心业务逻辑（Clean Architecture 四层）
│   ├── application/              # 应用层：用例编排
│   │   ├── assembler/            # DTO ↔ Entity 转换器
│   │   ├── dto/                  # 数据传输对象（请求/响应结构体）
│   │   └── service/              # 应用服务（FilterAppService, WordAppService）
│   │
│   ├── domain/                   # 领域层：业务规则（零外部依赖）
│   │   ├── entity/               # 实体（SensitiveWord）
│   │   ├── event/                # 领域事件定义
│   │   ├── repository/           # 仓储接口（WordRepository）
│   │   ├── service/              # 领域服务接口（FilterEngine）
│   │   └── valueobject/          # 值对象（Category, RiskLevel, FilterStrategy, FilterResult）
│   │
│   ├── infrastructure/           # 基础设施层：技术实现
│   │   ├── algorithm/            # AC 自动机实现 + 文本归一化
│   │   ├── cache/                # 多级缓存（L1 本地 + L2 Redis + 熔断器）
│   │   ├── config/               # 配置加载（Viper，分层合并 + 环境变量覆盖）
│   │   ├── database/             # 数据库连接（MySQL / Redis）
│   │   ├── mq/                   # Redis Pub/Sub 消息队列
│   │   ├── persistence/mysql/    # GORM 仓储实现 + 数据模型 + 自动迁移
│   │   └── trace/                # OpenTelemetry 分布式追踪（→ Jaeger）
│   │
│   └── interfaces/               # 接口适配层：外部接入
│       ├── grpc/                  # gRPC 服务实现
│       ├── http/                  # HTTP 路由 + Handler + 响应封装
│       │   ├── handler/           # FilterHandler, WordHandler, HealthHandler
│       │   ├── response/          # 统一响应格式
│       │   └── router.go          # Gin 路由注册
│       └── middleware/            # 中间件（Recovery, RequestID, Auth, RateLimit, CORS, Metrics, Tracing, Logger, BodyLimit）
│
├── pkg/                          # 可复用工具包
│   ├── errors/                   # 业务错误码定义（10001~10010）
│   ├── logger/                   # Zap 结构化日志工厂
│   └── metrics/                  # Prometheus 业务指标集中定义（缓存命中、熔断状态、过滤命中、引擎词数/重建次数）
│
├── scripts/                      # 构建/工具脚本
│
├── test/                         # 测试套件
│   ├── e2e/                      # 端到端测试
│   └── integration/              # 集成测试
│
├── Makefile                      # 构建自动化
├── go.mod / go.sum               # Go 模块依赖
└── .golangci.yml                 # Lint 配置
```

---

## 4. 各组件详解

### 4.1 核心算法 — Aho-Corasick 自动机

位于 `internal/infrastructure/algorithm/`。

**实现原理**：
1. **Trie 构建** — 将所有敏感词插入字典树，终端节点存储词元数据（类别、等级）
2. **Fail 指针构建** — BFS 遍历计算失败链接（后缀链接优化）
3. **搜索** — 单次遍历文本，时间复杂度 O(n + m + z)，其中 n=文本长度，m=模式总长，z=匹配数

**文本归一化**（`text_normalizer.go`）：
- NFKC Unicode 归一化
- 移除零宽字符（U+200B, U+200C 等）
- 全角→半角转换
- 统一转小写

**无锁热更新**（`filter_engine.go`）：
```go
type ACFilterEngine struct {
    current atomic.Value  // 存储 *AhoCorasick，读操作无锁
    mu      sync.Mutex    // 仅保护 Rebuild 写操作
}
```
- 读操作（`Match`）通过 `atomic.Value` 实现零竞争
- 写操作（`Rebuild`）通过互斥锁串行化，构建新自动机后原子替换

### 4.2 过滤策略 — 策略模式

定义于 `internal/domain/valueobject/filter_strategy.go`：

| 策略 | 行为 |
|------|------|
| **Detect** | 仅返回匹配结果，不修改文本 |
| **Replace** | 将匹配词替换为 `***` |
| **Highlight** | 将匹配词包裹在 `<mark></mark>` 中 |

### 4.3 多级缓存

```
请求 → L1 LocalCache (进程内, 5min TTL)
         └─ 未命中 → L2 RedisCache (分布式, 30min TTL)
                       └─ 未命中 → 执行过滤 → 写入 L1 + L2
```

- **L1**（`local_cache.go`）— 内存 Map + RWLock + 自动过期清理（每分钟一次，每轮最多淘汰 1000 条，避免长时间持锁）+ 条目数上限（`Cache.LocalMaxItems`，默认 10 万，超限时拒绝新 key 写入）
- **L2**（`redis_cache.go`）— Redis 分布式缓存，支持前缀删除（SCAN COUNT 1000 + Pipeline + UNLINK）
- **MultiLevelCache** — 编排 L1 + L2，内置**熔断器**（连续 5 次失败触发 10 秒 L2 降级，Half-Open 探测 2 次成功后恢复 Closed）
- **缓存 Key**：`filter:{strategy}:{base36(FNV-64a(text))}`（非密码学哈希，性能优于 SHA256）
- **可观测**：L1/L2 命中/未命中计数 → `censorhub_cache_operations_total`；熔断器状态 → `censorhub_circuit_breaker_state`

### 4.4 热更新机制 — Redis Pub/Sub + 本地防抖

频道：`censorhub:word_update`

```
词库变更 (Create/Update/Delete/Import)
  │
  ├─ 立即清除缓存："words:" + "filter:" 前缀（L1 + L2）
  │
  └─ 防抖 500ms（time.AfterFunc，窗口内重复触发会重置计时器）
       │
       └─ 从 DB 加载全部活跃词 → 本地重建 AC 自动机
             │
             └─ 发布消息到 Redis 频道
                  │
                  └─ 其它订阅实例：用带 30s 超时的 ctx 重新加载并重建
```

特性：
- 本地写路径防抖 500ms，避免批量导入触发 N 次重建
- PubSub 订阅端指数退避自动重连（1s → 30s max），ctx 取消时优雅退出
- 关停流程调用 `wordAppService.Close()` 停止未触发的防抖 Timer

### 4.5 应用服务

**FilterAppService**（过滤编排）：
- `Detect` / `Replace` / `Highlight` — 单条过滤
- `BatchDetect` — 批量检测（goroutine 池 + 信号量控制并发）
- 查缓存 → 未命中则调用 Engine → 写缓存

**WordAppService**（词库管理）：
- CRUD 操作 + 引擎重建触发
- `Import` — 批量导入（上限 10000 条，ON DUPLICATE KEY UPDATE）
- `Export` — CSV 导出
- `InitEngine` — 启动时从 DB 加载全部活跃词构建自动机

---

## 5. API 接口

### 5.1 REST API（端口 8080）

**基础路径**: `/api/v1`

#### 过滤接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/filter/detect` | 检测敏感词，返回匹配详情 |
| POST | `/filter/replace` | 替换敏感词为 `***` |
| POST | `/filter/highlight` | 高亮标记敏感词 |
| POST | `/filter/batch` | 批量检测（上限 100 条） |

**请求体**：
```json
{ "text": "待过滤文本（最大 50000 字符）", "strategy": "detect" }
```

**响应体**：
```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "original": "原始文本",
    "filtered": "过滤后文本",
    "is_hit": true,
    "hit_count": 2,
    "matches": [
      { "word": "敏感词", "position": 5, "end_position": 8, "category": "politics", "level": 3 }
    ],
    "risk_level": 3,
    "cost_ms": 1
  },
  "trace_id": "abc123"
}
```

#### 词库管理接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/words` | 分页查询词库 |
| POST | `/words` | 新增敏感词 |
| GET | `/words/:id` | 查询单个词 |
| PUT | `/words/:id` | 更新敏感词 |
| DELETE | `/words/:id` | 删除敏感词 |
| POST | `/words/import` | 批量导入 |
| GET | `/words/export` | CSV 导出 |

#### 运维接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/healthz` | 存活探针 |
| GET | `/readyz` | 就绪探针（检查 DB/Redis） |
| GET | `/metrics` | Prometheus 指标 |

### 5.2 gRPC 接口（端口 9090）

```protobuf
service CensorService {
  rpc Detect(FilterRequest) returns (FilterResponse);
  rpc Replace(FilterRequest) returns (FilterResponse);
  rpc BatchDetect(BatchFilterRequest) returns (BatchFilterResponse);
}
```

---

## 6. 数据模型

### MySQL 表：`sensitive_words`

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT, PK, AUTO_INCREMENT | 主键 |
| text | VARCHAR(255), UNIQUE | 敏感词文本（NFKC 归一化后） |
| category | VARCHAR(50), INDEX | 类别：politics / porn / ad / violence / abuse / custom |
| level | TINYINT, DEFAULT 1 | 风险等级：1=低 2=中 3=高 4=严重 |
| status | TINYINT, DEFAULT 1 | 状态：0=禁用 1=启用 |
| tag | VARCHAR(100), INDEX | 自定义标签 |
| created_at | TIMESTAMP | 创建时间 |
| updated_at | TIMESTAMP | 更新时间 |

---

## 7. 中间件管道

HTTP 请求经过以下中间件链（按执行顺序）：

**全局中间件**（所有路由）：
1. **Recovery** — Panic 恢复 + 日志记录
2. **RequestID** — 生成 UUID 请求标识
3. **BodyLimit** — 请求体大小限制（默认 10MB）
4. **Logger** — 结构化请求日志
5. **CORS** — 跨域处理
6. **Metrics** — Prometheus 计数器/直方图
7. **Tracing** — OpenTelemetry 上下文传播

**API 路由组中间件**：

8. **RateLimit** — 令牌桶限流，按 API Key / IP 分桶（默认每桶 1000 rps，突发 2000，10 分钟未活跃自动回收桶）
9. **Auth** — API Key 验证（仅支持 `X-API-Key` 请求头，已禁用 query string 传递）

**gRPC 拦截器栈**（`cmd/server/main.go:152-160` → `internal/interfaces/grpc/interceptor.go`）：

1. **Recovery** — panic 恢复，统一返回 `codes.Internal`
2. **Logging** — 结构化日志，记录 method / 耗时 / 错误
3. **RateLimit** — 限流（与 HTTP 共用配置）
4. **Auth** — `authorization` / `x-api-key` metadata 校验

另外通过 `google.golang.org/grpc/health` 注册了 `grpc.health.v1.Health` 服务，支持 K8s 1.24+ 的原生 gRPC 探针。

---

## 8. 启动流程

`cmd/server/main.go` 的初始化顺序：

```
1. 加载配置（base + env + 环境变量）
2. 初始化 Zap 结构化日志
3. 初始化 OpenTelemetry 追踪（→ Jaeger）
4. 连接 MySQL + 自动迁移表结构
5. 连接 Redis
6. 创建 WordRepository (GORM)、ACFilterEngine
7. 创建生命周期 ctx（defer cancel），控制所有后台协程的退出
8. 创建 MultiLevelCache (L1 + L2)、Redis PubSub
9. 创建 FilterAppService + WordAppService
10. WordAppService.InitEngine() — 从 DB 加载全部活跃词构建自动机
11. PubSub.SubscribeWordUpdate() — 订阅热更新通知（回调中派生带 30s 超时的 ctx）
12. 构建 gRPC Server：
     - 注册 Recovery / Logging / RateLimit / Auth 四个 UnaryInterceptor
     - 注册 grpc.health.v1.Health，`censor.v1.CensorService` 设为 SERVING
     - 监听端口并启动（goroutine）
13. 构建 HTTP Server（含 IdleTimeout）并启动（goroutine）
14. 监听 SIGINT/SIGTERM，关停时：
     a. cancel() 结束 PubSub 订阅与后台清理协程
     b. wordAppService.Close() 停止防抖 Timer
     c. grpcSrv.GracefulStop()，5 秒超时后兜底 grpcSrv.Stop()
     d. httpSrv.Shutdown(10s ctx)
```

---

## 9. 交互流程图

### 9.1 过滤请求流程

```
客户端 ──POST /filter/detect──▶ Gin Router
                                   │
                          中间件链（Auth, RateLimit...）
                                   │
                                   ▼
                            FilterHandler.Detect()
                                   │
                                   ▼
                          FilterAppService.Detect()
                                   │
                        ┌──── 查缓存 ────┐
                        │                │
                     命中              未命中
                        │                │
                    返回缓存       ACFilterEngine.Match()
                                         │
                                   AC 自动机搜索
                                         │
                                 应用过滤策略（Detect/Replace/Highlight）
                                         │
                                  写入 L1 + L2 缓存
                                         │
                                   返回 FilterResponse
```

### 9.2 词库热更新流程

```
管理员 ──POST /words──▶ WordHandler.Create()
                              │
                              ▼
                      WordAppService.Create()
                              │
                   ┌──────────┼──────────┐
                   │          │          │
              DB 写入     本地重建     发布 PubSub
              (MySQL)    AC 自动机    (Redis 频道)
                                        │
                              ┌─────────┼─────────┐
                              │         │         │
                          实例 A     实例 B     实例 C
                          (各自独立从 DB 加载 → 重建自动机)
                              │         │         │
                          失效缓存   失效缓存   失效缓存
```

---

## 10. 可观测性

| 维度 | 技术 | 说明 |
|------|------|------|
| **日志** | Zap | 结构化 JSON 日志，级别/格式/输出可配 |
| **指标** | Prometheus + Grafana | HTTP 请求计数、延迟直方图、自定义业务指标 |
| **追踪** | OpenTelemetry → Jaeger | 分布式链路追踪，采样率可配（开发 100%，生产 10%） |

---

## 11. 部署架构

### Docker Compose（本地开发）

```
┌─────────────────────────────────────────┐
│           docker-compose.yaml           │
│                                         │
│  ┌───────────┐  ┌───────┐  ┌────────┐  │
│  │ CensorHub │  │ MySQL │  │ Redis  │  │
│  │  :8080    │  │ :3306 │  │ :6379  │  │
│  │  :9090    │  │       │  │        │  │
│  └───────────┘  └───────┘  └────────┘  │
│  ┌───────────┐  ┌───────────┐          │
│  │  Jaeger   │  │Prometheus │          │
│  │  :16686   │  │  :9093    │          │
│  └───────────┘  └───────────┘          │
│  ┌───────────┐                         │
│  │  Grafana  │                         │
│  │  :3000    │                         │
│  └───────────┘                         │
└─────────────────────────────────────────┘
```

### Kubernetes（生产）

- Kustomize 分层：`base/` → `staging/` / `production/`
- Deployment + Service + ConfigMap + HPA
- Liveness（`/healthz`）+ Readiness（`/readyz`）探针

---

## 12. 技术栈总结

| 类别 | 技术 |
|------|------|
| Web 框架 | Gin v1.12 |
| RPC 框架 | gRPC v1.79 + Protobuf v1.36 |
| ORM | GORM v1.31 (MySQL) |
| 缓存 | go-redis/v9 |
| 配置管理 | Viper v1.21 |
| 日志 | Zap v1.27 |
| 指标 | Prometheus Client v1.23 |
| 追踪 | OpenTelemetry v1.42 |
| 文本处理 | golang.org/x/text (NFKC) |
| 限流 | golang.org/x/time (令牌桶) |
| 容器化 | 多阶段 Docker 构建 (alpine) |
| 编排 | Kubernetes + Kustomize |

---

## 13. 配置加载优先级

```
CLI 参数 (--config, --env)
    ↓ 覆盖
环境变量 (CENSORHUB_*)
    ↓ 覆盖
环境配置文件 (config.{env}.yaml)
    ↓ 覆盖
基础配置文件 (config.yaml)
    ↓ 覆盖
硬编码默认值
```

---

## 14. 错误码定义

| 错误码 | 含义 | HTTP 状态码 |
|--------|------|------------|
| 10001 | 敏感词已存在 | 409 |
| 10002 | 敏感词不存在 | 404 |
| 10003 | 无效类别 | 400 |
| 10004 | 无效风险等级 | 400 |
| 10005 | 文本过长 | 400 |
| 10006 | 导入失败 | 500 |
| 10007 | 限流 | 429 |
| 10008 | 未授权 | 401 |
| 10009 | 无效请求 | 400 |
| 10010 | 内部错误 | 500 |
