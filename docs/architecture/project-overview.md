# CensorHub 项目介绍

> 一份完整的项目介绍文档：架构、组件、难点、亮点、过程中踩过的坑与解决方案。

---

## 一、项目背景与定位

CensorHub 是一个**高性能敏感词过滤服务**，面向 UGC 内容审核场景（评论、弹幕、聊天、消息等），核心目标是**在毫秒级完成万级词库的多模式匹配**，并同时解决以下生产化问题：

- 词库**10 万级规模**下端到端 P99 < 200ms（业界「良好」门槛；算法层 AC 搜索 P99 < 1ms）
- 词条增删改的**运行时热更新**（不停机、不阻塞过滤请求）
- 对抗用户插入**零宽字符 / 全角变体 / 大小写混用**等常见绕过手段
- 多实例部署下的**词库一致性同步**
- **高可用**：缓存层故障、Redis 断连等异常下服务不雪崩
- **可观测**：结构化日志 + 业务指标 + 分布式追踪

项目代号 CensorHub，Go 1.25 实现，对外同时提供 **HTTP REST**（管理 + 过滤）和 **gRPC**（低延迟内部调用）双协议。

---

## 二、技术栈总览

| 类别 | 选型 |
|---|---|
| 语言 | Go 1.25 |
| 架构 | Clean Architecture / DDD 分层 |
| 核心算法 | Aho-Corasick 多模式匹配自动机 |
| HTTP 框架 | Gin |
| RPC 框架 | gRPC + Protobuf |
| 持久化 | MySQL 8.0（GORM） |
| 缓存 | 本地 Map + RWMutex（L1）+ Redis 7（L2） |
| 消息 | Redis Pub/Sub（跨实例热更新通知） |
| 配置 | Viper（base → env → 环境变量，分层合并） |
| 日志 | Zap（结构化 JSON） |
| 指标 | Prometheus + Grafana |
| 链路追踪 | OpenTelemetry → Jaeger |
| 容器化 | Docker 多阶段构建 |
| 编排 | Kubernetes + Kustomize（base/staging/production） |

---

## 三、整体架构

### 3.1 分层架构

项目严格遵循 **Clean Architecture** 的四层划分，依赖方向一律从外向内：

```
┌───────────────────────────────────────────────────────┐
│  Interfaces 层（接口适配）                             │
│  - HTTP Router / Handler（Gin）                        │
│  - gRPC Server + Interceptors                         │
│  - Middleware 栈（Auth/RateLimit/Metrics/Tracing 等） │
├───────────────────────────────────────────────────────┤
│  Application 层（用例编排）                            │
│  - FilterAppService（过滤用例：Detect/Replace/Highlight/Batch）│
│  - WordAppService（词库管理：CRUD + Import/Export）    │
│  - DTO 与 Assembler（DTO ↔ Entity 转换）              │
├───────────────────────────────────────────────────────┤
│  Domain 层（纯业务规则，零外部依赖）                   │
│  - Entity: SensitiveWord                              │
│  - ValueObject: Category / RiskLevel / FilterStrategy │
│  - Interface: WordRepository / FilterEngine           │
├───────────────────────────────────────────────────────┤
│  Infrastructure 层（技术实现）                         │
│  - algorithm: AC 自动机 + 文本归一化 + 三种策略        │
│  - cache: LocalCache + RedisCache + MultiLevelCache  │
│  - persistence/mysql: WordRepository 的 GORM 实现     │
│  - mq: Redis Pub/Sub 订阅发布                         │
│  - config / trace / database                          │
└───────────────────────────────────────────────────────┘
```

**关键原则：**
- 领域层只定义接口，不依赖任何第三方库；基础设施层实现这些接口
- 应用层通过构造器注入具体实现，易于替换与单测
- 接口层负责协议适配（HTTP/gRPC），不触碰业务规则

### 3.2 目录结构

