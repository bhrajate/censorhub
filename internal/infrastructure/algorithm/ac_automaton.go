package algorithm

import (
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// wordMeta 存储在 AC 自动机输出表中的词信息
type wordMeta struct {
	Text     string
	Length   int // rune 长度
	Category valueobject.Category
	Level    valueobject.RiskLevel
}

// acNode Aho-Corasick 自动机节点
type acNode struct {
	children map[rune]*acNode
	fail     *acNode
	output   []*wordMeta
}

func newACNode() *acNode {
	return &acNode{
		children: make(map[rune]*acNode),
	}
}

// AhoCorasick Aho-Corasick 多模式匹配自动机
type AhoCorasick struct {
	root      *acNode
	wordCount int
}

// NewAhoCorasick 构建 AC 自动机
func NewAhoCorasick(words []WordEntry) *AhoCorasick {
	ac := &AhoCorasick{
		root: newACNode(),
	}

	// 1. 构建 Trie 树
	for _, w := range words {
		ac.insert(w)
	}

	// 2. BFS 构建失败指针
	ac.buildFailPointers()

	return ac
}

// WordEntry AC 自动机词条输入
type WordEntry struct {
	Text     string
	Category valueobject.Category
	Level    valueobject.RiskLevel
}

// insert 将一个词插入 Trie 树
func (ac *AhoCorasick) insert(entry WordEntry) {
	normalized := Normalize(entry.Text)
	if len(normalized) == 0 {
		return
	}

	runes := []rune(normalized)
	curr := ac.root

	for _, r := range runes {
		if _, ok := curr.children[r]; !ok {
			curr.children[r] = newACNode()
		}
		curr = curr.children[r]
	}

	// 在终止节点记录词信息
	curr.output = append(curr.output, &wordMeta{
		Text:     entry.Text,
		Length:   len(runes),
		Category: entry.Category,
		Level:    entry.Level,
	})
	ac.wordCount++
}

// buildFailPointers 使用 BFS 构建失败指针
func (ac *AhoCorasick) buildFailPointers() {
	queue := make([]*acNode, 0, 64)

	// root 的直接子节点的 fail 都指向 root
	for _, child := range ac.root.children {
		child.fail = ac.root
		queue = append(queue, child)
	}

	// BFS 遍历构建其余节点的 fail 指针
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for r, child := range curr.children {
			queue = append(queue, child)

			// 沿着 fail 链查找最长匹配后缀
			failNode := curr.fail
			for failNode != nil && failNode.children[r] == nil {
				failNode = failNode.fail
			}

			if failNode == nil {
				child.fail = ac.root
			} else {
				child.fail = failNode.children[r]
				// 避免自引用
				if child.fail == child {
					child.fail = ac.root
				}
			}

			// 合并 fail 节点的输出表（suffix link 优化）
			if len(child.fail.output) > 0 {
				child.output = append(child.output, child.fail.output...)
			}
		}
	}
}

// Search 在文本中搜索所有匹配的敏感词
func (ac *AhoCorasick) Search(text string) []valueobject.MatchItem {
	if ac.root == nil || ac.wordCount == 0 {
		return nil
	}

	normalized := Normalize(text)
	runes := []rune(normalized)

	var results []valueobject.MatchItem
	curr := ac.root

	for i, r := range runes {
		// 沿 fail 链查找可接受当前字符的节点
		for curr != ac.root && curr.children[r] == nil {
			curr = curr.fail
		}

		if next, ok := curr.children[r]; ok {
			curr = next
		}

		// 检查当前节点的输出表
		for _, meta := range curr.output {
			results = append(results, valueobject.MatchItem{
				Word:     meta.Text,
				Position: i - meta.Length + 1,
				EndPos:   i + 1,
				Category: meta.Category,
				Level:    meta.Level,
			})
		}
	}

	return results
}

// WordCount 返回词条数量
func (ac *AhoCorasick) WordCount() int {
	return ac.wordCount
}
