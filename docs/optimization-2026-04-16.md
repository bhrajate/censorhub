# CensorHub 优化分析报告（2026-04-16）

> 基于当前代码状态的全面审查。前一轮优化（见 `optimization-changelog.md`）已修复 19 项问题，本次聚焦于**仍然存在的问题**和**新发现的优化点**。

---

## 一、严重 — 缓存一致性 Bug

### 1.1 词库更新未清除过滤结果缓存

**文件：** `internal/application/service/word_app_service.go:250`

**问题：** `triggerRebuild` 中只清除了 `words:` 前缀的缓存：

```go
s.cache.InvalidateByPrefix(ctx, "words:")
```

但过滤结果缓存使用的是 `filter:` 前缀（`filter_app_service.go:47`）：

```go
return "filter:" + strategy + ":" + hex.EncodeToString(h[:16])
```

这意味着词库增删改后，旧的过滤结果仍从缓存中返回，直到 TTL 自然过期（L1 = 5min，L2 = 30min）。**最长 30 分钟内，用户看到的过滤结果可能与最新词库不一致。**

`optimization-changelog.md` 3.3 节明确写了"词库更新时通过 `InvalidateByPrefix("filter:")` 自动清除所有过滤结果缓存"，但实际代码并未实现这一点。

**修复方案：**

```go
func (s *WordAppService) triggerRebuild(ctx context.Context) {
    // 清除词条缓存
    s.cache.InvalidateByPrefix(ctx, "words:")
    // 清除过滤结果缓存（关键！）
    s.cache.InvalidateByPrefix(ctx, "filter:")
    // ... 后续防抖重建逻辑不变
}
```

---

### 1.2 Highlight 策略存在 XSS 风险

**文件：** `internal/infrastructure/algorithm/strategy_highlight.go:66-104`

**问题：** `highlightMatches` 将用户输入的原始文本直接拼接进 HTML（`<mark>...</mark>`），但未对非匹配部分进行 HTML 实体转义。如果原始文本包含 `<script>alert('xss')</script>`，且这段文本不是敏感词，它会原样出现在输出中。

当 Highlight 结果被前端直接 `innerHTML` 渲染时（这是高亮功能的典型用法），会触发 XSS 攻击。

**修复方案：** 在输出非匹配区间的 rune 时，对 HTML 特殊字符进行转义：

```go
import "html"

func highlightMatches(original string, matches []valueobject.MatchItem) string {
    // ... 前置逻辑不变

    var b strings.Builder
    idx := 0
    for _, iv := range intervals {
        // 写入区间前的文本（转义）
        for i := idx; i < iv.start && i < len(targetRunes); i++ {
            b.WriteString(html.EscapeString(string(targetRunes[i])))
        }
        // 写入高亮标记（匹配内容也需转义）
        b.WriteString("<mark>")
        for i := iv.start; i < iv.end && i < len(targetRunes); i++ {
            b.WriteString(html.EscapeString(string(targetRunes[i])))
        }
        b.WriteString("</mark>")
        idx = iv.end
    }
    // 写入剩余文本（转义）
    for i := idx; i < len(targetRunes); i++ {
        b.WriteString(html.EscapeString(string(targetRunes[i])))
    }
    return b.String()
}
```

---

## 二、高危 — 功能缺陷

### 2.1 gRPC 端无中间件保护

**文件：** `cmd/server/main.go:148-149`、`internal/interfaces/grpc/server.go`

**问题：** HTTP 端有完整的中间件栈（Recovery、RequestID、BodyLimit、Logger、CORS、Metrics、Tracing、RateLimit、Auth），但 gRPC Server 完全裸跑：

```go
grpcSrv := grpc.NewServer() // 无任何 interceptor
```

gRPC 端口（9090）缺少：
- **认证**：任何人可以直接调用 gRPC 接口
- **限流**：无法防御 gRPC 端的暴力请求
- **日志**：gRPC 请求无日志记录，排障困难
- **Metrics**：Prometheus 指标不包含 gRPC 流量
- **Tracing**：gRPC 请求不参与链路追踪
- **Panic Recovery**：gRPC handler panic 会导致整个 server 崩溃

