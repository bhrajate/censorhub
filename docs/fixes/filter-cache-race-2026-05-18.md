# Filter Cache 失效 Race 修复:用 engine_version 给 cache key 分版本（2026-05-18）

## 摘要

修复一个**早就存在但从未被发现**的 race：词条变更后，filter 接口的 cache 里残留旧匹配结果，导致已经在引擎里的新词在客户端"看不见"，最长可持续到 L1 TTL（5 分钟）才自愈。

修复手段：把 `engine.Version()` 拼进 filter cache key（`filter:<strategy>:v<version>:<text_hash>`）。引擎每次 Rebuild 时版本号自增，旧 cache key 自然失效，**不再依赖 `InvalidateByPrefix(SCAN)` 的并发可见性保证**。

实测：高频写入场景下生效延迟超时率从 6-9% 降到 **0%**。

---

## 一、为什么会发现这个问题

本次重构（[hot-update-poll-refactor-2026-05-18.md](./hot-update-poll-refactor-2026-05-18.md)）落地后，跑了一个新写的 `freshness` 压测工具来验证生效延迟分布：

```
流程：写入新词 → 立刻每 25ms 调一次 detect → 直到结果命中
重复 100 轮，统计延迟分布
```

预期 p99 ≤ pollInterval + jitter ≈ 750ms。

实测结果：
- p50 / p99：565ms / 740ms（**完全符合预期**）
- 100 轮里 6-9 轮 **5s 仍未命中**（timeout）

5s 远大于理论最大值 750ms，多个 poll tick 都过去了仍然没命中——说明**不是改造的 poll 机制慢**，而是**别的地方把客户端关在门外**了。

---

## 二、根因分析

### 2.1 排查 — 词条到底进引擎了没

对超时的 3 个词条（round=42/66/68，word_id=10377/10401/10403）做了三处对照：

| 检查 | 结果 |
|---|---|
| DB 中是否存在 | ✅ 三条都在，status=active |
| 直接 curl detect 是否命中 | ✅ 三条都 `is_hit: true, hit_count: 2` |
| server log `engine rebuilt` 计数 | ✅ 100 次（与 100 次写入 1:1） |

**结论**：词进了引擎，但 freshness 工具的 detect 请求一直返回 "not hit"。**问题出在 cache 层**。

### 2.2 翻代码 — Filter cache key 怎么写的

```go
// 修复前：internal/application/service/filter_app_service.go
func filterCacheKey(text string, strategy string) string {
    h := fnv.New64a()
    h.Write([]byte(text))
    return "filter:" + strategy + ":" + base36(h.Sum64())
}
```

key 的两个维度：`strategy`（detect/replace/highlight）+ `text 的 FNV64a 哈希`。**没有任何"版本号"维度**。

也就是说：同一段文本在引擎重建前后产生的 cache key **完全一样**。如果重建前的"miss 结果"被写入 cache，重建后查同一段文本会直接读到旧 miss 结果。

### 2.3 为什么旧结果会留下来

reconcile 流程（修复前）：

```
1. ActiveFingerprint() → 发现指纹变了
2. FindAllActive() + engine.Rebuild() → 引擎更新到新词库
3. invalidatePrefix(ctx, "filter:")  ← 这步本应清掉所有旧 cache
4. 完成，下次 detect 走新 cache
```

第 3 步 `invalidatePrefix("filter:")` 底层用 **Redis `SCAN`**：

```go
// internal/infrastructure/cache/redis_cache.go
func (c *RedisCache) DeleteByPrefix(ctx, prefix) error {
    var cursor uint64
    for {
        keys, nextCursor, _ := c.client.Scan(ctx, cursor, prefix+"*", 1000).Result()
        // UNLINK 这一批 keys
        cursor = nextCursor
        if cursor == 0 { break }
    }
}
```

**Redis `SCAN` 的语义是"尽力而为的快照"**：
- "SCAN 开始之前就存在、且 SCAN 期间没被删除"的 key 一定能被看到
- "SCAN 期间新写入"的 key —— **看运气**：写入到已扫过的 bucket 就漏掉，写入到未扫的 bucket 就能看到

这是 Redis 的设计取舍（为了保持 O(1) 内存 + 不阻塞），不是 bug。`KEYS prefix*` 能拿到完整快照，但会阻塞整个 Redis，生产环境禁用。

### 2.4 race 的完整时间轴

注意代码实际顺序：`reconcileOnce` 里 SCAN（`invalidatePrefix("filter:")`）**永远在 Rebuild 之后**才启动——
`rebuildWithRetry` 返回后才走到 `invalidatePrefix`。所以 race 不是"SCAN 先于 Rebuild"，
而是**某个 detect 请求横跨了"Rebuild 切引擎"和"SCAN 经过对应 bucket"两个时刻**：
`Match` 用的是切换前的 `oldAC`（拿到 "not hit"），但 `cache.Set` 在 SCAN 已经扫过该 bucket 之后才落盘。

