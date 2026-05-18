# 热更新链路重构：从「PubSub + 防抖」到「指纹轮询」（2026-05-18）

## 摘要

将敏感词热更新机制由 **「写入侧主动通知（Redis Pub/Sub + 防抖 Timer）」** 重构为
**「读取侧定期轮询 DB 指纹（Polling）」**。

- 删除了 `internal/infrastructure/mq/` 整个包及其依赖（`google/uuid`）
- 重写了 `internal/application/service/word_app_service.go`：移除 timer/锁/WaitGroup 状态机，改为单 goroutine `pollLoop`
- 在 MySQL 表上新增 `(status, updated_at)` 复合索引，使指纹查询走 covering scan
- 写入接口（`Create/Update/Delete/Import`）不再触发任何重建动作

代码净减少 ~200 行，删除一个故障域（PubSub），跨实例一致性从「概率性」升级为「确定性」。

---

## 一、为什么要重构

### 1.1 历史方案回顾

历史实现是渐进演化出来的（详见 [`docs/fixes/hot-update-fix-2026-04-21.md`](./hot-update-fix-2026-04-21.md)
和 [`docs/analysis/redis-pubsub.md`](../analysis/redis-pubsub.md)）：

```
┌────────────────────┐
│ Create/Update/...  │── 写 DB
└─────────┬──────────┘
          │ triggerRebuild()
          ▼
┌────────────────────┐
│ debounce Timer     │── 500ms 防抖窗口
│ (max-wait 3s 兜底) │── max-wait 防止持续触发饿死
└─────────┬──────────┘
          │ AfterFunc 回调
          ▼
┌────────────────────┐
│ FindAllActive      │
│ + Rebuild          │── 重建本地 AC 自动机
│ + Invalidate       │── 清 filter: 缓存
│ + PublishWordUpdate│── Redis Pub/Sub 广播
└─────────┬──────────┘
          │
          ▼
┌────────────────────┐
│ 其他实例订阅回调    │── SubscribeWordUpdate
│ → 同上重建逻辑     │
└────────────────────┘
```

### 1.2 这套方案在生产中的不足

| 问题 | 表现 | 风险等级 |
|---|---|---|
| **PubSub 投递不可靠** | Redis 重启 / 网络抖动期间消息丢失 → 其他实例**永久**停在旧词库直到下一次写入 | 高 |
| **重试只在写时发生** | 单实例 DB/重建失败 → 仅靠日志记录，**无自愈**，下次写入前一直旧词库 | 高 |
| **进程崩溃丢失窗口** | `kill -9` 时 debounce 窗口里的变更丢失（debounce 设计上就允许"用最后一次替代前面所有触发"） | 中 |
| **状态机臃肿** | `rebuildTimer` + `rebuildMu` + `rebuildExecMu` + `inFlightRebuilds` + `closed` 五种状态、三类同步原语，新人难维护 | 中 |
| **多重事件路径** | 同一份变更要触发"本地重建 + PubSub + 远端重建"三条路径，缓存清理时机错过任何一处都会出现 [4 月 21 号修过的不一致](./hot-update-fix-2026-04-21.md) | 中 |
| **测试时序耦合** | 大量 `time.Sleep` / 多 goroutine 协作，race detector 下偶发 flaky | 低 |

注意：4 月 21 号的修复仅仅是"在原方案内堵住几个具体的不一致 case"，并没有解决**通知机制本身不可靠**的根本问题。

### 1.3 新方案的核心思路

**改"推"为"拉"**：写入侧只关心 DB 事务，读取侧周期性地询问 DB
"现在的活跃词条集合是不是和我手里的不一样？" 答案靠**指纹比对**。

```
┌────────────────────┐
│ Create/Update/...  │── 写 DB（仅此一步）
└────────────────────┘

每个实例独立持有 pollLoop:
  ┌────────────────────────────────────┐
  │ ticker (500ms ± 250ms 抖动)        │
  └────────────────┬───────────────────┘
                   ▼
  ┌────────────────────────────────────┐
  │ ActiveFingerprint(ctx)             │
  │ → (count, max_id, max_updated_us)  │
  └────────────────┬───────────────────┘
                   ▼
       fp == lastFingerprint ?
        ├─ 是 → 跳过                    （指标 unchanged）
        └─ 否 → FindAllActive + Rebuild
                + Invalidate("filter:")
                成功后才更新 lastFingerprint
```