**修复方案：** 使用 gRPC interceptor（UnaryInterceptor + StreamInterceptor）：

```go
import (
    grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
    grpc_logging "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
    "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
)

grpcSrv := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        otelgrpc.UnaryServerInterceptor(),        // Tracing
        grpc_recovery.UnaryServerInterceptor(),    // Panic recovery
        // 自定义 Auth interceptor
        // 自定义 RateLimit interceptor
    ),
)
```

---

### 2.2 gRPC GracefulStop 无超时兜底

**文件：** `cmd/server/main.go:190`

**问题：** `grpcSrv.GracefulStop()` 会等待所有正在处理的 RPC 完成，但没有超时限制。如果有一个长时间运行的 gRPC 请求（如 BatchDetect 处理大批量数据），优雅关停可能无限等待。

```go
grpcSrv.GracefulStop() // 可能永远阻塞
```

**修复方案：** 设置超时，超时后强制关闭：

```go
grpcDone := make(chan struct{})
go func() {
    grpcSrv.GracefulStop()
    close(grpcDone)
}()

select {
case <-grpcDone:
    log.Info("gRPC server stopped gracefully")
case <-time.After(5 * time.Second):
    log.Warn("gRPC graceful stop timeout, forcing...")
    grpcSrv.Stop()
}
```

---

### 2.3 PubSub 回调中使用 context.Background()

**文件：** `cmd/server/main.go:126-137`

**问题：** PubSub 订阅回调中直接使用 `context.Background()` 查询数据库：

```go
pubsub.SubscribeWordUpdate(ctx, func() {
    words, err := wordRepo.FindAllActive(context.Background()) // 无超时、不受关停控制
    // ...
})
```

应用关停时（`ctx` 已取消），如果此时收到 PubSub 消息，回调仍会执行一次完整的 DB 查询 + 引擎重建，可能与关停流程产生竞争。

**修复方案：** 将 ctx 传入回调闭包，或使用带超时的 context：

```go
pubsub.SubscribeWordUpdate(ctx, func() {
    rebuildCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    
    words, err := wordRepo.FindAllActive(rebuildCtx)
    // ...
})
```

---

## 三、中等 — 性能优化

### 3.1 Replace/Highlight 策略重复执行 Normalize

**文件：** `internal/infrastructure/algorithm/strategy_replace.go:43`、`strategy_highlight.go:72`

**问题：** AC 自动机的 `Search` 方法已经对文本做了一次 Normalize（`ac_automaton.go:133`），之后 Replace 和 Highlight 策略又各自再调用一次 `Normalize(original)`：

```go
// strategy_replace.go:43
normalizedRunes := []rune(Normalize(original))

// strategy_highlight.go:72
normalizedRunes := []rune(Normalize(original))
```

Normalize 包含 NFKC 归一化 + 零宽字符去除 + 全角转半角 + 小写转换，对长文本而言是不可忽视的 CPU 开销。**每次请求都多做了一次完全冗余的文本归一化。**

**修复方案：** 让 `FilterEngine.Match` 同时返回归一化后的文本，或在 `FilterResult` 中携带：

```go
// 方案 A：Match 返回 normalized text
type MatchResult struct {
    Matches        []valueobject.MatchItem
    NormalizedText string
}

func (e *ACFilterEngine) Match(text string) MatchResult {
    ac := e.current.Load().(*AhoCorasick)
    normalized := Normalize(text)
    matches := ac.searchNormalized(normalized) // 内部不再重复 Normalize
    return MatchResult{Matches: matches, NormalizedText: normalized}
}

// 方案 B：Strategy 接口调整为接收 normalized text
type FilterStrategy interface {
    Apply(original string, normalized string, matches []MatchItem) *FilterResult
}
```

---

### 3.2 缓存 Key 使用 SHA256 开销偏大

