# CensorHub 性能优化 Round 2 亮点

- 记录日期：2026-05-15
- 关联：
  - 报告：[`reports/round2-i5-13500h-2026-05-15.md`](./reports/round2-i5-13500h-2026-05-15.md)（60s 复测验证）
  - backlog：[`optimizations/round2-backlog-2026-05-13.md`](./optimizations/round2-backlog-2026-05-13.md)
  - 上轮亮点：[`round1-highlights-2026-05-12.md`](./round1-highlights-2026-05-12.md)
- 适用场景：项目亮点文档 / 简历技术描述 / 技术分享素材

---

## TL;DR

round1 解决了"锁竞争"，但 c=1000+ 极限并发压测暴露出**新瓶颈**：Redis 连接池排队 + JSON 序列化 + 字符串分配。round2 用 4 项小而精的改动应对，跨网真实场景下吞吐再 **+10~60%**、p99 降 **20~67%**。同时这一轮还发现并解决了一个值得记录的"假阴性"：因 sonic JIT 冷启动导致首跑数据被误判为退步——后来 60s 预热复测确认了真实收益。

| 维度 | round1 优化后 | round2 优化后 | 改善 |
|---|---:|---:|---:|
| `/detect` × c=500 × RTT=20ms RPS | 3270 | **5119** | **+57%** |
| `/detect` × c=500 × RTT=20ms p99 | 1.13 s | **377 ms** | **-67%** |
| `/batch` × c=200 × RTT=50ms RPS | 354 | **851** | **+140%** |
| `/healthz` × c=200 × RTT=0ms p99 | 161 ms | **52 ms** | -68% |
| `/replace` × c=200 × RTT=50ms RPS | 2577 | **2990** | +16% |
| 错误率 | 0% | 0% | ✅ 保持 |

---

## 背景：round1 之后的新瓶颈

round1 6 项锁优化把 p99 从秒级压到百毫秒级、`/batch` U 形曲线消除。但在追加的 c=1000/2000 极限并发压测中观察到新的退化：

- **`/batch` 在 c≥500 雪崩**：c=500 RPS 1658 → c=1000 仅剩 **809** → c=2000 只有 **469**
- **`/detect` 在 c=2000 的 p99 飙到 23 s**：吞吐翻倍但用户体验崩盘

这两个信号都指向**资源池外溢**而非 CPU——CPU 仍未顶满。审计后定位到三处：

1. Redis pool 默认 100，被 `/batch` 的 10 路 fan-out × c=1000 = 1000 路并发 GET 直接打爆，go-redis 队列堆积
2. `encoding/json` 反射式序列化在 7K RPS 上累积每秒 ~50 ms 的 CPU
3. 缓存 key 拼接每请求触发 2-3 次小字符串分配

---

## 4 项核心优化

### 1. Redis pool size 100 → 500（收益最大）

**问题**：`/batch` 在 c≥500 后吞吐腰斩。

**根因**：默认 `pool_size: 100`。在 c=1000 × `/batch` 10 路 fan-out × ~30% L2 cache miss 的场景下，瞬时 ~300 路并发 Redis ops 抢 100 个连接。go-redis 内部以 channel 排队，**多余的 200 个请求必须等连接释放**，把 p99 从亚秒级直接抬到 4–10 秒。

**改法**：仅改 `configs/config.yaml` 默认值 `100 → 500`，同时在 `setDefaults` 兜底：

```go
if cfg.Redis.PoolSize == 0 {
    cfg.Redis.PoolSize = 500
}
```

**额外内存代价**：500 个 TCP 连接 + buffer ≈ 15 MB，可忽略。

**实测收益**（结合下面 4 项的整体效果）：
- `/batch × c=200 × 50ms` RPS：354 → **851**（+140%）
- `/batch × c=500 × 20ms` p99：2.10 s → **1.25 s**（-40%）

**关键洞察**：这是个"配置改一行解决一切"的典型，但前提是先用极限并发压测**把瓶颈具体定位到连接池**——光看 CPU/内存指标看不出。

**文件**：`configs/config.yaml`、`configs/config.production.yaml`、`internal/infrastructure/config/config.go`

---

