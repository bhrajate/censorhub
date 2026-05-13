package cache

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

// localCacheShards 分片数量；取 2 的幂便于用位运算代替取模。
// 32 分片在 16 逻辑核机器上足够稀释 RWMutex 争抢。
const localCacheShards = 32

type cacheItem struct {
	value     []byte
	expiredAt time.Time
}

type cacheShard struct {
	mu    sync.RWMutex
	items map[string]*cacheItem
}

// LocalCache 进程内缓存（L1 缓存），内部按 key 哈希分片以降低锁竞争。
type LocalCache struct {
	shards   [localCacheShards]*cacheShard
	ttl      time.Duration
	maxItems int // 最大条目数，0 表示不限制；分片各自按 maxItems/shards 控制容量
}

// NewLocalCache 创建本地缓存，ctx 取消时清理协程自动退出
func NewLocalCache(ctx context.Context, ttl time.Duration, maxItems int) *LocalCache {
	lc := &LocalCache{
		ttl:      ttl,
		maxItems: maxItems,
	}
	for i := range lc.shards {
		lc.shards[i] = &cacheShard{items: make(map[string]*cacheItem)}
	}
	go lc.cleanup(ctx)
	return lc
}

// shardFor 用 FNV-1a 哈希把 key 分散到固定 shard。
// 使用位运算 & (N-1) 取模，依赖 localCacheShards 必须是 2 的幂。
func (c *LocalCache) shardFor(key string) *cacheShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return c.shards[h.Sum32()&(localCacheShards-1)]
}

// perShardLimit 计算单分片允许的最大条目数；0 表示不限。
func (c *LocalCache) perShardLimit() int {
	if c.maxItems <= 0 {
		return 0
	}
	if c.maxItems < localCacheShards {
		return 1
	}
	return c.maxItems / localCacheShards
}

func (c *LocalCache) Get(key string) ([]byte, bool) {
	s := c.shardFor(key)
	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(item.expiredAt) {
		return nil, false
	}
	return item.value, true
}

func (c *LocalCache) Set(key string, value []byte) {
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	// 容量保护：超限时跳过新 key 写入（已有 key 更新不受限）
	if limit := c.perShardLimit(); limit > 0 && len(s.items) >= limit {
		if _, exists := s.items[key]; !exists {
			return
		}
	}

	s.items[key] = &cacheItem{
		value:     value,
		expiredAt: time.Now().Add(c.ttl),
	}
}

func (c *LocalCache) Delete(key string) {
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
}

// DeleteByPrefix 按前缀删除匹配的缓存条目，遍历全部分片。
func (c *LocalCache) DeleteByPrefix(prefix string) {
	for _, s := range c.shards {
		s.mu.Lock()
		for k := range s.items {
			if strings.HasPrefix(k, prefix) {
				delete(s.items, k)
			}
		}
		s.mu.Unlock()
	}
}

// InvalidateAll 清空所有缓存（全分片）。
func (c *LocalCache) InvalidateAll() {
	for _, s := range c.shards {
		s.mu.Lock()
		s.items = make(map[string]*cacheItem)
		s.mu.Unlock()
	}
}

// maxEvictPerShardPerCycle 单分片每轮最多淘汰数，避免长时间持锁
const maxEvictPerShardPerCycle = 1000

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

// evictExpired 分批清理各分片过期条目。
// 分片独立加锁，避免单把大锁导致的清理期阻塞。
func (c *LocalCache) evictExpired() {
	now := time.Now()
	for _, s := range c.shards {
		s.mu.Lock()
		evicted := 0
		for k, v := range s.items {
			if now.After(v.expiredAt) {
				delete(s.items, k)
				evicted++
				if evicted >= maxEvictPerShardPerCycle {
					break
				}
			}
		}
		s.mu.Unlock()
	}
}
