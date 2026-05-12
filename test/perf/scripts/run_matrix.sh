#!/bin/bash
# CensorHub 压测矩阵执行器
#
# 精简矩阵：4 档延迟 × 3 并发(50/200/500) × {/detect,/batch}
#           + 对照组（2 档 × 4 旁路接口）
# 每组 30 秒，总时长 ~15-18 分钟。
#
# 前置条件：
#   1. CensorHub 服务已在 upstream 地址运行（默认 127.0.0.1:18080）
#   2. toxiproxy 已监听管理端口 8474，且创建了名为 censor-http 的 proxy
#      转发 127.0.0.1:20080 -> upstream。详见 test/perf/README.md
#   3. 词库 words.txt 已生成（见 cmd/gen_words）
#
# 环境变量：
#   BASE        压测入口（默认 http://127.0.0.1:20080，即 toxiproxy 端口）
#   DURATION    单组时长秒数（默认 30）
#   RESULTS_DIR 结果输出目录（默认 test/perf/results）
#   SCRIPTS_DIR lua 脚本目录（默认 test/perf/scripts）
#
# 建议从仓库根目录执行：  bash test/perf/scripts/run_matrix.sh

set -u

BASE=${BASE:-http://127.0.0.1:20080}
DURATION=${DURATION:-30}
RESULTS_DIR=${RESULTS_DIR:-test/perf/results}
SCRIPTS_DIR=${SCRIPTS_DIR:-test/perf/scripts}

mkdir -p "$RESULTS_DIR"
: > "$RESULTS_DIR/summary.tsv"
echo -e "endpoint\trtt_ms\tconns\tthreads\tduration_s\trequests\trps\tlatency_avg_ms\tlatency_p50_ms\tlatency_p75_ms\tlatency_p90_ms\tlatency_p99_ms\tlatency_max_ms\terrors\tnon2xx" > "$RESULTS_DIR/summary.tsv"

run_one() {
  local endpoint=$1 lua=$2 rtt=$3 conns=$4 duration=${5:-$DURATION}
  local threads=$conns
  if [ $threads -gt 16 ]; then threads=16; fi
  local short=${endpoint##*/}
  local tag="${short}_rtt${rtt}_c${conns}"
  local out="$RESULTS_DIR/${tag}.txt"

  bash "$SCRIPTS_DIR/set_latency.sh" "$rtt" >/dev/null

  {
    echo "=============================================================="
    echo "Endpoint : $endpoint"
    echo "RTT      : ${rtt}ms    Conns: $conns   Threads: $threads   Duration: ${duration}s"
    echo "Time     : $(date -Iseconds)"
    echo "=============================================================="
  } > "$out"

  local args=(-t"$threads" -c"$conns" -d"${duration}s" --latency --timeout 30s)
  if [ -n "$lua" ]; then
    args+=(-s "$lua")
  fi
  args+=("$endpoint")

  wrk "${args[@]}" >> "$out" 2>&1

  # 解析
  local body
  body=$(tail -n 30 "$out")
  local rps reqs errs non2xx avg max p50 p75 p90 p99
  rps=$(echo "$body" | awk '/Requests\/sec:/ {print $2}')
  reqs=$(echo "$body" | awk '/requests in/ {print $1}')
  errs=$(echo "$body" | awk '/Socket errors:/ {gsub(","," "); print $4+$6+$8+$10}')
  errs=${errs:-0}
  non2xx=$(echo "$body" | awk '/Non-2xx/ {print $NF}')
  non2xx=${non2xx:-0}
  avg=$(echo "$body" | awk '/Thread Stats/{getline; print $2}')
  max=$(echo "$body" | awk '/Thread Stats/{getline; print $4}')
  p50=$(echo "$body" | awk '/^[ ]*50%/ {print $2; exit}')
  p75=$(echo "$body" | awk '/^[ ]*75%/ {print $2; exit}')
  p90=$(echo "$body" | awk '/^[ ]*90%/ {print $2; exit}')
  p99=$(echo "$body" | awk '/^[ ]*99%/ {print $2; exit}')

  echo -e "$endpoint\t$rtt\t$conns\t$threads\t$duration\t$reqs\t$rps\t$avg\t$p50\t$p75\t$p90\t$p99\t$max\t$errs\t$non2xx" >> "$RESULTS_DIR/summary.tsv"
  printf "%-50s rtt=%-4s c=%-4s rps=%-10s p50=%-8s p99=%-10s err=%s\n" "$endpoint" "${rtt}ms" "$conns" "$rps" "$p50" "$p99" "$errs"
}

# /detect 全矩阵
for rtt in 0 20 50 150; do
  for c in 50 200 500; do
    run_one "$BASE/api/v1/filter/detect" "$SCRIPTS_DIR/detect.lua" "$rtt" "$c"
    sleep 2
  done
done

# /batch 全矩阵
for rtt in 0 20 50 150; do
  for c in 50 200 500; do
    run_one "$BASE/api/v1/filter/batch" "$SCRIPTS_DIR/batch.lua" "$rtt" "$c"
    sleep 2
  done
done

# 对照：replace / highlight / healthz / metrics 只跑 0 + 50ms × c=200
for rtt in 0 50; do
  run_one "$BASE/api/v1/filter/replace"   "$SCRIPTS_DIR/detect.lua" "$rtt" 200
  sleep 2
  run_one "$BASE/api/v1/filter/highlight" "$SCRIPTS_DIR/detect.lua" "$rtt" 200
  sleep 2
  run_one "$BASE/healthz"                 ""                        "$rtt" 200
  sleep 2
  run_one "$BASE/metrics"                 ""                        "$rtt" 200
  sleep 2
done

# 清延迟
bash "$SCRIPTS_DIR/set_latency.sh" 0 >/dev/null
echo "==== DONE ===="
column -t -s $'\t' "$RESULTS_DIR/summary.tsv"
