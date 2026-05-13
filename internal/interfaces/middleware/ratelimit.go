package middleware

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// perClientLimiter 按客户端维度的限流器
//
// 使用 sync.Map 作为主存储，消除 RWMutex 的双锁 dance。
// clientEntry.lastSeen 改用 atomic.Int64（UnixNano），避免"命中缓存却仍要写锁"的场景。
type perClientLimiter struct {
	limiters sync.Map // map[string]*clientEntry
	rps      rate.Limit
	burst    int
}

type clientEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64 // UnixNano
}

func newPerClientLimiter(rps int, burst int) *perClientLimiter {
	pcl := &perClientLimiter{
		rps:   rate.Limit(rps),
		burst: burst,
	}
	go pcl.cleanup()
	return pcl
}

func (l *perClientLimiter) getLimiter(key string) *rate.Limiter {
	now := time.Now().UnixNano()

	if v, ok := l.limiters.Load(key); ok {
		entry := v.(*clientEntry)
		entry.lastSeen.Store(now)
		return entry.limiter
	}

	// 未命中，构造新条目；LoadOrStore 保证并发写入时只保留一份
	e := &clientEntry{limiter: rate.NewLimiter(l.rps, l.burst)}
	e.lastSeen.Store(now)
	actual, _ := l.limiters.LoadOrStore(key, e)
	entry := actual.(*clientEntry)
	// 如果 LoadOrStore 返回的是别人先放进去的，也要把 lastSeen 刷新
	entry.lastSeen.Store(now)
	return entry.limiter
}

func (l *perClientLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute).UnixNano()
		l.limiters.Range(func(key, value any) bool {
			if value.(*clientEntry).lastSeen.Load() < cutoff {
				l.limiters.Delete(key)
			}
			return true
		})
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