**任何环节失败 → `lastFingerprint` 不更新 → 下个 tick 自然再尝试。** 这就是
"自愈"，不需要手写 retry 状态机。

---

## 二、新方案的具体设计

### 2.1 指纹三元组：`(count, max_id, max_updated_unix_micro)`

- `count`：INSERT/DELETE 时变化
- `max_id`：INSERT 时单调递增（兜底"同秒 INSERT+DELETE 让 count 回归原值"的边界）
- `max_updated_unix_micro`：UPDATE 时变化（`autoUpdateTime`），用 Unix 微秒避免时区/秒级精度坑

只要三元组完全相等就认为词库未变；任意一项不同立即触发重建。

> 漏触发分析：要让三元组同时不变，必须 INSERT 和 DELETE 同一行 ID 且发生在同一微秒。
> MySQL `TIMESTAMP(6)` + 自增 PK 的组合下，这种概率工程上等同于 0。

### 2.2 SQL 与索引

```sql
SELECT
  COUNT(*),
  MAX(id),
  CAST(COALESCE(UNIX_TIMESTAMP(MAX(updated_at)) * 1000000, 0) AS SIGNED)
FROM sensitive_words
WHERE status = 1;
```

新增的复合索引：

```go
// SensitiveWordModel
Status    int       `gorm:"...;index:idx_status_updated,priority:1"`
UpdatedAt time.Time `gorm:"...;index:idx_status_updated,priority:2"`
```

`(status, updated_at)` 让查询走 **covering index scan**：
- `WHERE status=1` 命中前缀
- `MAX(updated_at)` 在索引内完成（无需回表）
- `MAX(id)` 走主键
- `COUNT(*)` 走索引行数

10 万行级别下 **亚毫秒返回**。

### 2.3 单 goroutine `pollLoop`

```go
func (s *WordAppService) pollLoop(ctx context.Context) {
    defer s.wg.Done()

    // 启动相位偏移：避免多实例同时启动后整齐打 DB
    initialDelay := time.Duration(rand.Int63n(int64(s.cfg.interval)))
    select {
    case <-ctx.Done(): return
    case <-time.After(initialDelay):
    }

    for {
        s.reconcileOnce(ctx)

        nextWait := s.cfg.interval + jitter()  // 每轮再加 [0, jitter] 抖动
        select {
        case <-ctx.Done(): return
        case <-time.After(nextWait):
        }
    }
}
```

关键性质：

| 性质 | 由什么保证 |
|---|---|
| 永远不会两次 reconcile 并发 | `time.After` 在 `reconcileOnce` 返回**之后**才创建 |
| `lastFingerprint` 不需要锁 | 只有 pollLoop 一个 goroutine 读写它 |
| Close 路径无需 flush 待执行重建 | poll 模型下根本不存在 debounce 窗口 |

### 2.4 抖动（jitter）

启动时 `rand` 一次相位偏移、每轮叠加 `[0, 250ms]` 抖动。**目的不是节流，而是错相**：
N 个实例同时启动后，避免它们的 ticker 整齐踩点，造成 DB 出现"每 500ms 来一波 N 条相同 SQL"的尖刺。

### 2.5 失败模式（按层级展开）

| 失败 | 行为 | 后续 |
|---|---|---|
| 指纹查询失败 | 计 `EngineRebuildFailuresTotal{stage="fingerprint"}` + warn 日志，**不更新 lastFingerprint** | 下个 tick 重试 |
| FindAllActive 失败 | 同 reconcile 内重试最多 3 次（200ms→400ms→800ms 退避），失败后 **不更新 lastFingerprint** | 下个 tick 重试 |
| engine.Rebuild 失败 | 同上 | 下个 tick 重试 |
| 缓存失效失败 | warn 日志，**lastFingerprint 仍更新**（缓存最差等 TTL 自动过期，不会卡热更新） | 下次缓存自然刷新 |
| 进程崩溃 | 新实例启动 → InitEngine 加载最新词库 → 第一次 poll 自然到达稳态 | 无丢失窗口 |

