package service

import (
	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// MatchResult 匹配结果，包含匹配项和归一化后的文本
type MatchResult struct {
	Matches        []valueobject.MatchItem
	NormalizedText string
}

// FilterEngine 过滤引擎接口（领域层定义，基础设施层实现）
type FilterEngine interface {
	// Match 返回文本中所有匹配的敏感词及归一化后的文本
	Match(text string) MatchResult

	// Rebuild 重建引擎（热更新时调用）
	Rebuild(words []*entity.SensitiveWord) error

	// WordCount 当前加载的词条数
	WordCount() int

	// Version 返回引擎当前版本号；每次 Rebuild 成功必然递增。
	// 上层用它给 filter cache key 加版本号，引擎一更新所有旧 cache 自然失效，
	// 避免 InvalidateByPrefix(SCAN) 在并发写入下漏清的 race。
	Version() uint64
}
