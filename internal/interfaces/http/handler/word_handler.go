package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/application/service"
	"github.com/bhrajate/censorhub/internal/interfaces/http/response"
)

// WordHandler 词条 CRUD handler
type WordHandler struct {
	wordService *service.WordAppService
}

// NewWordHandler 创建词条 handler
func NewWordHandler(wordService *service.WordAppService) *WordHandler {
	return &WordHandler{wordService: wordService}
}

// Create 创建词条
func (h *WordHandler) Create(c *gin.Context) {
	var req dto.CreateWordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.wordService.Create(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created", "data": result})
}

// Get 获取词条详情
func (h *WordHandler) Get(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.wordService.Get(c.Request.Context(), id)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Update 更新词条
func (h *WordHandler) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, err)
		return
	}

	var req dto.UpdateWordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.wordService.Update(c.Request.Context(), id, &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Delete 删除词条
func (h *WordHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, err)
		return
	}

	if err := h.wordService.Delete(c.Request.Context(), id); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// List 词条列表
func (h *WordHandler) List(c *gin.Context) {
	var req dto.WordListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	if req.Page == 0 {
		req.Page = 1
	}
	if req.PageSize == 0 {
		req.PageSize = 20
	}

	result, err := h.wordService.List(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Import 批量导入词条
func (h *WordHandler) Import(c *gin.Context) {
	var req dto.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	result, err := h.wordService.Import(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Export 导出词条（流式写入响应）
func (h *WordHandler) Export(c *gin.Context) {
	category := c.Query("category")

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=sensitive_words.csv")
	c.Status(http.StatusOK)

	if err := h.wordService.ExportToWriter(c.Request.Context(), category, c.Writer); err != nil {
		// Header 已发送，只能记录日志
		_ = err
	}
}
