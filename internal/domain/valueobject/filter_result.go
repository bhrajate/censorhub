package valueobject

import "time"

// MatchItem 单个匹配项
type MatchItem struct {
	Word     string    // 匹配到的敏感词
	Position int       // 在原文中的起始位置（rune index）
	EndPos   int       // 结束位置（rune index，不含）
	Category Category  // 分类
	Level    RiskLevel // 风险等级
}

// FilterResult 过滤结果值对象
type FilterResult struct {
	Original    string      // 原始文本
	Filtered    string      // 处理后的文本（Replace/Highlight 策略时填充）
	IsHit       bool        // 是否命中
	HitCount    int         // 命中次数
	Matches     []MatchItem // 匹配详情
	RiskLevel   RiskLevel   // 最高风险等级
	ProcessedAt time.Time   // 处理时间
	CostMs      int64       // 处理耗时（毫秒）
}

// MaxRiskLevel 从匹配结果中获取最高风险等级
func MaxRiskLevel(matches []MatchItem) RiskLevel {
	var max RiskLevel
	for _, m := range matches {
		if m.Level > max {
			max = m.Level
		}
	}
	return max
}
