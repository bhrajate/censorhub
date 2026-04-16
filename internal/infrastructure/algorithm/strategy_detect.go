package algorithm

import (
	"time"

	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// DetectStrategy 检测策略：仅检测，返回匹配列表
type DetectStrategy struct{}

func NewDetectStrategy() *DetectStrategy {
	return &DetectStrategy{}
}

func (s *DetectStrategy) Name() valueobject.FilterStrategyType {
	return valueobject.StrategyDetect
}

func (s *DetectStrategy) Apply(original string, normalized string, matches []valueobject.MatchItem) *valueobject.FilterResult {
	return &valueobject.FilterResult{
		Original:    original,
		Filtered:    original,
		IsHit:       len(matches) > 0,
		HitCount:    len(matches),
		Matches:     matches,
		RiskLevel:   valueobject.MaxRiskLevel(matches),
		ProcessedAt: time.Now(),
	}
}
