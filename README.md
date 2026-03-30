# CensorHub

高性能敏感词过滤服务，基于 **Aho-Corasick 多模式匹配算法**，提供 REST API 和 gRPC 双协议接入。支持多策略过滤（检测/替换/高亮）、敏感词热更新、多级缓存、分布式多实例同步，适用于内容审核场景。

## 技术栈

| 类别 | 技术 |
|------|------|
| 语言 | Go 1.25 |
| 架构 | Clean Architecture / DDD |
| 核心算法 | Aho-Corasick 自动机 |
| HTTP 框架 | Gin |
| RPC 框架 | gRPC + Protobuf |
| 数据库 | MySQL 8.0（GORM） |
| 缓存 | Redis 7 (L2) + 本地内存 (L1) |
| 消息同步 | Redis Pub/Sub |
| 日志 | Zap（结构化 JSON） |
| 链路追踪 | OpenTelemetry → Jaeger |
| 指标监控 | Prometheus + Grafana |
| 部署 | Docker / Docker Compose / Kubernetes |

## 架构概览

项目采用 **Clean Architecture** 分层设计，依赖方向由外向内：

```
┌─────────────────────────────────────────────────────────┐
│                    Interfaces 层                         │
│              HTTP (Gin) / gRPC / Middleware              │
├─────────────────────────────────────────────────────────┤
│                   Application 层                         │
│           Service (用例编排) / DTO / Assembler           │
├─────────────────────────────────────────────────────────┤
│                     Domain 层                            │
│       Entity / Value Object / Repository 接口            │
├─────────────────────────────────────────────────────────┤
│                 Infrastructure 层                        │
│   Algorithm / Cache / Database / MQ / Config / Trace    │
└─────────────────────────────────────────────────────────┘
```

## 目录结构

```
censorhub/
├── api/proto/censor/v1/          # Protobuf / gRPC 服务定义
│   └── censor.proto
├── cmd/server/                   # 应用入口
│   └── main.go                   #   启动引导、依赖组装、优雅关停
├── configs/                      # 分环境配置文件
│   ├── config.yaml               #   基础配置
│   ├── config.dev.yaml           #   开发环境覆盖
│   ├── config.staging.yaml       #   预发布环境覆盖
│   ├── config.production.yaml    #   生产环境覆盖
│   └── config.test.yaml          #   测试环境覆盖
├── deployments/                  # 部署相关
│   ├── docker/
│   │   ├── Dockerfile            #   多阶段构建
│   │   ├── docker-compose.yaml   #   本地开发全套服务编排
│   │   └── prometheus.yml        #   Prometheus 采集配置
│   └── kubernetes/               #   K8s 清单 (Deployment/Service/Ingress/HPA/ConfigMap)
│       ├── base/                 #     基础定义
│       ├── staging/              #     预发布 overlay
│       └── production/           #     生产 overlay
├── internal/                     # 核心业务代码（不可外部引用）
│   ├── domain/                   # 领域层 — 纯业务规则，零外部依赖
│   │   ├── entity/               #   业务实体 (SensitiveWord)
│   │   ├── valueobject/          #   值对象 (Category / RiskLevel / WordStatus / FilterStrategy / FilterResult)
│   │   ├── repository/           #   仓储接口 (WordRepository)
│   │   ├── service/              #   领域服务接口 (FilterEngine)
│   │   └── event/                #   领域事件
│   ├── application/              # 应用层 — 用例编排
│   │   ├── service/              #   FilterAppService (过滤) / WordAppService (词库管理)
│   │   ├── dto/                  #   请求/响应数据传输对象
│   │   └── assembler/            #   DTO ↔ Entity 转换器
│   ├── infrastructure/           # 基础设施层 — 接口实现
│   │   ├── algorithm/            #   AC 自动机、过滤策略(Detect/Replace/Highlight)、文本归一化
│   │   ├── cache/                #   多级缓存 (LocalCache + RedisCache + MultiLevelCache)
│   │   ├── database/             #   MySQL / Redis 连接初始化
│   │   ├── persistence/mysql/    #   WordRepository MySQL 实现 + 数据模型 + 自动迁移
│   │   ├── mq/                   #   Redis Pub/Sub 跨实例消息广播
│   │   ├── config/               #   Viper 配置加载 (base → env → 环境变量)
│   │   └── trace/                #   OpenTelemetry 初始化
│   └── interfaces/               # 接口适配层 — 外部接入
│       ├── http/
│       │   ├── router.go         #   Gin 路由注册
│       │   ├── handler/          #   FilterHandler / WordHandler / HealthHandler
│       │   └── response/         #   统一响应格式
│       ├── grpc/
│       │   └── server.go         #   gRPC CensorService 实现
│       └── middleware/           #   中间件栈 (Auth/RateLimit/RequestID/Logger/Metrics/Tracing/Recovery/CORS)
├── pkg/                          # 可复用工具包
│   ├── errors/                   #   业务错误码定义
│   └── logger/                   #   日志初始化
├── test/                         # 测试套件
│   ├── e2e/                      #   端到端测试
│   └── integration/              #   集成测试
├── scripts/                      # 脚本工具
├── Makefile                      # 构建/测试/部署自动化
├── go.mod / go.sum               # Go 模块依赖
└── README.md
```

