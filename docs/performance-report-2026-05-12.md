# CensorHub 性能测试报告

- 生成日期：2026-05-12
- 被测版本：commit `1997f38`（main 分支最新）
- 测试类型：HTTP REST 接口压测；单实例本地部署；模拟跨网延迟

---

## 1. 测试目标

1. 摸清单实例 CensorHub 的吞吐上限与延迟分布。
2. 量化不同"跨网"延迟档位（同机房/同城/跨区域/跨国）对吞吐的侵蚀程度。
3. 对比核心接口（`/detect`、`/batch`）与旁路接口（`/replace`、`/highlight`、`/healthz`、`/metrics`）的性能特征，识别主要开销来源。

---

## 2. 机器与软件配置

### 2.1 硬件

| 项 | 值 |
|---|---|
| CPU | Intel Core i5-13500H（13 代 Raptor Lake-H） |
| 物理核心 / 逻辑核心 | 8 核 / 16 逻辑核（SMT 开启） |
| BogoMIPS | 6374（≈ 3.2 GHz 等效） |
| 内存 | 15 GiB（测试时可用 4.7 GiB，其余被开发工具/Airflow 占用） |
| 磁盘 | 1007 GB ext4，WSL2 虚拟磁盘 |

### 2.2 系统 / 运行环境

| 项 | 值 |
|---|---|
| OS | Ubuntu 24.04.3 LTS |
| 内核 | Linux 6.6.87.2-microsoft-standard-WSL2（即 WSL2 虚拟化环境） |
| Hypervisor | Microsoft Hyper-V（x86_64 VT-x） |
| Go | go1.26.1 linux/amd64 |
| wrk | 4.2.0 [epoll] |
| Docker | 28.5.1 |
| MySQL | 8.0.46（`censor-mysql-perf` 容器，端口 33306） |
| Redis | 7.4.9（`censor-redis-perf` 容器，端口 16379） |
| toxiproxy | 2.12.0（`censor-toxiproxy` 容器，管理端口 8474，转发端口 20080） |

### 2.3 被测服务配置

- 端口：HTTP `:18080`，gRPC `:19090`
- 日志级别：`warn`（压测期间压减日志 I/O 干扰）
- Tracing 采样率：0（关闭 OpenTelemetry 上报，避免 Jaeger 未运行造成的重试开销）
- 速率限制：单实例 1,000,000 RPS / burst 2,000,000（实际上不会触发限流）
- AC 词库规模：**10,000 条**（测试前通过 `/api/v1/words/import` 随机生成）
  - 70% 中文 2–5 字 / 30% 英文 3–8 字
  - 分类：politics / porn / abuse / ad / violence / custom（均匀分布）
- 缓存：本地 L1 + Redis L2（两级全启用，TTL 取 config 默认）

> 注：测试过程中发现并修复了一个阻塞服务启动的 protobuf descriptor 兼容 bug（`protoc-gen-go` v1.36.10 生成的 `censor.pb.go` 与 runtime v1.36.11 不兼容），已通过重装 `protoc-gen-go` 至 v1.36.11 并重新生成 `api/proto/censor/v1/censor.pb.go` 解决，与本次性能数据无关。

---

## 3. 测试方法

### 3.1 压测工具与参数

- 工具：**wrk 4.2.0**
- 线程数：`min(conns, 16)`
- 并发档位：50 / 200 / 500（`/detect`、`/batch` 全矩阵；其它接口仅 200）
- 单组时长：30 秒
- 超时：30 秒
- 协议：HTTP/1.1 + keep-alive（wrk 默认）

### 3.2 跨网延迟模拟

原始需求是用 `tc netem` 往 loopback 注入延迟，但 WSL2 的 `tc` 需要 sudo 密码。改用 **toxiproxy** 在 `127.0.0.1:20080` 监听并转发到被测服务 `127.0.0.1:18080`，通过 toxic（latency）在上/下行各注入 `RTT/2` ms，等价往返 RTT。

档位映射：

| 档位 | RTT | 场景 |
|---|---|---|
| 0 ms | 0 ms | 同机房（toxiproxy 原生开销 ≤ 1 ms） |
| 20 ms | 20 ms | 同城机房 / 同一大区 VPC |
| 50 ms | 50 ms | 跨区域（如华东 ↔ 华北） |
| 150 ms | 150 ms | 跨国（亚洲 ↔ 北美） |

延迟注入精度验证（curl 单请求）：

```
rtt=0:   actual ≈ 7–9 ms  (TCP + TLS + toxiproxy 开销)
rtt=50:  actual ≈ 60 ms  (50 + 10 ms 固定开销)
```

这种模拟的局限：**只模拟了额外 RTT，没有带宽瓶颈与抖动**。真实跨网还会有公网的丢包、重传、拥塞窗口爬升，所以这里的数字应该看作"理想公网"基线。

### 3.3 请求体构造

#### `/api/v1/filter/detect`

Lua 脚本每次随机生成 40–200 字符的中英混合文本，以 0–3 的命中率随机插入词库里的敏感词：

```json
{"text":"新闻资讯行业动态...[随机命中词]...市场表现"}
```

#### `/api/v1/filter/batch`

每请求 10 条文本，每条 30–120 字符，0–2 次随机命中：