```
censorhub/
├── api/proto/censor/v1/        # Protobuf 定义 + 生成代码
├── cmd/server/main.go          # 唯一入口：配置加载、依赖装配、优雅关停
├── configs/                    # 分环境配置（dev/staging/production/test/docker）
├── deployments/
│   ├── docker/                 # Dockerfile + docker-compose
│   └── kubernetes/             # base / staging / production Kustomize overlay
├── internal/
│   ├── domain/                 # 领域层
│   ├── application/            # 应用层
│   ├── infrastructure/         # 基础设施层
│   └── interfaces/             # 接口适配层
├── pkg/
│   ├── errors/                 # 业务错误码（10001~10010）
│   ├── logger/                 # Zap 日志工厂
│   └── metrics/                # Prometheus 业务指标集中定义
├── scripts/
├── test/
├── Makefile
└── go.mod
```

### 3.3 启动流程

`cmd/server/main.go` 是唯一入口，初始化按照依赖顺序进行：

```
1. 加载配置（base + env + 环境变量，CLI 参数优先级最高）
2. 初始化 Zap 日志
3. 初始化 OpenTelemetry Tracer → Jaeger
4. 连接 MySQL，AutoMigrate 建表
5. 连接 Redis
6. 构建依赖：WordRepository、ACFilterEngine
7. 创建生命周期 ctx（defer cancel），统一控制所有后台协程退出
8. 构建 MultiLevelCache（L1 + L2 + 熔断器）、Redis PubSub
9. 构建 FilterAppService + WordAppService + 三种策略
10. InitEngine: 从 DB 加载活跃词条，构建初始 AC 自动机
11. SubscribeWordUpdate: 启动 PubSub 订阅（带指数退避重连）
12. gRPC Server：
     - Chain 4 个 UnaryInterceptor（Recovery/Logging/RateLimit/Auth）
     - 注册业务服务 + grpc.health.v1.Health
     - goroutine 监听
13. HTTP Server：注册路由 + 全局/API 中间件，goroutine 监听
14. 监听 SIGINT/SIGTERM，关停时：
     cancel() → wordAppService.Close() → grpcSrv.GracefulStop(5s兜底) → httpSrv.Shutdown(10s)
```

---

## 四、核心组件详解

### 4.1 AC 自动机（`internal/infrastructure/algorithm/`）

这是项目**最核心的模块**，负责多模式敏感词匹配。

**为什么选 AC 自动机而不是正则或 Trie？**

| 方案 | 时间复杂度 | 说明 |
|---|---|---|
| 逐词 strings.Contains | O(N × L) | N = 词库数，L = 文本长度；万级词库延迟爆炸 |
| 正则 | O(L × 编译复杂度) | 大量模式组合时回溯严重，难以稳定毫秒级 |
| 朴素 Trie | O(L × 最长词长) | 每个位置要从根重试 |
| **AC 自动机** | **O(L + M + Z)** | M = 所有模式总长（构建），Z = 匹配数；**搜索阶段只与文本长度成正比** |

**三步实现：**

1. **构建 Trie**：`insert()` 按 rune 逐层插入，终端节点挂 `wordMeta`（文本、长度、Category、RiskLevel）
2. **BFS 构建 fail 指针**：`buildFailPointers()` 队列从 root 子节点开始，每个节点沿父节点 fail 链查找最长后缀匹配；做了 **suffix-link 优化**——在构建时把 fail 节点的 output 列表合并到当前节点，搜索时每个节点只需遍历本地 output，省去沿 fail 链回收 output 的开销
3. **SearchNormalized**：一次遍历文本 runes，失配时沿 fail 跳转，命中时输出 MatchItem（含 Position/EndPos/Category/Level）

### 4.2 无锁热更新（`filter_engine.go`）

```go
type ACFilterEngine struct {
    current atomic.Value // *AhoCorasick
    mu      sync.Mutex   // 仅保护并发重建
}

func (e *ACFilterEngine) Match(text string) service.MatchResult {
    ac := e.current.Load().(*AhoCorasick) // 读路径无锁
    ...
}

func (e *ACFilterEngine) Rebuild(words []*entity.SensitiveWord) error {
    e.mu.Lock(); defer e.mu.Unlock()
    newAC := NewAhoCorasick(entries)
    e.current.Store(newAC) // 原子替换，读不阻塞
    return nil
}
```

