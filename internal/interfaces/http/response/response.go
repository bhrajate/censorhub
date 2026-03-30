package response

import (
	"net/http"

	"github.com/gin-gonic/gin"

	pkgerrors "github.com/bhrajate/censorhub/pkg/errors"
)

// Response 统一响应格式
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data:    data,
		TraceID: c.GetString("trace_id"),
	})
}

// Error 错误响应
func Error(c *gin.Context, err error) {
	if bizErr, ok := pkgerrors.IsBizError(err); ok {
		c.JSON(bizErr.HTTPStatus, Response{
			Code:    bizErr.Code,
			Message: bizErr.Message,
			TraceID: c.GetString("trace_id"),
		})
		return
	}
	c.JSON(http.StatusInternalServerError, Response{
		Code:    pkgerrors.ErrInternal.Code,
		Message: "internal server error",
		TraceID: c.GetString("trace_id"),
	})
}

// BadRequest 参数错误
func BadRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, Response{
		Code:    pkgerrors.ErrInvalidRequest.Code,
		Message: err.Error(),
		TraceID: c.GetString("trace_id"),
	})
}
