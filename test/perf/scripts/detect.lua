-- wrk 脚本：/api/v1/filter/detect
-- 每次请求随机构造长度 40-200 字符的文本，随机插入 0-3 个敏感词

wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"
wrk.headers["X-API-Key"] = "censorhub-default-key"

local words = {}
local filler_cjk = "新闻资讯行业动态经济政策市场表现研究报告分析解读用户评论观点分享社区讨论生活方式运动健身美食菜谱旅游攻略风景介绍历史文化民俗故事科技前沿创新产品"
local filler_ascii = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua "

-- 每线程初始化：读取词库
function init(args)
  math.randomseed(os.time())
  -- 词表路径可通过环境变量 WORDS_FILE 覆盖；默认 test/perf/data/words.txt（相对仓库根执行 wrk）
  local path = os.getenv("WORDS_FILE") or "test/perf/data/words.txt"
  local f = io.open(path, "r")
  if not f then
    print("words file not found: " .. path)
    os.exit(1)
  end
  for line in f:lines() do
    if #line > 0 then
      words[#words + 1] = line
    end
  end
  f:close()
end

local function random_filler(n)
  -- n 是字符目标长度（近似）
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
  local base = random_filler(math.random(40, 200))
  local hits = math.random(0, 3)
  for i = 1, hits do
    local w = words[math.random(1, #words)]
    local pos = math.random(1, #base)
    base = base:sub(1, pos) .. w .. base:sub(pos + 1)
  end
  local body = string.format('{"text":%q}', base)
  return wrk.format(nil, nil, nil, body)
end