**要点：**
- 读路径通过 `atomic.Value.Load()`，**零锁开销**、无读写竞争
- 写路径用 `sync.Mutex` 串行化，构建完成后原子 Swap，旧自动机由 GC 回收
- AC 自动机的 fail 指针是全局依赖，增量修改复杂度接近全量重建，故采用**全量重建 + atomic swap** 策略

### 4.3 文本归一化流水线（`text_normalizer.go`）

对抗绕过的核心设计，四层处理：

1. **Unicode NFKC 正规化**：把兼容等价字符合并（如连字 ﬁ → f+i）
2. **零宽字符剥离**：清除 12 种零宽/不可见字符（ZWS、ZWNJ、ZWJ、BOM、Soft Hyphen、LRM、RLM、Word Joiner、Function Application、Invisible Times/Separator/Plus）
3. **全角转半角**：U+FF01~U+FF5E 映射到 ASCII，U+3000 全角空格转普通空格
4. **大小写折叠**：`unicode.ToLower` 统一小写

**入库与匹配两端都调用同一套 Normalize**（`NormalizeForIndex` 额外 trim 前后空白），确保索引与匹配一致。

这样即使用户输入 `Ｆ‌Ｕ‌Ｃ‌Ｋ`（全角 + 零宽），归一化后和词库 `fuck` 可以精确匹配。

### 4.4 策略模式（三种过滤策略）

所有策略实现同一接口：

```go
type FilterStrategy interface {
    Apply(original, normalized string, matches []MatchItem) *FilterResult
}
```

| 策略 | 行为 | 关键点 |
|---|---|---|
| **Detect** | 只返回匹配列表，不改文本 | 最轻量 |
| **Replace** | 命中词替换为 `*` | 合并重叠区间后统一替换；归一化前后长度相同则在原文上 mask，不同则降级到归一化文本 |
| **Highlight** | 用 `<mark></mark>` 包裹命中词 | 对非匹配/匹配文本都做 `html.EscapeString` 防 XSS |

**性能优化：** `FilterEngine.Match` 返回 `MatchResult{Matches, NormalizedText}`，策略层直接复用，**不重复调用 Normalize**。

### 4.5 多级缓存 + 熔断降级（`internal/infrastructure/cache/`）

```
请求 → L1 LocalCache（进程内，5min TTL，最多 10 万条）
         └─ 未命中 → 熔断器 Allow? → L2 Redis（30min TTL）
                                   └─ 未命中或熔断 → 执行过滤 → 回填 L1 + L2
```

**L1 LocalCache:**
- `map[string]*cacheItem` + `sync.RWMutex`
- 后台 goroutine 每分钟清理过期条目，**单轮最多淘汰 1000 条**后释放锁（避免长时间持锁影响过滤接口 P99）
- 有 **maxItems 容量上限**（默认 10 万），超限时拒绝新 key 写入

**L2 RedisCache:**
- 前缀删除使用 **SCAN COUNT 1000 + Pipeline + UNLINK**（UNLINK 不阻塞 Redis 主线程）

**MultiLevelCache（编排层）:**
- 内置 **CircuitBreaker**：连续 5 次 Redis 失败 → Open；10 秒后 Half-Open 探测；连续 2 次成功 → Closed
- 熔断期间 `Get` 直接返回 `redis.Nil`、写操作仅落 L1，**Redis 故障不向上层传播、不拖慢过滤请求**
- `redis.Nil` **不计入失败次数**（缓存未命中是业务正常情况）

**缓存 Key**：`filter:{strategy}:{base36(FNV-64a(text))}`——FNV 非密码学哈希，比 SHA256 快约一个数量级，不需要抗碰撞安全。

**可观测：**
- `censorhub_cache_operations_total{level, result}`：L1/L2 hit/miss
- `censorhub_circuit_breaker_state`：0=closed, 1=open

