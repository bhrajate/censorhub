package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/application/service"
	"github.com/bhrajate/censorhub/internal/interfaces/http/response"
)

// FilterHandler 过滤 HTTP handler
type FilterHandler struct {
	filterService *service.FilterAppService
}

// NewFilterHandler 创建过滤 handler
func NewFilterHandler(filterService *service.FilterAppService) *FilterHandler {
	return &FilterHandler{filterService: filterService}
}

// Detect 检测文本中的敏感词
func (h *FilterHandler) Detect(c *gin.Context) {
	var req dto.FilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.filterService.Detect(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Replace 替换文本中的敏感词
func (h *FilterHandler) Replace(c *gin.Context) {
	var req dto.FilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.filterService.Replace(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Highlight 高亮标记文本中的敏感词
func (h *FilterHandler) Highlight(c *gin.Context) {
	var req dto.FilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.filterService.Highlight(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// BatchDetect 批量检测
func (h *FilterHandler) BatchDetect(c *gin.Context) {
	var req dto.BatchFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.filterService.BatchDetect(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}