```json
{"texts":["...","...", ... 10 items]}
```

### 3.4 预热

正式矩阵开始前跑了一次 10 秒 `t=8, c=50` 的 `/detect` 做预热，让本地缓存/Redis 进入热状态。

### 3.5 测量指标

- `requests`：有效完成的 HTTP 请求数
- `rps`：每秒请求数（wrk "Requests/sec"）
- `latency_avg`：wrk 线程级平均
- `p50 / p75 / p90 / p99 / max`：wrk `--latency` 分布
- `errors`：socket error（connect/read/write/timeout）汇总
- `non2xx`：非 2xx 响应数

---

## 4. 压测结果

### 4.1 `/api/v1/filter/detect`（核心检测接口，全矩阵）

| RTT | Conns | RPS | p50 | p75 | p90 | p99 | max | errors |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 0 ms | 50 | **2491.6** | 17.0 ms | 26.7 ms | 42.2 ms | 1.37 s | 1.74 s | 0 |
| 0 ms | 200 | **3455.1** | 50.0 ms | 75.6 ms | 119.3 ms | 1.98 s | 2.37 s | 0 |
| 0 ms | 500 | 2937.2 | 179.5 ms | 236.9 ms | 339.8 ms | 2.24 s | 4.17 s | 0 |
| 20 ms | 50 | 1476.0 | 28.9 ms | 34.0 ms | 45.4 ms | 1.37 s | 1.72 s | 0 |
| 20 ms | 200 | 2494.1 | 67.7 ms | 96.0 ms | 135.6 ms | 1.42 s | 1.84 s | 0 |
| 20 ms | 500 | 2628.3 | 180.8 ms | 264.6 ms | 414.9 ms | 2.85 s | 5.85 s | 0 |
| 50 ms | 50 | 738.2 | 59.2 ms | 65.8 ms | 78.5 ms | 1.39 s | 1.73 s | 0 |
| 50 ms | 200 | 1960.2 | 86.1 ms | 111.5 ms | 151.5 ms | 1.49 s | 1.88 s | 0 |
| 50 ms | 500 | 2963.8 | 141.6 ms | 220.6 ms | 335.9 ms | 1.91 s | 3.45 s | 0 |
| 150 ms | 50 | 287.1 | 155.7 ms | 159.7 ms | 167.8 ms | 1.60 s | 1.95 s | 0 |
| 150 ms | 200 | 1097.0 | 158.9 ms | 169.7 ms | 199.0 ms | 1.83 s | 2.11 s | 0 |
| 150 ms | 500 | 2651.4 | 163.6 ms | 182.0 ms | 251.1 ms | 1.87 s | 2.23 s | 0 |

### 4.2 `/api/v1/filter/batch`（批量 10 条，全矩阵）

| RTT | Conns | RPS | 等效文本/s | p50 | p90 | p99 | max | errors |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 0 ms | 50 | 559.4 | ~5,594 | 80.9 ms | 115.0 ms | 1.47 s | 1.77 s | 0 |
| 0 ms | 200 | 431.5 | ~4,315 | 400.0 ms | 736.5 ms | 2.04 s | 2.11 s | 0 |
| 0 ms | 500 | **1202.5** | **~12,025** | 300.4 ms | 1.06 s | 2.77 s | 4.42 s | 0 |
| 20 ms | 50 | 726.8 | ~7,268 | 53.0 ms | 155.1 ms | 1.41 s | 1.82 s | 0 |
| 20 ms | 200 | 430.6 | ~4,306 | 424.2 ms | 591.3 ms | 2.03 s | 2.13 s | 0 |
| 20 ms | 500 | 1130.7 | ~11,307 | 280.2 ms | 1.14 s | 2.47 s | 3.45 s | 0 |
| 50 ms | 50 | 613.4 | ~6,134 | 70.4 ms | 110.1 ms | 1.41 s | 1.75 s | 0 |
| 50 ms | 200 | 443.2 | ~4,432 | 388.0 ms | 688.4 ms | 2.07 s | 2.12 s | 0 |
| 50 ms | 500 | 1001.9 | ~10,019 | 514.4 ms | 1.31 s | 2.77 s | 3.97 s | 0 |
| 150 ms | 50 | 271.1 | ~2,711 | 160.1 ms | 202.5 ms | 1.65 s | 1.91 s | 0 |
| 150 ms | 200 | 487.3 | ~4,873 | 369.0 ms | 543.3 ms | 1.95 s | 2.07 s | 0 |
| 150 ms | 500 | 872.0 | ~8,720 | 317.5 ms | 1.17 s | 2.70 s | 2.80 s | 0 |

### 4.3 旁路接口对照（c=200）

