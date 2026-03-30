# Stage 1: Build
# 提示：生产部署前，请将镜像 tag 替换为 digest 以锁定版本
# 运行 `docker pull golang:1.22-alpine && docker inspect --format='{{index .RepoDigests 0}}' golang:1.22-alpine` 获取 digest
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /app

# 依赖缓存
COPY go.mod go.sum ./
RUN go mod download

# 编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /censorhub ./cmd/server

# Stage 2: Runtime
# 提示：同上，生产部署前请替换为 digest
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -g '' appuser

COPY --from=builder /censorhub /usr/local/bin/censorhub

# 复制所有环境配置（基础 + 环境覆盖）
COPY configs/config.yaml /etc/censorhub/config.yaml
COPY configs/config.*.yaml /etc/censorhub/

# 通过 APP_ENV 环境变量控制环境（默认 production）
ENV APP_ENV=production

USER appuser

EXPOSE 8080 9090

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["censorhub"]
CMD ["--config", "/etc/censorhub/config.yaml"]