**文件：** `internal/application/service/filter_app_service.go:45-48`

**问题：** 过滤结果缓存 key 使用 SHA256 哈希：

```go
func filterCacheKey(text string, strategy string) string {
    h := sha256.Sum256([]byte(text))
    return "filter:" + strategy + ":" + hex.EncodeToString(h[:16])
}
```

SHA256 是密码学哈希，计算成本远高于非密码学哈希。对于缓存 key 场景，不需要抗碰撞安全性，只需要分布均匀和速度快。对 50KB 文本计算 SHA256 约需 ~5μs，而 xxhash 或 FNV 只需 ~0.5μs。

**修复方案：** 使用 `hash/fnv` 或项目已有依赖 `github.com/cespare/xxhash/v2`（go-redis 的间接依赖）：

```go
import "github.com/cespare/xxhash/v2"

func filterCacheKey(text string, strategy string) string {
    h := xxhash.Sum64String(text)
    return "filter:" + strategy + ":" + strconv.FormatUint(h, 36)
}
```

---

### 3.3 本地缓存无容量上限

**文件：** `internal/infrastructure/cache/local_cache.go`

**问题：** `LocalCache` 只有 TTL 过期机制，没有最大容量限制。在以下场景可能导致内存持续增长：

- 大量不同文本的过滤请求（每个文本生成不同的缓存 key）
- 过期清理每分钟最多淘汰 1000 条，如果写入速率远超淘汰速率，缓存会无限膨胀
- 没有 OOM 前的自我保护机制

**修复方案：** 增加最大条目数限制，达到上限时拒绝新写入或执行 LRU/随机淘汰：

```go
type LocalCache struct {
    mu       sync.RWMutex
    items    map[string]*cacheItem
    ttl      time.Duration
    maxItems int // 新增：最大条目数，0 表示不限制
}

func (c *LocalCache) Set(key string, value []byte) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // 容量保护：超限时跳过写入（简单策略）
    if c.maxItems > 0 && len(c.items) >= c.maxItems {
        if _, exists := c.items[key]; !exists {
            return // 已满且不是更新，跳过
        }
    }
    c.items[key] = &cacheItem{value: value, expiredAt: time.Now().Add(c.ttl)}
}
```

---

### 3.4 全局限流器无法区分客户端

**文件：** `internal/interfaces/middleware/ratelimit.go`

**问题：** 限流器是全局共享的单个 `rate.Limiter`：

```go
limiter := rate.NewLimiter(rate.Limit(rps), burst)
```

所有客户端共享同一个令牌桶，意味着：
- 一个高频调用的客户端可以耗尽全部配额，导致其他正常客户端被限流
- 无法对不同客户端设置不同的速率限制
- 无法识别和封禁异常客户端

**修复方案：** 按 API Key 或 IP 维度进行限流：

```go
type perClientLimiter struct {
    mu       sync.RWMutex
    limiters map[string]*rate.Limiter
    rps      rate.Limit
    burst    int
}

func (l *perClientLimiter) getLimiter(key string) *rate.Limiter {
    l.mu.RLock()
    lim, ok := l.limiters[key]
    l.mu.RUnlock()
    if ok {
        return lim
    }
    l.mu.Lock()
    defer l.mu.Unlock()
    lim = rate.NewLimiter(l.rps, l.burst)
    l.limiters[key] = lim
    return lim
}
```

需注意对 `limiters` map 的定期清理，防止已过期客户端条目积累。

---

### 3.5 Export 全量加载到内存

**文件：** `internal/application/service/word_app_service.go:215-245`

**问题：** `Export` 方法通过 `PageSize: 100000` 一次性将所有词条加载到内存，然后序列化为 CSV：

```go
q := repository.WordQuery{Category: &cat, Page: 1, PageSize: 100000}
words, _, err = s.repo.List(ctx, q)
```

当词库量达到数十万甚至百万级别时：
- 一次性加载所有 Entity 对象占用大量内存
- CSV 序列化后的 `bytes.Buffer` 再占一份内存
- 多个并发 Export 请求可能导致 OOM

