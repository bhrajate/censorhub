package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/bhrajate/censorhub/internal/application/service"
)

// HealthHandler 健康检查 handler
type HealthHandler struct {
	db            *gorm.DB
	filterService *service.FilterAppService
}

// NewHealthHandler 创建健康检查 handler
func NewHealthHandler(db *gorm.DB, filterService *service.FilterAppService) *HealthHandler {
	return &HealthHandler{db: db, filterService: filterService}
}

// Liveness 存活探针
func (h *HealthHandler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

// Readiness 就绪探针
func (h *HealthHandler) Readiness(c *gin.Context) {
	// 检查数据库连接
	sqlDB, err := h.db.DB()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "error": "database unavailable"})
		return
	}
	if err := sqlDB.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "error": "database ping failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ready",
		"word_count": h.filterService.EngineWordCount(),
	})
}
