package algorithm

import "testing"

func TestNormalize_FullWidthToHalfWidth(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ＡＢＣ", "abc"},           // 全角字母 -> 半角小写
		{"１２３", "123"},           // 全角数字 -> 半角
		{"　", " "},                // 全角空格 -> 半角空格
		{"ＡＢＣ１２３", "abc123"}, // 混合
	}

	for _, tt := range tests {
		result := Normalize(tt.input)
		if result != tt.expected {
			t.Errorf("Normalize(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNormalize_ZeroWidthChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"赌\u200b博", "赌博"},     // Zero Width Space
		{"赌\u200c博", "赌博"},     // Zero Width Non-Joiner
		{"赌\u200d博", "赌博"},     // Zero Width Joiner
		{"赌\ufeff博", "赌博"},     // BOM
		{"\u200b\u200c\u200d", ""}, // 全零宽
	}

	for _, tt := range tests {
		result := Normalize(tt.input)
		if result != tt.expected {
			t.Errorf("Normalize(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNormalize_ToLower(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"HELLO", "hello"},
		{"Hello World", "hello world"},
		{"已UTF8中文", "已utf8中文"},
	}

	for _, tt := range tests {
		result := Normalize(tt.input)
		if result != tt.expected {
			t.Errorf("Normalize(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNormalize_Mixed(t *testing.T) {
	// 混合场景：全角 + 零宽 + 大写
	input := "Ｆ\u200bＵ\u200cＣ\u200dＫ"
	expected := "fuck"
	result := Normalize(input)
	if result != expected {
		t.Errorf("Normalize(%q) = %q, want %q", input, result, expected)
	}
}

func TestNormalizeForIndex(t *testing.T) {
	// 前后空格应被去除
	result := NormalizeForIndex("  赌博  ")
	if result != "赌博" {
		t.Errorf("NormalizeForIndex got %q, want %q", result, "赌博")
	}
}