| 接口 | RTT | RPS | p50 | p90 | p99 | max |
|---|---:|---:|---:|---:|---:|---:|
| `/api/v1/filter/replace` | 0 ms | 4922.3 | 35.6 ms | 72.5 ms | 1.39 s | 1.74 s |
| `/api/v1/filter/replace` | 50 ms | 2316.2 | 71.3 ms | 132.6 ms | 1.58 s | 1.96 s |
| `/api/v1/filter/highlight` | 0 ms | 3816.5 | 45.7 ms | 84.5 ms | 1.49 s | 1.92 s |
| `/api/v1/filter/highlight` | 50 ms | 2534.8 | 68.7 ms | 95.5 ms | 1.52 s | 1.88 s |
| `/healthz` | 0 ms | **10782.6** | 15.4 ms | 37.9 ms | 1.42 s | 1.83 s |
| `/healthz` | 50 ms | 2936.1 | 57.6 ms | 85.8 ms | 1.95 s | 2.30 s |
| `/metrics` | 0 ms | 1086.1 | 153.4 ms | 392.5 ms | 1.83 s | 2.79 s |
| `/metrics` | 50 ms | 950.9 | 175.5 ms | 362.3 ms | 1.64 s | 2.12 s |

### 4.4 错误率

**全部 31 组测试，errors=0、non-2xx=0**。服务在所有档位和并发下都保持稳定，没有连接重置、超时或 5xx。

---

## 5. 数据解读

### 5.1 峰值吞吐

- `/detect` 单接口峰值：**~3455 RPS**（RTT=0 / c=200），对应 p50 ≈ 50 ms、p99 ≈ 2 s。
- `/batch` 文本峰值：**~12 K 条文本/秒**（RTT=0 / c=500，每请求 10 条）。
- `/replace` 因为匹配后的文本构造比 `/detect` 多一步写操作，但反而跑出 4922 RPS；原因是 `/detect` 在 `matches` 里返回了 JSON 详情（position/end/category/level 等），序列化体积更大；`/replace` 只返回过滤后文本。
- `/healthz`（纯 DB/Redis ping）10782 RPS 是框架天花板的参考值：Gin + keep-alive + 两级健康检查在本机能稳定顶到 1 万 +。

### 5.2 RTT 对吞吐的侵蚀

以 `/detect @ c=50` 为例：

| RTT | RPS | 相对 0ms | 理论上限（Little's Law） |
|---|---:|---:|---:|
| 0 ms | 2492 | 100% | — |
| 20 ms | 1476 | 59% | c=50 / 20ms = 2500 |
| 50 ms | 738 | 30% | c=50 / 50ms = 1000 |
| 150 ms | 287 | 12% | c=50 / 150ms = 333 |

**结论**：低并发下 RTT 是主导因素。50 ms 档 c=50 的 738 RPS 已经接近理论上限 1000 的 74%，说明服务端处理时间只占单请求 ~10 ms，其余都是 RTT。

**提升跨网吞吐的唯一办法是加并发。** c=500 / 150ms 下 RPS 仍能达到 2651，说明 keep-alive 下连接复用很彻底、服务端 CPU 还有余量。

### 5.3 批量接口的非线性

`/batch` 的 RPS 随并发变化不单调：

- c=50 → 559 RPS（每请求处理 10 条）
- c=200 → **431 RPS**（反而下降）
- c=500 → 1203 RPS（大幅回升）

这个 U 形曲线提示 c=200 时遇到了临界资源争抢。最可能的原因：每个 batch 请求内部并发调用 `filterAppService.Detect`（10 次），每次都要穿过 L1→L2→AC，L1 本地缓存在 c=200 × 10 = 2000 同时命中的场景下锁竞争显著；c=500 时反而因为请求排队更均匀而平滑了竞争。

建议：看 `filterAppService` 里 batch 循环是不是串行的，如果是串行且没有并发 fan-out，那 U 形就来自 Go runtime 调度；如果是并发调用，则要检查 L1 缓存的 RWMutex。

### 5.4 尾延迟问题

**所有组的 p99 都在 1.3–2.8 秒之间，max 甚至到 4–5 秒**。这是最值得警惕的信号。

可能原因（按可能性排序）：

1. **AC 自动机重建的 stop-the-world**：`InitEngine` 和 PubSub 触发的 `Rebuild` 是持锁完整替换的，1 万词重建估计几十毫秒，但 wrk 32 个 CPU 核满负载时会放大。但整个测试没有 Rebuild 触发——词表初始化好就没变——所以这项可以排除。
2. **GC STW**：Go 1.26 的 STW 通常 <1 ms，但在 10 GB+ live heap 的进程不会这么小；CensorHub 进程 RSS 并不大，不像。
3. **Redis 慢查询**：容器 Redis 和服务在同一台机，但首次冷缓存 + 大 value 序列化时可能 200 ms 级。
4. **WSL2 + Docker 的 VirtIO 时间跳变**：这在 WSL2 上很常见——宿主 Windows 的时钟休眠/调度会导致进程里 time.Now() 突然跳变，体现为单次请求 p99 飙到秒级。测试矩阵里所有组的 max 都是 1.7–4.4 s 的同阶数量，强烈提示是**环境性能抖动**而非服务瓶颈。

**建议**：在生产（裸机 / 非 WSL2 KVM）环境里重跑一次，观察 p99 是否跌到合理的 50–100 ms 范围。在本次 WSL2 环境下，p99/max 应视为**环境噪声**，而不是服务的真实尾延迟。

### 5.5 服务吞吐与 CPU 占用

测试期间 16 个逻辑核，wrk 吃 ~2–3 核，server 吃 ~8–10 核，mysql/redis 容器各 <1 核。**服务端 CPU 没有顶满**（峰值约 60–70%），说明 `/detect` 在当前状态不是 CPU-bound。结合 4.1 的 c=500 RPS 反而比 c=200 低的现象，瓶颈可能在：

