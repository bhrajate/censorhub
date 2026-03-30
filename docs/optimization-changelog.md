# CensorHub 生产级优化实施记录

> 本文档记录已完成的全部优化项，包含原始问题分析和实际落地的解决方案。

---

## 一、严重级别（4 项）

### 1.1 LocalCache 清理协程泄漏

**文件：** `internal/infrastructure/cache/local_cache.go`、`cmd/server/main.go`

**问题：**
`NewLocalCache` 内部启动的 `go lc.cleanup()` 协程没有退出机制。`cleanup` 通过 `for range ticker.C` 无限循环，无法响应外部关停信号。长时间运行或热重启场景下，僵尸协程会持续积累，造成内存泄漏。

**解决方案：**
- `NewLocalCache` 新增 `context.Context` 参数，cleanup 协程通过 `select` 同时监听 `ctx.Done()` 和 `ticker.C`
- `main.go` 中创建统一的生命周期 context，`defer cancel()` 确保关停时所有后台协程收到退出信号
- 将原来 main.go 中 PubSub 专用的 `ctx, cancel` 提前到缓存创建之前，统一管理所有后台协程生命周期

```go
// local_cache.go
func NewLocalCache(ctx context.Context, ttl time.Duration) *LocalCache {
    lc := &LocalCache{items: make(map[string]*cacheItem), ttl: ttl}
    go lc.cleanup(ctx)
    return lc
}

func (c *LocalCache) cleanup(ctx context.Context) {
    ticker := time.NewTicker(time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            c.evictExpired()
        }
    }
}
```

---

### 1.2 BatchDetect 无界并发协程

**文件：** `internal/application/service/filter_app_service.go`

**问题：**
`BatchDetect` 为请求中的每条文本直接 `go func()` 启动协程，没有并发上限。DTO 层虽然限制了 `max=100`，但在内部并发层面没有保护。极端情况下大量并发 batch 请求会导致协程爆炸、内存耗尽。同时缺少 context 取消检测，请求超时后仍在后台空跑。

**解决方案：**
- 使用 `chan struct{}` 作为 semaphore，并发上限为 `runtime.NumCPU()`
- 每个协程启动前获取信号量（阻塞），完成后释放
- 循环体内增加 `select { case <-ctx.Done() }` 检测，请求取消时提前退出

```go
maxWorkers := runtime.NumCPU()
sem := make(chan struct{}, maxWorkers)

for i, text := range req.Texts {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
    }
    wg.Add(1)
    sem <- struct{}{}
    go func(idx int, t string) {
        defer wg.Done()
        defer func() { <-sem }()
        // ... 执行过滤
    }(i, text)
}
```

---

### 1.3 Redis Pub/Sub 订阅无恢复机制

**文件：** `internal/infrastructure/mq/redis_pubsub.go`

**问题：**
原实现中 `SubscribeWordUpdate` 启动一个协程直接读取 channel，如果 Redis 断连导致 channel 关闭，协程静默退出，无日志、无重试。此后该实例永远收不到词库更新通知，与其他实例数据不一致，且无任何告警。

**解决方案：**
- 拆分为外层重连循环（`SubscribeWordUpdate`）和内层单次订阅（`runSubscription`）
- `runSubscription` 先调用 `sub.Receive()` 验证订阅建立成功，再进入消息循环
- 断线后指数退避重连（1s → 2s → 4s → ... → 30s 上限），退避期间监听 `ctx.Done()` 以支持正常关停
- context 取消时直接退出，不触发重连

```go
func (p *RedisPubSub) SubscribeWordUpdate(ctx context.Context, handler func()) {
    go func() {
        backoff := initialBackoff // 1s
        for {
            err := p.runSubscription(ctx, handler)
            if ctx.Err() != nil {
                return // 正常关停
            }
            p.logger.Error("PubSub subscription lost, reconnecting...",
                zap.Error(err), zap.Duration("backoff", backoff))
            select {
            case <-ctx.Done():
                return
            case <-time.After(backoff):
            }
            backoff = min(backoff*2, maxBackoff) // 上限 30s
        }
    }()
}
```

---

### 1.4 triggerRebuild 使用 context.Background()