### 4.6 跨实例热更新（`internal/infrastructure/mq/redis_pubsub.go`）

频道：`censorhub:word_update`

**写路径（`word_app_service.go triggerRebuild`）：**

```go
func (s *WordAppService) triggerRebuild(ctx context.Context) {
    // 1. 立即清除缓存：words: 和 filter: 两个前缀
    s.cache.InvalidateByPrefix(ctx, "words:")
    s.cache.InvalidateByPrefix(ctx, "filter:")

    // 2. 防抖 Timer：500ms 内重复触发只保留最后一次
    s.rebuildMu.Lock(); defer s.rebuildMu.Unlock()
    if s.rebuildTimer != nil { s.rebuildTimer.Stop() }
    s.rebuildTimer = time.AfterFunc(500*time.Millisecond, func() {
        rebuildCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        words, _ := s.repo.FindAllActive(rebuildCtx)
        s.engine.Rebuild(words)
        s.pubsub.PublishWordUpdate(rebuildCtx) // 通知其他实例
    })
}
```

**读路径（订阅端）：**
- 外层 `SubscribeWordUpdate` 是重连循环（**指数退避 1s → 30s max**），ctx 取消时优雅退出
- 内层 `runSubscription` 先 `sub.Receive()` 验证订阅建立成功，再进入消息循环
- 订阅回调中派生带 **30s 超时的 ctx**，避免用已取消的生命周期 ctx 执行 DB 查询

### 4.7 中间件栈

**HTTP 全局中间件**（按执行顺序）：

1. Recovery — panic 恢复
2. RequestID — 生成/透传请求 ID
3. BodyLimit — `http.MaxBytesReader` 限制 Body 大小（默认 10MB，防 OOM）
4. Logger — 结构化访问日志
5. CORS — 按配置白名单匹配 Origin，命中才回写 `Access-Control-Allow-Origin + Vary: Origin`
6. Metrics — Prometheus 计数器 + 延迟直方图
7. Tracing — OpenTelemetry 上下文传播

**API 路由组中间件：**

8. RateLimit — **按 API Key / IP 分桶限流**（每桶 1000 rps, burst 2000；10 分钟未活跃的桶自动回收）
9. Auth — **仅支持 `X-API-Key` 请求头**（禁用了 query string 传递，避免出现在访问日志/Referer/浏览器历史中）

**gRPC 拦截器栈：**

自研 4 个 UnaryInterceptor（Recovery / Logging / RateLimit / Auth），配置与 HTTP 共用；另注册 `grpc.health.v1.Health` 支持 K8s 1.24+ 原生 gRPC 探针。

---

## 五、关键流程

### 5.1 过滤请求

```
POST /api/v1/filter/detect
  → Middleware 栈（Auth / RateLimit / ...）
  → FilterHandler.Detect
  → FilterAppService.Filter
       ├─ filterCacheKey = "filter:detect:" + base36(FNV(text))
       ├─ multiCache.Get(key)
       │    ├─ L1 hit? → 反序列化返回
       │    ├─ breaker.Allow? L2 Redis get → 回填 L1
       │    └─ miss 或熔断
       ├─ engine.Match(text) → MatchResult{Matches, NormalizedText}
       ├─ strategy.Apply(original, normalized, matches)
       ├─ FilterHitsTotal metric + 异步写缓存
       └─ 返回 FilterResponse（original/filtered/hit_count/matches/risk_level/cost_ms）
```

### 5.2 词条更新（CRUD）

```
POST /api/v1/words（管理员）
  → WordHandler.Create → WordAppService.Create
       ├─ DTO → Entity → Validate
       ├─ FindByText 去重 → repo.Create (MySQL)
       └─ triggerRebuild(ctx):
            ├─ 立即清除 words: 和 filter: 前缀缓存（L1 + L2）
            └─ time.AfterFunc(500ms):
                 ├─ FindAllActive → engine.Rebuild
                 ├─ metrics.EngineRebuildTotal.Inc() + EngineWordCount.Set
                 └─ pubsub.PublishWordUpdate → 通知其他实例
其他实例：
  PubSub 回调 → FindAllActive → engine.Rebuild（本地）
```

