package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// MultiLevelCache 多级缓存编排
type MultiLevelCache struct {
	local *LocalCache
	redis *RedisCache
}

// NewMultiLevelCache 创建多级缓存
func NewMultiLevelCache(local *LocalCache, redis *RedisCache) *MultiLevelCache {
	return &MultiLevelCache{
		local: local,
		redis: redis,
	}
}

// Get 读取：L1 -> L2
func (c *MultiLevelCache) Get(ctx context.Context, key string) ([]byte, error) {
	// L1
	if v, ok := c.local.Get(key); ok {
		return v, nil
	}

	// L2
	v, err := c.redis.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	// 回填 L1
	c.local.Set(key, v)
	return v, nil
}

// Set 写入 L1 + L2
func (c *MultiLevelCache) Set(ctx context.Context, key string, value []byte) error {
	c.local.Set(key, value)
	return c.redis.Set(ctx, key, value)
}

// Invalidate 失效 L1 + L2
func (c *MultiLevelCache) Invalidate(ctx context.Context, key string) error {
	c.local.Delete(key)
	return c.redis.Delete(ctx, key)
}

// InvalidateByPrefix 按前缀失效
func (c *MultiLevelCache) InvalidateByPrefix(ctx context.Context, prefix string) error {
	c.local.InvalidateAll() // 本地缓存全清（简单策略）
	return c.redis.DeleteByPrefix(ctx, prefix)
}

// IsNotFound 判断是否为缓存未命中
func IsNotFound(err error) bool {
	return err == redis.Nil
}
