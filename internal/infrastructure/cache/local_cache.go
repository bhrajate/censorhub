package cache

import (
	"context"
	"strings"
	"sync"
	"time"
)

type cacheItem struct {
	value     []byte
	expiredAt time.Time
}

// LocalCache 进程内缓存（L1 缓存）
type LocalCache struct {
	mu    sync.RWMutex
	items map[string]*cacheItem
	ttl   time.Duration
}

// NewLocalCache 创建本地缓存，ctx 取消时清理协程自动退出
func NewLocalCache(ctx context.Context, ttl time.Duration) *LocalCache {
	lc := &LocalCache{
		items: make(map[string]*cacheItem),
		ttl:   ttl,
	}
	// 后台清理过期缓存
	go lc.cleanup(ctx)
	return lc
}

func (c *LocalCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(item.expiredAt) {
		return nil, false
	}
	return item.value, true
}

func (c *LocalCache) Set(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = &cacheItem{
		value:     value,
		expiredAt: time.Now().Add(c.ttl),
	}
}

func (c *LocalCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// DeleteByPrefix 按前缀删除匹配的缓存条目
func (c *LocalCache) DeleteByPrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if strings.HasPrefix(k, prefix) {
			delete(c.items, k)
		}
	}
}

// InvalidateAll 清空所有缓存
func (c *LocalCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*cacheItem)
}

// maxEvictPerCycle 每轮清理最多淘汰的条目数，避免长时间持锁
const maxEvictPerCycle = 1000

func (c *LocalCache) cleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.evictExpired()
		}
	}
}

// evictExpired 分批清理过期条目，每次最多清理 maxEvictPerCycle 个
func (c *LocalCache) evictExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	evicted := 0
	for k, v := range c.items {
		if now.After(v.expiredAt) {
			delete(c.items, k)
			evicted++
			if evicted >= maxEvictPerCycle {
				break
			}
		}
	}
}
