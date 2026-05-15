# CensorHub 热更新问题修复记录（2026-04-21）

## 背景

本次修复聚焦敏感词热更新链路：

1. 词库变更后触发 `triggerRebuild`
2. 本地实例防抖重建 AC 自动机
3. 通过 Redis Pub/Sub 通知其他实例重建

排查过程中确认，这条链路存在缓存一致性和重复重建两个问题。

---

## 1. `filter:` 缓存清理时机错误

### 问题

原实现中，`triggerRebuild` 一开始就清理 `filter:` 缓存，然后才进入防抖等待，最终异步执行自动机重建。

这会产生一个窗口：

1. `filter:` 缓存被清空
2. 新自动机尚未构建完成，线上仍在使用旧自动机
3. 新请求发生缓存未命中，回退到旧自动机匹配
4. 旧匹配结果再次写回 `filter:` 缓存

结果是：即使本地实例已经进入“准备热更新”状态，仍可能把旧结果重新灌回缓存，直到 TTL 过期才恢复一致。

### 修复方案

调整顺序为：

1. 立即失效 `words:` 前缀
2. 防抖后执行自动机重建
3. 重建成功后再失效 `filter:` 前缀
4. 再发布重建通知

### 落地位置

- `internal/application/service/word_app_service.go`

---

## 2. 其他实例收到重建通知后未清理 `filter:` 缓存

### 问题

原实现中，远端实例收到 Pub/Sub 通知后只执行：

1. 从 DB 读取最新词条
2. 重建本地 AC 自动机

但没有清理本地 `filter:` 缓存。

结果是：即使远端实例的自动机已经更新，旧的过滤结果缓存仍可能继续命中，造成“引擎已新、结果仍旧”的不一致。

### 修复方案

远端实例在收到重建通知并成功完成 `engine.Rebuild(...)` 之后，立即清理本地 `filter:` 缓存。

### 落地位置

- `cmd/server/main.go`

---

## 3. 实例会收到自己发布的重建通知

### 问题

原实现中，每个实例既是发布者也是订阅者，Pub/Sub 消息内容只有固定字符串 `"rebuild"`，没有来源标识，也没有去重逻辑。

因此本地实例在完成一次主动重建后：

1. 发布 `"rebuild"`
2. 自己的订阅协程也会收到这条消息
3. 再执行一次 `FindAllActive + Rebuild`

这不会形成无限循环，因为订阅回调本身不会再次发布消息；但会造成一次额外的 DB 读取和自动机构建，纯属重复开销。

### 修复方案

将 Pub/Sub 消息改为结构化 payload，携带：

- `type`
- `source`

每个实例启动时生成自己的 `instanceID`。订阅端收到消息后：

1. 若是旧格式 `"rebuild"`，继续兼容处理
2. 若是新格式且 `source == self`，直接跳过
3. 若是其他实例发出的重建通知，则正常处理

### 落地位置

- `internal/infrastructure/mq/redis_pubsub.go`

---

## 4. `words:` 前缀目前仍保留，但属于预留约定

### 现状

当前项目没有启用 `words:*` 的读缓存链路，代码里保留 `InvalidateByPrefix("words:")` 主要是为了保留词条管理缓存前缀的约定。

因此：

- `words:` 失效逻辑当前不会影响正确性
- 但它不是当前过滤读路径一致性的关键点

### 处理方式

本次没有删除该逻辑，而是在代码中补充注释，明确说明：

- 当前未启用 `words:*` 读缓存
- 保留该前缀是为了后续扩展时沿用统一约定

### 落地位置

- `internal/application/service/word_app_service.go`

---

## 本次修复后的热更新顺序

### 本地实例

1. 数据库写入成功
2. 立即失效 `words:`
3. 进入防抖窗口
4. 加载最新活跃词条
5. 重建本地 AC 自动机
6. 失效本地 `filter:`
7. 发布带 `source` 的重建通知

### 远端实例

1. 收到重建通知
2. 若消息来源是自己，直接跳过
3. 否则加载最新活跃词条
4. 重建本地 AC 自动机
5. 失效本地 `filter:`

---

## 回归测试

本次新增了两类回归测试：

1. `WordAppService` 重建顺序测试
   - 验证 `filter:` 失效发生在 `engine.Rebuild(...)` 成功之后
   - 验证发布通知发生在 `filter:` 失效之后

2. `RedisPubSub` 自消息过滤测试
   - 验证兼容旧格式 `"rebuild"`
   - 验证会跳过 `source == self` 的消息
   - 验证仍会处理来自其他实例的消息

---

## 涉及文件

- `internal/application/service/word_app_service.go`
- `internal/application/service/word_app_service_test.go`
- `internal/infrastructure/mq/redis_pubsub.go`
- `internal/infrastructure/mq/redis_pubsub_test.go`
- `cmd/server/main.go`
