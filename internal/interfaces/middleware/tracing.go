package middleware

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tracingMiddleware 返回链路追踪中间件。
// enabled=false 时直接透传，不创建 span、不设置 header，避免在采样率为 0 的部署
// （压测或非观测场景）上白白吃掉每请求 ~10–50 μs 的 span 分配与属性写入开销。
func tracingMiddleware(enabled bool) gin.HandlerFunc {
	if !enabled {
		return func(c *gin.Context) { c.Next() }
	}

	tracer := otel.Tracer("censorhub-http")
	return func(c *gin.Context) {
		spanName := c.FullPath()
		if spanName == "" {
			spanName = c.Request.URL.Path
		}

		ctx, span := tracer.Start(c.Request.Context(), spanName,
			trace.WithAttributes(
				attribute.String("http.method", c.Request.Method),
				attribute.String("http.url", c.Request.URL.String()),
				attribute.String("http.client_ip", c.ClientIP()),
			),
		)
		defer span.End()

		c.Request = c.Request.WithContext(ctx)

		if span.SpanContext().HasTraceID() {
			traceID := span.SpanContext().TraceID().String()
			c.Set("trace_id", traceID)
			c.Header("X-Trace-ID", traceID)
		}

		c.Next()

		span.SetAttributes(
			attribute.Int("http.status_code", c.Writer.Status()),
			attribute.Int("http.response_size", c.Writer.Size()),
		)
	}
}
