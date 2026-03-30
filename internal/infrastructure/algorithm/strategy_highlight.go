package algorithm

import (
	"sort"
	"strings"
	"time"

	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// HighlightStrategy 高亮策略：用 <mark></mark> 包裹匹配词
type HighlightStrategy struct{}

func NewHighlightStrategy() *HighlightStrategy {
	return &HighlightStrategy{}
}

func (s *HighlightStrategy) Name() valueobject.FilterStrategyType {
	return valueobject.StrategyHighlight
}

func (s *HighlightStrategy) Apply(original string, matches []valueobject.MatchItem) *valueobject.FilterResult {
	filtered := highlightMatches(original, matches)
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

// interval 表示一个合并后的区间
type interval struct {
	start, end int
}

// mergeIntervals 合并重叠区间
func mergeIntervals(matches []valueobject.MatchItem) []interval {
	if len(matches) == 0 {
		return nil
	}

	sorted := make([]valueobject.MatchItem, len(matches))
	copy(sorted, matches)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Position < sorted[j].Position
	})

	intervals := []interval{{sorted[0].Position, sorted[0].EndPos}}
	for _, m := range sorted[1:] {
		last := &intervals[len(intervals)-1]
		if m.Position <= last.end {
			if m.EndPos > last.end {
				last.end = m.EndPos
			}
		} else {
			intervals = append(intervals, interval{m.Position, m.EndPos})
		}
	}
	return intervals
}

func highlightMatches(original string, matches []valueobject.MatchItem) string {
	if len(matches) == 0 {
		return original
	}

	runes := []rune(original)
	normalizedRunes := []rune(Normalize(original))
	intervals := mergeIntervals(matches)

	// 如果长度一致，直接在原始 runes 上插入标记
	targetRunes := runes
	if len(runes) != len(normalizedRunes) {
		targetRunes = normalizedRunes
	}

	var b strings.Builder
	b.Grow(len(targetRunes)*3 + len(intervals)*13)

	idx := 0
	for _, iv := range intervals {
		// 写入区间前的文本
		for i := idx; i < iv.start && i < len(targetRunes); i++ {
			b.WriteRune(targetRunes[i])
		}
		// 写入高亮标记
		b.WriteString("<mark>")
		for i := iv.start; i < iv.end && i < len(targetRunes); i++ {
			b.WriteRune(targetRunes[i])
		}
		b.WriteString("</mark>")
		idx = iv.end
	}
	// 写入剩余文本
	for i := idx; i < len(targetRunes); i++ {
		b.WriteRune(targetRunes[i])
	}

	return b.String()
}
