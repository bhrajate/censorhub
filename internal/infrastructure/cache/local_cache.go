package cache

import (
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

// NewLocalCache 创建本地缓存
func NewLocalCache(ttl time.Duration) *LocalCache {
	lc := &LocalCache{
		items: make(map[string]*cacheItem),
		ttl:   ttl,
	}
	// 后台清理过期缓存
	go lc.cleanup()
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

// InvalidateAll 清空所有缓存
func (c *LocalCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*cacheItem)
}

func (c *LocalCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.expiredAt) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}
