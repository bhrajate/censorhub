package valueobject

// FilterStrategyType 过滤策略类型
type FilterStrategyType string

const (
	StrategyDetect    FilterStrategyType = "detect"
	StrategyReplace   FilterStrategyType = "replace"
	StrategyHighlight FilterStrategyType = "highlight"
)

func (s FilterStrategyType) IsValid() bool {
	return s == StrategyDetect || s == StrategyReplace || s == StrategyHighlight
}

// FilterStrategy 过滤策略接口
type FilterStrategy interface {
	Name() FilterStrategyType
	Apply(original string, matches []MatchItem) *FilterResult
}