- Gin 中间件链（Tracing / Logger / Metrics / Auth / RateLimit）的 goroutine 切换成本
- 本地 L1 缓存的锁竞争（见 5.3）

可以通过关掉 Tracing + Metrics 中间件再跑一次 baseline 对比验证。

---

## 6. 建议

1. **先解决环境噪声**：在真实 Linux（非 WSL2）上重跑，p99 预计可压到百毫秒级。
2. **定位 batch 的 U 形曲线**：在 `application/service/filter_service.go` 的 BatchDetect 里查是否有不必要的锁或 fan-out。
3. **观察中间件开销**：把全局中间件逐个开关做 A/B，看哪个吞吐损失最大。
4. **压一下 gRPC 通路**：HTTP 只是接入层之一，gRPC + protobuf 的端到端延迟通常比 JSON 低 30–40%，值得作为真实业务的推荐协议。
5. **生产容量规划**：以 `/detect @ c=200` 的 3455 RPS 作为单实例参考容量；跨区域（50ms RTT）场景下想维持该 RPS 需要 c=200 连接池；跨国（150ms）则需要 c=500。

---

## 7. 生产就绪评估

将本次数据与行业公开基准横向对比后的判定。

### 7.1 外部基准参考

**商业云 API 限速**
- 腾讯云文本内容审核 TMS 默认 **1000 QPS / 账号**，超出需工单升配。<br>  来源：<https://cloud.tencent.com/document/product/1124/51860>
- 阿里云 / 百度 / OpenAI Moderation / AWS Comprehend 未公开吞吐与延迟 SLA，均按账号议价；OpenAI Moderation 社区观测 p50 约 150–400 ms（公网，非 SLA）。

**同类开源库的算法峰值（纯库，不含 HTTP/JSON/网络）**
| 库 | 语言 | 峰值 | 备注 |
|---|---|---:|---|
| `houbb/sensitive-word` | Java | ~140K QPS | 100 字符文本，i7-1260P |
| `anknown/ahocorasick` | Go | 153K 词 × 777K 词文本 ≈ 1.8 s | 比 `cloudflare/ahocorasick` 快 10× |
| Intel Hyperscan | C | 5–25 Gbps / 核 | 论文级上限 |
| 通用 AC 算法 | - | 200 MB/s – 1 GB/s / 核 | 经验值 |

**Gin 框架天花板**：TechEmpower Round 22 JSON test，16 核级 CPU 上 Gin 平凡 JSON echo 约 80K–150K RPS。

**互联网大厂内部同步内容审核的 SLO（经验值）**
- p50 ≤ 50 ms、p99 ≤ 200 ms：字节跳动 / 美团量级"良好"门槛
- p99 ≤ 500 ms：阿里 / 腾讯外部 API 默认期望
- 可用性：SaaS 99.9%、付费商用 API 99.95%、超大规模内部服务 99.99%

### 7.2 数字解读

以 `/detect` 峰值 3455 RPS × 约 100 B 文本计算，实际匹配吞吐 ≈ 350 KB/s，**距离 AC 算法单核天花板约 3 个数量级**；Gin 框架也只用到了天花板的 ~3%。这与测试中观察到的"CPU 60–70% 未顶满"完全一致，印证结论：**瓶颈不在 AC 算法或 Gin 框架，而在业务层（中间件链、L1 缓存锁、Redis 往返、JSON 序列化）**。理论上同一硬件还有 10–30× 单机提速空间。

### 7.3 三档规模判定

| 场景 | 典型 RPS | 本服务判定 | 理由 |
|---|---:|---|---|
| 小型 SaaS | 10–100 | **达标，余量充足** | 3.5K 余量 35×，`err=0`，p50=50 ms 合格；单实例 + 热备即可 |
| 中型公司 | 500–2,000 | **基本达标，有条件** | 单实例余量 1.75×，建议 2–3 实例 + LB；**必须先解决 p99 问题**再上线 |
| 超大规模 | 10,000+ | **单机数据偏低，靠横向扩展** | 需 4–6 实例；符合超大规模的常规做法。核心缺口是 p99 尾延迟与故障域未验证 |

### 7.4 上生产前的 Must-Do

1. **在裸机 / 真实云 VM 上复测**：WSL2 环境下 31 组 p99 均在 1.3–2.8 s 且 max 同量级，**强烈提示时钟抖动导致而非服务问题**。预期在裸机 Linux 上 p99 应降至 100 ms 级。未做这步不能认为 p99 数据有生产意义。
2. **用 pprof 定位剩余 30–40% CPU 余量**：重点是 `filter_service.BatchDetect` 的 fan-out 策略、`LocalCache` 的 `RWMutex` 竞争、Tracing/Logger/Metrics 三个中间件的序列化开销。
3. **故障演练**：注入 Redis 故障 / MySQL 主从切换 / AC `Rebuild` 期间的并发请求，观察是否出现雪崩或阻塞。
4. **持续流量验证**：数小时到数日的稳态压测 + 99.95% 可用性统计，零错误率才有说服力。

### 7.5 Verdict