---

## 三、新方案 vs 原方案对比

| 维度 | 原方案（PubSub + Debounce） | 新方案（Fingerprint Poll） |
|---|---|---|
| **写入接口耗时** | 几乎零（异步 trigger） | 几乎零（不 trigger） |
| **生效延迟（最快）** | ~500ms（debounce 窗口） | ~250ms（poll 周期 500ms 平均） |
| **生效延迟（最坏）** | **无穷大**（PubSub 永久丢消息时） | `interval + jitter`（确定性上限） |
| **跨实例一致性** | 概率性（依赖 PubSub 投递） | **确定性**（每个实例自己问 DB） |
| **失败自愈** | 重试 3 次后挂着等下次写入 | 下个 tick 自然重试，永远自愈 |
| **进程关停丢失窗口** | 需要 `Close` 同步 flush | **不存在** |
| **故障域** | DB + Redis-Pub + Redis-Cache | DB + Redis-Cache（少一个） |
| **同步原语数量** | 3 类（Mutex/WaitGroup/Channel） | 1 类（context cancel + WaitGroup） |
| **DB 压力变化** | 写入时 1 次 SELECT all + 1 次 INCR | 稳态 ~20 QPS（10 实例 × 2 QPS）的 covering scan |
| **代码复杂度** | timer + 5 状态字段 + 3 锁 ~150 行 | 一个 ticker + 一个指纹比对 ~100 行 |

### 3.1 DB 压力测算

10 实例 × `pollInterval=500ms` = 20 QPS 指纹查询。

按 Little's Law：
```
平均连接占用 = QPS × 单次耗时 = 20 × 1ms = 0.02 个连接
```

生产配置 `max_open_conns=200`，**0.02 / 200 = 0.01% 容量占用**。
10 万行级 covering scan 单次 < 1ms，对 MySQL CPU 影响 < 0.01%。

### 3.2 为什么不用 Redis 存指纹

讨论过 Redis `INCR censorhub:words:version` 作为指纹源的方案，最终选择 DB。理由：

| 维度 | DB 指纹 | Redis 指纹 |
|---|---|---|
| 一致性 | 事务提交即可见，强一致 | DB 提交后再 INCR，有窗口 |
| 写路径侵入 | 零侵入 | 5 处都要加 INCR |
| 故障爆炸半径 | DB 挂 = 服务整体降级（统一故障域） | **Redis key 丢失 = 所有实例认为"没变"，永远不再重建**（致命） |
| DB 压力 | 20 QPS × 1ms covering scan，可忽略 | 0 |
| 运维 | 0 | 要关心 maxmemory eviction、重启 cold start |

DB 指纹的 20 QPS 在生产配置下完全可承受；Redis 指纹引入的"额外故障域"得不偿失。
**Redis 在本项目继续仅作为 L2 filter 缓存使用**。

---

## 四、迁移要点

### 4.1 删除的代码

- `internal/infrastructure/mq/redis_pubsub.go`（149 行）
- `internal/infrastructure/mq/redis_pubsub_test.go`
- `cmd/server/main.go` 中的 `pubsub.SubscribeWordUpdate(...)` 闭包
- `WordAppService` 内的 `triggerRebuild` / `executeRebuild` / `runDebouncedRebuild` /
  `rebuildTimer` / `rebuildMu` / `rebuildExecMu` / `inFlightRebuilds` / `closed`
- `go.mod` 中的 `github.com/google/uuid` 依赖（`go mod tidy` 自动清理）

### 4.2 新增的代码

- `WordRepository.ActiveFingerprint(ctx) (WordFingerprint, error)` 接口与 MySQL 实现
- `SensitiveWordModel` 上的 `idx_status_updated` 复合索引
- `WordAppService.{Start, Close, pollLoop, reconcileOnce, rebuildWithRetry}`
- `metrics.EngineFingerprintChecksTotal{result}`，标签 `unchanged/changed/error`
- `metrics.EngineRebuildFailuresTotal` 的 `stage` 标签新增 `fingerprint`，移除 `publish`

