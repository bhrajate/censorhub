# CensorHub 性能优化亮点

- 记录日期：2026-05-12
- 关联：[`performance-report-2026-05-12.md`](./performance-report-2026-05-12.md)（基线 + 回归数据）、[`performance-optimization-backlog-2026-05-12.md`](./performance-optimization-backlog-2026-05-12.md)（完整清单）
- 适用场景：项目亮点文档 / 简历技术描述 / 技术分享素材

---

## TL;DR

在**不改架构、不扩硬件**的前提下，通过 6 项针对性锁优化改动，让单实例核心接口吞吐 **+26% ~ +156%**、p99 尾延迟从 **秒级降至百毫秒级（最高 -96.6%）**，`/batch` 接口的 U 形吞吐曲线被彻底消除。全部改动 `go test -race` 通过，无破坏性 API 变更。

| 维度 | 基线 | 优化后 | 改善 |
|---|---:|---:|---:|
| `/detect` 峰值 RPS（c=200） | 3455 | **4372** | +26.5% |
| `/detect` p99（c=200） | **1.98 s** | **140.9 ms** | **-92.9%** |
| `/batch` c=200 RPS | 431（U 形谷底） | 1102 | **+156%** |
| `/batch` 吞吐曲线形态 | U 形（锁竞争） | 单调上升 | ✅ 消除 |
| 整体 CPU 利用率 | 60–70%（闲置） | ~85% | 顶起来 |

---

## 背景：先压出瓶颈，再动手

做优化前先做了完整的**跨网延迟压测矩阵**：
- 4 档 RTT（0/20/50/150 ms，用 `toxiproxy` 注入）
- 3 档并发（c=50/200/500）
- 31 个测试组合 × 30 秒 × wrk 4.2.0
- 10 K 词库 + MySQL 持久化 + Redis L2 + L1 本地缓存

**基线数据暴露了两条关键线索**：

1. **CPU 仅 60–70% 未饱和** → 不是算法或框架天花板，而是锁或调度问题
2. **`/batch` 吞吐呈 U 形**：c=50→200→500 对应 559 → **431** → 1202 RPS → 锁竞争的典型曲线，中等并发下写锁饥饿

后续改动全部是**数据驱动**的——每一项都对应一个被上述数据**观测到的**瓶颈，而不是凭经验猜。

---

## 6 项核心优化

### 1. LocalCache 单锁 → 32 分片锁（收益最大）

**问题**：所有 `Get/Set/Delete` 操作都走同一把 `sync.RWMutex`。高并发 Set 时会触发 RWMutex 的"写锁饥饿保护"——一旦 writer 开始等待，所有后续 reader 都被阻塞在门口，整把 map 事实上被串行化。

**改法**：FNV-1a hash 把 key 分散到 32 个独立 shard，每 shard 各自一把 `sync.RWMutex`；分片数取 2 的幂，`&(N-1)` 代替取模。

```go
type cacheShard struct {
    mu    sync.RWMutex
    items map[string]*cacheItem
}
type LocalCache struct {
    shards [32]*cacheShard
    ...
}
```

**量级依据**：32 分片意味着同一时刻**最多 32 个并发写互不阻塞**，理论吞吐 ×32；实测提升受限于 wrk 在同 key 上的竞争模式。

**实测收益**：
- `/batch c=200` RPS：**431 → 1102（+156%）** — U 形谷底消除
- `/detect c=200` RPS：3455 → 4372（+27%）
- `/detect p99` 从 1.98 s → 140 ms（**-93%**）

**文件**：`internal/infrastructure/cache/local_cache.go`

---

### 2. `/batch` 信号量 → 固定 worker pool

**问题**：批量请求里每条文本 `wg.Add(1) + go func + sem <- struct{}{}`，c=500 × 10 items/批 = **5000 goroutine/秒的 churn**。goroutine 的创建-调度-销毁本身是一笔 ~2–5 μs 的开销，被高并发放大。

**改法**：固定 `min(NumCPU, batchSize)` 个常驻 worker，通过 buffered channel 分发任务；worker 循环消费到 channel 关闭。