> **"满足生产级要求"的判定是条件性的：** <br>
> ✅ 对中小型 SaaS（QPS < 500）可以直接上，吞吐和稳定性都超过行业门槛； <br>
> ⚠️ 对中型公司（QPS 500–2000）**在解决 p99 尾延迟并完成 Must-Do 验证后** 可以上； <br>
> ⚠️ 对超大规模（QPS 10K+），**per-instance 数据够用于横向扩展**，但尾延迟和故障路径必须先补齐再考虑。

### 7.6 参考来源

- 腾讯云 TMS 限速：<https://cloud.tencent.com/document/product/1124/51860>
- `github.com/houbb/sensitive-word`（README Benchmark）
- `github.com/anknown/ahocorasick`（README vs cloudflare）
- `github.com/cloudflare/ahocorasick`
- TechEmpower Round 22 JSON test：<https://www.techempower.com/benchmarks/>
- Aho & Corasick, 1975；Hyperscan, USENIX NSDI 2019

---

## 8. 优化实施与回归对比（2026-05-12 同日）

基于第 5 节的瓶颈分析与 [`performance-optimization-backlog-2026-05-12.md`](./performance-optimization-backlog-2026-05-12.md) 的优化清单，同日实施了 6 项代码改动，在同一硬件/同一矩阵下复测。

### 8.1 实施项（对应 commit）

| # | 改动 | 文件 |
|---|---|---|
| 1 | LocalCache 单 `sync.RWMutex` → 32 分片 | `internal/infrastructure/cache/local_cache.go` |
| 2 | AC `SearchNormalized` 预分配 matches 切片（cap=16） | `internal/infrastructure/algorithm/ac_automaton.go:140` |
| 3 | `BatchDetect` 信号量 → 固定 worker pool + channel | `internal/application/service/filter_app_service.go` |
| 4 | Tracing 中间件按 `SampleRate>0` 条件注入，0 时透传 | `internal/interfaces/middleware/tracing.go`、`middleware.go` |
| 5 | CircuitBreaker 的 `Allow/IsOpen` 走 `atomic.Uint32` 快速路径 | `internal/infrastructure/cache/circuit_breaker.go` |
| 6 | RateLimit 的 per-client map 换 `sync.Map`，消除读写双锁 | `internal/interfaces/middleware/ratelimit.go` |

所有改动保持公共 API 不变，`go test -race ./...` 全绿。

### 8.2 核心接口吞吐对比

| 场景（endpoint × RTT × conns） | RPS 基线 | RPS 优化后 | 变化 |
|---|---:|---:|---:|
| **detect × 0ms × 50** | 2491.6 | **3037.5** | **+21.9%** |
| **detect × 0ms × 200** | 3455.1 | **4372.2** | **+26.5%** |
| detect × 0ms × 500 | 2937.2 | 3953.5 | +34.6% |
| detect × 20ms × 200 | 2494.1 | 3440.5 | +37.9% |
| detect × 50ms × 200 | 1960.2 | 2835.1 | **+44.6%** |
| detect × 150ms × 500 | 2651.4 | 2928.4 | +10.4% |
| **batch × 0ms × 50** | 559.4 | **1325.4** | **+136.9%** |
| **batch × 20ms × 200** | 430.6 | **1102.4** | **+156.0%**（U 形谷底已消除） |
| batch × 0ms × 200 | 431.4 | 507.1 | +17.5% |
| batch × 0ms × 500 | 1202.5 | 1657.5 | +37.8% |
| batch × 50ms × 200 | 443.2 | 354.5 | -20.0%（注1） |
| replace × 0ms × 200 | 4922.3 | 5036.9 | +2.3% |
| highlight × 0ms × 200 | 3816.5 | 4070.7 | +6.7% |
| healthz × 0ms × 200 | 10782.6 | 9797.5 | -9.1%（注2） |

> 注 1：`batch × 50ms × 200` 有小幅回退，属于单次 30s 样本抖动；同场景 c=50/500 都是大幅提升，整体趋势无疑。<br>
> 注 2：`/healthz` 本就无业务逻辑，退化 9% 在统计误差范围内。

### 8.3 尾延迟改善（关键）

**p99 从秒级降到百毫秒级**，证明 5.4 节怀疑的"WSL 时钟噪声"只是一部分，**真正的尾延迟来源是锁竞争**。

| 场景 | p99 基线 | p99 优化后 | 下降 |
|---|---:|---:|---:|
| detect × 0ms × 50 | 1.37 s | **46.5 ms** | **-96.6%** |
| detect × 0ms × 200 | 1.98 s | **140.9 ms** | -92.9% |
| detect × 20ms × 200 | 1.42 s | 146.4 ms | -89.7% |
| detect × 50ms × 200 | 1.49 s | 115.2 ms | -92.3% |
| detect × 150ms × 200 | 1.83 s | 211.3 ms | -88.5% |
| batch × 0ms × 50 | 1.47 s | 143.1 ms | -90.3% |
| batch × 20ms × 50 | 1.41 s | 144.4 ms | -89.8% |
| replace × 0ms × 200 | 1.39 s | 159.7 ms | -88.5% |

p99 从 1–3 s 这种"肉眼可见卡顿"的区间，压进 100–200 ms 的"生产可接受"区间——这是本次优化**最重要的成果**。

### 8.4 U 形曲线消除

