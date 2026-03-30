package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// bodyLimitMiddleware 限制请求体大小，防止超大 payload 耗尽内存
func bodyLimitMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
