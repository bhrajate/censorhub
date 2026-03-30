package algorithm

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// zeroWidthChars 零宽字符集合
var zeroWidthChars = map[rune]bool{
	'\u200B': true, // Zero Width Space
	'\u200C': true, // Zero Width Non-Joiner
	'\u200D': true, // Zero Width Joiner
	'\uFEFF': true, // Zero Width No-Break Space (BOM)
	'\u00AD': true, // Soft Hyphen
	'\u200E': true, // Left-to-Right Mark
	'\u200F': true, // Right-to-Left Mark
	'\u2060': true, // Word Joiner
	'\u2061': true, // Function Application
	'\u2062': true, // Invisible Times
	'\u2063': true, // Invisible Separator
	'\u2064': true, // Invisible Plus
}

// Normalize 对文本进行归一化处理，用于敏感词匹配前的预处理
// 处理步骤：NFKC 归一化 -> 全角转半角 -> 去除零宽字符 -> 转小写
func Normalize(text string) string {
	// 1. Unicode NFKC 归一化（兼容分解后再组合）
	text = norm.NFKC.String(text)

	var b strings.Builder
	b.Grow(len(text))

	for _, r := range text {
		// 2. 去除零宽字符
		if zeroWidthChars[r] {
			continue
		}

		// 3. 全角转半角（不处理全角空格）
		r = fullWidthToHalfWidth(r)

		// 4. 转小写
		r = unicode.ToLower(r)

		b.WriteRune(r)
	}

	return b.String()
}

// NormalizeForIndex 用于词条入库时的归一化（与 Normalize 逻辑一致）
func NormalizeForIndex(text string) string {
	text = strings.TrimSpace(text)
	return Normalize(text)
}

// fullWidthToHalfWidth 全角字符转半角
func fullWidthToHalfWidth(r rune) rune {
	// 全角 ASCII 字符范围：U+FF01 ~ U+FF5E 对应半角 U+0021 ~ U+007E
	if r >= 0xFF01 && r <= 0xFF5E {
		return r - 0xFEE0
	}
	// 全角空格
	if r == 0x3000 {
		return ' '
	}
	return r
}
