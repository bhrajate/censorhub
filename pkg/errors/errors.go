package errors

import (
	"fmt"
	"net/http"
)

// BizError 业务错误
type BizError struct {
	Code       int    `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *BizError) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// New 创建业务错误
func New(code int, message string, httpStatus int) *BizError {
	return &BizError{Code: code, Message: message, HTTPStatus: httpStatus}
}

// 预定义业务错误码
var (
	ErrWordAlreadyExists = New(10001, "word already exists", http.StatusConflict)
	ErrWordNotFound      = New(10002, "word not found", http.StatusNotFound)
	ErrInvalidCategory   = New(10003, "invalid category", http.StatusBadRequest)
	ErrInvalidRiskLevel  = New(10004, "invalid risk level", http.StatusBadRequest)
	ErrTextTooLong       = New(10005, "text too long", http.StatusBadRequest)
	ErrImportFailed      = New(10006, "import failed", http.StatusInternalServerError)
	ErrRateLimitExceeded = New(10007, "rate limit exceeded", http.StatusTooManyRequests)
	ErrUnauthorized      = New(10008, "unauthorized", http.StatusUnauthorized)
	ErrInvalidRequest    = New(10009, "invalid request", http.StatusBadRequest)
	ErrInternal          = New(10010, "internal server error", http.StatusInternalServerError)
)

// IsBizError 判断是否为业务错误
func IsBizError(err error) (*BizError, bool) {
	if bizErr, ok := err.(*BizError); ok {
		return bizErr, true
	}
	return nil, false
}

// Wrap 包装业务错误并附加详情
func Wrap(err *BizError, detail string) *BizError {
	return &BizError{
		Code:       err.Code,
		Message:    err.Message + ": " + detail,
		HTTPStatus: err.HTTPStatus,
	}
}