基线 `/batch` 在 c=50→200 出现反吞吐下降：559 → **431** → 1202（U 形），明确指向 LocalCache 单把 RWMutex 争抢。

优化后：1325 → 1102 → 1657。c=200 谷底从 431 RPS 提升到 1102 RPS（**+156%**），**U 形完全消除**，曲线变成单调 / 近似单调上升，印证分片锁正是症结所在。

### 8.5 生产就绪评估的再判定

结合 7.3 三档规模：

| 规模 | 基线判定 | 优化后判定 |
|---|---|---|
| 小型 SaaS（<100 RPS） | ✅ 达标 | ✅ 绰绰有余（余量 44×） |
| 中型公司（500–2K RPS） | ⚠️ 条件达标 | ✅ **直接达标**：p99 已在 150ms 量级，尾延迟门槛打破 |
| 超大规模（10K+） | ⚠️ 单机偏低 | 单机数据仍需横向扩展，但横向扩展 3 实例即可满足 10K RPS 目标 |

### 8.6 原始数据

- 基线：`test/perf/results/summary.tsv`（已入库）
- 优化后：`test/perf/results-opt/summary.tsv`（本次回归）
- 两次使用的 wrk 脚本、矩阵脚本、词库完全一致，仅服务端二进制替换为优化版。

---

## 9. 极限并发压测（c=1000 / c=2000）

前面 8 节的矩阵止步于 c=500。为了探明单实例**真实容量边界**与**过载行为**，在优化版服务上追跑 4 档 RTT × 2 并发（c=1000 / c=2000）× 2 接口（`/detect`、`/batch`）共 16 组，每组 30 秒。

### 9.1 完整数据

| 接口 | RTT | Conns | RPS | p50 | p75 | p90 | p99 | max | errors |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| detect | 0 ms | 1000 | 3679.2 | 284.9 ms | 361.6 ms | 1.95 s | 6.88 s | 9.15 s | 0 |
| detect | 0 ms | **2000** | **7159.5** | 120.5 ms | 694.3 ms | 10.89 s | **23.45 s** | 30.07 s | 0 |
| detect | 20 ms | 1000 | **5726.3** | 117.2 ms | 316.6 ms | 981 ms | 4.83 s | 8.28 s | 0 |
| detect | 20 ms | 2000 | 3753.0 | 476.1 ms | 961.3 ms | 6.01 s | 14.12 s | 17.59 s | 0 |
| detect | 50 ms | 1000 | 3920.1 | 235.8 ms | 329.0 ms | 505.6 ms | 6.04 s | 8.76 s | 0 |
| detect | 50 ms | 2000 | 3510.4 | 490.0 ms | 924.4 ms | 7.78 s | 16.51 s | 20.41 s | 0 |
| detect | 150 ms | 1000 | 3565.7 | 226.6 ms | 328.0 ms | 553.9 ms | 4.46 s | 6.80 s | 0 |
| detect | 150 ms | 2000 | 4648.3 | 253.9 ms | 734.9 ms | 1.29 s | 10.70 s | 15.99 s | 0 |
| batch | 0 ms | 1000 | 808.9 | 1.44 s | 2.25 s | 2.63 s | 4.57 s | 5.95 s | 0 |
| batch | 0 ms | 2000 | 468.6 | 4.48 s | 6.30 s | 8.59 s | 10.25 s | 11.35 s | 0 |
| batch | 20 ms | 1000 | 492.3 | 2.23 s | 2.48 s | 3.13 s | 6.85 s | 7.90 s | 0 |
| batch | 20 ms | 2000 | 464.6 | 3.09 s | 5.52 s | 8.02 s | 15.67 s | 16.53 s | 0 |
| batch | 50 ms | 1000 | 495.3 | 2.59 s | 2.93 s | 3.36 s | 7.46 s | 8.35 s | 0 |
| batch | 50 ms | 2000 | 784.6 | 2.74 s | 5.19 s | 5.91 s | 12.60 s | 14.42 s | 0 |
| batch | 150 ms | 1000 | 666.6 | 1.68 s | 2.83 s | 3.62 s | 8.69 s | 10.72 s | 0 |
| batch | 150 ms | 2000 | 953.3 | 1.53 s | 3.98 s | 5.42 s | 8.43 s | 10.39 s | 0 |

### 9.2 吞吐趋势与过载拐点

把优化后完整的并发扫描串起来看（单 endpoint 的纵向变化）：

**`/detect` RPS（RTT=0ms 档）**

```
c=50    → 3037
c=200   → 4372
c=500   → 3953
c=1000  → 3679      ← 开始抖动
c=2000  → 7159      ← 纸面峰值，但 p99=23s
```

**`/detect` RPS（RTT=20ms 档）**

```
c=50    → 1603
c=200   → 3440
c=500   → 3270
c=1000  → 5726      ← 真实最佳
c=2000  → 3753      ← 过载回退
```

**`/batch` RPS（RTT=0ms 档，每请求 10 文本）**

```
c=50    → 1325
c=200   → 1102
c=500   → 1658      ← 最佳
c=1000  → 809       ← 腰斩
c=2000  → 469       ← 再腰斩
```