**修复方案：** 使用 GORM 的 `FindInBatches` 分批流式处理，配合 `io.Writer` 直接写入响应流：

```go
func (s *WordAppService) Export(ctx context.Context, category string, w io.Writer) error {
    csvWriter := csv.NewWriter(w)
    csvWriter.Write([]string{"text", "category", "level", "tag"})

    db := s.repo.QueryBuilder(ctx, category)
    var models []SensitiveWordModel
    db.FindInBatches(&models, 1000, func(tx *gorm.DB, batch int) error {
        for _, m := range models {
            csvWriter.Write([]string{m.Text, m.Category, ...})
        }
        csvWriter.Flush()
        return nil
    })
    return csvWriter.Error()
}
```

---

## 四、低优 — 运维与可观测性

### 4.1 Prometheus Metrics 定义但未使用

**文件：** `internal/interfaces/middleware/metrics.go:39-59`

**问题：** 以下 Prometheus 指标已定义但在业务代码中未被更新：

```go
FilterHitsTotal   // 过滤命中计数 — 未在 FilterAppService 中调用
EngineWordCount   // 引擎词条数 — 未在 Rebuild 后更新
EngineRebuildTotal // 重建次数 — 未在 triggerRebuild 中调用
```

这些指标始终为 0，在 Grafana 中不会显示有意义的数据，形成监控盲区。

**修复方案：** 在对应的业务逻辑中更新这些指标：

```go
// filter_app_service.go - Filter 方法末尾
middleware.FilterHitsTotal.WithLabelValues(string(strategyType), strconv.FormatBool(resp.IsHit)).Inc()

// word_app_service.go - triggerRebuild 的 AfterFunc 回调中
middleware.EngineRebuildTotal.Inc()
middleware.EngineWordCount.Set(float64(s.engine.WordCount()))

// word_app_service.go - InitEngine 末尾
middleware.EngineWordCount.Set(float64(s.engine.WordCount()))
```

---

### 4.2 缺少缓存命中率指标

**文件：** `internal/infrastructure/cache/multi_level_cache.go`、`internal/infrastructure/cache/circuit_breaker.go`

**问题：** 多级缓存和熔断器的关键状态无法被监控：
- L1 / L2 缓存命中率未知，无法判断缓存配置是否合理
- 熔断器开断次数、当前状态无法观测
- 缓存穿透（L1 + L2 都未命中）频率不可见

**修复方案：** 添加 Prometheus 指标：

```go
var (
    cacheOps = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "censorhub_cache_operations_total",
    }, []string{"level", "result"}) // level=l1/l2, result=hit/miss

    circuitBreakerState = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "censorhub_circuit_breaker_open",
    })
)
```

---

### 4.3 Docker Compose 使用 `latest` 标签

**文件：** `deployments/docker/docker-compose.yaml`

**问题：** Jaeger、Prometheus、Grafana 均使用 `latest` 标签：

```yaml
jaeger:
  image: jaegertracing/all-in-one:latest
prometheus:
  image: prom/prometheus:latest
grafana:
  image: grafana/grafana:latest
```

`latest` 标签会在不同时间拉取到不同版本，可能引入不兼容的变更，特别是 Jaeger 和 Prometheus 的配置格式在大版本间有差异。

**修复方案：** 锁定到具体版本：

```yaml
jaeger:
  image: jaegertracing/all-in-one:1.55
prometheus:
  image: prom/prometheus:v2.51.0
grafana:
  image: grafana/grafana:10.4.0
```

---

### 4.4 WordAppService 关停时未停止防抖 Timer

**文件：** `internal/application/service/word_app_service.go:260`

**问题：** `rebuildTimer` 是一个 `time.AfterFunc` 定时器。应用关停时（`ctx` 取消），如果正好在 500ms 防抖窗口内：
1. PubSub 订阅已停止
2. HTTP/gRPC server 正在 Shutdown
3. 但 `rebuildTimer` 仍会在 500ms 后触发，执行 DB 查询和引擎重建
4. 此时 DB 连接可能已关闭（`defer dbCleanup()` 已执行），导致 panic 或错误日志