**文件：** `internal/application/service/word_app_service.go`

**问题：**
`triggerRebuild` 内部启动的协程使用 `context.Background()` 执行数据库查询和引擎重建。这意味着即使调用方的 context 已取消（如应用关停），重建操作仍然会继续执行。优雅关停时可能无限等待 DB 查询完成。

**解决方案：**
- 协程内部使用 `context.WithTimeout(ctx, 30*time.Second)` 替代 `context.Background()`
- 继承调用方 context 的取消信号，同时设置 30 秒超时兜底

```go
func (s *WordAppService) triggerRebuild(ctx context.Context) {
    go func() {
        rebuildCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
        defer cancel()
        words, err := s.repo.FindAllActive(rebuildCtx)
        // ...
    }()
    // ...
}
```

---

## 二、高危级别（3 项）

### 2.1 API Key 支持 Query String 传递

**文件：** `internal/interfaces/middleware/auth.go`

**问题：**
认证中间件同时支持 `X-API-Key` Header 和 `?api_key=xxx` Query String 两种方式。Query String 中的 API Key 会出现在：
- Nginx / 代理访问日志
- 浏览器地址栏和历史记录
- HTTP Referer 头（跳转时泄露给第三方）
- 搜索引擎爬虫索引

这是 OWASP API Security Top 10 中明确列出的安全风险。

**解决方案：**
- 移除 `c.Query("api_key")` 分支，仅保留 `c.GetHeader("X-API-Key")`
- 错误提示明确要求使用 Header 方式

```go
apiKey := c.GetHeader("X-API-Key")
if !keySet[apiKey] {
    c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
        "code":    10008,
        "message": "unauthorized: invalid or missing X-API-Key header",
    })
    return
}
```

---

### 2.2 CORS 配置过于宽松

**文件：** `internal/interfaces/middleware/cors.go`、`internal/infrastructure/config/config.go`、`configs/*.yaml`

**问题：**
CORS 中间件硬编码 `Access-Control-Allow-Origin: *`，允许任意域名的跨域请求。攻击者可以在恶意网页中直接调用 API，结合已认证的浏览器 Cookie/Header 实施 CSRF 攻击或窃取数据。

**解决方案：**
- 新增 `CORSConfig.AllowedOrigins` 配置项
- 中间件根据请求 `Origin` 头动态匹配白名单，命中则返回对应 Origin + `Vary: Origin`
- 未命中白名单的 OPTIONS 预检请求直接返回 403
- 仍支持 `"*"` 通配符用于开发环境
- `config.yaml`（开发）默认 `*`，`config.production.yaml` 配置具体域名

```go
func corsMiddleware(allowedOrigins []string) gin.HandlerFunc {
    allowAll := false
    originSet := make(map[string]bool, len(allowedOrigins))
    for _, o := range allowedOrigins {
        if o == "*" { allowAll = true; break }
        originSet[o] = true
    }
    return func(c *gin.Context) {
        origin := c.GetHeader("Origin")
        if allowAll {
            c.Header("Access-Control-Allow-Origin", "*")
        } else if originSet[origin] {
            c.Header("Access-Control-Allow-Origin", origin)
            c.Header("Vary", "Origin")
        } else {
            if c.Request.Method == http.MethodOptions {
                c.AbortWithStatus(http.StatusForbidden)
                return
            }
            c.Next()
            return
        }
        // ... 设置其他 CORS 头
    }
}
```

---

### 2.3 无 Request Body 大小限制

**文件：** `internal/interfaces/middleware/body_limit.go`（新增）、`internal/interfaces/http/router.go`、`internal/infrastructure/config/config.go`

**问题：**
路由层没有请求体大小限制。攻击者可以发送 GB 级别的 JSON payload，导致 Gin 解析时占满内存，引发 OOM。尤其是 `/filter/batch` 和 `/words/import` 这类接受数组的端点风险更大。

**解决方案：**
- 新增 `bodyLimitMiddleware`，使用 `http.MaxBytesReader` 包装 `Request.Body`，超限时自动返回 413
- `HTTPConfig` 新增 `MaxBodySize` 配置项，默认 10MB
- 在 router 全局中间件栈中，`BodyLimit` 放在 `Logger` 之前，超大请求不进入后续处理