```
T0:     poll 进入 rebuildWithRetry, FindAllActive 开始

T0+1ms: client A 发起 POST /api/v1/filter/detect {"text": "...word_N..."}
        s.engine.Version() 取版本号(无关紧要,旧版本下也是同一份代码路径)
        L1 miss → L2 miss
        engine.Match():
          ac := e.current.Load()  ← 拿到 oldAC 引用,后续 Match 始终在 oldAC 上跑
                                    (atomic.Value Load 一次后引用不变)
          → 返回 "not hit"(oldAC 不含 word_N)
        ← 此时 cache.Set 还没发出: 还要走 sonic.Marshal + Redis pipeline,
           几十~几百 μs 在路上

T0+5ms: poll: engine.Rebuild 内部 e.current.Store(newAC) —— 引擎切换完成

T0+8ms: poll: rebuildWithRetry 返回, lastFingerprint 更新

T0+9ms: poll: invalidatePrefix("filter:") → SCAN cursor=0 启动

T0+10ms: SCAN 已经扫过 hash(word_N) 落入的 bucket
         (此刻该 bucket 里还没有 client A 的 key, SCAN 看不到)

T0+12ms: client A 终于把 "not hit" 写到 L1 + L2
         key = filter:detect:v<旧>:hash(word_N)
         L2 落到的 bucket 已被 SCAN 扫过,本批 UNLINK 看不见这个 key

T0+15ms: SCAN cursor=0 闭环, UNLINK 一批 key, 但漏掉了 client A 刚写的那条

T0+15ms ~ T0+5min:
        所有 client 查 word_N
        L1 hit! 拿到 T0+12ms 写入的 stale "not hit"
        返回 "未命中" —— 但 newAC 实际能命中

T0+5min: L1 TTL 过期, stale 终于消失
        client 这时再查才会重新走引擎, 得到正确结果
```

触发条件的实质是两个子窗口同时命中：

1. **Match 子窗口**:`e.current.Load()` 拿到 oldAC —— 必须在 `Rebuild` 内部 `Store(newAC)` 之前
2. **Set 子窗口**:`cache.Set` 落盘到对应 Redis bucket —— 必须在 SCAN 经过该 bucket 之后

中间穿插着 `Match → Apply → Marshal → Redis Set` 几十~几百 μs 的路径,足以让一个请求
"开头看到旧引擎,结尾错过 SCAN"。

### 2.5 这个 bug 不是本次 poll 改造引入的

本质上，**只要存在以下三个条件,这个 race 就成立**：

1. cache key 不区分 engine 版本
2. cache 失效用 SCAN 类工具（无法看到 SCAN 期间的写入）
3. 引擎 Rebuild 期间允许并发的 detect 请求

旧 PubSub + Debounce 方案下三个条件**全部满足**，所以同样有这个 race。但当时没人写"写后立即查"的压测，所以从未被发现。

这次重构因为有 `freshness` 工具来量化"生效延迟"，顺带把这个潜伏 bug 照了出来。

---

## 三、为什么 race 触发率是 6-9%

不是必现，但触发概率不低，原因：

- 每轮 freshness 在 word_N 写入后立刻发起 detect（间隔 100ms）
- pollLoop 周期 500-750ms（含 jitter），所以约 5-7 次 detect 落在 Rebuild 完成之前
- 这 5-7 次中只要**任意一次**满足"Match 用 oldAC 且 Set 晚于 SCAN 经过对应 bucket"，
  就会把 "not hit" 旧 cache 残留下来
- 一旦 cache 被 poison，后续所有同 text 的 detect 都 hit 那个旧记录直到 TTL 过期

实际触发率 6-9% 与两段窗口的总时长有关：
- Rebuild 期间窗口（Match 拿 oldAC）：本机 1 万词条 reconcile ~150ms，FindAllActive + 建 AC 占大头
- Set 晚于 SCAN 的窗口：SCAN 跑完 filter:* 一般 < 10ms，Set 路径上的 Marshal + Redis 往返
  让单次请求"开头早 / 结尾晚"是常见情况

两窗口叠加下,9% 的 detect 撞上是合理的。reconcile 越快、cache key 越少,window 越小,race 越罕见——
但只要这两个子窗口存在,就永远有概率触发,这才是必须从"清 cache"换成"换 cache key"的根本原因。

---

## 四、修复方案：cache key 携带 engine 版本

### 4.1 思路

```
旧：filter:<strategy>:<text_hash>
新：filter:<strategy>:v<engine_version>:<text_hash>
```

