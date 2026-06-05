# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

CensorHub 是基于 Aho-Corasick 多模式匹配的高性能敏感词过滤服务，REST + gRPC 双协议，Go + Clean Architecture/DDD。Module path: `github.com/bhrajate/censorhub`。

## 常用命令

```bash
make build              # 编译到 bin/censorhub（CGO_ENABLED=0，-ldflags="-s -w"）
make run-dev            # 开发模式运行（等价 make run ENV=dev）
make run ENV=staging    # 指定环境运行（dev/staging/production）
make test               # 全量测试（-race -count=1，APP_ENV=test）
make bench              # AC 算法基准测试（仅 internal/infrastructure/algorithm/...）
make coverage           # 覆盖率报告 → coverage.html
make lint               # golangci-lint（缺失时自动安装）
make proto              # 由 api/proto/censor/v1/censor.proto 重新生成 gRPC 代码
make docker-up          # docker-compose 起全套依赖（MySQL/Redis/Jaeger/Prometheus/Grafana + 应用）
```

运行单个测试：
```bash
APP_ENV=test go test ./internal/application/service/... -run TestFilterAppService_Filter -race -count=1 -v
```

二进制接受两个参数：`--config`（默认 `configs/config.yaml`）和 `--env`（覆盖 `APP_ENV` 环境变量）。

## 架构分层

依赖方向严格由外向内（Clean Architecture），`internal/` 下四层：

- `domain/` — 纯业务，零外部依赖。实体 `SensitiveWord`、值对象（Category/RiskLevel/WordStatus/FilterStrategy/FilterResult）、仓储接口 `WordRepository`、领域服务接口 `FilterEngine`。新增依赖前确认本层不引入任何基础设施/框架包。
- `application/` — 用例编排。`FilterAppService`（过滤）、`WordAppService`（词库管理 + 热更新轮询）、DTO、Assembler。
- `infrastructure/` — 接口实现。`algorithm/`（AC 自动机、过滤策略、文本归一化）、`cache/`、`persistence/mysql/`、`mq/`、`config/`、`trace/`。
- `interfaces/` — 外部接入。`http/`（Gin handler + router）、`grpc/`、`middleware/`。

依赖注入是 **手动组装**，全部在 `cmd/server/main.go` 中按 database → repository → engine → service → handler 顺序构建。Makefile 的 `wire` 目标是历史残留，**项目不使用 google wire**，无 wire 配置文件。

## 关键机制（代码已演进，README 部分描述已过时）

阅读 `docs/fixes/` 和 `docs/analysis/` 了解这些重构的来龙去脉。改动以下逻辑时务必理解其约束：

1. **多实例热更新 = DB 指纹轮询，不是 Redis Pub/Sub**。README 仍写 Pub/Sub，但实际机制是 `WordAppService.pollLoop` 定期调用 `repo.ActiveFingerprint()`，指纹三元组 `(Count, MaxID, MaxUpdatedUnixMicro)` 变化时触发 `rebuildWithRetry()` 重建 AC 自动机。每个实例独立轮询自愈，无单点投递依赖。重构记录见 `docs/fixes/hot-update-poll-refactor-2026-05-18.md`。

2. **AC 自动机无锁热更新**：`ACFilterEngine` 用 `atomic.Value` 持有当前自动机，`Match()` 无锁 `Load()`，`Rebuild()` 在 `sync.Mutex` 下 `Store()` 新实例并 `version.Add(1)`。文件：`internal/infrastructure/algorithm/filter_engine.go`。

3. **filter cache key 含 engine_version（正确性由 key 版本号保证，不靠清缓存）**：格式 `filter:<strategy>:v<engine_version>:<text_hash>`（FNV-1a + base36）。版本号来自 `engine.Version()`，每次 Rebuild +1，旧版本 key 永不被命中。这是为了消除"Redis SCAN 前缀失效漏清"导致的 stale-cache race —— `reconcileOnce` 末尾**仍调用** `invalidatePrefix("filter:")`，但它已降级为**内存回收兜底**，不再承担正确性。改这块时：`engine.Version()` 必须在 `Match()` 之前取（见 `filter_app_service.go:100-104` 的不变式注释），且不要把正确性重新寄托到 SCAN 清缓存上。背景见 `docs/fixes/filter-cache-race-2026-05-18.md`。

4. **多级缓存**：L1 本地（TTL 5min）+ L2 Redis（TTL 30min），`MultiLevelCache` 组合。L1 未命中查 L2 并回填。

5. **文本归一化防绕过**：`algorithm/text_normalizer.go` 的 `Normalize`（NFKC + 移除零宽字符 + 全角转半角 + 小写）由 `engine.Match()` 统一调用，匹配前归一化查询文本；`NormalizeForIndex`（额外 TrimSpace）在词条入库时归一化索引文本——查询与入库走同一套规则才能命中。注意：策略层（Detect/Replace/Highlight）拿到的 `matchResult.NormalizedText` **已是归一化结果**（`filter_app_service.go:124` 传入），新增策略不要再自行 Normalize，否则双重处理会错位。

## 配置

分层加载，优先级低→高：`configs/config.yaml`（基础）→ `configs/config.{env}.yaml`（环境覆盖）→ 环境变量（前缀 `CENSORHUB_`，如 `CENSORHUB_DATABASE_DSN`）。Viper 加载逻辑在 `infrastructure/config/`。

## 测试与性能

- 单元/集成测试散落在各包内 `*_test.go`；`test/e2e/`、`test/integration/` 为端到端套件。
- `test/perf/` 是 wrk 压测矩阵：`scripts/run_matrix.sh` 跑 detect/replace/highlight/batch × 多并发/RTT 组合，结果按机器型号归档在 `results/`。`test/perf/cmd/freshness/` 用于验证热更新跨实例可见延迟。
- 压测与修复的完整分析在 `docs/performance/` 和 `docs/fixes/`，改性能相关代码前先读对应轮次报告对齐基线与目标。

## 数据模型

`sensitive_words` 表：`text`（唯一索引）、`category`（politics/porn/ad/violence/abuse/custom）、`level`（1-4 风险等级）、`status`（0 停用 / 1 启用）。GORM 自动迁移在 `infrastructure/persistence/mysql/`，初始 SQL 见 `scripts/init.sql`。