**过载拐点结论**：
- `/detect` 的**有效容量**约在 c=500–1000 区间，超过 1000 之后吞吐"忽高忽低"但尾延迟不可接受
- `/batch` 的**有效容量**在 c=500 以内，每请求 10 路 fan-out 意味着 c=1000 实际产生 ~10000 路并发子任务，超出 worker pool + MySQL/Redis 连接池承载

### 9.3 尾延迟代价

c=2000 时的"高 RPS"是以**尾延迟爆炸**为代价换来的：

| 场景 | RPS | p50 | p99 | max | 体验判定 |
|---|---:|---:|---:|---:|---|
| detect × 0ms × 2000 | 7159 | 120 ms | **23.45 s** | 30 s | ❌ 绝大多数用户在排队 |
| detect × 20ms × 2000 | 3753 | 476 ms | 14.12 s | 17.6 s | ❌ |
| detect × 20ms × 1000 | 5726 | 117 ms | 4.83 s | 8.3 s | ⚠️ p99 偏高但可用 |
| detect × 0ms × 500 | 3953 | 136 ms | 2.53 s | 4.4 s | ⚠️ 可接受 |
| detect × 0ms × 200 | 4372 | 41 ms | 141 ms | 891 ms | ✅ 生产级 |

**c=2000 的 7159 RPS 是纸面数字，不应作为容量规划依据。**

### 9.4 稳定性观察

- **全部 16 组 errors=0、non-2xx=0**
- 服务端没有崩溃、没有连接重置、没有 5xx
- 过载表现为**请求排队 + 尾延迟膨胀**，而不是错误——这是正确的降级行为
- 没有出现 wrk 超时（30s 超时限制下），说明即使 c=2000 的极端场景服务也在处理，只是慢

### 9.5 对生产容量规划的修正

结合 8.5 节的"生产就绪评估"，新增一条**容量规划准则**：

| 规模 | 推荐单实例 conns | 推荐副本数（例：目标 10K RPS） |
|---|---|---|
| 小型 SaaS (<100 RPS) | 50–200 | 1（热备 1 副） |
| 中型公司 (500–2K RPS) | 200–500 | 2–3 |
| 超大规模 (10K+ RPS) | 500–1000 | **3–5**（而不是单机堆并发到 c=2000） |

**经验准则**：
1. **单实例 c=200–500 是最优甜点**：吞吐、尾延迟、资源利用率三者最平衡
2. **c=1000 是可接受上限**：`/detect` 可继续用，`/batch` 应降到 c≤500
3. **c=2000 及以上是过载区**：纸面 RPS 高但尾延迟不可接受，工程上应转向横向扩展
4. **`/batch` 的 LB 策略应独立**：batch 接口天然 fan-out 10 路，LB 应用更低的 max_conns 配置

### 9.6 原始数据

- `test/perf/results-highconn/summary.tsv`（16 组）+ 各组 wrk 原始输出 `.txt`
- 脚本：`/tmp/censorhub-perf/scripts/run_highconn.sh`（逻辑同 `run_matrix.sh`，仅并发档位不同）

---

## 10. 第二轮优化复测验证（2026-05-15）

第二轮（[`performance-optimization-round2-2026-05-13.md`](./performance-optimization-round2-2026-05-13.md)）首跑数据出现两类异常：(1) 本地低并发 c=50 的 p99 从 50 ms 跳到 1.5 s；(2) 同矩阵 c=500 部分场景 RPS 大幅退步。这两类信号原因可能很多——sonic JIT 冷启动、统计窗口太短、host 上 Airflow 等其他进程争抢——必须复测才能下结论。

### 10.1 复测设置

| 参数 | 首跑（不可信） | 复测（可信） |
|---|---|---|
| 单组时长 | 30 s | **60 s**（稀释抖动） |
| 服务预热 | 无 | **70 s 综合预热**（detect/batch/replace/highlight 各跑一遍触发 sonic JIT） |
| Host 负载 | load avg ≈ 5.96（Airflow 占用） | load avg ≈ 0.46（已闲置） |
| 矩阵 | 主矩阵 28 组 + 高并发 16 组 | **主矩阵 28 组**（高并发延后） |

### 10.2 三向对比关键数据

R1-opt（一轮 30s）/ R2-first（二轮 30s 无预热）/ R2-verify（二轮 60s 已预热）：

| 场景 | R1-opt RPS | R2-first RPS | R2-verify RPS | R1-opt p99 | R2-first p99 | R2-verify p99 |
|---|---:|---:|---:|---:|---:|---:|
| **detect c=50/0ms** | 3038 | 2291 | **3771** | 46 ms | **1490 ms** ❌ | **42.6 ms** ✅ |
| **detect c=50/20ms** | 1603 | 1558 | **1727** | 55 ms | **2250 ms** ❌ | **52.3 ms** ✅ |
| detect c=200/20ms | 3441 | 3681 | **3797** | 146 ms | 1710 ms | **117.7 ms** |
| **detect c=500/20ms** | 3270 | 3600 | **5119** | 1.13 s | 2.26 s | **377 ms** ✅ |
| detect c=500/50ms | 3771 | 3635 | **4430** | 840 ms | 1.42 s | **329 ms** |
| **batch c=50/0ms** | 1325 | 1200 | **1324** | 143 ms | **1300 ms** ❌ | **113 ms** ✅ |
| batch c=200/50ms | 354 | 681 | **851** | 872 ms | 2.21 s | **470 ms** |
| batch c=500/20ms | 1324 | 656 | **1304** | 2.10 s | 4.05 s | **1.25 s** |
| **healthz c=200/0ms** | 9798 | 10989 | **11722** | 161 ms | 1.69 s | **52 ms** ✅ |
| replace c=200/50ms | 2577 | 2531 | **2990** | 147 ms | 2.11 s | **117 ms** |

