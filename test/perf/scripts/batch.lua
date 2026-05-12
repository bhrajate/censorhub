-- wrk 脚本：/api/v1/filter/batch  每请求 10 条文本
wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"
wrk.headers["X-API-Key"] = "censorhub-default-key"

local words = {}
local filler_cjk = "新闻资讯行业动态经济政策市场表现研究报告分析解读用户评论观点分享社区讨论生活方式运动健身美食菜谱旅游攻略风景介绍历史文化民俗故事科技前沿创新产品"
local filler_ascii = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua "

function init(args)
  math.randomseed(os.time())
  local path = os.getenv("WORDS_FILE") or "test/perf/data/words.txt"
  local f = io.open(path, "r")
  if not f then
    print("words file not found: " .. path)
    os.exit(1)
  end
  for line in f:lines() do
    if #line > 0 then words[#words + 1] = line end
  end
  f:close()
end

local function random_filler(n)
  local src = (math.random() < 0.6) and filler_cjk or filler_ascii
  local out = {}
  local total = 0
  while total < n do
    local s = math.random(1, #src - 4)
    local e = s + math.random(2, 12)
    if e > #src then e = #src end
    out[#out + 1] = src:sub(s, e)
    total = total + (e - s + 1)
  end
  return table.concat(out)
end

function request()
  local items = {}
  for i = 1, 10 do
    local base = random_filler(math.random(30, 120))
    local hits = math.random(0, 2)
    for _ = 1, hits do
      local w = words[math.random(1, #words)]
      local pos = math.random(1, #base)
      base = base:sub(1, pos) .. w .. base:sub(pos + 1)
    end
    items[#items + 1] = string.format("%q", base)
  end
  local body = '{"texts":[' .. table.concat(items, ",") .. ']}'
  return wrk.format(nil, nil, nil, body)
end
