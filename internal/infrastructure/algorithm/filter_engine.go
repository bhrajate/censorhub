package algorithm

import (
	"sync"
	"sync/atomic"

	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// ACFilterEngine 基于 AC 自动机的过滤引擎实现
// 使用 atomic.Value 实现无锁读 + 安全热更新
type ACFilterEngine struct {
	current atomic.Value // *AhoCorasick
	mu      sync.Mutex   // 防止并发重建
}

// NewACFilterEngine 创建过滤引擎实例
func NewACFilterEngine() *ACFilterEngine {
	e := &ACFilterEngine{}
	// 初始化空的 AC 自动机
	e.current.Store(NewAhoCorasick(nil))
	return e
}

// Match 返回文本中所有匹配的敏感词（无锁读）
func (e *ACFilterEngine) Match(text string) []valueobject.MatchItem {
	ac := e.current.Load().(*AhoCorasick)
	return ac.Search(text)
}

// Rebuild 从词条列表重建 AC 自动机（线程安全）
func (e *ACFilterEngine) Rebuild(words []*entity.SensitiveWord) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	entries := make([]WordEntry, 0, len(words))
	for _, w := range words {
		if !w.IsActive() {
			continue
		}
		entries = append(entries, WordEntry{
			Text:     w.Text,
			Category: w.Category,
			Level:    w.Level,
		})
	}

	newAC := NewAhoCorasick(entries)
	e.current.Store(newAC) // 原子替换，读不阻塞
	return nil
}

// WordCount 当前加载的词条数
func (e *ACFilterEngine) WordCount() int {
	ac := e.current.Load().(*AhoCorasick)
	return ac.WordCount()
}
