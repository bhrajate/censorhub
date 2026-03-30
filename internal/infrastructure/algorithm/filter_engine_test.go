package algorithm

import (
	"sync"
	"testing"

	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

func makeWords(texts ...string) []*entity.SensitiveWord {
	words := make([]*entity.SensitiveWord, len(texts))
	for i, t := range texts {
		words[i] = &entity.SensitiveWord{
			ID:       uint64(i + 1),
			Text:     t,
			Category: valueobject.CategoryCustom,
			Level:    valueobject.RiskMedium,
			Status:   valueobject.WordStatusActive,
		}
	}
	return words
}

func TestACFilterEngine_MatchAfterRebuild(t *testing.T) {
	engine := NewACFilterEngine()

	// 初始无词条
	results := engine.Match("赌博网站")
	if len(results) != 0 {
		t.Error("expected no matches before rebuild")
	}

	// 加载词条
	err := engine.Rebuild(makeWords("赌博", "色情"))
	if err != nil {
		t.Fatal(err)
	}

	results = engine.Match("赌博网站")
	if len(results) != 1 {
		t.Errorf("expected 1 match, got %d", len(results))
	}
	if results[0].Word != "赌博" {
		t.Errorf("expected word '赌博', got %q", results[0].Word)
	}

	// 词条数
	if engine.WordCount() != 2 {
		t.Errorf("expected word count 2, got %d", engine.WordCount())
	}
}

func TestACFilterEngine_HotUpdate(t *testing.T) {
	engine := NewACFilterEngine()
	engine.Rebuild(makeWords("赌博"))

	// 热更新：移除赌博，增加色情
	engine.Rebuild(makeWords("色情"))

	results := engine.Match("赌博网站")
	if len(results) != 0 {
		t.Error("expected no matches after hot update removed the word")
	}

	results = engine.Match("色情内容")
	if len(results) != 1 {
		t.Errorf("expected 1 match after hot update, got %d", len(results))
	}
}

func TestACFilterEngine_InactiveWordsSkipped(t *testing.T) {
	engine := NewACFilterEngine()

	words := []*entity.SensitiveWord{
		{ID: 1, Text: "赌博", Category: valueobject.CategoryCustom, Level: valueobject.RiskMedium, Status: valueobject.WordStatusActive},
		{ID: 2, Text: "色情", Category: valueobject.CategoryCustom, Level: valueobject.RiskMedium, Status: valueobject.WordStatusInactive},
	}
	engine.Rebuild(words)

	if engine.WordCount() != 1 {
		t.Errorf("expected 1 active word, got %d", engine.WordCount())
	}

	results := engine.Match("色情内容")
	if len(results) != 0 {
		t.Error("inactive word should not be matched")
	}
}

func TestACFilterEngine_ConcurrentAccess(t *testing.T) {
	engine := NewACFilterEngine()
	engine.Rebuild(makeWords("赌博", "色情"))

	var wg sync.WaitGroup

	// 并发读
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := engine.Match("赌博和色情")
			if len(results) < 1 {
				t.Error("expected at least 1 match during concurrent read")
			}
		}()
	}

	// 并发写
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			engine.Rebuild(makeWords("赌博", "色情", "暴力"))
		}()
	}

	wg.Wait()
}
