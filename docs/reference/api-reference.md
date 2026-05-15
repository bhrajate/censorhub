# CensorHub 接口文档（API Reference）

本文档汇总 CensorHub 当前对外暴露的全部接口，包括 **HTTP/JSON**、**gRPC** 与
**运维探针/指标** 三类，覆盖：地址、参数、返回结构、错误码、用法示例。

> 适用版本：`api/proto/censor/v1`（gRPC）+ `internal/interfaces/http`（HTTP v1）。
> 本文档随代码持续维护，不带日期后缀。

---

## 目录

- [1. 通用约定](#1-通用约定)
  - [1.1 监听地址与端口](#11-监听地址与端口)
  - [1.2 认证（X-API-Key）](#12-认证x-api-key)
  - [1.3 限流](#13-限流)
  - [1.4 请求体上限与跨域](#14-请求体上限与跨域)
  - [1.5 统一响应格式](#15-统一响应格式)
  - [1.6 错误码](#16-错误码)
  - [1.7 公共枚举](#17-公共枚举)
- [2. 运维与可观测](#2-运维与可观测)
  - [2.1 GET /healthz](#21-get-healthz存活探针)
  - [2.2 GET /readyz](#22-get-readyz就绪探针)
  - [2.3 GET /metrics](#23-get-metricsprometheus-指标)
- [3. 文本过滤接口（HTTP）](#3-文本过滤接口http)
  - [3.1 POST /api/v1/filter/detect](#31-post-apiv1filterdetect)
  - [3.2 POST /api/v1/filter/replace](#32-post-apiv1filterreplace)
  - [3.3 POST /api/v1/filter/highlight](#33-post-apiv1filterhighlight)
  - [3.4 POST /api/v1/filter/batch](#34-post-apiv1filterbatch)
- [4. 词条管理接口（HTTP）](#4-词条管理接口http)
  - [4.1 GET /api/v1/words](#41-get-apiv1words)
  - [4.2 POST /api/v1/words](#42-post-apiv1words)
  - [4.3 GET /api/v1/words/:id](#43-get-apiv1wordsid)
  - [4.4 PUT /api/v1/words/:id](#44-put-apiv1wordsid)
  - [4.5 DELETE /api/v1/words/:id](#45-delete-apiv1wordsid)
  - [4.6 POST /api/v1/words/import](#46-post-apiv1wordsimport)
  - [4.7 GET /api/v1/words/export](#47-get-apiv1wordsexport)
- [5. gRPC 接口（censor.v1.CensorService）](#5-grpc-接口censorv1censorservice)
  - [5.1 Detect](#51-detect)
  - [5.2 Replace](#52-replace)
  - [5.3 BatchDetect](#53-batchdetect)
  - [5.4 健康检查（grpc.health.v1）](#54-健康检查grpchealthv1)

---

## 1. 通用约定

### 1.1 监听地址与端口

默认配置（`configs/config.yaml`）：


| 协议    | 监听           | 路由前缀                            |
| ----- | ------------ | ------------------------------- |
| HTTP  | `:8080`      | `/api/v1`                       |
| gRPC  | `:9090`      | `censor.v1.CensorService`       |
| 健康/指标 | HTTP `:8080` | `/healthz`、`/readyz`、`/metrics` |


### 1.2 认证（X-API-Key）

- 仅 `/api/v1/`** 受认证保护；`/healthz`、`/readyz`、`/metrics` **不**鉴权。
- HTTP 通过请求头 `X-API-Key: <key>` 携带；gRPC 通过 metadata `x-api-key`。
- 未配置 `auth.api_keys` 时跳过认证。
- 失败响应：HTTP `401`、`{"code":10008,"message":"unauthorized: ..."}`；gRPC `Unauthenticated`。

### 1.3 限流

- 维度：优先按 `X-API-Key`，无 Key 时按 `客户端 IP`。
- 实现：`golang.org/x/time/rate` 漏桶。
- 默认配置：`rps=1000`，`burst=2000`（详见 `configs/config.yaml`）。
- 失败响应：HTTP `429`、`{"code":10007,"message":"rate limit exceeded"}`；gRPC `ResourceExhausted`。

### 1.4 请求体上限与跨域

- 请求体上限：由 `server.http.max_body_size` 控制，超出返回 `413`。
- CORS：`cors.allowed_origins` 列表；包含 `*` 时允许所有来源；非白名单的预检请求返回 `403`。
- 自动注入响应头：`X-Request-ID`（由 `RequestID` 中间件生成）。

### 1.5 统一响应格式

除 **CSV 导出**（`/api/v1/words/export`）和 **/metrics** 外，所有 HTTP 接口均返回 JSON：

```json
{
  "code": 0,
  "message": "ok",
  "data": { ... },
  "trace_id": "a1b2c3..."
}
```


| 字段         | 类型           | 说明                                |
| ---------- | ------------ | --------------------------------- |
| `code`     | int          | 业务码，`0` 表示成功，非 0 见 [错误码](#16-错误码) |
| `message`  | string       | 人类可读的错误描述；成功时为 `"ok"`             |
| `data`     | object/array | 业务数据；错误时缺省                        |
| `trace_id` | string       | 链路追踪 ID（与响应头 `X-Request-ID` 同值）   |


> `POST /api/v1/words` 在 **创建成功** 时返回 HTTP `201` 而非 `200`，响应体为
> `{"code":0,"message":"created","data":{...}}`，无 `trace_id`。

### 1.6 错误码


| code  | HTTP    | 含义                            |
| ----- | ------- | ----------------------------- |
| 0     | 200/201 | 成功                            |
| 10001 | 409     | 词条已存在（`ErrWordAlreadyExists`） |
| 10002 | 404     | 词条不存在（`ErrWordNotFound`）      |
| 10003 | 400     | 非法分类（`ErrInvalidCategory`）    |
| 10004 | 400     | 非法风险等级（`ErrInvalidRiskLevel`） |
| 10005 | 400     | 文本过长（`ErrTextTooLong`）        |
| 10006 | 500     | 批量导入失败（`ErrImportFailed`）     |
| 10007 | 429     | 限流（`ErrRateLimitExceeded`）    |
| 10008 | 401     | 未授权（`ErrUnauthorized`）        |
| 10009 | 400     | 请求参数错误（`ErrInvalidRequest`）   |
| 10010 | 500     | 服务器内部错误（`ErrInternal`）        |


源：`pkg/errors/errors.go`。

### 1.7 公共枚举

**Category（分类）**：`politics` / `porn` / `ad` / `violence` / `abuse` / `custom`

**RiskLevel（风险等级）**：`1=low` / `2=medium` / `3=high` / `4=critical`

**WordStatus（词条状态）**：`0=inactive` / `1=active`

**FilterStrategy（过滤策略）**：`detect` / `replace` / `highlight`

---

## 2. 运维与可观测

### 2.1 GET /healthz（存活探针）

- 用途：K8s liveness。**不**检查依赖。
- 鉴权 / 限流：**否**。
- 请求示例：

```bash
curl -s http://localhost:8080/healthz
```

- 响应示例：

```json
{ "status": "alive" }
```

### 2.2 GET /readyz（就绪探针）

- 用途：K8s readiness。检查 MySQL / Redis ping 与引擎词条数。
- 鉴权 / 限流：**否**。
- 请求示例：

```bash
curl -s http://localhost:8080/readyz
```

- 成功响应（HTTP `200`）：

```json
{ "status": "ready", "word_count": 12345 }
```

- 失败响应（HTTP `503`）：

```json
{ "status": "not ready", "error": "redis ping failed" }
```

### 2.3 GET /metrics（Prometheus 指标）

- 用途：Prometheus 抓取。
- 鉴权 / 限流：**否**。
- 响应格式：`text/plain; version=0.0.4` 的 Prometheus 文本协议。
- 关键指标：
  - `censorhub_http_requests_total{method,path,status}`
  - `censorhub_http_request_duration_seconds_bucket{method,path,le}`
  - `censorhub_http_response_size_bytes_bucket{method,path,le}`
  - `censorhub_filter_hits_total{strategy,is_hit}`
  - `censorhub_engine_word_count`、`censorhub_engine_rebuild_total`
- 请求示例：

```bash
curl -s http://localhost:8080/metrics | head
```

---

## 3. 文本过滤接口（HTTP）

> 全部位于 `POST /api/v1/filter/*`，需 `X-API-Key`。
> 请求体公共结构 `FilterRequest`：

```jsonc
{
  "text": "string，必填，最长 50000 字符",
  "strategy": "detect | replace | highlight，可选；各 endpoint 内部强制覆盖"
}
```

> 响应体公共结构 `FilterResponse`：


| 字段           | 类型               | 说明                    |
| ------------ | ---------------- | --------------------- |
| `original`   | string           | 原文                    |
| `filtered`   | string           | 处理后文本（detect 时为空）     |
| `is_hit`     | bool             | 是否命中任一敏感词             |
| `hit_count`  | int              | 命中条数                  |
| `matches`    | array<MatchItem\> | 命中详情，空时省略             |
| `risk_level` | int              | 命中里最高风险等级（1–4，未命中为 0） |
| `cost_ms`    | int64            | 服务端耗时（毫秒）             |


`MatchItem`：


| 字段             | 类型     | 说明                    |
| -------------- | ------ | --------------------- |
| `word`         | string | 命中的敏感词                |
| `position`     | int    | 在归一化文本中的起始 rune 下标    |
| `end_position` | int    | 结束 rune 下标（不含）        |
| `category`     | string | 分类（见 [1.7](#17-公共枚举)） |
| `level`        | int    | 词条风险等级 1–4            |


### 3.1 POST /api/v1/filter/detect

- 仅检测，不修改原文。
- 请求示例：

```bash
curl -s -X POST http://localhost:8080/api/v1/filter/detect \
  -H "X-API-Key: censorhub-default-key" \
  -H "Content-Type: application/json" \
  -d '{"text":"这是一段包含敏感词的测试文本"}'
```

- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "original": "这是一段包含敏感词的测试文本",
    "filtered": "",
    "is_hit": true,
    "hit_count": 1,
    "matches": [
      {"word":"敏感词","position":6,"end_position":9,"category":"custom","level":2}
    ],
    "risk_level": 2,
    "cost_ms": 1
  },
  "trace_id": "..."
}
```

### 3.2 POST /api/v1/filter/replace

- 命中的敏感词替换为同等长度的 `*`，结果写入 `filtered`。
- 请求示例：

```bash
curl -s -X POST http://localhost:8080/api/v1/filter/replace \
  -H "X-API-Key: censorhub-default-key" \
  -H "Content-Type: application/json" \
  -d '{"text":"这是一段包含敏感词的测试文本"}'
```

- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "original": "这是一段包含敏感词的测试文本",
    "filtered": "这是一段包含***的测试文本",
    "is_hit": true,
    "hit_count": 1,
    "matches": [
      {"word":"敏感词","position":6,"end_position":9,"category":"custom","level":2}
    ],
    "risk_level": 2,
    "cost_ms": 1
  },
  "trace_id": "..."
}
```

### 3.3 POST /api/v1/filter/highlight

- 命中位置使用 `<mark>...</mark>` 包裹，结果写入 `filtered`。
- 请求示例：

```bash
curl -s -X POST http://localhost:8080/api/v1/filter/highlight \
  -H "X-API-Key: censorhub-default-key" \
  -H "Content-Type: application/json" \
  -d '{"text":"这是一段包含敏感词的测试文本"}'
```

- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "original": "这是一段包含敏感词的测试文本",
    "filtered": "这是一段包含<mark>敏感词</mark>的测试文本",
    "is_hit": true,
    "hit_count": 1,
    "matches": [
      {"word":"敏感词","position":6,"end_position":9,"category":"custom","level":2}
    ],
    "risk_level": 2,
    "cost_ms": 1
  },
  "trace_id": "..."
}
```

### 3.4 POST /api/v1/filter/batch

- 批量检测，**仅** `detect` 策略生效（命中详情不修改原文）。
- 请求体 `BatchFilterRequest`：

```jsonc
{
  "texts": ["string", ...],   // 必填，1–100 条，单条 ≤ 50000 字符
  "strategy": "detect"        // 可选；当前实现路由强制为 detect
}
```

- 请求示例：

```bash
curl -s -X POST http://localhost:8080/api/v1/filter/batch \
  -H "X-API-Key: censorhub-default-key" \
  -H "Content-Type: application/json" \
  -d '{"texts":["普通文案","一段含敏感词的文案","另一段安全文案"]}'
```

- 响应体 `BatchFilterResponse`：


| 字段        | 类型                    | 说明             |
| --------- | --------------------- | -------------- |
| `results` | array<FilterResponse\> | 与 `texts` 顺序一致 |
| `total`   | int                   | `len(texts)`   |
| `hit_num` | int                   | 命中文本数（不是命中词次数） |


- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "results": [
      {"original":"普通文案","filtered":"","is_hit":false,"hit_count":0,"risk_level":0,"cost_ms":0},
      {
        "original":"一段含敏感词的文案",
        "filtered":"",
        "is_hit":true,
        "hit_count":1,
        "matches":[
          {"word":"敏感词","position":3,"end_position":6,"category":"custom","level":2}
        ],
        "risk_level":2,
        "cost_ms":1
      },
      {"original":"另一段安全文案","filtered":"","is_hit":false,"hit_count":0,"risk_level":0,"cost_ms":0}
    ],
    "total": 3,
    "hit_num": 1
  },
  "trace_id": "..."
}
```

---

## 4. 词条管理接口（HTTP）

> 位于 `/api/v1/words`，全部需 `X-API-Key`。
> 任何写操作（Create / Update / Delete / Import）都会触发 **防抖 500ms**
> 的引擎重建（max-wait `3s`），并通过 Redis Pub/Sub 通知集群其他实例。

> 响应体公共结构 `WordResponse`（被 4.1–4.4 复用）：

| 字段           | 类型     | 说明                                                      |
| ------------ | ------ | ------------------------------------------------------- |
| `id`         | uint64 | 词条主键                                                    |
| `text`       | string | 敏感词文本（落库前会做归一化）                                         |
| `category`   | string | 分类枚举，见 [1.7](#17-公共枚举)                                  |
| `level`      | int    | 风险等级 1–4                                                |
| `status`     | int    | 状态：`0=inactive`、`1=active`                              |
| `tag`        | string | 自定义标签，可空字符串                                             |
| `created_at` | string | ISO 8601（RFC 3339），UTC                                  |
| `updated_at` | string | ISO 8601（RFC 3339），UTC                                  |

### 4.1 GET /api/v1/words

列表查询。Query 参数（`WordListRequest`）：


| 参数          | 类型     | 默认   | 约束              | 说明                |
| ----------- | ------ | ---- | --------------- | ----------------- |
| `category`  | string | -    | 任意              | 见 [1.7](#17-公共枚举) |
| `level`     | int    | `0`  | `0..4`，`0`=不过滤  | 风险等级              |
| `status`    | int    | `0`  | `0/1`（仅显式传入时生效） | 词条状态              |
| `keyword`   | string | -    | -               | 文本模糊匹配            |
| `page`      | int    | `1`  | `≥1`            | 页码                |
| `page_size` | int    | `20` | `1..100`        | 每页条数              |


> 注意：当前实现里 `status` 即便不传也会作为 `0` 落到查询条件之外（仓储仅使用 `category/level/keyword`），
> 默认不会按 `status` 过滤。

- 请求示例：

```bash
curl -s "http://localhost:8080/api/v1/words?category=custom&level=2&page=1&page_size=20" \
  -H "X-API-Key: censorhub-default-key"
```

- 响应体（`data` 为 `WordListResponse`）：

| 字段          | 类型                  | 说明                                          |
| ----------- | ------------------- | ------------------------------------------- |
| `items`     | array<WordResponse\> | 当前页词条列表，元素见上文 `WordResponse`               |
| `total`     | int64               | 满足查询条件的总条数                                  |
| `page`      | int                 | 回显当前页码                                      |
| `page_size` | int                 | 回显每页条数                                      |

- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 42,
        "text": "敏感词",
        "category": "custom",
        "level": 2,
        "status": 1,
        "tag": "demo",
        "created_at": "2026-05-15T03:14:15Z",
        "updated_at": "2026-05-15T03:14:15Z"
      }
    ],
    "total": 1,
    "page": 1,
    "page_size": 20
  }
}
```

### 4.2 POST /api/v1/words

创建单个词条。请求体（`CreateWordRequest`）：


| 字段         | 类型     | 必填  | 约束                |
| ---------- | ------ | --- | ----------------- |
| `text`     | string | 是   | 1–255             |
| `category` | string | 是   | 见 [1.7](#17-公共枚举) |
| `level`    | int    | 是   | `1..4`            |
| `tag`      | string | 否   | 自定义标签             |


- 错误：`10001` 已存在 / `10003` 非法分类 / `10004` 非法等级 / `10009` 参数错误。
- 请求示例：

```bash
curl -s -X POST http://localhost:8080/api/v1/words \
  -H "X-API-Key: censorhub-default-key" \
  -H "Content-Type: application/json" \
  -d '{"text":"敏感词","category":"custom","level":2,"tag":"demo"}'
```

- 响应体（HTTP `201`）：

| 字段        | 类型           | 说明                                          |
| --------- | ------------ | ------------------------------------------- |
| `code`    | int          | 业务码，固定 `0`                                  |
| `message` | string       | 固定 `"created"`                              |
| `data`    | WordResponse | 新创建的词条，结构见章节 4 顶部的 `WordResponse` 字段表       |

> 不同于其它接口，4.2 创建成功响应**不携带** `trace_id` 字段。

- 响应示例：

```json
{
  "code": 0,
  "message": "created",
  "data": {
    "id": 42,
    "text": "敏感词",
    "category": "custom",
    "level": 2,
    "status": 1,
    "tag": "demo",
    "created_at": "2026-05-15T03:14:15Z",
    "updated_at": "2026-05-15T03:14:15Z"
  }
}
```

### 4.3 GET /api/v1/words/:id

按主键查询。`id` 为 `uint64`。

- 错误：`10002` 词条不存在；`10009` ID 不是合法整数。
- 请求示例：

```bash
curl -s http://localhost:8080/api/v1/words/42 \
  -H "X-API-Key: censorhub-default-key"
```

- 响应体：`data` 为 `WordResponse`，字段表见章节 4 顶部。
- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 42,
    "text": "敏感词",
    "category": "custom",
    "level": 2,
    "status": 1,
    "tag": "demo",
    "created_at": "2026-05-15T03:14:15Z",
    "updated_at": "2026-05-15T03:14:15Z"
  },
  "trace_id": "..."
}
```

### 4.4 PUT /api/v1/words/:id

部分更新。请求体（`UpdateWordRequest`，所有字段 **可空**，仅传需要更新的字段）：


| 字段         | 类型      | 约束                |
| ---------- | ------- | ----------------- |
| `text`     | string? | 1–255             |
| `category` | string? | 见 [1.7](#17-公共枚举) |
| `level`    | int?    | `1..4`            |
| `status`   | int?    | `0..1`            |
| `tag`      | string? | -                 |


- 错误：`10002` / `10003` / `10004` / `10009`。
- 请求示例：

```bash
curl -s -X PUT http://localhost:8080/api/v1/words/42 \
  -H "X-API-Key: censorhub-default-key" \
  -H "Content-Type: application/json" \
  -d '{"level":3,"status":1}'
```

- 响应体：`data` 为更新后的 `WordResponse`，字段表见章节 4 顶部。
- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 42,
    "text": "敏感词",
    "category": "custom",
    "level": 3,
    "status": 1,
    "tag": "demo",
    "created_at": "2026-05-15T03:14:15Z",
    "updated_at": "2026-05-15T04:20:00Z"
  },
  "trace_id": "..."
}
```

### 4.5 DELETE /api/v1/words/:id

删除词条。

- 错误：`10002` 不存在；`10009` 非法 ID。
- 请求示例：

```bash
curl -s -X DELETE http://localhost:8080/api/v1/words/42 \
  -H "X-API-Key: censorhub-default-key"
```

- 响应体：

| 字段         | 类型     | 说明                                     |
| ---------- | ------ | -------------------------------------- |
| `code`     | int    | 固定 `0`                                 |
| `message`  | string | 固定 `"ok"`                              |
| `data`     | null   | 删除接口不返回业务数据                            |
| `trace_id` | string | 链路追踪 ID                                |

- 响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": null,
  "trace_id": "..."
}
```

### 4.6 POST /api/v1/words/import

批量导入。请求体（`ImportRequest`）：

```jsonc
{
  "words": [
    { "text":"...", "category":"custom", "level":2, "tag":"" }
  ]
}
```

- `words`：1–10000 条，每条遵循 [4.2](#42-post-apiv1words) 校验规则。
- 重复词条由数据库唯一索引去重（不视为失败）。
- 请求示例：

```bash
curl -s -X POST http://localhost:8080/api/v1/words/import \
  -H "X-API-Key: censorhub-default-key" \
  -H "Content-Type: application/json" \
  -d '{
    "words": [
      {"text":"广告 A","category":"ad","level":1},
      {"text":"非法分类示例","category":"unknown","level":2},
      {"text":"暴力 B","category":"violence","level":3,"tag":"v3"}
    ]
  }'
```

- 响应体（`data` 为 `ImportResponse`）：

| 字段         | 类型                    | 说明                                         |
| ---------- | --------------------- | ------------------------------------------ |
| `total`    | int                   | 客户端提交的总条数（即 `len(words)`）                  |
| `imported` | int                   | 实际成功写入数据库的条数                               |
| `skipped`  | int                   | 校验失败被跳过的条数（**不含** DB 唯一索引去重的条数）           |
| `failures` | array<ImportFailure\> | 校验失败明细列表；全部成功时省略此字段                       |

`ImportFailure` 元素结构：

| 字段       | 类型     | 说明                                              |
| -------- | ------ | ----------------------------------------------- |
| `index`  | int    | 在 `words` 数组中的下标（从 `0` 开始），便于客户端定位失败行           |
| `word`   | string | 失败行的 `text` 值，方便日志排查                            |
| `reason` | string | 失败原因，例如 `"invalid category"`、`"invalid risk level"`、`"word text exceeds maximum length"` |

- 响应示例（含部分校验失败）：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "total": 3,
    "imported": 2,
    "skipped": 1,
    "failures": [
      {"index":1,"word":"非法分类示例","reason":"invalid category"}
    ]
  },
  "trace_id": "..."
}
```

### 4.7 GET /api/v1/words/export

流式导出 CSV（无分页限制）。Query 参数：


| 参数         | 类型     | 说明       |
| ---------- | ------ | -------- |
| `category` | string | 可选，按分类筛选 |


- 请求示例：

```bash
curl -s "http://localhost:8080/api/v1/words/export?category=ad" \
  -H "X-API-Key: censorhub-default-key" -o words.csv
head -5 words.csv
```

- 响应头：

| 响应头                     | 值                                                       |
| ----------------------- | ------------------------------------------------------- |
| `Content-Type`          | `text/csv; charset=utf-8`                               |
| `Content-Disposition`   | `attachment; filename=sensitive_words.csv`              |
| `X-Request-ID`          | 由 `RequestID` 中间件注入的链路 ID                                |

- CSV 列说明（首行为表头 `text,category,level,tag`）：

| 列          | 类型     | 说明                                                       |
| ---------- | ------ | -------------------------------------------------------- |
| `text`     | string | 敏感词文本（已归一化）                                              |
| `category` | string | 分类枚举，见 [1.7](#17-公共枚举)                                   |
| `level`    | string | **字符串形式** 的风险等级：`low` / `medium` / `high` / `critical`    |
| `tag`      | string | 自定义标签，可空                                                 |

> 与 JSON 接口不同：CSV 中的 `level` **不是数字**，而是 `RiskLevel.String()` 输出的英文枚举名。

- 响应内容示例（`words.csv`）：

```csv
text,category,level,tag
广告 A,ad,low,
广告 B,ad,medium,promo
暴力 B,violence,high,v3
```

---

## 5. gRPC 接口（censor.v1.CensorService）

- proto 路径：`api/proto/censor/v1/censor.proto`
- 默认监听：`:9090`
- 拦截器链：`Recovery → Logging → RateLimit → Auth`，鉴权 metadata 键为 `x-api-key`。

> gRPC 仅暴露过滤侧（Detect / Replace / BatchDetect）；词条管理只走 HTTP。
> gRPC `FilterRequest` **没有** `strategy` 字段，由 RPC 名称决定策略。

服务定义（节选 `censor.proto`）：

```proto
service CensorService {
  rpc Detect(FilterRequest) returns (FilterResponse);
  rpc Replace(FilterRequest) returns (FilterResponse);
  rpc BatchDetect(BatchFilterRequest) returns (BatchFilterResponse);
}

message FilterRequest  { string text = 1; }
message BatchFilterRequest { repeated string texts = 1; }

message MatchItem {
  string word = 1; int32 position = 2; int32 end_position = 3;
  string category = 4; int32 level = 5;
}

message FilterResponse {
  string original = 1; string filtered = 2;
  bool is_hit = 3; int32 hit_count = 4;
  repeated MatchItem matches = 5;
  int32 risk_level = 6; int64 cost_ms = 7;
}

message BatchFilterResponse {
  repeated FilterResponse results = 1;
  int32 total = 2; int32 hit_num = 3;
}
```

错误映射：


| 业务情况                | gRPC code           |
| ------------------- | ------------------- |
| `text` / `texts` 为空 | `InvalidArgument`   |
| 鉴权失败                | `Unauthenticated`   |
| 限流                  | `ResourceExhausted` |
| 服务端异常               | `Internal`          |


### 5.1 Detect

- RPC：`censor.v1.CensorService/Detect`
- 入参：`FilterRequest{text}`；出参：`FilterResponse`
- `grpcurl` 示例：

```bash
grpcurl -plaintext \
  -H "x-api-key: censorhub-default-key" \
  -d '{"text":"这是一段包含敏感词的测试文本"}' \
  localhost:9090 censor.v1.CensorService/Detect
```

- 响应示例（`grpcurl` 输出 JSON 形式）：

```json
{
  "original": "这是一段包含敏感词的测试文本",
  "isHit": true,
  "hitCount": 1,
  "matches": [
    {"word":"敏感词","position":6,"endPosition":9,"category":"custom","level":2}
  ],
  "riskLevel": 2,
  "costMs": "1"
}
```

### 5.2 Replace

- RPC：`censor.v1.CensorService/Replace`
- 行为：响应 `filtered` 中命中部分被 `*` 替换。

```bash
grpcurl -plaintext \
  -H "x-api-key: censorhub-default-key" \
  -d '{"text":"这是一段包含敏感词的测试文本"}' \
  localhost:9090 censor.v1.CensorService/Replace
```

- 响应示例：

```json
{
  "original": "这是一段包含敏感词的测试文本",
  "filtered": "这是一段包含***的测试文本",
  "isHit": true,
  "hitCount": 1,
  "matches": [
    {"word":"敏感词","position":6,"endPosition":9,"category":"custom","level":2}
  ],
  "riskLevel": 2,
  "costMs": "1"
}
```

### 5.3 BatchDetect

- RPC：`censor.v1.CensorService/BatchDetect`
- 入参：`BatchFilterRequest{texts}`；出参：`BatchFilterResponse`
- 注意：**没有** `strategy` 字段，固定为 detect。

```bash
grpcurl -plaintext \
  -H "x-api-key: censorhub-default-key" \
  -d '{"texts":["普通文案","一段含敏感词的文案"]}' \
  localhost:9090 censor.v1.CensorService/BatchDetect
```

- 响应示例：

```json
{
  "results": [
    {"original":"普通文案","isHit":false,"hitCount":0,"riskLevel":0,"costMs":"0"},
    {
      "original":"一段含敏感词的文案",
      "isHit":true,
      "hitCount":1,
      "matches":[
        {"word":"敏感词","position":3,"endPosition":6,"category":"custom","level":2}
      ],
      "riskLevel":2,
      "costMs":"1"
    }
  ],
  "total": 2,
  "hitNum": 1
}
```

> 字段名风格说明：proto 字段 `is_hit`、`hit_count` 等在 `grpcurl` 默认输出
> 中转为 `isHit`、`hitCount` 的 lowerCamelCase；`int64` 字段（`cost_ms`）
> 在 JSON 中以 **字符串** 形式呈现。客户端用生成的 stub 调用时仍是原 snake_case。

### 5.4 健康检查（grpc.health.v1）

- 实现：标准 `google.golang.org/grpc/health` 服务。
- 服务名：`censor.v1.CensorService` 已被显式标记为 `SERVING`，可用于 K8s gRPC 探针。
- RPC：
  - `grpc.health.v1.Health/Check(HealthCheckRequest{service}) -> HealthCheckResponse{status}`
  - `grpc.health.v1.Health/Watch`（流式，状态变化时持续推送 `HealthCheckResponse`）

```bash
grpc_health_probe -addr=localhost:9090 -service=censor.v1.CensorService
# 或
grpcurl -plaintext -d '{"service":"censor.v1.CensorService"}' \
  localhost:9090 grpc.health.v1.Health/Check
```

- 响应示例：

```json
{ "status": "SERVING" }
```

`status` 取值：`UNKNOWN` / `SERVING` / `NOT_SERVING` / `SERVICE_UNKNOWN`（仅 Watch 流式中出现）。

---

## 附：完整接口一览


| #   | 协议   | 方法 / RPC     | 路径 / 全名                               | 鉴权  | 限流  | 说明                  |
| --- | ---- | ------------ | ------------------------------------- | --- | --- | ------------------- |
| 1   | HTTP | GET          | `/healthz`                            | 否   | 否   | 存活探针                |
| 2   | HTTP | GET          | `/readyz`                             | 否   | 否   | 就绪探针（含 DB/Redis/引擎） |
| 3   | HTTP | GET          | `/metrics`                            | 否   | 否   | Prometheus 指标       |
| 4   | HTTP | POST         | `/api/v1/filter/detect`               | 是   | 是   | 检测敏感词               |
| 5   | HTTP | POST         | `/api/v1/filter/replace`              | 是   | 是   | 替换敏感词为 `*`          |
| 6   | HTTP | POST         | `/api/v1/filter/highlight`            | 是   | 是   | `<mark>` 包裹敏感词      |
| 7   | HTTP | POST         | `/api/v1/filter/batch`                | 是   | 是   | 批量检测（≤100 条）        |
| 8   | HTTP | GET          | `/api/v1/words`                       | 是   | 是   | 词条列表 / 分页           |
| 9   | HTTP | POST         | `/api/v1/words`                       | 是   | 是   | 创建词条                |
| 10  | HTTP | GET          | `/api/v1/words/:id`                   | 是   | 是   | 词条详情                |
| 11  | HTTP | PUT          | `/api/v1/words/:id`                   | 是   | 是   | 更新词条                |
| 12  | HTTP | DELETE       | `/api/v1/words/:id`                   | 是   | 是   | 删除词条                |
| 13  | HTTP | POST         | `/api/v1/words/import`                | 是   | 是   | 批量导入（≤10000 条）      |
| 14  | HTTP | GET          | `/api/v1/words/export`                | 是   | 是   | CSV 流式导出            |
| 15  | gRPC | unary        | `censor.v1.CensorService/Detect`      | 是   | 是   | 检测                  |
| 16  | gRPC | unary        | `censor.v1.CensorService/Replace`     | 是   | 是   | 替换                  |
| 17  | gRPC | unary        | `censor.v1.CensorService/BatchDetect` | 是   | 是   | 批量检测                |
| 18  | gRPC | unary/stream | `grpc.health.v1.Health/Check`、`Watch` | 否   | 否   | 标准健康检查              |


