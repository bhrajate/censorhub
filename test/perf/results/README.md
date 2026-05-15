# 压测原始数据索引

按"轮次 × 硬件"分子目录归档。每份 wrk 原始 `*.txt` 输出与一两份 `*.tsv` 汇总并存，方便复现核对。

## 目录结构

```
test/perf/results/
├── README.md                       # 本文件
├── baseline-i5-13500h/             # 首次基线（未优化）— commit 1997f38
│   ├── matrix.tsv                  #   主矩阵 31 组 × 30s
│   └── *.txt                       #   每组 wrk 原始输出
├── round1-i5-13500h/               # round1 锁优化回归 — commit cdb97b7
│   ├── matrix.tsv                  #   主矩阵 31 组 × 30s
│   ├── highconn.tsv                #   高并发 c=1000/2000 共 16 组 × 30s
│   └── *.txt
├── round1-ultra9-185h/             # round1 在 Ultra 9 新硬件上的复测 — commit 984a014
│   ├── matrix.tsv                  #   主矩阵 30s
│   ├── highconn.tsv                #   高并发含 c=1000/2000/5000
│   └── *.txt
└── round2-i5-13500h/               # round2 sonic + Redis pool 优化 — commit bc5f137
    ├── first-30s-matrix.tsv        #   首跑 30s 主矩阵（sonic 未预热，p99 异常）
    ├── first-30s-highconn.tsv      #   首跑 30s 高并发
    ├── verify-60s-matrix.tsv       #   60s 复测主矩阵（已预热，正式数据）
    └── *.txt
```

## 与文档对应关系

| 报告 | 引用本目录下哪些数据 |
|---|---|
| [`reports/baseline-i5-13500h-2026-05-12.md`](../../../docs/performance/reports/baseline-i5-13500h-2026-05-12.md) | `baseline-i5-13500h/` |
| [`reports/round1-i5-13500h-2026-05-12.md`](../../../docs/performance/reports/round1-i5-13500h-2026-05-12.md) | `baseline-i5-13500h/`（基线） + `round1-i5-13500h/`（优化后） |
| [`reports/round1-ultra9-185h-2026-05-13.md`](../../../docs/performance/reports/round1-ultra9-185h-2026-05-13.md) | `round1-ultra9-185h/` |
| [`reports/round2-i5-13500h-2026-05-15.md`](../../../docs/performance/reports/round2-i5-13500h-2026-05-15.md) | `round1-i5-13500h/`（对比基线） + `round2-i5-13500h/`（首跑 + 复测） |

## 文件命名约定

- **`matrix.tsv`** — 主矩阵汇总（4 RTT × 3 conns × 31 组 endpoint）
- **`highconn.tsv`** — 高并发独立矩阵（c=1000+ 的扩展测试）
- **`first-*` / `verify-*` 前缀** — 同硬件同轮次内的多次跑，用于区分初测/复测
- 文件名里的 `rttN_cN` 由 wrk 脚本生成，分别表示注入 RTT（毫秒）和并发连接数

## 复现新一轮压测

新跑一次的输出建议直接落到本目录下新建的子目录，例如：

```bash
RESULTS_DIR=test/perf/results/round3-myhw-$(date +%Y%m%d) \
  bash test/perf/scripts/run_matrix.sh
```