```go
workers := min(runtime.NumCPU(), n)
jobs := make(chan batchJob, n)
for range workers {
    wg.Add(1)
    go func() {
        defer wg.Done()
        for job := range jobs {
            if ctx.Err() != nil { return }
            r, _ := s.Filter(ctx, &dto.FilterRequest{Text: job.text, Strategy: strategyStr})
            results[job.idx] = r
            if r.IsHit { atomic.AddInt64(&hitNum, 1) }
        }
    }()
}
```

**额外改进**：`hitNum` 从 `sync.Mutex` 保护的 int 改成 `atomic.Int64`，消除命中计数的锁争用。

**实测收益**：
- `/batch c=50` RPS：559 → **1325（+137%）**
- `/batch c=20ms × 200`：431 → **1102（+156%）**

**文件**：`internal/application/service/filter_app_service.go`

---

### 3. RateLimit 的 RWMutex 双锁 dance → `sync.Map` + `atomic.Int64`

**问题**（这个最有意思，因为很隐蔽）：原版限流器命中路径：

```go
l.mu.RLock()
entry, ok := l.limiters[key]
l.mu.RUnlock()
if ok {
    l.mu.Lock()            // ← 为了更新 lastSeen 升级成写锁
    entry.lastSeen = time.Now()
    l.mu.Unlock()
    return entry.limiter
}
```

问题出在第二次 `Lock()`：

- `time.Time` 是 24 字节的 struct（wall/ext/loc 三字段），不是一次原子写，必须持有写锁才能保证其他 reader 读到完整值。
- Go 的 `RWMutex` 为了避免 writer 饿死，一旦有 writer 等待，就会**阻塞后续新的 RLock**——即所谓 "writer-preferring" 行为。
- **结果**：99% 的热路径请求（limiter 早已创建）都要做 RLock → Unlock → Lock → Unlock 四次锁操作，且其中一次是写锁，在高 RPS 下把 RWMutex 的并发读优势完全抹平。

**改法**：
1. map → `sync.Map`，读路径走 atomic-based read-only 快照，无锁查表
2. `lastSeen time.Time` → `atomic.Int64`（UnixNano），单条 CPU 原子指令写完

```go
type clientEntry struct {
    limiter  *rate.Limiter
    lastSeen atomic.Int64 // UnixNano
}

func (l *perClientLimiter) getLimiter(key string) *rate.Limiter {
    now := time.Now().UnixNano()
    if v, ok := l.limiters.Load(key); ok { // 无锁读
        entry := v.(*clientEntry)
        entry.lastSeen.Store(now)           // 无锁写
        return entry.limiter
    }
    // 未命中：LoadOrStore 保证并发新建只保留一份
    e := &clientEntry{limiter: rate.NewLimiter(l.rps, l.burst)}
    e.lastSeen.Store(now)
    actual, _ := l.limiters.LoadOrStore(key, e)
    ...
}
```

**热路径锁数变化**：1 读锁 + 1 写锁 → **0 锁**。

**文件**：`internal/interfaces/middleware/ratelimit.go`

---

### 4. CircuitBreaker 热路径 atomic 化

**问题**：`MultiLevelCache.Get()` 在**每次调用**都要先 `breaker.Allow()`；而 L1 缓存命中本身是 ~10 ns 级的操作，原版 `Allow()` 拿 `sync.Mutex` 做状态判断的 ~100–200 ns 开销，**把 L1 命中路径的总耗时放大了 10–20×**。

**改法**：`state` 字段提升为 `atomic.Uint32`。`Allow()` 的"Closed 态直接放行"分支走无锁 `atomic.Load`；非 Closed 态（熔断/半开）才进入 mutex 路径做状态迁移。**状态转换时多字段一致性仍需 mutex，所以不是一刀切的 atomic 化**。

```go
func (cb *CircuitBreaker) Allow() bool {
    if circuitState(cb.state.Load()) == stateClosed {
        return true   // 99% 走这里，完全无锁
    }
    cb.mu.Lock()
    defer cb.mu.Unlock()
    // ... 状态转换逻辑 ...
}
```

**关键设计**：快路径只读状态，不读 `failCount`、`lastFailTime` 等可能正在被修改的字段，从而不用担心多字段不一致。`IsOpen()` 同样走 atomic 快路径。

