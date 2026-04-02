# 断路器（Circuit Breaker）原理分析

## 概述

项目在 `internal/infrastructure/cache/circuit_breaker.go` 中实现了一个轻量级断路器，用于保护 Redis（L2 缓存）调用。当 Redis 不可用时快速失败并降级到本地缓存，避免大量超时请求拖垮整个服务。

## 三态流转

```
        连续失败 ≥ 5 次              超过 10s
 ┌────────┐ ──────────────→ ┌────────┐ ──────────→ ┌──────────┐
 │ Closed │                 │  Open  │              │ HalfOpen │
 │ (放行) │ ←────────────── │ (熔断) │ ←──────────  │  (探测)  │
 └────────┘  半开连续成功2次  └────────┘  探测失败1次  └──────────┘
```

### 1. Closed（关闭/正常）

所有请求正常通过 Redis。每次 Redis 调用失败，`failCount++`；成功则重置为 0。当连续失败达到阈值（5 次），转为 Open 状态。

### 2. Open（打开/熔断）

直接跳过 Redis，所有操作降级为仅使用本地缓存（L1）。熔断持续 10 秒后自动转为 HalfOpen 状态。

### 3. HalfOpen（半开/探测）

放行请求试探 Redis 是否恢复：

- 连续成功 **2 次** → 恢复到 Closed
- 失败 **1 次** → 立即回到 Open

## 配置参数

| 参数 | 值 | 含义 |
|------|-----|------|
| `failThreshold` | 5 | 连续失败多少次触发熔断 |
| `resetTimeout` | 10s | 熔断后多久尝试半开探测 |
| `halfOpenRequired` | 2 | 半开状态需要连续成功几次才恢复 |

## 在多级缓存中的应用

断路器包裹了 `MultiLevelCache`（`internal/infrastructure/cache/multi_level_cache.go`）中所有 Redis 操作：

| 操作 | 正常时 | 熔断时 |
|------|--------|--------|
| **Get** | L1 → L2 → 回填 L1 | 仅查 L1，未命中返回 `redis.Nil` |
| **Set** | 写 L1 + L2 | 仅写 L1，不报错 |
| **Invalidate** | 删 L1 + L2 | 仅删 L1 |
| **InvalidateByPrefix** | 按前缀删 L1 + L2 | 仅按前缀删 L1 |

## 设计要点

- **只对真实错误熔断**：`redis.Nil`（缓存未命中）不计入失败，只有网络/服务端错误才触发
- **降级不报错**：熔断时写操作静默降级（`return nil`），不向上层传播错误
- **线程安全**：所有状态变更在 `sync.Mutex` 保护下进行
- **零依赖**：仅依赖标准库 `sync` 和 `time`，无第三方熔断框架

## 调用流程示例（以 Get 为例）

```
请求到达 MultiLevelCache.Get()
  │
  ├─ 查 L1（本地缓存）
  │    ├─ 命中 → 直接返回
  │    └─ 未命中 → 继续
  │
  ├─ 检查 breaker.Allow()
  │    ├─ false（熔断中）→ 返回 redis.Nil
  │    └─ true → 查 Redis
  │
  ├─ 查 Redis
  │    ├─ 成功 → breaker.RecordSuccess() → 回填 L1 → 返回
  │    └─ 失败
  │         ├─ redis.Nil → 不记录失败，返回未命中
  │         └─ 其他错误 → breaker.RecordFailure() → 返回错误
```