```go
func bodyLimitMiddleware(maxBytes int64) gin.HandlerFunc {
    return func(c *gin.Context) {
        if c.Request.Body != nil {
            c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
        }
        c.Next()
    }
}
```

---

## 三、中等级别（6 项）

### 3.1 Redis 缓存无熔断降级

**文件：** `internal/infrastructure/cache/circuit_breaker.go`（新增）、`internal/infrastructure/cache/multi_level_cache.go`

**问题：**
`MultiLevelCache` 中所有 Redis 操作都是同步的，Redis 故障时每次请求都会等待 Redis 超时（通常数秒），导致接口延迟从毫秒级飙升到秒级。更严重的是，Redis 故障会级联到所有过滤请求，形成全局雪崩。

**解决方案：**
- 新增轻量级 `CircuitBreaker`（三态：closed / open / half-open），无外部依赖
- 连续 5 次 Redis 操作失败触发熔断（open），之后所有 Redis 操作直接跳过
- 10 秒后自动进入 half-open 探测，连续 2 次成功恢复（closed）
- `MultiLevelCache` 的 Get/Set/Invalidate 全部受熔断器保护
- 熔断期间降级为仅 L1 本地缓存，Redis 错误不向上层传播

```go
// Get 示例
func (c *MultiLevelCache) Get(ctx context.Context, key string) ([]byte, error) {
    if v, ok := c.local.Get(key); ok {
        return v, nil
    }
    if !c.breaker.Allow() {
        return nil, redis.Nil // 熔断中，视为缓存未命中
    }
    v, err := c.redis.Get(ctx, key)
    if err != nil {
        if err != redis.Nil { c.breaker.RecordFailure() }
        return nil, err
    }
    c.breaker.RecordSuccess()
    c.local.Set(key, v)
    return v, nil
}
```

---

### 3.2 多级缓存失效策略过于粗暴

**文件：** `internal/infrastructure/cache/local_cache.go`、`internal/infrastructure/cache/multi_level_cache.go`

**问题：**
`MultiLevelCache.InvalidateByPrefix` 调用 `c.local.InvalidateAll()` 直接清空整个 L1 缓存（代码注释写的"简单策略"）。词库更新时只需清除 `filter:` 前缀的缓存，但实际把所有缓存都杀掉了，造成大量不必要的缓存穿透和重建开销。

**解决方案：**
- `LocalCache` 新增 `DeleteByPrefix(prefix string)` 方法，遍历 map 按前缀精确删除
- `MultiLevelCache.InvalidateByPrefix` 改为调用 `DeleteByPrefix` 替代 `InvalidateAll`

```go
func (c *LocalCache) DeleteByPrefix(prefix string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    for k := range c.items {
        if strings.HasPrefix(k, prefix) {
            delete(c.items, k)
        }
    }
}
```

---

### 3.3 过滤结果未缓存

**文件：** `internal/application/service/filter_app_service.go`、`cmd/server/main.go`

**问题：**
`FilterAppService.Filter` 每次请求都执行完整的 AC 自动机搜索 + 策略应用。对于内容审核场景，同一段文本经常被重复检查（如表单提交重试、多次预览），但每次都重新计算，浪费 CPU。

**解决方案：**
- `FilterAppService` 注入 `MultiLevelCache`
- 以 `filter:{strategy}:{sha256(text)[:32]}` 为缓存 key
- 缓存命中时直接反序列化返回（仅重算 CostMs），未命中时计算后 JSON 序列化写入缓存
- 词库更新时通过 `InvalidateByPrefix("filter:")` 自动清除所有过滤结果缓存
- cache 参数支持 nil（测试场景），此时退化为无缓存模式

```go
cacheKey := filterCacheKey(req.Text, string(strategyType))
if s.cache != nil {
    if data, err := s.cache.Get(ctx, cacheKey); err == nil {
        var cached dto.FilterResponse
        if json.Unmarshal(data, &cached) == nil {
            cached.CostMs = time.Since(start).Milliseconds()
            return &cached, nil
        }
    }
}
// ... 执行过滤，写入缓存
```