**修复方案：** 为 `WordAppService` 添加 `Close` 方法，在关停流程中调用：

```go
func (s *WordAppService) Close() {
    s.rebuildMu.Lock()
    defer s.rebuildMu.Unlock()
    if s.rebuildTimer != nil {
        s.rebuildTimer.Stop()
    }
}

// main.go 关停流程中
cancel()
wordAppService.Close() // 停止防抖 timer
grpcSrv.GracefulStop()
```

---

### 4.5 测试覆盖不足

**文件：** `test/e2e/`、`test/integration/`

**问题：** E2E 和集成测试目录为空，关键路径缺少测试：
- 缓存一致性（词库更新 → 缓存清除 → 新结果返回）
- 熔断器状态转换（closed → open → half-open → closed）
- PubSub 断线重连后的引擎一致性
- 防抖 timer 的并发安全性
- Normalize 长度变化时的 Replace/Highlight 降级行为

**建议：** 优先补充以下测试：

1. **缓存一致性集成测试**：创建词 → 过滤命中 → 删除词 → 验证缓存清除后过滤不命中
2. **熔断器单元测试**：模拟连续失败 → 验证进入 open → 等待 timeout → 验证进入 half-open → 成功后恢复
3. **BatchDetect 压力测试**：100 条 × 50KB 文本 × 并发请求，验证内存和 CPU 不会失控

---

### 4.6 gRPC 缺少健康检查协议

**文件：** `internal/interfaces/grpc/server.go`

**问题：** K8s 从 1.24 起原生支持 gRPC 健康检查探针，但当前 gRPC server 未实现标准的 gRPC Health Checking Protocol（`grpc.health.v1.Health`）。K8s 只能通过 HTTP 端口（8080）的 `/readyz` 来探测，无法独立判断 gRPC 端口（9090）的可用性。

**修复方案：**

```go
import "google.golang.org/grpc/health"
import healthpb "google.golang.org/grpc/health/grpc_health_v1"

healthServer := health.NewServer()
healthpb.RegisterHealthServer(grpcSrv, healthServer)
healthServer.SetServingStatus("censor.v1.CensorService", healthpb.HealthCheckResponse_SERVING)
```

K8s Deployment 可配置 gRPC 探针：
```yaml
readinessProbe:
  grpc:
    port: 9090
```

---

## 优化优先级路线图

```
Phase 1 — 紧急修复（0.5 天）
  ├── 1.1 修复过滤缓存未清除的一致性 Bug
  └── 1.2 修复 Highlight XSS 风险

Phase 2 — 安全加固（1 天）
  ├── 2.1 gRPC 端添加 interceptor（Auth / RateLimit / Recovery / Logging）
  ├── 2.2 gRPC GracefulStop 增加超时
  └── 2.3 PubSub 回调 context 修复

Phase 3 — 性能提升（1-2 天）
  ├── 3.1 消除 Normalize 重复调用
  ├── 3.2 缓存 key 改用非密码学哈希
  ├── 3.3 本地缓存增加容量上限
  ├── 3.4 按客户端维度限流
  └── 3.5 Export 流式处理

Phase 4 — 可观测性与运维（1 天）
  ├── 4.1 Prometheus Metrics 实际接入业务
  ├── 4.2 增加缓存命中率 / 熔断器状态指标
  ├── 4.3 Docker Compose 锁定版本
  ├── 4.4 关停时停止防抖 Timer
  ├── 4.5 补充关键路径测试
  └── 4.6 gRPC 健康检查协议
```

---

## 与前一轮优化的关系

| 状态 | 数量 | 说明 |
|------|------|------|
| 前一轮已修复 | 19 项 | 见 `optimization-changelog.md` |
| 前一轮修复不完整 | 1 项 | 1.1 缓存清除前缀错误 |
| 本次新发现 | 15 项 | 本文档中的所有条目 |