### 5.3 批量导入

```
POST /api/v1/words/import （最多 10000 条）
  → WordAppService.Import
       ├─ 逐条 Validate，失败的收集到 ImportFailure[]（index/word/reason）
       ├─ repo.BatchCreate（ON DUPLICATE KEY UPDATE）
       ├─ triggerRebuild（防抖确保 N 条只触发 1 次重建）
       └─ 返回 ImportResponse{total, imported, skipped, failures}
```

### 5.4 流式导出

```
GET /api/v1/words/export
  → WordAppService.ExportToWriter(ctx, category, http.ResponseWriter)
       ├─ csv.NewWriter 直接挂到响应流
       └─ repo.FindInBatches(ctx, category, 1000, func(batch) {
              // 每批 1000 条写入 CSV → Flush
          })
```

全程**不一次性加载全部词条到内存**，规避大词库 Export 的 OOM 风险。

---

## 六、项目难点与解决方案

### 难点 1：多模式匹配的性能瓶颈

**问题：** 词库从千级扩到万级后，逐词匹配 O(N×L) 延迟失控。

**解决：** 引入 AC 自动机，时间复杂度降到 O(L + Z)（M 只在构建阶段）；搜索阶段与词库规模无关，只看文本长度。Benchmark 验证：10 万词库 + 1KB 文本单次匹配在微秒级，加上缓存 P99 < 1ms。

### 难点 2：引擎热更新不能阻塞线上过滤

**问题：** AC 自动机 fail 指针是全局依赖，增量更新极其复杂；全量重建耗时百毫秒级，直接加锁会阻塞过滤请求。

**解决：** `atomic.Value.Store` 原子 Swap + `sync.Mutex` 串行化重建，读路径零阻塞；旧自动机由 GC 自然回收。

### 难点 3：批量更新引爆 rebuild

**问题：** 运营导入 1000 条敏感词 → 触发 1000 次 rebuild，每次百毫秒 → 实例在接下来 100 秒内几乎打满 CPU。

**解决：** `time.AfterFunc(500ms)` 防抖——窗口内多次触发只保留最后一次。关键细节：
- 缓存清除**立即执行**，保证期间新请求不会命中旧缓存
- 防抖回调里必须用 `context.Background()` 派生 ctx（不能继承请求 ctx，因为 request 早已结束）
- 进程关停时必须 `wordAppService.Close()` 主动停掉 Timer，否则可能在 DB 连接已关闭后回调仍然触发

### 难点 4：文本变体绕过

**问题：** 用户用 `敏`+ZWJ+`感`、全角 `Ｆｕｃｋ`、混用大小写等手段绕过检测。

**解决：** 四层归一化流水线（NFKC + 零宽剥离 + 全角→半角 + toLower），索引与搜索两端都过同一套 → 词库只需存归一化形式，匹配时用户输入也先归一化，两端天然对齐。

### 难点 5：多实例词库一致性

**问题：** 多副本部署，管理员在一个实例上改词条，其他实例还是旧词库。

**解决：** Redis Pub/Sub 广播 `"rebuild"` 消息 + 各实例独立从 DB 重新加载。
- 因为消息不需要持久化（重启会自动从 DB 加载兜底），Pub/Sub 比 Redis Streams / Kafka 更轻量
- 订阅端做了**指数退避重连**（1s → 30s），ctx 取消时优雅退出

### 难点 6：Redis 故障不能拖垮过滤接口

**问题：** 单纯多级缓存下，Redis 超时每次都等几秒 → 接口 P99 飙到秒级 → 雪崩。

**解决：** 轻量自研 `CircuitBreaker`（三态：Closed / Open / Half-Open），无外部依赖：
- 连续 5 次 Redis 失败 → Open，之后所有 Redis 操作直接跳过
- 10 秒后 Half-Open，探测 2 次成功 → Closed
- `redis.Nil`（未命中）**不计失败**，避免冷 key 触发假熔断
- 熔断期间降级为仅 L1 + 引擎计算，**业务完全无感**

