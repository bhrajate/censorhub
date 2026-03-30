package algorithm

import (
	"testing"

	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

func makeEntries(words ...string) []WordEntry {
	entries := make([]WordEntry, len(words))
	for i, w := range words {
		entries[i] = WordEntry{
			Text:     w,
			Category: valueobject.CategoryCustom,
			Level:    valueobject.RiskMedium,
		}
	}
	return entries
}

func TestAhoCorasick_BasicMatch(t *testing.T) {
	ac := NewAhoCorasick(makeEntries("he", "she", "his", "hers"))
	results := ac.Search("ushers")

	words := make(map[string]bool)
	for _, r := range results {
		words[r.Word] = true
	}

	if !words["she"] {
		t.Error("expected to find 'she'")
	}
	if !words["he"] {
		t.Error("expected to find 'he'")
	}
	if !words["hers"] {
		t.Error("expected to find 'hers'")
	}
}

func TestAhoCorasick_ChineseMatch(t *testing.T) {
	ac := NewAhoCorasick(makeEntries("赌博", "博彩", "色情"))

	tests := []struct {
		text     string
		expected []string
	}{
		{"这是赌博内容", []string{"赌博"}},
		{"赌博彩票", []string{"赌博", "博彩"}},
		{"正常文本", nil},
		{"色情和赌博都不好", []string{"色情", "赌博"}},
	}

	for _, tt := range tests {
		results := ac.Search(tt.text)
		if len(results) != len(tt.expected) {
			t.Errorf("Search(%q): got %d matches, want %d", tt.text, len(results), len(tt.expected))
			continue
		}
		words := make(map[string]bool)
		for _, r := range results {
			words[r.Word] = true
		}
		for _, w := range tt.expected {
			if !words[w] {
				t.Errorf("Search(%q): missing expected word %q", tt.text, w)
			}
		}
	}
}

func TestAhoCorasick_OverlappingPatterns(t *testing.T) {
	ac := NewAhoCorasick(makeEntries("ab", "bc", "abc"))
	results := ac.Search("abc")

	if len(results) < 2 {
		t.Errorf("expected at least 2 matches for overlapping patterns, got %d", len(results))
	}

	words := make(map[string]bool)
	for _, r := range results {
		words[r.Word] = true
	}
	if !words["ab"] {
		t.Error("expected to find 'ab'")
	}
	if !words["abc"] {
		t.Error("expected to find 'abc'")
	}
}

func TestAhoCorasick_EmptyInput(t *testing.T) {
	// 空词库
	ac := NewAhoCorasick(nil)
	results := ac.Search("any text")
	if len(results) != 0 {
		t.Error("empty automaton should return no results")
	}

	// 空文本
	ac2 := NewAhoCorasick(makeEntries("test"))
	results2 := ac2.Search("")
	if len(results2) != 0 {
		t.Error("empty text should return no results")
	}
}

func TestAhoCorasick_Position(t *testing.T) {
	ac := NewAhoCorasick(makeEntries("赌博"))
	results := ac.Search("我爱赌博啊")

	if len(results) != 1 {
		t.Fatalf("expected 1 match, got %d", len(results))
	}
	if results[0].Position != 2 {
		t.Errorf("expected position 2, got %d", results[0].Position)
	}
	if results[0].EndPos != 4 {
		t.Errorf("expected end position 4, got %d", results[0].EndPos)
	}
}

func TestAhoCorasick_Normalization(t *testing.T) {
	// 全角字符
	ac := NewAhoCorasick(makeEntries("赌博"))
	results := ac.Search("赌\u200b博") // 零宽字符
	if len(results) != 1 {
		t.Errorf("expected 1 match with zero-width chars, got %d", len(results))
	}
}

func TestAhoCorasick_WordCount(t *testing.T) {
	ac := NewAhoCorasick(makeEntries("a", "b", "c"))
	if ac.WordCount() != 3 {
		t.Errorf("expected word count 3, got %d", ac.WordCount())
	}
}

func TestAhoCorasick_CaseInsensitive(t *testing.T) {
	ac := NewAhoCorasick(makeEntries("fuck", "shit"))
	results := ac.Search("FUCK this SHIT")
	if len(results) != 2 {
		t.Errorf("expected 2 case-insensitive matches, got %d", len(results))
	}
}