---

### 3.4 Redis SCAN 删除效率低

**文件：** `internal/infrastructure/cache/redis_cache.go`

**问题：**
`DeleteByPrefix` 使用 `Scan(..., 100).Iterator()` 逐条读取，然后一次性 `Del(keys...)`。存在两个问题：
1. SCAN COUNT 只有 100，大量 key 时需要多次往返
2. 所有 key 收集到内存后一次性删除，如果 key 量巨大会占用大量内存，且 `DEL` 是同步阻塞命令，会阻塞 Redis 主线程

**解决方案：**
- SCAN COUNT 增大到 1000，减少往返次数
- 每轮 SCAN 后立即用 Pipeline 批量删除，不再一次性收集
- 使用 `UNLINK` 替代 `DEL`，UNLINK 将实际内存释放放到后台线程，不阻塞 Redis 主线程

```go
func (c *RedisCache) DeleteByPrefix(ctx context.Context, prefix string) error {
    var cursor uint64
    for {
        keys, nextCursor, err := c.client.Scan(ctx, cursor, prefix+"*", 1000).Result()
        if err != nil { return err }
        if len(keys) > 0 {
            pipe := c.client.Pipeline()
            for _, key := range keys { pipe.Unlink(ctx, key) }
            if _, err := pipe.Exec(ctx); err != nil { return err }
        }
        cursor = nextCursor
        if cursor == 0 { break }
    }
    return nil
}
```

---

### 3.5 本地缓存清理持锁遍历全表

**文件：** `internal/infrastructure/cache/local_cache.go`

**问题：**
`cleanup` 每分钟执行一次，持写锁遍历整个 `items` map。当缓存条目数量达到数十万级别时，遍历耗时可达数百毫秒，期间所有 `Get`/`Set` 操作被阻塞，直接影响过滤接口的 P99 延迟。

**解决方案：**
- 新增 `maxEvictPerCycle = 1000` 常量，每轮清理最多淘汰 1000 个过期条目后释放锁
- Go map 的 `range` 遍历顺序是随机的，天然实现了概率采样
- 如果某轮未清理完，下一轮继续，避免单次长时间持锁

```go
const maxEvictPerCycle = 1000

func (c *LocalCache) evictExpired() {
    c.mu.Lock()
    defer c.mu.Unlock()
    now := time.Now()
    evicted := 0
    for k, v := range c.items {
        if now.After(v.expiredAt) {
            delete(c.items, k)
            evicted++
            if evicted >= maxEvictPerCycle { break }
        }
    }
}
```

---

### 3.6 HTTP Server 缺少 IdleTimeout

**文件：** `internal/infrastructure/config/config.go`、`cmd/server/main.go`

**问题：**
`http.Server` 只设置了 `ReadTimeout` 和 `WriteTimeout`，未设置 `IdleTimeout`。`IdleTimeout` 控制 keep-alive 连接在空闲多久后关闭。缺少此配置时默认使用 `ReadTimeout`（10s），但更关键的是会受到 Slowloris 攻击——攻击者打开大量连接后不发送数据，耗尽服务器连接池。

**解决方案：**
- `HTTPConfig` 新增 `IdleTimeout` 字段
- 默认值 60 秒（平衡正常 keep-alive 复用和异常连接回收）
- `httpSrv` 创建时应用该配置

```go
httpSrv := &http.Server{
    Addr:         cfg.Server.HTTP.Addr,
    Handler:      router,
    ReadTimeout:  cfg.Server.HTTP.ReadTimeout,
    WriteTimeout: cfg.Server.HTTP.WriteTimeout,
    IdleTimeout:  cfg.Server.HTTP.IdleTimeout, // 新增，默认 60s
}
```

---

## 四、低优级别（6 项）

### 4.1 Dockerfile 未锁定镜像 SHA256

**文件：** `deployments/docker/Dockerfile`

**问题：**
`FROM alpine:3.19` 和 `FROM golang:1.22-alpine` 使用 tag 引用镜像。Docker tag 是可变的——同一个 tag 可以随时被推送新内容。不同时间、不同机器构建可能拉取到不同的底层镜像，破坏构建可复现性，也存在供应链攻击风险（tag 被劫持指向恶意镜像）。

