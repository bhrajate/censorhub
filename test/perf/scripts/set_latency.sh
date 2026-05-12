#!/bin/bash
# 使用 toxiproxy API 设置/清除 censor-http 的双向延迟
# 用法：  set_latency.sh <rtt_ms>       # rtt_ms=0 表示清除
#
# 双向注入：upstream 方向(client->server) 和 downstream 方向(server->client) 各注入 rtt/2，
# 这样一次请求的 RTT 近似等于 rtt_ms。

set -euo pipefail
RTT_MS=${1:-0}
PROXY=${TOXIPROXY_URL:-http://127.0.0.1:8474}
NAME=${TOXIPROXY_NAME:-censor-http}

# 先删除已有 toxics
for tox in latency_up latency_down; do
  curl -s -o /dev/null -X DELETE "$PROXY/proxies/$NAME/toxics/$tox" || true
done

if [ "$RTT_MS" = "0" ]; then
  echo "[netem] cleared (RTT=0ms)"
  exit 0
fi

HALF=$((RTT_MS / 2))
curl -s -o /dev/null -X POST "$PROXY/proxies/$NAME/toxics" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"latency_up\",\"type\":\"latency\",\"stream\":\"upstream\",\"toxicity\":1.0,\"attributes\":{\"latency\":$HALF,\"jitter\":0}}"
curl -s -o /dev/null -X POST "$PROXY/proxies/$NAME/toxics" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"latency_down\",\"type\":\"latency\",\"stream\":\"downstream\",\"toxicity\":1.0,\"attributes\":{\"latency\":$HALF,\"jitter\":0}}"
echo "[netem] set RTT=${RTT_MS}ms (each direction ${HALF}ms)"
