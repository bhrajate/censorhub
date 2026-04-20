# CensorHub 生产级优化清单

> **[已归档]** 本文档是项目初期的 19 项优化**计划**，已在 2026-03 ~ 2026-04 两轮迭代中全部落地。
>
> - 具体落地方案与变更详情 → 见 [`optimization-changelog.md`](./optimization-changelog.md)
> - 第一轮完成后又发现的新问题 → 见 [`optimization-2026-04-16.md`](./optimization-2026-04-16.md)（其中 15 项已随 commit `7936e51` 落地，仅 4.5「测试覆盖不足」仍待处理）
>
> 保留此文档作为需求演进史，方便追溯每项优化的原始动机。当前代码已不需要再按本文档执行任何动作。

---

## 一、严重 — 必须修复

### 1.1 LocalCache 清理协程泄漏

**文件：** `internal/infrastructure/cache/local_cache.go`

**问题：** `go lc.cleanup()` 启动后无停止机制，缺少 `context` 取消信号。应用重启或长时间运行会积累僵尸协程。

**修复方案：**
- `NewLocalCache` 接收 `context.Context` 参数
- cleanup 协程监听 `<-ctx.Done()` 退出
- 在 `main.go` 优雅关停时 cancel context

```go
// Before
func NewLocalCache(defaultTTL time.Duration) *LocalCache {
    lc := &LocalCache{...}
    go lc.cleanup()
    return lc
}

// After
func NewLocalCache(ctx context.Context, defaultTTL time.Duration) *LocalCache {
    lc := &LocalCache{...}
    go lc.cleanup(ctx)
    return lc
}

func (lc *LocalCache) cleanup(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            lc.evictExpired()
        }
    }
}
```

---

### 1.2 BatchDetect 无界并发协程

**文件：** `internal/application/service/filter_app_service.go`

**问题：** 批量检测为每条文本启动一个 goroutine，无并发上限。恶意大批量请求可耗尽服务器内存和 CPU。

**修复方案：**
- 增加批次上限校验（如最大 100 条）
- 使用 worker pool 限制并发数（如 `runtime.NumCPU()` 个 worker）
- 添加超时控制

```go
// 使用 semaphore 限制并发
sem := make(chan struct{}, runtime.NumCPU())
for i, text := range texts {
    sem <- struct{}{}
    go func(idx int, t string) {
        defer func() { <-sem }()
        results[idx] = s.filter(ctx, t, strategy)
    }(i, text)
}
```

---

### 1.3 Redis Pub/Sub 订阅无恢复机制

**文件：** `internal/infrastructure/mq/redis_pubsub.go`

**问题：** 订阅失败后静默退出，无重试、无告警。其他实例可能永远收不到词库更新通知，导致实例间数据不一致。

**修复方案：**
- 添加指数退避重连逻辑
- 订阅失败时记录错误日志并触发告警
- 提供健康检查接口检测订阅状态

```go
func (ps *RedisPubSub) SubscribeWordUpdate(ctx context.Context, handler func()) {
    go func() {
        for {
            err := ps.subscribe(ctx, handler)
            if ctx.Err() != nil {
                return // 正常关停
            }
            logger.Error("pubsub subscription lost, reconnecting...", zap.Error(err))
            time.Sleep(backoff()) // 指数退避
        }
    }()
}
```

---

### 1.4 triggerRebuild 使用 context.Background()

**文件：** `internal/application/service/word_app_service.go`

**问题：** 后台重建 AC 自动机时使用 `context.Background()`，忽略调用方的 deadline。优雅关停时可能无限等待重建完成。

**修复方案：**
- 传递带超时的 context
- 或使用独立的 shutdown context，在 main 关停时统一取消

```go
func (s *WordAppService) triggerRebuild(ctx context.Context) {
    rebuildCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    // 使用 rebuildCtx 执行重建...
}
```

---

## 二、高危 — 安全问题

### 2.1 API Key 支持 Query String 传递

**文件：** `internal/interfaces/middleware/auth.go`

**问题：** `c.Query("api_key")` 会把密钥暴露在 URL 中，进而出现在访问日志、浏览器历史、代理日志、Referer 头等位置。

**修复方案：**
- 移除 query string 方式，只保留 `X-API-Key` Header
- 如果必须兼容 query string，至少在日志中脱敏

```go
// Before
if apiKey == "" {
    apiKey = c.Query("api_key")
}

// After — 直接移除 query string 支持
apiKey := c.GetHeader("X-API-Key")
if apiKey == "" {
    response.Unauthorized(c, "missing X-API-Key header")
    c.Abort()
    return
}
```

---

### 2.2 CORS 配置过于宽松

**文件：** `internal/interfaces/middleware/cors.go`

**问题：** `Access-Control-Allow-Origin: *` 允许任意域名跨域请求，存在 CSRF 和数据泄露风险。

**修复方案：**
- 从配置文件读取允许的 Origin 白名单
- 根据请求的 `Origin` 头动态匹配返回