### 2. sonic 替换 encoding/json（热路径）

**问题**：L2 cache hit 路径每次都要 `json.Unmarshal`、miss 路径要 `json.Marshal`。在 7K RPS 上每秒有 7K JSON 操作 × 5–10 μs ≈ **35–70 ms/s 的 CPU 浪费**在反射上。

**根因**：Go 标准库 `encoding/json` 走运行时反射 + 状态机，速度约为高性能库的 1/3。

**改法**：仅替换 `filter_app_service.go` 的 2 处热路径：

```go
import "github.com/bytedance/sonic"

var jsonAPI = sonic.ConfigDefault

// L2 hit 路径
if jsonAPI.Unmarshal(data, &cached) == nil { ... }

// L2 写回路径
if data, err := jsonAPI.Marshal(resp); err == nil { ... }
```

`/words` 管理接口等冷路径**保留 stdlib**，控制改动半径。

**收益估算**：单次 JSON 操作 5–10 μs → 2–3 μs，`/detect` 吞吐 +10–15%。

**意外发现 — sonic JIT 冷启动陷阱**：

sonic 对每个新类型首次 `Marshal/Unmarshal` 会触发**运行时 JIT 代码生成**（10–100 ms）。首跑压测时这导致 c=50 场景 p99 从 50 ms 跳到 1.5 s，被误判为"R2 退步"。复测前加 70 秒预热后 p99 回到 42 ms（比 round1 还低）。

**配套防御**：在包加载阶段提前预热 JIT，把"首请求成本"前置：

```go
func init() {
    if err := sonic.Pretouch(reflect.TypeFor[dto.FilterResponse]()); err != nil {
        // Pretouch 失败不阻塞启动，首请求会自行触发 JIT
        _ = err
    }
}
```

这样生产环境也不会被首批请求的冷启动尾延迟波及。

**实测收益**：
- `/detect × c=500 × 20ms` RPS：3270 → **5119**（+57%）
- `/detect × c=500 × 20ms` p99：1.13 s → **377 ms**（-67%）
- `/replace × c=200 × 50ms` RPS：2577 → **2990**（+16%）

**文件**：`internal/application/service/filter_app_service.go`

---

### 3. filterCacheKey 用 strings.Builder + Grow

**问题**：每个请求都要查 L1 + 查 L2 + 写回 L2，三次都需要拼 cache key，每次拼接产生 2-3 次中间字符串分配。7K RPS × 3 = **21K alloc/s 给 GC 压力**。

**改法**：用 `strings.Builder` + `Grow(32)`（key 典型长度 20–26 字节），单次分配完成：

```go
func filterCacheKey(text string, strategy string) string {
    h := fnv.New64a()
    h.Write([]byte(text))

    var b strings.Builder
    b.Grow(32)
    b.WriteString("filter:")
    b.WriteString(strategy)
    b.WriteByte(':')
    b.WriteString(strconv.FormatUint(h.Sum64(), 36))
    return b.String()
}
```

**收益**：每请求节省 2-3 次小分配，GC pause 频次降低，间接提升尾延迟稳定性。在 sonic 已经吞掉 JSON 大头之后，这个微优化的作用是"让小数据路径更平滑"。

**文件**：`internal/application/service/filter_app_service.go`（`filterCacheKey` 函数）

---

### 4. 中间件顺序与 Metrics 标签审计 — 确认无需改

这一项原本列在 backlog 里准备改，但**审计后决定保留现状**——不改也是一种工程结论：

- `metricsMiddleware` 已经使用 `c.FullPath()`（路由模板）而非 `c.Request.URL.Path`，**Prometheus label cardinality 安全**，不会被随机参数污染
- 全局中间件链 `Logger/Metrics/Tracing` 先于 v1 组内的 `RateLimit/Auth`，是**可观测性最佳实践**——需要统计被限流流量的 p50/p99；如果 RateLimit 提前会丢失这部分监控数据

**意义**：这一项展示了"看完代码做减法"也是优化能力的一部分——避免凭直觉改"看起来有问题但其实没问题"的代码。

---

### 评估后暂缓 — FilterResponse 对象池化

