# CensorHub 性能优化待办清单

- 记录日期：2026-05-12
- 关联基线报告：[`reports/baseline-i5-13500h-2026-05-12.md`](../reports/baseline-i5-13500h-2026-05-12.md)
- 基线数据摘要：`/detect` 峰值 3455 RPS，CPU 仅 60–70% 未饱和；`/batch` 在 c=50/200/500 呈 U 形（559 → 431 → 1202）

## 审计结论

**CPU 未顶满 + `/batch` U 形曲线**已经非常直接地指向"锁竞争和无效 CPU 开销"。按经验，在不改架构的前提下，单实例还能再挤出 **40–80%** 的吞吐。

本文档整理 12 项具体发现，按投入产出比排优先级。

---

## 1. 大影响（Large Impact）

### 1.1 `LocalCache` 单把 `sync.RWMutex` → 分片锁

| 字段 | 值 |
|---|---|
| 文件 | `internal/infrastructure/cache/local_cache.go:17` |
| 现象对应 | `/batch` 的 U 形曲线在 c=200 达到争抢临界点 |
| 改法 | 16–32 个 shard，每个 shard 独立 `map + RWMutex`；key hash 后 `% numShards` 决定落在哪个 shard |
| 参考实现 | `groupcache`、`orcaman/concurrent-map`、标准库 `sync.Map`（读多写少场景） |
| 预估收益 | `/batch` 吞吐 +40–60%；`/detect` 高并发 p50 下降 |
| 工作量 | 中（1–2 小时 + 单元测试） |

### 1.2 AC 匹配 `results` 切片未预分配

| 字段 | 值 |
|---|---|
| 文件 | `internal/infrastructure/algorithm/ac_automaton.go:140` |
| 现象 | `var results []valueobject.MatchItem` 零容量起步，命中多时触发 ~log(N) 次 realloc |
| 改法 | `results := make([]valueobject.MatchItem, 0, 64)` |
| 预估收益 | 高命中率文本 latency –5~15%；纯算法路径更稳定 |
| 工作量 | 极小（5 分钟） |

### 1.3 `BatchDetect` 信号量 → 固定 worker pool

| 字段 | 值 |
|---|---|
| 文件 | `internal/application/service/filter_app_service.go:123-173` |
| 现象 | 每个批请求 per-text `wg.Add + go func`，c=500 × 10 items = 5000 goroutine/批的 churn |
| 改法 | 启动时固定 N 个 worker（`GOMAXPROCS * 2`），通过 channel 投递 job；请求共享同一个 pool |
| 注意 | 需要处理 ctx 取消、错误冒泡、保证结果顺序；可以加 `context.WithCancel` 早停 |
| 预估收益 | `/batch` 吞吐再提 10–20%，尾延迟更平稳 |
| 工作量 | 中（2–3 小时 + 测试） |

---

## 2. 中影响（Noticeable）

### 2.1 `Tracing` 中间件即使 `sample_rate=0` 也创建 span

| 字段 | 值 |
|---|---|
| 文件 | `internal/interfaces/middleware/tracing.go:18` |
| 现象 | 每请求 `tracer.Start()` + 4× `WithAttributes()` 仍跑了，即使采样率 0 |
| 改法 | 中间件入口先 `if !cfg.Trace.Enabled || cfg.Trace.SampleRate <= 0 { c.Next(); return }` |
| 预估收益 | 关 tracing 下 `/detect` 吞吐 +3–5% |
| 工作量 | 极小 |

### 2.2 `CircuitBreaker` 的 `sync.Mutex` → `atomic`

| 字段 | 值 |
|---|---|
| 文件 | `internal/infrastructure/cache/circuit_breaker.go:40,61,78,96`，以及 `multi_level_cache.go:38,42,53,70,86,111` |
| 现象 | L1 命中路径（~10 ns）也要过 breaker 的 mutex（~100–200 ns），10–20× 放大 |
| 改法 | `state atomic.Uint32`、`failCount atomic.Int32`、`lastFailTime atomic.Int64`；状态机用 CAS |
| 预估收益 | `/detect` 吞吐 +3–8% |
| 工作量 | 中（注意状态机一致性） |

### 2.3 `RateLimit` 的双锁 dance → `sync.Map`

| 字段 | 值 |
|---|---|
| 文件 | `internal/interfaces/middleware/ratelimit.go:36-57` |
| 现象 | RLock 读未命中 → Unlock → Lock 写，双锁路径；热路径重复 |
| 改法 | `limiters *sync.Map; limiters.LoadOrStore(key, newLimiter(...))` |
| 预估收益 | 稳定流量下 +2–4%；高基数 IP 流量显著 |
| 工作量 | 小 |

### 2.4 每请求 `rand.Read(16)` 生成 request ID

| 字段 | 值 |
|---|---|
| 文件 | `internal/interfaces/middleware/request_id.go:27-34` |
| 现象 | 每请求 ~1–2 μs 随机数，4K RPS 下 4–8 ms/s CPU |
| 改法 | 客户端已传 X-Request-ID 则复用（已做）；未传则用 ULID / Snowflake；或 per-goroutine 缓存 64 字节随机池 |
| 预估收益 | 微小（<2%） |
| 工作量 | 小 |

---

## 3. 已做得好，无需改（记录为检查点）

| 位置 | 已有最佳实践 |
|---|---|
| `internal/application/assembler/word_assembler.go:45-65` | `FilterResultToDTO` 预分配 matches 切片 |
| `internal/interfaces/http/router.go:18` | `gin.SetMode(gin.ReleaseMode)` |
| `internal/infrastructure/algorithm/filter_engine.go:13-35` | `Match()` 用 `atomic.Value.Load()` lock-free 读，仅 `Rebuild` 加锁 |
| `internal/infrastructure/algorithm/text_normalizer.go:32-50` | `strings.Builder.Grow()` 预设容量 |
| `internal/infrastructure/algorithm/strategy_replace.go:64-86` | Replace 用 Builder + Grow + WriteRune |

---

## 4. 落地顺序

Phase 1（核心 3 件，主攻吞吐与锁竞争）：
1. 1.1 LocalCache 分片
2. 1.2 AC results 预分配
3. 1.3 BatchDetect worker pool

Phase 2（顺手做，提炼 CPU）：
4. 2.1 Tracing 短路
5. 2.2 CircuitBreaker atomic
6. 2.3 RateLimit sync.Map
7. 2.4 request ID（低优，可不做）

Phase 3（验证）：
8. 在相同硬件上跑同一矩阵，对比前后 p50/p99/RPS；结果追加到 `performance-report-2026-05-12.md`

---

## 5. 预期目标

核心 3 件改完后：

| 指标 | 基线 | 目标 | 改善 |
|---|---:|---:|---:|
| `/detect @ c=200` RPS | 3455 | 5000+ | +45% |
| `/batch @ c=200` RPS | 431（U 形谷底） | ≥1000 | 消除 U 形 |
| `/batch @ c=500` RPS | 1202 | 1500+ | +25% |
| `/detect` CPU 利用率 | 60–70% | 85%+ | 顶掉闲置 |

> 注：p99 与尾延迟主要受 WSL2 环境影响，本次优化目标聚焦吞吐；p99 的真实评估需在裸机 Linux 复测。
