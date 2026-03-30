package algorithm

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

func generateWords(n int) []WordEntry {
	chars := []rune("abcdefghijklmnopqrstuvwxyz赌博色情暴力广告诈骗传销毒品枪支")
	entries := make([]WordEntry, n)
	for i := 0; i < n; i++ {
		wordLen := rand.Intn(5) + 2
		word := make([]rune, wordLen)
		for j := range word {
			word[j] = chars[rand.Intn(len(chars))]
		}
		entries[i] = WordEntry{
			Text:     string(word),
			Category: valueobject.CategoryCustom,
			Level:    valueobject.RiskLevel(rand.Intn(4) + 1),
		}
	}
	return entries
}

func generateText(size int) string {
	chars := []rune("这是一段测试文本用于基准测试包含各种中英文字符abcdefghijklmnopqrstuvwxyz")
	var b strings.Builder
	for b.Len() < size {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}

func BenchmarkAhoCorasick_Build(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		words := generateWords(n)
		b.Run(fmt.Sprintf("words_%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				NewAhoCorasick(words)
			}
		})
	}
}

func BenchmarkAhoCorasick_Search(b *testing.B) {
	text1K := generateText(1024)
	text10K := generateText(10240)

	for _, n := range []int{1000, 10000, 100000} {
		words := generateWords(n)
		ac := NewAhoCorasick(words)

		b.Run(fmt.Sprintf("words_%d_text_1K", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ac.Search(text1K)
			}
		})
		b.Run(fmt.Sprintf("words_%d_text_10K", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ac.Search(text10K)
			}
		})
	}
}

func BenchmarkNormalize(b *testing.B) {
	text := "这是Ｆ\u200bＵ\u200cＣ\u200dＫ一段包含全角和零宽字符的测试文本ＡＢＣＤ"
	for i := 0; i < b.N; i++ {
		Normalize(text)
	}
}