## 核心流程

### 1. 文本过滤流程

```
客户端请求 (text)
    │
    ▼
中间件栈 (认证 → 限流 → 日志 → 指标 → 追踪)
    │
    ▼
FilterHandler (选择策略)
    │
    ▼
FilterAppService.Filter()
    │
    ├──→ 文本归一化 (Unicode NFKC、移除零宽字符、全角→半角、统一小写)
    │
    ├──→ AC 自动机多模式匹配 (O(n + m + z) 时间复杂度)
    │        n = 文本长度, m = 模式串总长, z = 匹配数
    │
    └──→ 策略应用
         ├─ Detect:   返回匹配列表 (位置、分类、风险等级)
         ├─ Replace:  命中词替换为 ***
         └─ Highlight: 命中词包裹 <mark></mark>
    │
    ▼
FilterResult (原文、过滤结果、命中数、风险等级、耗时)
```

### 2. 敏感词热更新流程

```
管理员 CRUD 操作 (新增/修改/删除敏感词)
    │
    ▼
WordAppService → MySQL 持久化
    │
    ├──→ 从 DB 加载全部活跃词
    ├──→ 原子重建 AC 自动机 (atomic.Value 无锁读)
    ├──→ 清除多级缓存
    └──→ Redis Pub/Sub 广播 "word_update" 事件
              │
              ▼
         其他实例收到事件 → 各自重建 AC 自动机
```

### 3. 多级缓存策略

```
查询请求
    │
    ├──→ L1 本地缓存命中? (TTL 5min) → 返回
    │
    ├──→ L2 Redis 缓存命中? (TTL 30min) → 回填 L1 → 返回
    │
    └──→ 执行实际过滤 → 写入 L1 + L2 → 返回
```

## API 接口

### REST API (默认端口 8080)

**过滤接口** — 需要 `X-API-Key` 认证头

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/v1/filter/detect` | 检测敏感词 |
| `POST` | `/api/v1/filter/replace` | 替换敏感词为 `***` |
| `POST` | `/api/v1/filter/highlight` | 高亮敏感词 |
| `POST` | `/api/v1/filter/batch` | 批量检测（上限 100 条） |

请求体：
```json
{ "text": "需要过滤的文本内容" }
```

**词库管理接口**

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/words` | 分页查询词库 |
| `POST` | `/api/v1/words` | 新增敏感词 |
| `GET` | `/api/v1/words/:id` | 查询单个敏感词 |
| `PUT` | `/api/v1/words/:id` | 更新敏感词 |
| `DELETE` | `/api/v1/words/:id` | 删除敏感词 |
| `POST` | `/api/v1/words/import` | 批量导入（上限 10000 条） |
| `GET` | `/api/v1/words/export` | 导出为 CSV |