**解决方案：**
- 在 Dockerfile 中添加注释，说明生产部署前需替换为 `image@sha256:...` 格式
- 提供获取 digest 的命令示例

```dockerfile
# 提示：生产部署前，请将镜像 tag 替换为 digest 以锁定版本
# 运行 `docker pull golang:1.22-alpine && docker inspect --format='{{index .RepoDigests 0}}' golang:1.22-alpine` 获取 digest
FROM golang:1.22-alpine AS builder
```

---

### 4.2 K8s 缺少 PodDisruptionBudget

**文件：** `deployments/kubernetes/pdb.yaml`（新增）、`deployments/kubernetes/base/kustomization.yaml`

**问题：**
没有 PodDisruptionBudget（PDB），Kubernetes 在执行节点维护（drain）、集群升级、节点自动修复等操作时，可能同时驱逐所有 3 个 Pod 副本，导致服务完全中断。`maxUnavailable: 0` 的 RollingUpdate 策略只在 Deployment 自身更新时生效，对集群级别的 Pod 驱逐无效。

**解决方案：**
- 新增 `pdb.yaml`，设置 `minAvailable: 1`
- 加入 base kustomization 资源列表，所有环境自动继承

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: censorhub-pdb
  namespace: censorhub
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: censorhub
```

---

### 4.3 Readiness Probe 不完整

**文件：** `internal/interfaces/http/handler/health_handler.go`、`cmd/server/main.go`

**问题：**
就绪探针 `/readyz` 只检查 MySQL 连接。如果 Redis 宕机，Pod 仍被标记为 ready 接收流量，但缓存层完全失效，性能急剧下降。如果 AC 引擎词库尚未加载完成（启动阶段），Pod 也会提前接收流量，返回空的过滤结果。

**解决方案：**
- `HealthHandler` 新增 Redis client 注入
- 就绪探针增加 Redis ping 检查
- 保留引擎词库数量返回（可用于监控告警）

```go
func (h *HealthHandler) Readiness(c *gin.Context) {
    // 检查 MySQL
    sqlDB, _ := h.db.DB()
    if err := sqlDB.Ping(); err != nil {
        c.JSON(503, gin.H{"status": "not ready", "error": "database ping failed"})
        return
    }
    // 检查 Redis
    if err := h.rdb.Ping(c.Request.Context()).Err(); err != nil {
        c.JSON(503, gin.H{"status": "not ready", "error": "redis ping failed"})
        return
    }
    c.JSON(200, gin.H{"status": "ready", "word_count": h.filterService.EngineWordCount()})
}
```

---

### 4.4 Import 静默跳过无效词

**文件：** `internal/application/dto/word_dto.go`、`internal/application/service/word_app_service.go`

**问题：**
批量导入时，校验失败的词条通过 `continue` 静默跳过。响应中只返回 `skipped` 数量，调用方无法知道：哪些词导入失败了？失败原因是什么？这在实际运营中会造成困惑——导入了 10000 条，跳过了 500 条，但不知道是哪 500 条。

**解决方案：**
- 新增 `ImportFailure` 结构体（index / word / reason）
- `ImportResponse` 增加 `failures` 字段（`omitempty`，成功时不返回）
- 校验失败时记录具体索引、词文本和错误原因

```go
type ImportFailure struct {
    Index  int    `json:"index"`
    Word   string `json:"word"`
    Reason string `json:"reason"`
}

