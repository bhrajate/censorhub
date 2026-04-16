package algorithm

import (
	"strings"
	"testing"

	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

func TestHighlightStrategy_XSSPrevention(t *testing.T) {
	strategy := NewHighlightStrategy()

	// 构造包含 HTML 特殊字符的输入文本，其中 "赌博" 是敏感词
	matches := []valueobject.MatchItem{
		{Word: "赌博", Position: 8, EndPos: 10, Category: valueobject.CategoryCustom, Level: valueobject.RiskMedium},
	}

	// 原文包含 <script> 标签
	original := "<script>alert('xss')</script>赌博内容"
	normalized := Normalize(original)

	result := strategy.Apply(original, normalized, matches)

	// 输出中不应包含未转义的 <script>
	if strings.Contains(result.Filtered, "<script>") {
		t.Errorf("XSS vulnerability: output contains unescaped <script> tag: %s", result.Filtered)
	}
	// 应该包含转义后的 &lt;script&gt;
	if !strings.Contains(result.Filtered, "&lt;script&gt;") {
		t.Errorf("expected HTML-escaped <script> tag, got: %s", result.Filtered)
	}
	// 敏感词应该被 <mark> 包裹
	if !strings.Contains(result.Filtered, "<mark>") {
		t.Errorf("expected <mark> tags in output, got: %s", result.Filtered)
	}
}

func TestHighlightStrategy_NormalText(t *testing.T) {
	strategy := NewHighlightStrategy()

	matches := []valueobject.MatchItem{
		{Word: "赌博", Position: 2, EndPos: 4, Category: valueobject.CategoryCustom, Level: valueobject.RiskMedium},
	}

	original := "这是赌博内容"
	normalized := Normalize(original)

	result := strategy.Apply(original, normalized, matches)

	// 中文不含 HTML 特殊字符，结果应直接高亮
	expected := "这是<mark>赌博</mark>内容"
	if result.Filtered != expected {
		t.Errorf("expected %q, got %q", expected, result.Filtered)
	}
}

func TestHighlightStrategy_NoMatches(t *testing.T) {
	strategy := NewHighlightStrategy()

	result := strategy.Apply("正常文本", "", nil)

	if result.Filtered != "正常文本" {
		t.Errorf("expected original text when no matches, got %q", result.Filtered)
	}
	if result.IsHit {
		t.Error("expected no hit")
	}
}
