package algorithm

import (
	"sort"
	"strings"
	"time"

	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// ReplaceStrategy 替换策略：将匹配词替换为 * 号
type ReplaceStrategy struct{}

func NewReplaceStrategy() *ReplaceStrategy {
	return &ReplaceStrategy{}
}

func (s *ReplaceStrategy) Name() valueobject.FilterStrategyType {
	return valueobject.StrategyReplace
}

func (s *ReplaceStrategy) Apply(original string, matches []valueobject.MatchItem) *valueobject.FilterResult {
	filtered := replaceMatches(original, matches, '*')
	return &valueobject.FilterResult{
		Original:    original,
		Filtered:    filtered,
		IsHit:       len(matches) > 0,
		HitCount:    len(matches),
		Matches:     matches,
		RiskLevel:   valueobject.MaxRiskLevel(matches),
		ProcessedAt: time.Now(),
	}
}

// replaceMatches 按匹配位置替换原文中的敏感词
// 处理重叠匹配：合并重叠区间后统一替换
func replaceMatches(original string, matches []valueobject.MatchItem, mask rune) string {
	if len(matches) == 0 {
		return original
	}

	runes := []rune(original)
	normalizedRunes := []rune(Normalize(original))

	// 按起始位置排序
	sorted := make([]valueobject.MatchItem, len(matches))
	copy(sorted, matches)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Position < sorted[j].Position
	})

	// 构建替换掩码（在 normalized runes 上标记需要替换的位置）
	masked := make([]bool, len(normalizedRunes))
	for _, m := range sorted {
		for i := m.Position; i < m.EndPos && i < len(masked); i++ {
			masked[i] = true
		}
	}

	// 将 normalized 上的掩码映射回原始 runes
	// 因为归一化可能改变长度，这里做保守映射
	if len(runes) == len(normalizedRunes) {
		// 长度一致，直接映射
		var b strings.Builder
		b.Grow(len(runes) * 3)
		for i, r := range runes {
			if masked[i] {
				b.WriteRune(mask)
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	// 长度不一致时，退回到使用归一化后的文本替换
	var b strings.Builder
	b.Grow(len(normalizedRunes) * 3)
	for i, r := range normalizedRunes {
		if masked[i] {
			b.WriteRune(mask)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