引擎每次 Rebuild 后版本号自增。新请求构造的 cache key 里 `version` 已经是新值，**永远读不到旧版本写入的 cache**。旧 key 跟着 L1/L2 TTL 自然过期，被动回收内存。

### 4.2 为什么这个方案是对的

| 性质 | 旧（SCAN 失效） | 新（version 路由） |
|---|---|---|
| 一致性保证 | "尽力而为"，依赖 SCAN 投递 | **确定性**，靠 atomic 单调递增 |
| race 窗口 | 整个 SCAN + Rebuild 期间 | **不存在** |
| L1 / L2 之间的同步 | 都要清，都可能漏 | 不需要清也不会读错（key 不一样） |
| 多实例 cluster 一致性 | 依赖 PubSub 广播失效（已下线） | 每实例 `engine.Version()` 各自演化，各自版本下的 cache 互不干扰 |
| Rebuild 期间的旧 cache 命运 | 期望 SCAN 一次清完 | TTL 自然过期或被 SCAN 兜底回收 |

### 4.3 关键代码

**领域接口加 `Version()`**（`internal/domain/service/filter_engine.go`）:

```go
type FilterEngine interface {
    Match(text string) MatchResult
    Rebuild(words []*entity.SensitiveWord) error
    WordCount() int
    // Version 返回引擎当前版本号；每次 Rebuild 成功必然递增。
    // 上层用它给 filter cache key 加版本号,引擎一更新所有旧 cache 自然失效,
    // 避免 InvalidateByPrefix(SCAN) 在并发写入下漏清的 race。
    Version() uint64
}
```

**实现用 `atomic.Uint64`**（`internal/infrastructure/algorithm/filter_engine.go`）:

```go
type ACFilterEngine struct {
    current atomic.Value  // *AhoCorasick
    version atomic.Uint64 // 单调递增,Rebuild 成功后 +1
    mu      sync.Mutex
}

func (e *ACFilterEngine) Rebuild(words []*entity.SensitiveWord) error {
    e.mu.Lock()
    defer e.mu.Unlock()

    newAC := NewAhoCorasick(buildEntries(words))
    e.current.Store(newAC) // 原子替换
    e.version.Add(1)        // 版本号 +1
    return nil
}

func (e *ACFilterEngine) Version() uint64 {
    return e.version.Load()
}
```

**cache key 拼版本号**（`internal/application/service/filter_app_service.go`）:

```go
// key 格式: filter:<strategy>:v<engine_version>:<text_hash>
func filterCacheKey(text string, strategy string, engineVersion uint64) string {
    h := fnv.New64a()
    h.Write([]byte(text))

    var b strings.Builder
    b.Grow(48)
    b.WriteString("filter:")
    b.WriteString(strategy)
    b.WriteString(":v")
    b.WriteString(strconv.FormatUint(engineVersion, 36))
    b.WriteByte(':')
    b.WriteString(strconv.FormatUint(h.Sum64(), 36))
    return b.String()
}

func (s *FilterAppService) Filter(ctx, req) (*FilterResponse, error) {
    // 注:engine.Version() 必须在 Match 之前先取,且 Match 在同一调用栈里复用同一引擎引用
    // (atomic.Value Load 一次后引用不变), 这样保证 cache key 标的版本号 = Match 实际用的引擎版本
    engineVersion := s.engine.Version()
    cacheKey := filterCacheKey(req.Text, string(strategyType), engineVersion)
    // ... L1/L2 查询、Match、回写 cache ...
}
```

### 4.4 一个微妙的不变式

`Version()` 取的版本号**必须**等于本次 `Match()` 实际使用的引擎版本。否则可能写入"标版本 N、实际算 N+1"的 cache，下次按 N 查会拿到。

实现上靠两个细节保证：
1. `engine.Version()` 在 `Match()` **之前**调用（顺序保证）
2. `ACFilterEngine` 用 `atomic.Value` 持有当前 `*AhoCorasick`，`Match` 内部 `Load` 一次后引用不变（即使期间 Rebuild 也不会切走）

实际场景下唯一可能"标错版本"的窗口：`Version()` 取到 N → 在 `Match` 之前 Rebuild 切到 N+1 → `Match` 拿到 N+1。这种情况下 key 标 N，但结果是 N+1 的——**不会读错（version 已经过期）**，只是这一条 cache 浪费了，下次同样的 text 会重新算。无正确性影响。

### 4.5 `invalidatePrefix("filter:")` 是否还需要

**保留作为内存回收保险绳**：

- 正确性上：cache key 已自带版本号，旧 key 永远不会被命中，不清也不会读错
- 内存上：不清的话旧 key 要等 L1 5min / L2 30min TTL 过期才回收。Redis 大量旧 key 占用内存，本机 OK，集群规模大时成本可观

所以 reconcile 末尾仍然调用 `invalidatePrefix("filter:")`，只是它**不再是正确性必须**了——SCAN 漏掉的 key 不会造成 race，只是延迟回收。

