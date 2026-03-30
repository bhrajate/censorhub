package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func authMiddleware(apiKeys []string) gin.HandlerFunc {
	keySet := make(map[string]bool, len(apiKeys))
	for _, k := range apiKeys {
		keySet[k] = true
	}

	return func(c *gin.Context) {
		// 如果没有配置 API Key，跳过认证
		if len(keySet) == 0 {
			c.Next()
			return
		}

		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			apiKey = c.Query("api_key")
		}

		if !keySet[apiKey] {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    10008,
				"message": "unauthorized: invalid or missing API key",
			})
			return
		}

		c.Next()
	}
}
