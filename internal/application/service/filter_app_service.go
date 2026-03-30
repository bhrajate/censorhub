package service

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/bhrajate/censorhub/internal/application/assembler"
	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/domain/service"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// FilterAppService 过滤应用服务
type FilterAppService struct {
	engine     service.FilterEngine
	strategies map[valueobject.FilterStrategyType]valueobject.FilterStrategy
	logger     *zap.Logger
}

// NewFilterAppService 创建过滤应用服务
func NewFilterAppService(
	engine service.FilterEngine,
	strategies map[valueobject.FilterStrategyType]valueobject.FilterStrategy,
	logger *zap.Logger,
) *FilterAppService {
	return &FilterAppService{
		engine:     engine,
		strategies: strategies,
		logger:     logger,
	}
}

// Filter 根据策略过滤文本
func (s *FilterAppService) Filter(ctx context.Context, req *dto.FilterRequest) (*dto.FilterResponse, error) {
	start := time.Now()

	// 匹配
	matches := s.engine.Match(req.Text)

	// 选择策略
	strategyType := valueobject.StrategyDetect
	if req.Strategy != "" {
		st := valueobject.FilterStrategyType(req.Strategy)
		if st.IsValid() {
			strategyType = st
		}
	}

	strategy, ok := s.strategies[strategyType]
	if !ok {
		strategy = s.strategies[valueobject.StrategyDetect]
	}

	// 应用策略
	result := strategy.Apply(req.Text, matches)
	result.CostMs = time.Since(start).Milliseconds()

	return assembler.FilterResultToDTO(result), nil
}

// Detect 检测文本
func (s *FilterAppService) Detect(ctx context.Context, req *dto.FilterRequest) (*dto.FilterResponse, error) {
	req.Strategy = string(valueobject.StrategyDetect)
	return s.Filter(ctx, req)
}

// Replace 替换敏感词
func (s *FilterAppService) Replace(ctx context.Context, req *dto.FilterRequest) (*dto.FilterResponse, error) {
	req.Strategy = string(valueobject.StrategyReplace)
	return s.Filter(ctx, req)
}

// Highlight 高亮标记敏感词
func (s *FilterAppService) Highlight(ctx context.Context, req *dto.FilterRequest) (*dto.FilterResponse, error) {
	req.Strategy = string(valueobject.StrategyHighlight)
	return s.Filter(ctx, req)
}

// BatchDetect 批量检测
func (s *FilterAppService) BatchDetect(ctx context.Context, req *dto.BatchFilterRequest) (*dto.BatchFilterResponse, error) {
	strategyStr := req.Strategy
	if strategyStr == "" {
		strategyStr = string(valueobject.StrategyDetect)
	}

	results := make([]*dto.FilterResponse, len(req.Texts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	hitNum := 0

	for i, text := range req.Texts {
		wg.Add(1)
		go func(idx int, t string) {
			defer wg.Done()
			r, err := s.Filter(ctx, &dto.FilterRequest{Text: t, Strategy: strategyStr})
			if err != nil {
				s.logger.Error("batch filter error", zap.Int("index", idx), zap.Error(err))
				return
			}
			results[idx] = r
			if r.IsHit {
				mu.Lock()
				hitNum++
				mu.Unlock()
			}
		}(i, text)
	}

	wg.Wait()

	return &dto.BatchFilterResponse{
		Results: results,
		Total:   len(req.Texts),
		HitNum:  hitNum,
	}, nil
}

// EngineWordCount 获取当前引擎词条数
func (s *FilterAppService) EngineWordCount() int {
	return s.engine.WordCount()
}