// Import 方法中
for i, w := range req.Words {
    word := assembler.CreateDTOToEntity(&w)
    if err := word.Validate(); err != nil {
        failures = append(failures, dto.ImportFailure{
            Index: i, Word: w.Text, Reason: err.Error(),
        })
        continue
    }
    words = append(words, word)
}
```

---

### 4.5 rand.Read 错误未处理

**文件：** `internal/interfaces/middleware/request_id.go`

**问题：**
`generateID` 函数调用 `rand.Read(b)` 但忽略了返回的 error。虽然 `crypto/rand.Read` 在主流操作系统上极少失败，但在容器环境启动早期、系统熵池耗尽、或 `/dev/urandom` 不可用等边缘情况下可能返回错误，此时生成的 Request ID 可能是全零或不完整的值。

**解决方案：**
- 检查 `rand.Read` 返回值
- 错误时 fallback 为基于 `time.Now().UnixNano()` 的 hex 字符串（非密码学安全，但作为 Request ID 足够）

```go
func generateID() string {
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil {
        return fmt.Sprintf("%x", time.Now().UnixNano())
    }
    return hex.EncodeToString(b)
}
```

---

### 4.6 数据库连接池比例不合理

**文件：** `internal/infrastructure/config/config.go`、`internal/infrastructure/database/mysql.go`

**问题：**
默认 `MaxOpenConns=100`、`MaxIdleConns=10`，idle 连接只占总连接数的 10%。在突发流量场景下，90% 的连接每次用完即关闭，下次请求又要重新建连（TCP 三次握手 + MySQL 认证）。频繁创建/销毁连接带来额外延迟和 CPU 开销，也增加 MySQL 的连接管理负担。同时缺少 `ConnMaxIdleTime` 配置，空闲连接永不超时回收。

**解决方案：**
- `MaxIdleConns` 默认值从 10 调整为 25（MaxOpenConns 的 25%）
- 新增 `ConnMaxIdleTime` 配置项，默认 5 分钟，及时回收长期空闲连接
- `mysql.go` 中调用 `sqlDB.SetConnMaxIdleTime()` 应用配置

```go
// config.go setDefaults
if cfg.Database.MaxIdleConns == 0 {
    cfg.Database.MaxIdleConns = 25 // ~25% of MaxOpenConns
}
if cfg.Database.ConnMaxIdleTime == 0 {
    cfg.Database.ConnMaxIdleTime = 5 * time.Minute
}

// mysql.go
sqlDB.SetConnMaxIdleTime(cfg.Database.ConnMaxIdleTime)
```

---

## 变更文件汇总

| 级别 | 文件 | 变更类型 |
|------|------|----------|
| 严重 | `internal/infrastructure/cache/local_cache.go` | 修改 |
| 严重 | `internal/application/service/filter_app_service.go` | 修改 |
| 严重 | `internal/infrastructure/mq/redis_pubsub.go` | 重写 |
| 严重 | `internal/application/service/word_app_service.go` | 修改 |
| 严重 | `cmd/server/main.go` | 修改 |
| 高危 | `internal/interfaces/middleware/auth.go` | 修改 |
| 高危 | `internal/interfaces/middleware/cors.go` | 重写 |
| 高危 | `internal/interfaces/middleware/body_limit.go` | **新增** |
| 高危 | `internal/interfaces/middleware/middleware.go` | 修改 |
| 高危 | `internal/interfaces/http/router.go` | 修改 |
| 高危 | `internal/infrastructure/config/config.go` | 修改 |
| 高危 | `configs/config.yaml` | 修改 |
| 高危 | `configs/config.production.yaml` | 修改 |
| 中等 | `internal/infrastructure/cache/circuit_breaker.go` | **新增** |
| 中等 | `internal/infrastructure/cache/multi_level_cache.go` | 重写 |
| 中等 | `internal/infrastructure/cache/redis_cache.go` | 重写 |
| 中等 | `internal/infrastructure/cache/local_cache.go` | 修改 |
| 中等 | `internal/application/service/filter_app_service.go` | 重写 |
| 中等 | `internal/infrastructure/config/config.go` | 修改 |
| 中等 | `cmd/server/main.go` | 修改 |
| 低优 | `deployments/docker/Dockerfile` | 修改 |
| 低优 | `deployments/kubernetes/pdb.yaml` | **新增** |
| 低优 | `deployments/kubernetes/base/kustomization.yaml` | 修改 |
| 低优 | `internal/interfaces/http/handler/health_handler.go` | 重写 |
| 低优 | `internal/application/dto/word_dto.go` | 修改 |
| 低优 | `internal/application/service/word_app_service.go` | 修改 |
| 低优 | `internal/interfaces/middleware/request_id.go` | 修改 |
| 低优 | `internal/infrastructure/database/mysql.go` | 修改 |