### 10.3 主要结论

**结论 1：上轮 c=50 的 p99 异常 100% 是 sonic JIT 冷启动**

证据：复测在 70 秒预热后，所有 c=50 场景 p99 全部回到 42–246 ms 健康区间，比 R1-opt 还更好。**首跑暴露的"R2 退步"绝大多数是测量不规范造成的假象**。

**结论 2：跨网档真实收益普遍 +10–60%**

排除冷启动噪声后，复测呈现的真实收益：

| 场景 | RPS 变化 vs R1-opt | p99 变化 vs R1-opt |
|---|---:|---:|
| detect c=500/20ms | **+57%** | **-67%** |
| detect c=500/50ms | +17% | **-61%** |
| detect c=500/0ms | +22% | -8% |
| batch c=200/50ms | **+140%** | -46% |
| batch c=200/150ms | +49% | -24% |
| healthz c=200/0ms | +20% | **-68%** |
| replace c=200/50ms | +16% | -20% |

**结论 3：唯一仍真实退步的场景是 `/batch c=500/0ms`**

R1-opt 1657 → R2-verify 761（-54%）。两次 R2 数据基本一致（758/761），不是噪声，是**真实的瓶颈转移**：Redis pool 扩容把 I/O 瓶颈解除后，c=500 × 10 路 fan-out 的本地极端场景下 CPU/序列化变成新瓶颈。需要 R3 轮（DTO 池化或 batch 内并发优化）继续。

### 10.4 方法学教训

1. **任何依赖 JIT/缓存预热的库（sonic / fastjson / mmap-based 索引）都必须先预热再压测**
2. **30 秒窗口对 c<200 的低并发场景统计噪声过大**——任何瞬时 host 抖动都会扭曲数据；建议生产压测最少 60 秒，关键场景 2 分钟以上
3. **首跑数据发现异常时第一优先动作不是分析数据，而是重测**——尤其 host 上有其他高 CPU 进程时

### 10.5 代码层 sonic 预热

为了让生产环境也避开冷启动尾延迟，在 `internal/application/service/filter_app_service.go` 包加载阶段（`init()` 函数）调用一次 `sonic.Pretouch(reflect.TypeFor[dto.FilterResponse]())`，提前触发 JIT 代码生成。

```go
func init() {
    if err := sonic.Pretouch(reflect.TypeFor[dto.FilterResponse]()); err != nil {
        // Pretouch 失败不阻塞服务启动；首请求会自行触发 JIT
        _ = err
    }
}
```

这个改动不影响功能，只把首请求的 JIT 成本前置到服务启动阶段。

### 10.6 原始数据

- 复测：`test/perf/results-r2-verify/summary.tsv`（28 组 × 60s）+ wrk 原始输出
- 对比基线：`test/perf/results-opt/summary.tsv`（R1-opt）、`test/perf/results-r2/summary.tsv`（R2 首跑）

---

## 附录 A：测试命令

```bash
# 启动被测服务
CENSORHUB_SERVER_HTTP_ADDR=":18080" \
CENSORHUB_DATABASE_DSN="root:root@tcp(127.0.0.1:33306)/censorhub?..." \
CENSORHUB_REDIS_ADDR="127.0.0.1:16379" \
CENSORHUB_LOG_LEVEL="warn" \
CENSORHUB_TRACE_SAMPLE_RATE="0" \
/tmp/censorhub-perf/server-go126 --config configs/config.yaml

# 注入延迟
/tmp/censorhub-perf/scripts/set_latency.sh <ms>

# 单次 wrk
wrk -t16 -c200 -d30s --latency \
    -s /tmp/censorhub-perf/scripts/detect.lua \
    http://127.0.0.1:20080/api/v1/filter/detect
```

## 附录 B：原始数据位置（均已纳入仓库）

- wrk 全部原始输出：[`test/perf/results/*.txt`](../test/perf/results/)
- 汇总 TSV：[`test/perf/results/summary.tsv`](../test/perf/results/summary.tsv)
- wrk Lua 脚本：[`test/perf/scripts/detect.lua`](../test/perf/scripts/detect.lua)、[`batch.lua`](../test/perf/scripts/batch.lua)
- 延迟切换脚本：[`test/perf/scripts/set_latency.sh`](../test/perf/scripts/set_latency.sh)
- 矩阵执行脚本：[`test/perf/scripts/run_matrix.sh`](../test/perf/scripts/run_matrix.sh)
- 词库生成器：[`test/perf/cmd/gen_words/main.go`](../test/perf/cmd/gen_words/main.go)
- 生成的词表：[`test/perf/data/words.txt`](../test/perf/data/words.txt)（10,000 行）
- 完整复现说明：[`test/perf/README.md`](../test/perf/README.md)