**运维接口**

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/healthz` | 存活探针 |
| `GET` | `/readyz` | 就绪探针 |
| `GET` | `/metrics` | Prometheus 指标 |

### gRPC (默认端口 9090)

```protobuf
service CensorService {
  rpc Detect(FilterRequest) returns (FilterResponse);
  rpc Replace(FilterRequest) returns (FilterResponse);
  rpc BatchDetect(BatchFilterRequest) returns (BatchFilterResponse);
}
```

## 数据模型

### sensitive_words 表

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | BIGINT | 主键自增 |
| `text` | VARCHAR(255) | 敏感词文本（唯一索引） |
| `category` | VARCHAR(50) | 分类：politics / porn / ad / violence / abuse / custom |
| `level` | TINYINT | 风险等级：1-低 / 2-中 / 3-高 / 4-严重 |
| `status` | TINYINT | 状态：0-停用 / 1-启用 |
| `tag` | VARCHAR(100) | 自定义标签 |
| `created_at` | TIMESTAMP | 创建时间 |
| `updated_at` | TIMESTAMP | 更新时间 |

## 快速开始

### 前置条件

- Go 1.22+
- Docker & Docker Compose（本地开发推荐）
- MySQL 8.0、Redis 7（若不用 Docker）

### 方式一：Docker Compose（推荐）

```bash
# 启动全部服务 (MySQL + Redis + Jaeger + Prometheus + Grafana + 应用)
make docker-up

# 查看服务状态
docker-compose -f deployments/docker/docker-compose.yaml ps

# 停止服务
make docker-down
```

服务启动后可访问：
- API: http://localhost:8080
- gRPC: localhost:9090
- Jaeger UI: http://localhost:16686
- Prometheus: http://localhost:9093
- Grafana: http://localhost:3000 (admin/admin)

### 方式二：本地开发

```bash
# 确保 MySQL 和 Redis 已启动，配置见 configs/config.dev.yaml

# 开发模式运行
make run-dev

# 或指定环境
make run ENV=staging
```

### 构建

```bash
# 编译二进制
make build

# 运行二进制
./bin/censorhub --config configs/config.yaml --env production
```

## 配置说明

配置采用分层加载机制，优先级从低到高：

1. `configs/config.yaml` — 基础配置
2. `configs/config.{env}.yaml` — 环境覆盖（dev / staging / production / test）
3. 环境变量 — 前缀 `CENSORHUB_`，如 `CENSORHUB_DATABASE_DSN`

## Makefile 命令

```bash
make build          # 编译
make run-dev        # 开发模式运行
make run-prod       # 生产模式运行
make test           # 运行测试 (race detector)
make bench          # AC 算法性能基准测试
make coverage       # 生成覆盖率报告
make lint           # 代码检查 (golangci-lint)
make fmt            # 格式化代码
make proto          # 生成 Protobuf / gRPC 代码
make docker-build   # 构建 Docker 镜像
make docker-up      # 启动 Docker Compose
make docker-down    # 停止 Docker Compose
make clean          # 清理构建产物
```

## Kubernetes 部署

```bash
# 预发布环境
kubectl apply -k deployments/kubernetes/staging/

# 生产环境
kubectl apply -k deployments/kubernetes/production/
```

K8s 部署包含：Deployment、Service、Ingress、HPA（自动扩缩容）、ConfigMap。

## 设计亮点

- **Aho-Corasick 自动机**：一次扫描匹配所有敏感词，时间复杂度 O(n+m+z)
- **无锁热更新**：通过 `atomic.Value` 原子替换自动机实例，读操作零阻塞
- **策略模式**：过滤策略可插拔，易于扩展新策略
- **多级缓存**：L1 本地 + L2 Redis，减少重复计算
- **多实例同步**：Redis Pub/Sub 广播词库变更，所有节点实时生效
- **Unicode 归一化**：NFKC 标准化 + 零宽字符清除 + 全半角转换，防止变体绕过
- **可观测性**：结构化日志 + Prometheus 指标 + OpenTelemetry 分布式追踪
