package service

import (
	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// FilterEngine 过滤引擎接口（领域层定义，基础设施层实现）
type FilterEngine interface {
	// Match 返回文本中所有匹配的敏感词
	Match(text string) []valueobject.MatchItem

	// Rebuild 重建引擎（热更新时调用）
	Rebuild(words []*entity.SensitiveWord) error

	// WordCount 当前加载的词条数
	WordCount() int
}