### 难点 7：缓存一致性 Bug

**问题：** 第一版实现时，`triggerRebuild` 只清了 `words:` 前缀，忽略了 `filter:`（过滤结果缓存）前缀。词条更新后，过滤结果最长 30 分钟（L2 TTL）仍然基于旧词库。

**解决：** 同步清除两个前缀：
```go
s.cache.InvalidateByPrefix(ctx, "words:")
s.cache.InvalidateByPrefix(ctx, "filter:")
```
并在 cache-consistency-analysis.md 中显式记录这次修复。

### 难点 8：Highlight 策略的 XSS 风险

**问题：** 原始文本如果包含 `<script>alert(1)</script>`，Highlight 策略会原样拼进 HTML，前端 innerHTML 渲染时触发 XSS。

**解决：** 非匹配与匹配两部分都走 `html.EscapeString`，只有 `<mark>` 包裹标签本身是字面量，其余一律转义。

### 难点 9：限流误伤

**问题：** 原实现用一个全局 `rate.Limiter`，所有客户端共享同一个令牌桶，一个高频客户端就能把配额耗尽。

**解决：** `perClientLimiter`——按 `X-API-Key`（认证通过后）或 `client_ip`（未认证）分桶，每桶独立 rate.Limiter；10 分钟未活跃的桶由后台 goroutine 定期回收（避免 map 无限增长）。

### 难点 10：优雅关停的边界

踩过多个坑，最终形成完整关停流程：

1. **LocalCache cleanup 协程泄漏**：原实现 `go lc.cleanup()` 无退出机制 → 改为接收 ctx，`select { case <-ctx.Done(): return }`
2. **triggerRebuild 用 `context.Background()`**：关停时仍无限执行 → 改为 `WithTimeout(parent, 30s)`，并随防抖改造派生自 `context.Background()` + Close() 兜底
3. **gRPC GracefulStop 无超时**：长 BatchDetect 会阻塞关停 → 加 5s 超时后 `grpcSrv.Stop()` 强停
4. **PubSub 回调用 `context.Background()`**：关停时仍跑 DB 查询 → 改为派生带 30s 超时的 ctx
5. **rebuildTimer 进程退出后触发**：DB 已关闭，回调仍跑 → 新增 `WordAppService.Close()`，关停链路主动 Stop Timer

---

## 七、工程亮点

### 7.1 架构层面

- **Clean Architecture 严格分层**：领域层零外部依赖；基础设施层实现领域接口；应用层通过构造器注入，单测只需替换接口
- **错误统一**：`pkg/errors` 定义业务错误码（10001~10010），中间件按错误类型映射 HTTP 状态码
- **配置分层**：Viper 三层合并（base → env → 环境变量 `CENSORHUB_*`），生产 yaml 支持环境变量插值
- **依赖注入清晰**：所有依赖在 `main.go` 装配，便于静态追踪

### 7.2 性能层面

- AC 自动机 + suffix-link 优化：搜索阶段 O(L + Z)
- `atomic.Value` 无锁读热更新：读路径零竞争
- 多级缓存 + 熔断降级：热点文本 P99 < 1ms，Redis 故障时自动降级
- 策略层不重复 Normalize：`MatchResult` 携带归一化文本
- 缓存 Key FNV-64a 代替 SHA256：大文本场景约 10× 加速
- Redis 前缀删除：SCAN 1000 + Pipeline + UNLINK，不阻塞 Redis 主线程

### 7.3 可靠性层面

- 防抖机制避免批量更新引爆 rebuild
- PubSub 指数退避重连 + ctx 优雅退出
- BatchDetect semaphore 限制并发协程数（`runtime.NumCPU()`）
- 入口处 `BodyLimit` 防大 body OOM
- 优雅关停五步走（cancel → Close → GracefulStop 5s 兜底 → Shutdown 10s）

