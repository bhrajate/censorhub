package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/bhrajate/censorhub/internal/infrastructure/config"
)

// Middleware 中间件集合
type Middleware struct {
	cfg    *config.Config
	logger *zap.Logger
}

// NewMiddleware 创建中间件集合
func NewMiddleware(cfg *config.Config, logger *zap.Logger) *Middleware {
	return &Middleware{cfg: cfg, logger: logger}
}

// Recovery Panic 恢复
func (m *Middleware) Recovery() gin.HandlerFunc {
	return recoveryMiddleware(m.logger)
}

// RequestID 请求 ID
func (m *Middleware) RequestID() gin.HandlerFunc {
	return requestIDMiddleware()
}

// Logger 请求日志
func (m *Middleware) Logger() gin.HandlerFunc {
	return loggerMiddleware(m.logger)
}

// CORS 跨域
func (m *Middleware) CORS() gin.HandlerFunc {
	return corsMiddleware(m.cfg.CORS.AllowedOrigins)
}

// RateLimit 限流
func (m *Middleware) RateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(m.cfg.RateLimit.RequestsPerSecond, m.cfg.RateLimit.Burst)
}

// Auth 认证
func (m *Middleware) Auth() gin.HandlerFunc {
	return authMiddleware(m.cfg.Auth.APIKeys)
}

// Metrics Prometheus 指标
func (m *Middleware) Metrics() gin.HandlerFunc {
	return metricsMiddleware()
}

// Tracing 链路追踪
func (m *Middleware) Tracing() gin.HandlerFunc {
	return tracingMiddleware()
}

// BodyLimit 请求体大小限制
func (m *Middleware) BodyLimit() gin.HandlerFunc {
	return bodyLimitMiddleware(m.cfg.Server.HTTP.MaxBodySize)
}