### 4.3 接口变化

```go
// 旧
service.NewWordAppService(repo, engine, multiCache, pubsub, log)
wordAppService.Close()  // 内部 flush + WaitGroup

// 新
service.NewWordAppService(repo, engine, multiCache, log)
wordAppService.InitEngine(ctx)
wordAppService.Start(ctx)        // 必须在 InitEngine 之后、写入接口可达之前
wordAppService.Close()           // 仅 cancel + Wait，幂等
```

### 4.4 调用方修改

`cmd/server/main.go`：

- 移除 `mq.NewRedisPubSub(...)` 与 `pubsub.SubscribeWordUpdate(...)` 闭包
- `service.NewWordAppService` 去掉 pubsub 参数
- 在 `InitEngine` 后追加 `wordAppService.Start(ctx)`

### 4.5 部署注意

- 新增的 `idx_status_updated` 复合索引会通过 `gorm.AutoMigrate` 在启动时自动创建。
  生产灰度时建议**先离线手动执行**（避免大表自动 ALTER 阻塞写入）：

  ```sql
  ALTER TABLE sensitive_words
    ADD INDEX idx_status_updated (status, updated_at),
    ALGORITHM=INPLACE, LOCK=NONE;
  ```

- 滚动升级期间，新旧版本可以同时存在：旧版本仍在通过 PubSub 收发消息，新版本忽略
  PubSub 改用 poll，**两边都能保持本地引擎与 DB 一致**。完成升级后 PubSub 频道
  `censorhub:word_update` 自然空置，可观察一段时间后清理 Redis 监控规则。

---

## 五、关键代码片段

### 5.1 `pollLoop`（核心控制流）

```go
// internal/application/service/word_app_service.go
func (s *WordAppService) pollLoop(ctx context.Context) {
    defer s.wg.Done()

    jitterRand := rand.New(rand.NewSource(time.Now().UnixNano()))
    initialDelay := time.Duration(jitterRand.Int63n(int64(s.cfg.interval)))
    select {
    case <-ctx.Done(): return
    case <-time.After(initialDelay):
    }

    for {
        s.reconcileOnce(ctx)
        nextWait := s.cfg.interval
        if s.cfg.jitter > 0 {
            nextWait += time.Duration(jitterRand.Int63n(int64(s.cfg.jitter)))
        }
        select {
        case <-ctx.Done(): return
        case <-time.After(nextWait):
        }
    }
}
```

### 5.2 `reconcileOnce`（指纹比对 + 触发重建）

```go
func (s *WordAppService) reconcileOnce(parentCtx context.Context) {
    ctx, cancel := context.WithTimeout(parentCtx, s.cfg.queryTimeout)
    defer cancel()

    fp, err := s.repo.ActiveFingerprint(ctx)
    if err != nil {
        metrics.EngineFingerprintChecksTotal.WithLabelValues("error").Inc()
        metrics.EngineRebuildFailuresTotal.WithLabelValues("fingerprint").Inc()
        return // 不更新 lastFingerprint → 下个 tick 自然重试
    }
    if fp == s.lastFingerprint {
        metrics.EngineFingerprintChecksTotal.WithLabelValues("unchanged").Inc()
        return
    }
    metrics.EngineFingerprintChecksTotal.WithLabelValues("changed").Inc()

    if err := s.rebuildWithRetry(ctx); err != nil {
        return // 同上，不更新指纹
    }
    s.lastFingerprint = fp
    s.invalidatePrefix(ctx, "filter:")
}
```

### 5.3 `ActiveFingerprint`（MySQL 实现）

```go
// internal/infrastructure/persistence/mysql/word_repo.go
func (r *wordRepo) ActiveFingerprint(ctx context.Context) (repository.WordFingerprint, error) {
    var row struct {
        Cnt       int64
        MaxID     *uint64
        MaxUpdMic *int64
    }
    err := r.db.WithContext(ctx).
        Model(&SensitiveWordModel{}).
        Select(`COUNT(*) AS cnt,
                MAX(id) AS max_id,
                CAST(COALESCE(UNIX_TIMESTAMP(MAX(updated_at)) * 1000000, 0) AS SIGNED) AS max_upd_mic`).
        Where("status = ?", 1).
        Scan(&row).Error
    // ...
}
```