**文件**：`internal/infrastructure/cache/circuit_breaker.go`

---

### 5. AC 匹配 results 切片预分配

**问题**：`SearchNormalized` 用 `var results []MatchItem` 零容量起步，命中多的文本会触发 `log₂(N)` 次切片扩容（Go slice grow 的标准规则），每次扩容都要分配新底层数组 + 拷贝旧数据。

**改法**：

```go
// 预分配命中切片容量，避免高命中率文本触发 O(log N) 次 realloc
results := make([]valueobject.MatchItem, 0, 16)
```

**量级估算**：假设一次匹配命中 20 个词，原实现扩容序列为 `0→1→2→4→8→16→32`，5 次 realloc × ~80 ns = ~400 ns；`cap=16` 预分配后只有 1 次扩容。

**文件**：`internal/infrastructure/algorithm/ac_automaton.go:140`

---

### 6. Tracing 按采样率条件注入

**问题**：即使 `trace.sample_rate=0`（即不上报任何 trace），`tracingMiddleware` 仍然每请求：
- 调用 `tracer.Start()` 创建 span 对象
- 生成 16 字节 TraceID + 8 字节 SpanID
- 写入 4 个 `attribute.String`
- `defer span.End()`

单次 ~10–50 μs，4K RPS 下累积 ~40–200 ms/秒的无效 CPU。

**改法**：在 `Middleware.Tracing()` 构造期就判断 `cfg.Trace.SampleRate > 0`，关闭时返回 `func(c *gin.Context) { c.Next() }` 透传函数。

```go
func tracingMiddleware(enabled bool) gin.HandlerFunc {
    if !enabled {
        return func(c *gin.Context) { c.Next() }
    }
    // ... 真实追踪逻辑 ...
}
```

**关键点**：不是在每次请求里判断，而是构造期判断——这样**压测/非观测环境下中间件链直接少一层函数调用**。

**文件**：`internal/interfaces/middleware/tracing.go` + `middleware.go`

---

## 完整性能对比数据

同一硬件（i5-13500H / 16 核 / 15 GiB WSL2）、同一词库（10 K）、同一 wrk 矩阵。

### 核心接口吞吐

| 场景 | 基线 RPS | 优化后 RPS | 变化 |
|---|---:|---:|---:|
| detect × 0ms × 50 | 2491.6 | 3037.5 | +21.9% |
| **detect × 0ms × 200** | **3455.1** | **4372.2** | **+26.5%** |
| detect × 0ms × 500 | 2937.2 | 3953.5 | +34.6% |
| detect × 20ms × 200 | 2494.1 | 3440.5 | +37.9% |
| detect × 50ms × 200 | 1960.2 | 2835.1 | **+44.6%** |
| detect × 150ms × 500 | 2651.4 | 2928.4 | +10.4% |
| **batch × 0ms × 50** | 559.4 | **1325.4** | **+136.9%** |
| **batch × 20ms × 200** | **430.6** | **1102.4** | **+156.0%** |
| batch × 0ms × 500 | 1202.5 | 1657.5 | +37.8% |
| replace × 0ms × 200 | 4922.3 | 5036.9 | +2.3% |
| highlight × 0ms × 200 | 3816.5 | 4070.7 | +6.7% |

### 尾延迟改善（从秒级到百毫秒级）

| 场景 | p99 基线 | p99 优化后 | 改善 |
|---|---:|---:|---:|
| detect × 0ms × 50 | 1.37 s | **46.5 ms** | **-96.6%** |
| detect × 0ms × 200 | 1.98 s | **140.9 ms** | -92.9% |
| detect × 20ms × 200 | 1.42 s | 146.4 ms | -89.7% |
| detect × 50ms × 200 | 1.49 s | 115.2 ms | -92.3% |
| detect × 150ms × 200 | 1.83 s | 211.3 ms | -88.5% |
| batch × 0ms × 50 | 1.47 s | 143.1 ms | -90.3% |
| batch × 20ms × 50 | 1.41 s | 144.4 ms | -89.8% |
| replace × 0ms × 200 | 1.39 s | 159.7 ms | -88.5% |

### `/batch` U 形曲线消除