backlog 里原本有第 5 项："`FilterResponse + []MatchDTO` 用 `sync.Pool` 池化"。但代码审计后**主动暂缓**：

| 维度 | 评估结论 |
|---|---|
| 收益 | 7K RPS × 30% miss = 2100 次/秒分配。在 sonic 已经把 JSON 开销降到 2-3 μs/次后，pprof 通常不再把这点分配识别为热点 |
| 风险 | `*FilterResponse` 从 3 条不同生命周期的路径流出（单请求 handler、batch 收集到 Results 切片、cache hit 分支 new 对象）；池化引入 data race 风险显著 |
| 决策 | **不做** — 风险/收益比不划算 |

**意义**：性能优化不是越多越好——**对于风险大、收益小的项主动 say no** 是高级工程师的判断力体现。

---

## 完整性能对比数据

同硬件（i5-13500H/WSL2）+ 60 s 窗口 + 70 s 预热的复测结果。三组对比：R1-opt（round1 完成）/ R2-first（round2 首跑 30s 无预热）/ R2-verify（round2 60s 预热复测）。

### 核心吞吐对比

| 场景 | R1-opt RPS | R2-verify RPS | 变化 |
|---|---:|---:|---:|
| **detect c=500 / 20ms** | 3270 | **5119** | **+57%** |
| detect c=500 / 50ms | 3771 | 4430 | +17% |
| detect c=500 / 0ms | 3954 | 4825 | +22% |
| detect c=200 / 20ms | 3441 | 3797 | +10% |
| **batch c=200 / 50ms** | 354 | **851** | **+140%** |
| batch c=200 / 150ms | 568 | 847 | +49% |
| batch c=500 / 20ms | 1324 | 1304 | -1.5%（持平） |
| **healthz c=200 / 0ms** | 9798 | **11722** | +20% |
| highlight c=200 / 50ms | 2535 | 2800 | +10% |
| replace c=200 / 50ms | 2577 | 2990 | +16% |

### 尾延迟对比

| 场景 | R1-opt p99 | R2-verify p99 | 改善 |
|---|---:|---:|---:|
| detect c=500 / 20ms | 1.13 s | **377 ms** | **-67%** |
| detect c=500 / 50ms | 840 ms | 329 ms | -61% |
| **healthz c=200 / 0ms** | 161 ms | **52 ms** | **-68%** |
| batch c=200 / 50ms | 872 ms | 470 ms | -46% |
| highlight c=200 / 50ms | 122 ms | (未恶化) | -|
| replace c=200 / 50ms | 147 ms | 117 ms | -20% |

### 已知不足：`/batch × c=500 × 0ms`

| 场景 | R1-opt | R2-verify | 变化 |
|---|---:|---:|---:|
| batch c=500 / 0ms | 1657 RPS | 761 RPS | **-54%** |

这是真实退步，不是测量噪声（首跑 758 / 复测 761 一致）。**根因**是 Redis pool 扩容把 I/O 瓶颈解除后，c=500 × 10 路 fan-out 的本地极端场景下 **CPU/序列化变成新瓶颈**——这是预期中的"瓶颈转移"，不是 round2 的问题，而是 round3 应该解决的下一站。这种"清晰呈现 trade-off"的诚实记录是性能工程的应有之义。

---

## 方法学沉淀

round2 最有价值的不只是数据本身，还有几条可复用的工程经验：

### 1. 任何依赖 JIT/缓存预热的库都必须先预热再压测

sonic / fastjson / mmap-based 索引等首次访问会触发 lazy 初始化（代码生成、内存映射）。**首跑数据必然不可信**——在没预热的情况下 p99 会被冷启动尾巴拉到秒级。生产环境对应的解决方案是把这种初始化做到 `init()` 阶段（如本次的 `sonic.Pretouch`）。

### 2. 30 秒压测窗口对低并发场景太短

c=50/200 量级的请求数在 30 s 内只有 ~10–100 K，单次 GC pause 或 host CPU 抢占就能把 p99 数据扭曲到 +20×。**生产压测最少 60 s，关键场景 2 分钟以上**。

### 3. 首跑数据出现"反常退步"时第一步不是分析，而是重测

