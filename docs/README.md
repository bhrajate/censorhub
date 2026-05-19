# CensorHub 文档索引

按主题分类的项目文档目录。每篇均为独立产物，可单独阅读。

## 推荐阅读顺序

第一次接触本项目，建议按下面顺序读：

1. [`architecture/project-overview.md`](./architecture/project-overview.md) — 项目目标、模块边界、数据流
2. [`architecture/architecture.md`](./architecture/architecture.md) — 分层与组件细节
3. [`performance/round1-highlights-2026-05-12.md`](./performance/round1-highlights-2026-05-12.md) — 性能优化亮点（推荐用作技术展示首选）
4. [`performance/reports/baseline-i5-13500h-2026-05-12.md`](./performance/reports/baseline-i5-13500h-2026-05-12.md) — 性能基线（首跑、未优化）
5. 按需翻 `analysis/`、`performance/optimizations/` 等专题

## 目录

```
docs/
├── README.md                    # 本文件（索引）
├── architecture/                # 项目概览
├── reference/                   # 接口/配置参考手册
├── analysis/                    # 关键技术专题分析
├── performance/                 # 性能测试与优化
│   ├── reports/                 #   不同硬件/时点的完整压测报告
│   ├── optimizations/           #   优化清单与变更记录
│   └── round*-highlights-*.md   #   各轮亮点汇总（面向技术展示）
├── fixes/                       # 重大问题修复与变更记录
└── interview/                   # 求职相关（简历/面试稿）
```

## 各分类详情

### architecture/ — 项目概览

| 文档 | 一句话 |
|---|---|
| [`project-overview.md`](./architecture/project-overview.md) | 业务背景、目标、整体功能矩阵、技术选型理由 |
| [`architecture.md`](./architecture/architecture.md) | Clean Architecture 四层划分、组件关系、依赖方向 |
| [`TODO.md`](./architecture/TODO.md) | 待办事项清单 |

### reference/ — 接口/配置参考手册

| 文档 | 一句话 |
|---|---|
| [`api-reference.md`](./reference/api-reference.md) | 全量接口文档：HTTP / gRPC / 运维探针，含参数、返回结构、错误码、示例 |

### analysis/ — 关键技术专题

| 文档 | 一句话 |
|---|---|
| [`cache-consistency.md`](./analysis/cache-consistency.md) | L1+L2 多级缓存与 MySQL 主存的一致性策略 |
| [`circuit-breaker.md`](./analysis/circuit-breaker.md) | 三态熔断器（closed/open/half-open）的设计原理与状态机 |
| [`redis-pubsub.md`](./analysis/redis-pubsub.md) | ⚠️ **已废弃**：原跨实例热更新广播方案。新方案见 [`fixes/hot-update-poll-refactor-2026-05-18.md`](./fixes/hot-update-poll-refactor-2026-05-18.md) |

### performance/ — 性能测试与优化

#### performance/reports/ — 压测报告

文件名格式：`<阶段>-<硬件>-<日期>.md`，按"轮次 × 硬件"独立成文。

| 文档 | 一句话 |
|---|---|
| [`baseline-i5-13500h-2026-05-12.md`](./performance/reports/baseline-i5-13500h-2026-05-12.md) | i5-13500H/WSL2 上的首跑基线（未优化），含跨网延迟矩阵 + 生产就绪评估 |
| [`round1-i5-13500h-2026-05-12.md`](./performance/reports/round1-i5-13500h-2026-05-12.md) | i5-13500H/WSL2 上 round1（6 项锁优化）的回归 + c=1000/2000 极限并发 |
| [`round2-i5-13500h-2026-05-15.md`](./performance/reports/round2-i5-13500h-2026-05-15.md) | i5-13500H/WSL2 上 round2（sonic + Redis pool）的 60s 复测验证（修正首跑误报） |
| [`round1-ultra9-185h-2026-05-13.md`](./performance/reports/round1-ultra9-185h-2026-05-13.md) | Ultra 9 185H 新硬件上对 round1（commit `984a014`）版本的复测，含 c=1000/2000/5000 极限并发 |

#### performance/optimizations/ — 优化清单与变更

文件名前缀 `round0/1/2` 标识轮次：

- **round0**（2026-04）：项目从 demo 走向生产化的首批改造（架构/可观测/限流/熔断等基础工程）
- **round1**（2026-05-12）：基于压测发现的 6 项锁优化（LocalCache 分片 / sync.Map / atomic / worker pool / AC 切片预分配 / Tracing 短路）
- **round2**（2026-05-13）：基于 c=1000+ 极限并发数据的针对性优化（Redis pool / sonic JSON / cache key 拼接 / sonic.Pretouch 预热）

| 文档 | 一句话 |
|---|---|
| [`round0-audit-2026-04-16.md`](./performance/optimizations/round0-audit-2026-04-16.md) | 生产化前的全量审计（架构、错误处理、可观测性、安全、测试覆盖率） |
| [`round0-checklist-production.md`](./performance/optimizations/round0-checklist-production.md) | 面向生产部署的检查清单（健康检查、优雅关停、配置环境化等） |
| [`round0-changelog.md`](./performance/optimizations/round0-changelog.md) | round0 优化的逐项落地变更记录 |
| [`round1-backlog-2026-05-12.md`](./performance/optimizations/round1-backlog-2026-05-12.md) | round1 锁优化的 backlog 与执行记录 |
| [`round2-backlog-2026-05-13.md`](./performance/optimizations/round2-backlog-2026-05-13.md) | round2 优化的 backlog + 60s 复测验证 |

#### performance/round*-highlights-*.md — 亮点汇总

| 文档 | 一句话 |
|---|---|
| [`round1-highlights-2026-05-12.md`](./performance/round1-highlights-2026-05-12.md) | round1 6 项锁优化的亮点叙事（含数据表与方法论），适合技术展示与简历素材 |
| [`round2-highlights-2026-05-15.md`](./performance/round2-highlights-2026-05-15.md) | round2 4 项 Redis pool / sonic / cache key 优化的亮点（含 sonic JIT 冷启动陷阱与"主动暂缓"方法学） |

### fixes/ — 变更记录

| 文档 | 一句话 |
|---|---|
| [`hot-update-fix-2026-04-21.md`](./fixes/hot-update-fix-2026-04-21.md) | 热更新链路 race 修复记录（在原 PubSub + Debounce 方案内堵漏） |
| [`hot-update-poll-refactor-2026-05-18.md`](./fixes/hot-update-poll-refactor-2026-05-18.md) | 热更新机制重构：从「PubSub + 防抖」改为「DB 指纹轮询」，删除 mq 模块 |
| [`filter-cache-race-2026-05-18.md`](./fixes/filter-cache-race-2026-05-18.md) | Filter cache 用 engine_version 拼 key，消除 `InvalidateByPrefix(SCAN)` 在并发写入下漏清的潜伏 race |

### interview/ — 求职辅助

| 文档 | 一句话 |
|---|---|
| [`resume.md`](./interview/resume.md) | 项目在简历上的多档写法（精简 / 标准 / 详细） |
| [`interview.md`](./interview/interview.md) | 面试逐字稿（STAR 原则） |

---

## 文档命名约定

- 带日期后缀的文档（如 `reports/baseline-i5-13500h-2026-05-12.md`）：**特定时点的快照**，不会被原地更新；新一轮压测/分析另起新文件
- 不带日期的文档（如 `architecture.md`、`changelog.md`）：**持续维护**，随代码演进刷新
- 同主题多份带日期的文件（如 `reports/` 下两份），代表**不同硬件/不同时点**的独立测试，互为佐证