```go
func CORS(allowedOrigins []string) gin.HandlerFunc {
    originSet := make(map[string]bool)
    for _, o := range allowedOrigins {
        originSet[o] = true
    }
    return func(c *gin.Context) {
        origin := c.GetHeader("Origin")
        if originSet[origin] {
            c.Header("Access-Control-Allow-Origin", origin)
            c.Header("Vary", "Origin")
        }
        // ...
    }
}
```

---

### 2.3 无 Request Body 大小限制

**文件：** `internal/interfaces/http/router.go`

**问题：** 路由层缺少 body size 中间件，攻击者可以发送 GB 级 payload 耗尽服务器内存。

**修复方案：**
- 添加全局 body size 限制中间件（建议 10MB）

```go
func MaxBodySize(maxBytes int64) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
        c.Next()
    }
}

// router.go
r.Use(MaxBodySize(10 << 20)) // 10MB
```

---

## 三、中等 — 性能与稳定性

### 3.1 Redis 缓存无熔断降级

**文件：** `internal/infrastructure/cache/redis_cache.go`

**问题：** Redis 故障时所有缓存操作同步失败，请求延迟急剧上升。无降级策略导致 Redis 故障扩散为全局故障。

**修复方案：**
- 引入 circuit breaker（如 `sony/gobreaker`）
- Redis 不可用时自动退化为仅 L1 本地缓存
- 记录降级状态到 metrics

```go
type RedisCacheWithBreaker struct {
    cache   *RedisCache
    breaker *gobreaker.CircuitBreaker
}

func (r *RedisCacheWithBreaker) Get(ctx context.Context, key string) (interface{}, error) {
    result, err := r.breaker.Execute(func() (interface{}, error) {
        return r.cache.Get(ctx, key)
    })
    if err != nil {
        return nil, err // 调用方应降级到 L1
    }
    return result, nil
}
```

---

### 3.2 多级缓存失效策略过于粗暴

**文件：** `internal/infrastructure/cache/multi_level_cache.go`

**问题：** `InvalidateByPrefix` 直接清空整个 L1 缓存（代码注释"简单策略"），大量有效缓存被误杀，导致缓存雪崩式重建。

**修复方案：**
- L1 缓存支持按 prefix 遍历删除
- 或维护一个 prefix → keys 的反向索引

```go
func (lc *LocalCache) DeleteByPrefix(prefix string) {
    lc.mu.Lock()
    defer lc.mu.Unlock()
    for key := range lc.items {
        if strings.HasPrefix(key, prefix) {
            delete(lc.items, key)
        }
    }
}
```

---

### 3.3 过滤结果未缓存

**文件：** `internal/application/service/filter_app_service.go`

**问题：** 相同文本 + 相同策略的重复请求每次都重新执行 AC 搜索，浪费 CPU。

**修复方案：**
- 以 `hash(text + strategy)` 为 key，缓存到多级缓存
- 设置合理 TTL（如 5 分钟）
- 词库更新时清除缓存

```go
func (s *FilterAppService) Filter(ctx context.Context, text string, strategyName string) (*FilterResult, error) {
    cacheKey := fmt.Sprintf("filter:%s:%x", strategyName, md5.Sum([]byte(text)))
    if cached, err := s.cache.Get(ctx, cacheKey); err == nil {
        return cached.(*FilterResult), nil
    }
    result := s.doFilter(text, strategyName)
    s.cache.Set(ctx, cacheKey, result, 5*time.Minute)
    return result, nil
}
```

---

### 3.4 Redis SCAN 删除效率低

**文件：** `internal/infrastructure/cache/redis_cache.go`

**问题：** `DeleteByPrefix` 每次 SCAN 100 条后逐条 DEL，大量 key 时产生 N+1 次 Redis 往返。

**修复方案：**
- 增大 SCAN COUNT（如 1000）
- 使用 Pipeline 批量删除
- 考虑使用 `UNLINK`（异步删除）替代 `DEL`

```go
func (rc *RedisCache) DeleteByPrefix(ctx context.Context, prefix string) error {
    var cursor uint64
    for {
        keys, nextCursor, err := rc.client.Scan(ctx, cursor, prefix+"*", 1000).Result()
        if err != nil {
            return err
        }
        if len(keys) > 0 {
            pipe := rc.client.Pipeline()
            for _, key := range keys {
                pipe.Unlink(ctx, key)
            }
            pipe.Exec(ctx)
        }
        cursor = nextCursor
        if cursor == 0 {
            break
        }
    }
    return nil
}
```

---

### 3.5 本地缓存清理持锁遍历全表

**文件：** `internal/infrastructure/cache/local_cache.go`

**问题：** 清理间隔固定 1 分钟，大量缓存条目时持写锁遍历整个 map，锁竞争严重，阻塞过滤请求。

**修复方案（选一）：**
- **分片锁：** 将缓存按 key hash 分成 N 个分片，每个分片独立加锁清理
- **惰性删除 + 概率淘汰：** 读取时检查过期，每次清理只随机抽样一批
- **过期堆：** 维护按过期时间排序的最小堆，清理时只弹出已过期条目