---

## 五、验证

### 5.1 实测对比

| 场景 | Successful | Timeouts | p50 | p99 | max |
|---|---|---|---|---|---|
| **修复前** 100 轮 单线程 gap=100ms timeout=5s | 91-94 | **6-9 (6-9%)** | 565ms | 741ms | 741ms |
| **修复后** 100 轮 单线程 gap=100ms timeout=5s | **100** | **0** | 590ms | 740ms | 740ms |
| **修复后** 200 轮 parallel=4 gap=50ms timeout=5s | **200** | **0** | 660ms | 806ms | 807ms |

注意：
- p99 几乎不变（740ms）——本来就是 `pollInterval(500ms) + jitter(0~250ms) + reconcile(~几十 ms)` 物理上限决定，与 cache race 无关
- 修复后 min 从 188ms 上升到 458ms ——修复前的 188ms 其实是"运气好"的伪值（碰上 reconcile 已完成 + cache 还没被 poison）；修复后 min 真实反映 pollInterval 的下界

### 5.2 单元测试

`TestFilterAppService_CacheKeyChangesAfterRebuild`：
- 引擎首次 Rebuild → 版本号 v1，记录同一段文本的 cache key key1
- 再次 Rebuild → 版本号必然 v1+1
- 同一段文本生成的 cache key key2 必须 ≠ key1

通过即证明"Rebuild 后旧 cache 永远查不到"。

---

## 六、影响范围与兼容性

### 6.1 改动文件

| 文件 | 改动 |
|---|---|
| `internal/domain/service/filter_engine.go` | 接口加 `Version() uint64` |
| `internal/infrastructure/algorithm/filter_engine.go` | 实现 `version atomic.Uint64`，Rebuild 后 +1 |
| `internal/application/service/filter_app_service.go` | `filterCacheKey` 增加 `engineVersion` 参数；`Filter()` 调用前先取 `engine.Version()` |
| `internal/application/service/filter_app_service_test.go` | 新增 `TestFilterAppService_CacheKeyChangesAfterRebuild` |
| `internal/application/service/word_app_service_test.go` | `fakeFilterEngine` 实现 `Version()`，Rebuild 成功后 +1 |

### 6.2 部署兼容性

- **滚动升级期间**：新旧版本可以同时跑
  - 旧版本写的 cache key = `filter:detect:<hash>`
  - 新版本写的 cache key = `filter:detect:v3:<hash>`
  - 新版本只读自己的 key，旧版本只读自己的 key，**互不干扰**
- **升级完成后**：旧 key 30min 内被 L2 TTL 自然回收，无需手动清理

### 6.3 性能影响

filter 接口路径的额外开销：
- 一次 `atomic.Uint64.Load()` ≈ 1ns
- cache key 多了 `:v<base36>` ≈ 多 3-15 字节，FNV 哈希不参与计算所以无影响
- L1/L2 命中率：不变（version 在稳态期间不变，命中行为与之前一致）

可以忽略。

---

## 七、未做的事

以下相关问题**有意没做**：

1. **没有改 `InvalidateByPrefix` 用 Lua 原子化**：需要 LuaScript + KEYS pattern 替代 SCAN，会阻塞 Redis；当前 cache key 加版本后已经不需要这个保证，没必要折腾。

2. **没有引入 cache 层"epoch counter"**：另一种思路是给整个 cache 设全局 epoch，每次 invalidation 自增；但 epoch 仍然需要广播给所有读 cache 的代码路径，不如 `engine.Version()` 直接。

3. **没有跨实例同步 `engine.Version()`**：每个实例自己 atomic counter 即可——它们各自指向**各自的引擎状态**，不需要数值一致。一个实例 v=42 写的 cache，另一个实例 v=42 读到也只是巧合相等，但因为对应同一份 DB 词库（poll 模型保证），结果也是正确的。

---

## 涉及文件

**改动**：
- `internal/domain/service/filter_engine.go`
- `internal/infrastructure/algorithm/filter_engine.go`
- `internal/application/service/filter_app_service.go`
- `internal/application/service/filter_app_service_test.go`
- `internal/application/service/word_app_service_test.go`（fakeFilterEngine 实现 Version）

---

## 相关文档

- [`hot-update-poll-refactor-2026-05-18.md`](./hot-update-poll-refactor-2026-05-18.md)：本次 poll 重构主文档；本 race 是配合 poll 改造跑压测时发现的
- [`../analysis/cache-consistency.md`](../analysis/cache-consistency.md)：L1+L2 多级缓存与 MySQL 主存的一致性策略
- [`hot-update-fix-2026-04-21.md`](./hot-update-fix-2026-04-21.md)：4 月份对热更新链路的几处修复（不涉及本 race）