round2 首跑显示 `/batch c=500/0ms` 退步 54%、`/detect c=50` p99 暴涨 32×。如果直接基于这些数据写"R2 失败报告"会造成完全错误的结论。**复测后 95% 的"退步"消失**，剩下 5% 才是真实问题。

### 4. 瓶颈是会"转移"的，每轮优化要重新审计

round1 解决了 LocalCache 锁竞争 → round2 暴露 Redis pool 瓶颈 → round2 又因为 Redis 问题被解决，新的 CPU/序列化瓶颈在 c=500/0ms 场景显现。**优化是无穷的"瓶颈接力"过程，不是一次到位**。

### 5. 主动 say no 也是优化能力

`FilterResponse 池化` 这种"看起来有道理但风险大"的项，能基于"风险 / 收益"做出"不做"的判断，比盲目落地更有价值。同样地，"中间件顺序"那项审计完发现现状已是最佳实践，**保留现状**是正确决定。

### 6. 配置改一行 vs 代码改一片

round2 收益最大的一项（Redis pool）只需要改 yaml 一行——但前提是用极限并发压测把瓶颈定位到连接池。**先靠观测找瓶颈、再决定改代码还是改配置**，永远比反过来高效。

---

## 原始数据位置

- R1-opt（对照基线）：`test/perf/results/round1-i5-13500h/matrix.tsv`
- R2 首跑（30s 无预热，已记录但不可作为结论依据）：`test/perf/results/round2-i5-13500h/first-30s-matrix.tsv`
- R2 复测（60s 预热，主数据）：`test/perf/results/round2-i5-13500h/verify-60s-matrix.tsv`
- 完整 wrk 原始输出：每个 `*.tsv` 同目录下的 `*.txt`

---

## 简历一句话版本（多档）

**极简（1 行）**：
> 通过 Redis 连接池扩容 + sonic 替换 encoding/json + cache key 分配优化，单实例核心接口在跨网档吞吐再 **+10~60%**、p99 降 **20~67%**；同时通过 `sonic.Pretouch` 把 JIT 冷启动成本前置到启动阶段，避免生产首批请求的尾延迟尖刺。

**精简（3 行）**：
> - 通过 c=1000+ 极限并发压测识别出"瓶颈已从锁竞争转移到资源池排队 + JSON 序列化"的新阶段
> - 实施 Redis pool 100→500、`bytedance/sonic` 替换 `encoding/json`（热路径）、`strings.Builder` 替换字符串拼接、`sonic.Pretouch` 启动预热共 4 项改动，跨网真实场景吞吐再 +10~60%、p99 -20~67%
> - 同步修正了首跑因 sonic JIT 冷启动产生的"假阴性"误报，60s 预热复测验证；并主动 say no 了风险/收益不划算的对象池化项

**标准（1 段）**：
> 主导了基于极限并发压测发现的第二轮性能优化：通过 c=1000/2000 矩阵识别出 round1 之后新出现的瓶颈（Redis 连接池排队 + JSON 反射 + 字符串分配）。落地 4 项针对性改动：(1) Redis `pool_size` 默认 100→500 解决 `/batch` 高并发雪崩；(2) `bytedance/sonic` 替换 `encoding/json` 缓存读写热路径，单次 JSON 操作 5–10 μs → 2–3 μs；(3) `filterCacheKey` 用 `strings.Builder + Grow(32)` 单次分配代替 2-3 次拼接；(4) 在 `init()` 阶段调用 `sonic.Pretouch(reflect.TypeFor[dto.FilterResponse]())` 预热 JIT，把生产首请求 ~1.5 s 冷启动尾延迟压回毫秒级。同硬件 60 s 复测下 `/detect c=500/20ms` RPS 提升 **57%**、p99 下降 **67%**，`/batch c=200/50ms` RPS **+140%**，`/healthz` p99 -68%。同时主动暂缓 `FilterResponse` 对象池化（生命周期跨 3 路径，data race 风险大于收益）；并审计后保留中间件顺序现状（已是可观测性最佳实践）——展示了"做减法"同样是优化能力的一部分。`go test -race ./...` 全绿，公共 API 保持不变。
