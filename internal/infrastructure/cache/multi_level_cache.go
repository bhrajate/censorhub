package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// MultiLevelCache 多级缓存编排，Redis 故障时自动降级为仅 L1
type MultiLevelCache struct {
	local   *LocalCache
	redis   *RedisCache
	breaker *CircuitBreaker
}

// NewMultiLevelCache 创建多级缓存
func NewMultiLevelCache(local *LocalCache, redis *RedisCache) *MultiLevelCache {
	return &MultiLevelCache{
		local:   local,
		redis:   redis,
		breaker: NewCircuitBreaker(5, 10*time.Second), // 连续 5 次失败熔断，10s 后探测
	}
}

// Get 读取：L1 -> L2（熔断时跳过 L2）
func (c *MultiLevelCache) Get(ctx context.Context, key string) ([]byte, error) {
	// L1
	if v, ok := c.local.Get(key); ok {
		return v, nil
	}

	// L2（受熔断器保护）
	if !c.breaker.Allow() {
		return nil, redis.Nil // 熔断中，视为缓存未命中
	}

	v, err := c.redis.Get(ctx, key)
	if err != nil {
		if err != redis.Nil {
			c.breaker.RecordFailure()
		}
		return nil, err
	}

	c.breaker.RecordSuccess()

	// 回填 L1
	c.local.Set(key, v)
	return v, nil
}

// Set 写入 L1 + L2（熔断时仅写 L1）
func (c *MultiLevelCache) Set(ctx context.Context, key string, value []byte) error {
	c.local.Set(key, value)

	if !c.breaker.Allow() {
		return nil // 熔断中，仅写 L1
	}

	if err := c.redis.Set(ctx, key, value); err != nil {
		c.breaker.RecordFailure()
		return nil // 降级，不向上层报错
	}
	c.breaker.RecordSuccess()
	return nil
}

// Invalidate 失效 L1 + L2
func (c *MultiLevelCache) Invalidate(ctx context.Context, key string) error {
	c.local.Delete(key)

	if !c.breaker.Allow() {
		return nil
	}

	if err := c.redis.Delete(ctx, key); err != nil {
		c.breaker.RecordFailure()
		return nil
	}
	c.breaker.RecordSuccess()
	return nil
}

// InvalidateByPrefix 按前缀失效
func (c *MultiLevelCache) InvalidateByPrefix(ctx context.Context, prefix string) error {
	c.local.DeleteByPrefix(prefix)

	if !c.breaker.Allow() {
		return nil
	}

	if err := c.redis.DeleteByPrefix(ctx, prefix); err != nil {
		c.breaker.RecordFailure()
		return nil
	}
	c.breaker.RecordSuccess()
	return nil
}

// IsNotFound 判断是否为缓存未命中
func IsNotFound(err error) bool {
	return err == redis.Nil
}
