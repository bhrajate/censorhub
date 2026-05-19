-- wrk 脚本：POST /api/v1/words
-- 用法：
--   wrk -t4 -c100 -d30s -s test/perf/scripts/write.lua \
--       http://127.0.0.1:18080/api/v1/words
--
-- 每次请求构造一个全局唯一的词条(基于 thread id + 自增计数器 + 起始时间戳),
-- 避免 ErrWordAlreadyExists,使每次请求都是真实的 INSERT。
-- 用来评估改造后写接口本身的延迟分布。

wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"
wrk.headers["X-API-Key"] = "censorhub-default-key"

local thread_id = 0
local counter = 0
local start_ts = 0

function setup(thread)
  thread:set("thread_id", thread:get("__index") or 0)
end

function init(args)
  -- wrk 在 init 时把 thread:get("__index") 传进来,但跨版本不可靠,直接用 os.time 做种子
  math.randomseed(os.time() * 1000 + (thread_id or 0))
  start_ts = os.time()
  -- 给每个线程一个 16-bit 随机后缀,降低碰撞
  thread_id = math.random(0, 65535)
  counter = 0
end

function request()
  counter = counter + 1
  -- 词条格式: "perf_w_<起始秒>_<线程后缀>_<计数器>"
  -- ASCII 14~24 字节,远低于 varchar(255),且不会和现有词库 (中文为主) 重复
  local text = string.format("perf_w_%d_%d_%d", start_ts, thread_id, counter)
  local body = string.format(
    '{"text":%q,"category":"custom","level":1,"tag":"perf"}',
    text
  )
  return wrk.format(nil, nil, nil, body)
end

-- 失败统计:打印非 2xx 响应(常见 401 / 409 / 500 等)
local nonok = 0
function response(status, headers, body)
  if status >= 400 then
    nonok = nonok + 1
  end
end

function done(summary, latency, requests)
  io.write(string.format("\n[write.lua] non-2xx responses: %d\n", nonok))
  io.write(string.format("[write.lua] total requests:    %d\n", summary.requests))
  io.write(string.format("[write.lua] errors (conn):     connect=%d read=%d write=%d timeout=%d status=%d\n",
    summary.errors.connect, summary.errors.read, summary.errors.write,
    summary.errors.timeout, summary.errors.status))
end
