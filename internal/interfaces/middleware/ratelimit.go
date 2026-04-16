package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// perClientLimiter 按客户端维度的限流器
type perClientLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*clientEntry
	rps      rate.Limit
	burst    int
}

type clientEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newPerClientLimiter(rps int, burst int) *perClientLimiter {
	pcl := &perClientLimiter{
		limiters: make(map[string]*clientEntry),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
	// 后台清理过期客户端条目（每 5 分钟清理 10 分钟未活跃的）
	go pcl.cleanup()
	return pcl
}

func (l *perClientLimiter) getLimiter(key string) *rate.Limiter {
	l.mu.RLock()
	entry, ok := l.limiters[key]
	l.mu.RUnlock()
	if ok {
		l.mu.Lock()
		entry.lastSeen = time.Now()
		l.mu.Unlock()
		return entry.limiter
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	// 双重检查
	if entry, ok := l.limiters[key]; ok {
		entry.lastSeen = time.Now()
		return entry.limiter
	}
	lim := rate.NewLimiter(l.rps, l.burst)
	l.limiters[key] = &clientEntry{limiter: lim, lastSeen: time.Now()}
	return lim
}

func (l *perClientLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for key, entry := range l.limiters {
			if entry.lastSeen.Before(cutoff) {
				delete(l.limiters, key)
			}
		}
		l.mu.Unlock()
	}
}

func rateLimitMiddleware(rps int, burst int) gin.HandlerFunc {
	pcl := newPerClientLimiter(rps, burst)
	return func(c *gin.Context) {
		// 优先使用 API Key 作为限流维度，其次使用客户端 IP
		key := c.GetHeader("X-API-Key")
		if key == "" {
			key = c.ClientIP()
		}

		if !pcl.getLimiter(key).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    10007,
				"message": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}