---

### 3.6 HTTP Server 缺少 IdleTimeout

**文件：** `cmd/server/main.go`

**问题：** 未设置 `IdleTimeout`，容易受 Slowloris 攻击——攻击者打开大量连接并极慢地发送数据，耗尽连接池。

**修复方案：**

```go
httpServer := &http.Server{
    Addr:         cfg.Server.HTTP.Addr,
    Handler:      router,
    ReadTimeout:  cfg.Server.HTTP.ReadTimeout,
    WriteTimeout: cfg.Server.HTTP.WriteTimeout,
    IdleTimeout:  30 * time.Second, // 新增
}
```

---

## 四、低优 — 部署与运维

### 4.1 Dockerfile 未锁定镜像 SHA256

**文件：** `deployments/docker/Dockerfile`

**问题：** `FROM alpine:3.19` 无 digest，不同时间构建可能拉取到不同的底层镜像，影响可复现性。

**修复方案：**

```dockerfile
FROM alpine:3.19@sha256:<具体 digest>
```

---

### 4.2 K8s 缺少 PodDisruptionBudget

**文件：** `deployments/kubernetes/`

**问题：** 集群滚动升级或节点维护时，可能同时驱逐所有 Pod，导致服务完全中断。

**修复方案：** 新增 `pdb.yaml`

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

**文件：** `deployments/kubernetes/deployment.yaml`、`internal/interfaces/http/handler/health_handler.go`

**问题：** 就绪探针只检查数据库连接，未检查 Redis 可用性和 AC 自动机是否加载完成。Pod 可能在初始化未完成时就接收流量。

**修复方案：**
- 就绪探针增加 Redis ping 检查
- 增加 AC 自动机词库加载状态检查（词库数 > 0 或显式标记 ready）

```go
func (h *HealthHandler) Readiness(c *gin.Context) {
    // 检查 MySQL
    if err := h.db.Ping(); err != nil {
        response.Error(c, 503, "mysql not ready")
        return
    }
    // 检查 Redis
    if err := h.redis.Ping(c).Err(); err != nil {
        response.Error(c, 503, "redis not ready")
        return
    }
    // 检查 AC 引擎
    if !h.engine.IsLoaded() {
        response.Error(c, 503, "filter engine not ready")
        return
    }
    response.Success(c, "ready")
}
```

---

### 4.4 Import 静默跳过无效词

**文件：** `internal/application/service/word_app_service.go`

**问题：** 批量导入时校验失败的词被直接跳过，调用方不知道哪些词未导入成功。

**修复方案：**
- 返回导入结果摘要：成功数、失败数、失败详情列表

```go
type ImportResult struct {
    SuccessCount int              `json:"success_count"`
    FailedCount  int              `json:"failed_count"`
    Failures     []ImportFailure  `json:"failures,omitempty"`
}

type ImportFailure struct {
    Index  int    `json:"index"`
    Word   string `json:"word"`
    Reason string `json:"reason"`
}
```

---

### 4.5 rand.Read 错误未处理

**文件：** `internal/interfaces/middleware/request_id.go`

**问题：** `rand.Read(b)` 返回的 error 被忽略，低熵环境下可能生成空/无效的 Request ID。

**修复方案：**
- 使用 `uuid` 库替代手动生成
- 或处理错误并 fallback 到时间戳

---

### 4.6 数据库连接池比例不合理

**文件：** `internal/infrastructure/config/config.go`

**问题：** 默认 `MaxOpenConns=100`、`MaxIdleConns=10`，idle 连接数过低。频繁创建和销毁连接带来额外开销。

**修复方案：**
- 推荐 `MaxIdleConns = MaxOpenConns * 0.25`（即 25）
- 增加 `ConnMaxIdleTime` 配置（如 5 分钟）

---

## 优化路线图

```
Phase 1 — 安全加固（1-2 天）
  ├── 1.1 修复 LocalCache 协程泄漏
  ├── 1.2 限制 BatchDetect 并发
  ├── 2.1 移除 query string API key
  ├── 2.2 收敛 CORS 白名单
  └── 2.3 添加 body size 限制

Phase 2 — 稳定性提升（2-3 天）
  ├── 1.3 Pub/Sub 重连机制
  ├── 1.4 修复 triggerRebuild context
  ├── 3.1 Redis 熔断降级
  ├── 3.6 添加 IdleTimeout
  └── 4.3 完善 Readiness Probe

Phase 3 — 性能优化（2-3 天）
  ├── 3.2 精细化缓存失效
  ├── 3.3 过滤结果缓存
  ├── 3.4 Redis Pipeline 批量删除
  └── 3.5 本地缓存分片锁

Phase 4 — 部署加固（1 天）
  ├── 4.1 镜像 SHA 锁定
  ├── 4.2 PodDisruptionBudget
  ├── 4.4 Import 返回失败详情
  └── 4.6 连接池参数调优
```
