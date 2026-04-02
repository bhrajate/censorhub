# 多级缓存与数据库的数据一致性分析

## 项目缓存架构

```
请求 → L1（进程内 LocalCache） → L2（Redis） → 数据库（MySQL）
```

- **L1**：进程内 map，TTL 过期 + 每分钟清理
- **L2**：Redis，带 TTL
- **数据库**：MySQL，作为数据权威来源

## 当前项目的一致性保障机制

### 1. 写操作时主动失效缓存

词条的增删改操作（`triggerRebuild`）采用**先写库，再删缓存**的策略：

```go
// word_app_service.go - triggerRebuild()
func (s *WordAppService) triggerRebuild(ctx context.Context) {
    // 1. 异步从 DB 全量加载并重建过滤引擎
    // 2. 通过 Redis Pub/Sub 通知其他实例重建
    // 3. 删除缓存：cache.InvalidateByPrefix(ctx, "words:")
}
```

流程：**DB 写入成功 → 异步重建引擎 → Pub/Sub 通知其他实例 → 按前缀清除缓存（L1 + L2）**

### 2. TTL 兜底

即使主动失效失败，L1 和 L2 都有 TTL 过期机制，缓存最终会自动失效，保证**最终一致性**。

### 3. 跨实例通知

通过 Redis Pub/Sub 广播 `rebuild` 消息，所有实例收到后会重建本地过滤引擎，确保多实例间的数据一致。

## 现存的一致性风险

### 风险 1：先写库再删缓存的时间窗口

```
时刻1: 请求A 写入 DB 成功
时刻2: 请求B 读到旧缓存（此时缓存尚未删除）
时刻3: 请求A 删除缓存
```

**影响**：时刻2 读到的是旧数据。但由于本项目是敏感词过滤系统，短暂读到旧词库（几毫秒级别）是可接受的。

### 风险 2：删缓存失败

`InvalidateByPrefix` 在断路器熔断时会跳过 Redis 删除，仅删除本实例的 L1：

```go
// 熔断时
func (c *MultiLevelCache) InvalidateByPrefix(ctx context.Context, prefix string) error {
    c.local.DeleteByPrefix(prefix)  // 只删了 L1
    if !c.breaker.Allow() {
        return nil  // Redis 的旧缓存没删掉
    }
    ...
}
```

**影响**：Redis 中残留旧缓存，其他实例从 Redis 读到旧数据。依赖 TTL 过期兜底。

### 风险 3：L1 回填导致缓存不一致

```
时刻1: 实例A 从 Redis 读到数据，回填到 L1
时刻2: 实例B 更新 DB 并删除 Redis 缓存
时刻3: 实例A 的 L1 仍然是旧数据（直到 TTL 过期）
```

**影响**：实例A 的 L1 在 TTL 过期前一直返回旧数据。但 `InvalidateByPrefix` 通过 Pub/Sub 触发所有实例清缓存，可以缓解此问题。

### 风险 4：异步重建引擎的时间窗口

`triggerRebuild` 中引擎重建是 goroutine 异步执行的：

```go
go func() {
    words, err := s.repo.FindAllActive(rebuildCtx)
    s.engine.Rebuild(words)
}()
```

在重建完成前，过滤请求使用的仍是旧引擎数据。

### 风险 5：缓存与引擎数据不一致

过滤结果缓存（`filter:*`）是基于引擎匹配结果生成的，但词条更新时只清除了 `words:` 前缀的缓存，`filter:` 前缀的缓存未被清除。

```go
s.cache.InvalidateByPrefix(ctx, "words:")  // ← 只清了 words: 前缀
// filter:* 的缓存可能仍返回基于旧词库的过滤结果
```

## 改进建议

### 建议 1：清除过滤结果缓存

词条变更时同时清除 `filter:` 前缀的缓存：

```go
func (s *WordAppService) triggerRebuild(ctx context.Context) {
    // ...
    s.cache.InvalidateByPrefix(ctx, "words:")
    s.cache.InvalidateByPrefix(ctx, "filter:")  // 新增
}
```

### 建议 2：Pub/Sub 重连后自动 rebuild

Pub/Sub 断线期间的通知会丢失，重连后应无条件触发一次 rebuild：

```go
func (p *RedisPubSub) runSubscription(ctx context.Context, handler func()) error {
    sub := p.client.Subscribe(ctx, WordUpdateChannel)
    defer sub.Close()

    _, err := sub.Receive(ctx)
    if err != nil {
        return err
    }

    // 重连成功后无条件 rebuild，弥补断线期间丢失的通知
    handler()

    // ... 继续监听
}
```

### 建议 3：批量更新防抖

批量导入时避免触发多次 rebuild，可以在消费端加防抖：

```go
// 收到通知后延迟 500ms 执行，期间新通知重置计时器
type Debouncer struct {
    timer    *time.Timer
    duration time.Duration
    mu       sync.Mutex
}
```

### 建议 4：熔断恢复后主动清理

断路器从 Open 恢复到 Closed 后，主动清理 Redis 中可能残留的脏缓存。

## 总结

| 机制 | 作用 | 覆盖场景 |
|------|------|----------|
| 先写库再删缓存 | 主动保证一致性 | 正常写操作 |
| TTL 过期 | 兜底最终一致 | 删缓存失败、熔断降级 |
| Pub/Sub 通知 | 跨实例同步 | 多实例部署 |
| 异步引擎重建 | 更新内存过滤引擎 | 敏感词热更新 |

当前方案提供了**最终一致性**保证，对于敏感词过滤系统来说是合理的。最需要修复的是**风险 5（filter 缓存未清除）**，这可能导致词条更新后过滤结果仍然是旧的。