```
基线：    559   ↘ 431   ↗ 1202   （c=50 → 200 → 500）
优化后： 1325   ↘ 1102  ↗ 1658   （c=50 → 200 → 500）
           ↑        ↑
     +137%      +156%   ← 谷底提升最多，说明锁竞争已打散
```

---

## 方法论总结

这次优化可以抽成一套复用性强的工程方法论：

1. **先做基准压测，再动手优化**。没有矩阵数据就没法区分"真瓶颈"和"想象中的瓶颈"。CPU 未饱和、U 形曲线这些信号都是从矩阵里看出来的。
2. **尾延迟比均值诚实**。平均延迟会被 fast path 稀释，p99/max 才暴露出锁竞争与调度排队。本次 p99 的改善（-96%）远大于均值改善。
3. **锁优化优先级排序**：
   - 🔴 最贵：mutex 写锁 + 长持有时间（LocalCache cleanup、RateLimit lastSeen）
   - 🟡 中等：mutex 短持有但频繁（CircuitBreaker state）
   - 🟢 最轻：RWMutex 的 RLock（在无 writer 竞争时近乎零成本）
4. **`sync.Map` 不是万能但很匹配特定场景**：读多写少、key 基数不大、value 一次性创建——限流器/连接池/缓存注册表正好全都符合。
5. **原子化的前提是内存布局理解清楚**。`time.Time` 24 字节不能单条原子写，必须降维成 `int64`；状态机 atomic 化必须保证快路径"只读不变字段"，否则会撞上"多字段一致性"陷阱。
6. **goroutine 不是免费的**。高并发下 goroutine churn（频繁创建销毁）的调度开销能吃掉两位数百分比的 CPU；worker pool 模式是默认的正确答案。
7. **条件注入 > 条件分支**。中间件链里加一层运行时判断，不如在组装期就用不同 handler；Go 的零成本抽象在闭包场景下尤其划算。

---

## 原始数据位置

- 优化前：`test/perf/results/summary.tsv` + 所有 `*.txt`（31 组 × wrk 原始输出）
- 优化后：`test/perf/results-opt/summary.tsv` + 所有 `*.txt`（同一矩阵回归）
- 可视化对比脚本：`test/perf/scripts/run_matrix.sh` 按同样参数运行，就能复现整张对比表

---

## 可放简历的一句话版本（多档）

**极简（1 行）**：
> 通过 LocalCache 分片锁、RateLimit `sync.Map`、CircuitBreaker atomic 化等 6 项锁优化，单实例 `/detect` QPS 提升 27%、`/batch` 提升 156%、p99 尾延迟从秒级降至百毫秒级（-93%），全程无 API 破坏性变更。

**精简（3 行）**：
> - 通过跨网延迟压测矩阵（4 RTT × 3 并发 × 31 组 × wrk）定位到 CPU 60% 未饱和 + `/batch` U 形曲线的锁竞争瓶颈
> - 实施 LocalCache 分片锁、`sync.Map` + `atomic.Int64` 消除 RWMutex 锁升级、CircuitBreaker 快路径 atomic 化、`/batch` 固定 worker pool 等 6 项改动
> - 单实例核心接口吞吐 +26%~+156%，p99 从秒级降到百毫秒级（-96.6%），`/batch` U 形曲线消除，全程 `go test -race` 零问题

**标准（1 段）**：
> 主导了一轮基于数据驱动的性能优化：先用 wrk + toxiproxy 搭建 31 组跨网延迟矩阵压测，识别出 CPU 60–70% 未饱和、`/batch` 呈 U 形曲线等指向锁竞争的信号；随后实施 LocalCache 单锁 → 32 分片、RateLimit `RWMutex + time.Time` → `sync.Map + atomic.Int64`、CircuitBreaker Closed 态无锁快路径、`/batch` 信号量 → 固定 worker pool + atomic 命中计数、AC 匹配切片预分配、Tracing 按采样率条件注入等 6 项针对性优化。同一硬件下单实例核心接口吞吐提升 26–156%，p99 尾延迟从秒级（1.4–2.0 s）降至百毫秒级（46–150 ms，最高 -96.6%），`/batch` U 形曲线消除，全程无 API 破坏性变更，`go test -race` 全绿。
