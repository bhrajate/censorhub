# CensorHub 性能测试

完整报告：[`docs/performance/reports/baseline-i5-13500h-2026-05-12.md`](../../docs/performance/reports/baseline-i5-13500h-2026-05-12.md)

## 目录结构

```
test/perf/
├── README.md                     # 本文件
├── cmd/gen_words/main.go         # 随机生成词库并通过 /api/v1/words/import 导入
├── scripts/
│   ├── detect.lua                # wrk 脚本：/api/v1/filter/detect
│   ├── batch.lua                 # wrk 脚本：/api/v1/filter/batch
│   ├── set_latency.sh            # 通过 toxiproxy 设置/清除双向延迟
│   └── run_matrix.sh             # 压测矩阵执行器（4 档延迟 × 3 并发 × 2 接口 + 对照）
├── data/
│   └── words.txt                 # 10k 条随机生成的测试词库（用于 wrk 请求体填充）
└── results/                      # 2026-05-12 一次完整运行的原始产物
    ├── summary.tsv               # 汇总表（tab 分隔，31 行数据）
    └── *.txt                     # 每组 wrk 的完整原始输出
```

## 快速复现

### 1. 准备依赖服务（docker）

被测实例不复用开发机上的 MySQL/Redis，避免端口冲突：

```bash
docker run -d --name censor-mysql-perf \
  -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=censorhub \
  -p 33306:3306 mysql:8.0

docker run -d --name censor-redis-perf -p 16379:6379 redis:7-alpine

docker run -d --name censor-toxiproxy --network host \
  ghcr.io/shopify/toxiproxy:latest -host 0.0.0.0 -port 8474
```

### 2. 启动 CensorHub（端口 18080，避开 8080）

```bash
go build -o bin/server ./cmd/server

CENSORHUB_SERVER_HTTP_ADDR=":18080" \
CENSORHUB_SERVER_GRPC_ADDR=":19090" \
CENSORHUB_DATABASE_DSN="root:root@tcp(127.0.0.1:33306)/censorhub?charset=utf8mb4&parseTime=True&loc=Local" \
CENSORHUB_REDIS_ADDR="127.0.0.1:16379" \
CENSORHUB_LOG_LEVEL="warn" \
CENSORHUB_TRACE_SAMPLE_RATE="0" \
CENSORHUB_RATELIMIT_REQUESTS_PER_SECOND="1000000" \
CENSORHUB_RATELIMIT_BURST="2000000" \
./bin/server --config configs/config.yaml
```

### 3. 配置 toxiproxy（一次性）

```bash
curl -X POST http://127.0.0.1:8474/proxies \
  -H "Content-Type: application/json" \
  -d '{"name":"censor-http","listen":"127.0.0.1:20080","upstream":"127.0.0.1:18080","enabled":true}'
```

之后 wrk 的压测入口统一走 `127.0.0.1:20080`，`scripts/set_latency.sh <ms>` 随时切延迟。

### 4. 导入测试词库

```bash
# 方案 A：用现成词表（test/perf/data/words.txt）直接走 curl 导入
#   （略，生产用少，略过）

# 方案 B：用 gen_words 重新生成 + 导入
go run ./test/perf/cmd/gen_words \
  -endpoint http://127.0.0.1:18080/api/v1/words/import \
  -api-key censorhub-default-key \
  -n 10000 \
  -out test/perf/data/words.txt

# 等 5 秒让 AC 引擎防抖重建
curl -s http://127.0.0.1:18080/readyz
# 期望：{"status":"ready","word_count":10000}
```

### 5. 运行压测矩阵

```bash
# 从仓库根目录执行（lua 默认读 test/perf/data/words.txt）
bash test/perf/scripts/run_matrix.sh
```

也可用环境变量调整：

```bash
BASE=http://127.0.0.1:20080 DURATION=60 \
RESULTS_DIR=test/perf/results/myrun-$(date +%Y%m%d) \
bash test/perf/scripts/run_matrix.sh
```

### 6. 查看结果

```bash
# 已落库的轮次（按 round 子目录组织）：
column -t -s $'\t' test/perf/results/baseline-i5-13500h/matrix.tsv | less -S
column -t -s $'\t' test/perf/results/round1-i5-13500h/matrix.tsv  | less -S
column -t -s $'\t' test/perf/results/round2-i5-13500h/verify-60s-matrix.tsv | less -S
```

## 矩阵说明

| 维度 | 档位 |
|---|---|
| RTT 延迟 | 0 / 20 / 50 / 150 ms（同机房 / 同城 / 跨区域 / 跨国） |
| 并发连接数 | 50 / 200 / 500 |
| 线程数 | `min(conns, 16)` |
| 单组时长 | 30 秒 |
| 核心接口（全矩阵） | `/api/v1/filter/detect`、`/api/v1/filter/batch` |
| 对照接口（仅 0/50 ms × c=200） | `/api/v1/filter/replace`、`/highlight`、`/healthz`、`/metrics` |

总计 31 组 × 30s ≈ 16 分钟。

## 已知限制

- **WSL2 环境时钟抖动会放大 p99/max**：在 WSL2 上所有组的 p99 都在 1.3–2.8 s 级，生产裸机通常在 50–100 ms。详见主报告第 5.4 节。
- **toxiproxy 只注入 RTT，不模拟丢包/抖动/带宽**：真实公网会更糟。想靠近真实公网可以在 toxic 配置里加 `jitter`、`bandwidth`、`timeout` 等 toxic 类型。
- **词表规模固定 10k**：词量翻倍对 AC 自动机的匹配代价是亚线性的（~O(1) 每字符），但会显著增加启动期的 Rebuild 耗时，没在本次矩阵里单独压。
