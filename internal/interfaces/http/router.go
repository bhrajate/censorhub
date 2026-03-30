package http

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bhrajate/censorhub/internal/interfaces/http/handler"
	"github.com/bhrajate/censorhub/internal/interfaces/middleware"
)

// NewRouter 创建 Gin 路由
func NewRouter(
	filterHandler *handler.FilterHandler,
	wordHandler *handler.WordHandler,
	healthHandler *handler.HealthHandler,
	mw *middleware.Middleware,
) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// 全局中间件
	r.Use(mw.Recovery())
	r.Use(mw.RequestID())
	r.Use(mw.Logger())
	r.Use(mw.CORS())
	r.Use(mw.Metrics())
	r.Use(mw.Tracing())

	// 健康检查（无需认证和限流）
	r.GET("/healthz", healthHandler.Liveness)
	r.GET("/readyz", healthHandler.Readiness)

	// Prometheus metrics
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API v1
	v1 := r.Group("/api/v1")
	v1.Use(mw.RateLimit())
	v1.Use(mw.Auth())
	{
		// 过滤接口
		filter := v1.Group("/filter")
		filter.POST("/detect", filterHandler.Detect)
		filter.POST("/replace", filterHandler.Replace)
		filter.POST("/highlight", filterHandler.Highlight)
		filter.POST("/batch", filterHandler.BatchDetect)

		// 词条管理
		words := v1.Group("/words")
		words.GET("", wordHandler.List)
		words.POST("", wordHandler.Create)
		words.GET("/:id", wordHandler.Get)
		words.PUT("/:id", wordHandler.Update)
		words.DELETE("/:id", wordHandler.Delete)
		words.POST("/import", wordHandler.Import)
		words.GET("/export", wordHandler.Export)
	}

	return r
}