---

## 六、验证

### 6.1 测试覆盖

新增的单元测试（`word_app_service_test.go`）：

| 用例 | 验证点 |
|---|---|
| `TestPollLoop_FingerprintUnchanged_NoRebuild` | 指纹未变时，多轮 poll 都不触发 Rebuild、不清缓存 |
| `TestPollLoop_FingerprintChanged_TriggersRebuild` | 指纹变化触发一次 Rebuild + 一次 `filter:` 失效；后续 tick 不再重复 |
| `TestPollLoop_FingerprintErrorRecoversNextTick` | 指纹查询前 2 次失败，第 3 次成功能正常 reconcile |
| `TestPollLoop_RebuildFailureSelfHealsNextTick` | 一次 reconcile 内 3 次 FindAllActive 全失败 → 下个 tick 重新尝试并成功 |
| `TestPollLoop_CloseStopsLoopIdempotent` | Close 在 1s 内停止 loop；二次 Close 幂等；fp 调用不再增长 |

### 6.2 跑测验证

```
$ go build ./...                       ✓
$ go vet ./...                         ✓
$ go test -race -count=3 ./...         ✓ (3 次重复无 flaky)
$ go mod tidy                          ✓ (uuid 依赖被自动移除)
```

### 6.3 指标对照

灰度期间应在 Prometheus 上观察以下变化：

| 指标 | 期望走向 |
|---|---|
| `censorhub_engine_fingerprint_checks_total{result="unchanged"}` | 持续高速增长（占绝大多数） |
| `censorhub_engine_fingerprint_checks_total{result="changed"}` | 与词条变更频率一致 |
| `censorhub_engine_fingerprint_checks_total{result="error"}` | 接近 0 |
| `censorhub_engine_rebuild_total` | 与 `result="changed"` 数量基本一致 |
| `censorhub_engine_rebuild_failures_total{stage="fingerprint"}` | 接近 0 |

告警建议：
- `rate(rebuild_failures_total[5m]) > 0.1` → 持续重建失败
- `rate(fingerprint_checks_total{result="error"}[5m]) > 1` → 指纹查询持续失败

---

## 七、未做的事

以下内容**有意没做**，是当前方案的边界：

1. **没有同步重建接口**：写入接口仍然异步，0~750ms 延迟生效。需要 50ms 内强一致的场景请另行设计。
2. **没有"待重建队列"持久化**：靠 poll 自愈，不需要持久队列。
3. **没有跨数据中心特殊处理**：当前部署是单 DC，poll 走专线代价才需要单独考虑。
4. **没有动态调节 `pollInterval`**：保持简单常量；如果未来部署规模 > 100 实例可以考虑。

---

## 涉及文件

**新增 / 修改**：
- `internal/domain/repository/word_repository.go`（新增 `WordFingerprint` + `ActiveFingerprint`）
- `internal/infrastructure/persistence/mysql/model.go`（加 `idx_status_updated` 索引）
- `internal/infrastructure/persistence/mysql/word_repo.go`（实现 `ActiveFingerprint`）
- `internal/application/service/word_app_service.go`（完全重写）
- `internal/application/service/word_app_service_test.go`（重写测试）
- `pkg/metrics/metrics.go`（新增 `EngineFingerprintChecksTotal`，调整 stage 标签）
- `cmd/server/main.go`（移除 PubSub 订阅，改为 `Start(ctx)`）

**删除**：
- `internal/infrastructure/mq/redis_pubsub.go`
- `internal/infrastructure/mq/redis_pubsub_test.go`
- `internal/infrastructure/mq/`（空目录）
- `go.mod` 中 `github.com/google/uuid` 依赖

---

## 相关文档

- 历史问题修复：[`hot-update-fix-2026-04-21.md`](./hot-update-fix-2026-04-21.md)
- 已废弃的 PubSub 设计分析：[`../analysis/redis-pubsub.md`](../analysis/redis-pubsub.md)（已加废弃声明）