### 7.4 安全层面

- API Key 只接受 Header（禁用 query string）
- CORS 白名单匹配（支持 `*` 开发模式 / 生产严格白名单）
- Highlight/Replace 输出 HTML 转义防 XSS
- 限流按 API Key/IP 分桶隔离
- 归一化防绕过（零宽字符/全角/大小写）

### 7.5 可观测层面

集中定义在 `pkg/metrics/metrics.go`：

| 指标 | 类型 | 用途 |
|---|---|---|
| `censorhub_cache_operations_total{level, result}` | Counter | L1/L2 hit/miss 命中率 |
| `censorhub_circuit_breaker_state` | Gauge | 熔断器状态（0/1/2） |
| `censorhub_filter_hits_total{strategy, is_hit}` | Counter | 过滤命中率 |
| `censorhub_engine_word_count` | Gauge | 当前引擎词条数 |
| `censorhub_engine_rebuild_total` | Counter | 重建次数 |

加上 Zap 结构化日志（关联 request_id、trace_id）+ OpenTelemetry 链路（Jaeger），三维可观测性闭环。

### 7.6 部署层面

- Docker 多阶段构建：生产镜像仅静态二进制 + alpine（镜像指明 digest 锁定方式）
- docker-compose 锁版本：Jaeger 1.55 / Prometheus v2.51.0 / Grafana 10.4.0
- K8s Kustomize 分层：base 定义 + staging/production overlay
- HPA + PodDisruptionBudget（`minAvailable: 1`，防驱逐全灭）
- Readiness 探针检查 MySQL + Redis（Redis 挂不能接流量）
- gRPC 支持 K8s 1.24+ 原生 gRPC 探针

---

## 八、性能实测

| 场景 | 指标 | 结果 |
|---|---|---|
| 1 万词库 + 1KB 文本 | 单次匹配 | 微秒级 |
| 10 万词库构建 | 全量重建耗时 | ~100ms |
| 热点文本（命中缓存） | P99 延迟 | < 1ms |
| 词条变更 | 引擎更新窗口 | 500ms（防抖）+ 重建耗时 |

---

## 九、遗留与未来优化

| 项 | 说明 |
|---|---|
| 测试覆盖 | `test/e2e/` 和 `test/integration/` 目录当前仍为空；需要补缓存一致性、熔断器状态机、BatchDetect 压测、PubSub 断线重连的集成测试 |
| PubSub 重连后补 rebuild | 订阅断线期间的更新通知会丢失；重连成功后应无条件触发一次 rebuild，消除短暂不一致窗口 |
| 熔断恢复后主动清理 | Breaker 从 Open → Closed 后，可以主动 scan 清理 Redis 中可能残留的脏缓存 |
| 词库版本号 | 加一个定时轮询（如 5min）对比词库版本号做最终一致性兜底 |
| gRPC Tracing | 自研拦截器未接 OpenTelemetry，可引入 `otelgrpc.UnaryServerInterceptor` |
| DAT 算法 | 词库稳定到百万级后，可考虑 Double-Array Trie 进一步降内存、提 cache 命中率（需要放弃动态增量） |

---

## 十、总结

CensorHub 的价值不只是"实现了 AC 自动机"，而是围绕**"高性能多模式匹配"这个核心能力，工程化地解决了热更新、多实例一致性、缓存降级、文本变体绕过、安全、可观测、优雅关停**等一整套生产化问题。

- **架构**：Clean Architecture 严格四层 + DDD
- **核心**：AC 自动机 + atomic swap 无锁热更新 + 四层归一化
- **可靠**：多级缓存 + 熔断降级 + PubSub 指数退避 + 防抖
- **安全**：Header-only Auth + CORS 白名单 + XSS 转义 + 分桶限流
- **可观测**：Prometheus + Jaeger + Zap 三位一体

在初版实现后经历了两轮系统性 code review（19 项 + 16 项），两轮修复全部落地，最终形成当前这套兼顾性能与工程健壮性的设计。
