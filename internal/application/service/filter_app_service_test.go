package service

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
	"github.com/bhrajate/censorhub/internal/infrastructure/algorithm"
)

func setupFilterService() *FilterAppService {
	engine := algorithm.NewACFilterEngine()
	engine.Rebuild([]*entity.SensitiveWord{
		{ID: 1, Text: "赌博", Category: valueobject.CategoryViolence, Level: valueobject.RiskHigh, Status: valueobject.WordStatusActive},
		{ID: 2, Text: "色情", Category: valueobject.CategoryPorn, Level: valueobject.RiskCritical, Status: valueobject.WordStatusActive},
	})

	strategies := map[valueobject.FilterStrategyType]valueobject.FilterStrategy{
		valueobject.StrategyDetect:    algorithm.NewDetectStrategy(),
		valueobject.StrategyReplace:   algorithm.NewReplaceStrategy(),
		valueobject.StrategyHighlight: algorithm.NewHighlightStrategy(),
	}

	return NewFilterAppService(engine, strategies, zap.NewNop())
}

func TestFilterAppService_Detect_Hit(t *testing.T) {
	svc := setupFilterService()
	ctx := context.Background()

	resp, err := svc.Detect(ctx, &dto.FilterRequest{Text: "这是赌博内容"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsHit {
		t.Error("expected hit")
	}
	if resp.HitCount != 1 {
		t.Errorf("expected 1 hit, got %d", resp.HitCount)
	}
	if resp.Matches[0].Word != "赌博" {
		t.Errorf("expected match word '赌博', got %q", resp.Matches[0].Word)
	}
}

func TestFilterAppService_Detect_NoHit(t *testing.T) {
	svc := setupFilterService()
	ctx := context.Background()

	resp, err := svc.Detect(ctx, &dto.FilterRequest{Text: "正常文本"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.IsHit {
		t.Error("expected no hit")
	}
}

func TestFilterAppService_Replace(t *testing.T) {
	svc := setupFilterService()
	ctx := context.Background()

	resp, err := svc.Replace(ctx, &dto.FilterRequest{Text: "赌博和色情"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsHit {
		t.Error("expected hit")
	}
	if resp.Filtered == "赌博和色情" {
		t.Error("expected text to be filtered")
	}
	// 应该有 ** 替换
	if resp.Filtered != "**和**" {
		t.Errorf("expected '**和**', got %q", resp.Filtered)
	}
}

func TestFilterAppService_Highlight(t *testing.T) {
	svc := setupFilterService()
	ctx := context.Background()

	resp, err := svc.Highlight(ctx, &dto.FilterRequest{Text: "赌博和色情"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsHit {
		t.Error("expected hit")
	}
	expected := "<mark>赌博</mark>和<mark>色情</mark>"
	if resp.Filtered != expected {
		t.Errorf("expected %q, got %q", expected, resp.Filtered)
	}
}

func TestFilterAppService_BatchDetect(t *testing.T) {
	svc := setupFilterService()
	ctx := context.Background()

	resp, err := svc.BatchDetect(ctx, &dto.BatchFilterRequest{
		Texts: []string{"赌博网站", "正常文本", "色情内容"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 3 {
		t.Errorf("expected total 3, got %d", resp.Total)
	}
	if resp.HitNum != 2 {
		t.Errorf("expected 2 hits, got %d", resp.HitNum)
	}
}

func TestFilterAppService_CostMs(t *testing.T) {
	svc := setupFilterService()
	ctx := context.Background()

	resp, err := svc.Detect(ctx, &dto.FilterRequest{Text: "测试文本"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.CostMs < 0 {
		t.Error("cost_ms should be non-negative")
	}
}

func TestFilterAppService_EngineWordCount(t *testing.T) {
	svc := setupFilterService()
	if svc.EngineWordCount() != 2 {
		t.Errorf("expected 2 words, got %d", svc.EngineWordCount())
	}
}
