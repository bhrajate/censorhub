package service

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"runtime"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/bhrajate/censorhub/internal/application/assembler"
	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/domain/service"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
	"github.com/bhrajate/censorhub/internal/infrastructure/cache"
	"github.com/bhrajate/censorhub/pkg/metrics"
)

// FilterAppService 过滤应用服务
type FilterAppService struct {
	engine     service.FilterEngine
	strategies map[valueobject.FilterStrategyType]valueobject.FilterStrategy
	cache      *cache.MultiLevelCache
	logger     *zap.Logger
}

// NewFilterAppService 创建过滤应用服务
func NewFilterAppService(
	engine service.FilterEngine,
	strategies map[valueobject.FilterStrategyType]valueobject.FilterStrategy,
	cache *cache.MultiLevelCache,
	logger *zap.Logger,
) *FilterAppService {
	return &FilterAppService{
		engine:     engine,
		strategies: strategies,
		cache:      cache,
		logger:     logger,
	}
}

// filterCacheKey 生成过滤结果缓存 key（使用 FNV 非密码学哈希，性能优于 SHA256）
func filterCacheKey(text string, strategy string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return "filter:" + strategy + ":" + strconv.FormatUint(h.Sum64(), 36)
}

// Filter 根据策略过滤文本
func (s *FilterAppService) Filter(ctx context.Context, req *dto.FilterRequest) (*dto.FilterResponse, error) {
	start := time.Now()

	// 选择策略
	strategyType := valueobject.StrategyDetect
	if req.Strategy != "" {
		st := valueobject.FilterStrategyType(req.Strategy)
		if st.IsValid() {
			strategyType = st
		}
	}

	// 查缓存
	cacheKey := filterCacheKey(req.Text, string(strategyType))
	if s.cache != nil {
		if data, err := s.cache.Get(ctx, cacheKey); err == nil {
			var cached dto.FilterResponse
			if json.Unmarshal(data, &cached) == nil {
				cached.CostMs = time.Since(start).Milliseconds()
				return &cached, nil
			}
		}
	}

	// 匹配
	matchResult := s.engine.Match(req.Text)

	strategy, ok := s.strategies[strategyType]
	if !ok {
		strategy = s.strategies[valueobject.StrategyDetect]
	}

	// 应用策略（传入归一化文本，避免策略层重复 Normalize）
	result := strategy.Apply(req.Text, matchResult.NormalizedText, matchResult.Matches)
	result.CostMs = time.Since(start).Milliseconds()

	resp := assembler.FilterResultToDTO(result)

	// 记录过滤命中指标
	metrics.FilterHitsTotal.WithLabelValues(string(strategyType), strconv.FormatBool(resp.IsHit)).Inc()

	// 写缓存（异步，不阻塞响应）
	if s.cache != nil {
		if data, err := json.Marshal(resp); err == nil {
			_ = s.cache.Set(ctx, cacheKey, data)
		}
	}

	return resp, nil
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

	// 使用 semaphore 限制并发协程数，防止大批量请求耗尽资源
	maxWorkers := runtime.NumCPU()
	sem := make(chan struct{}, maxWorkers)

	for i, text := range req.Texts {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{} // 获取信号量，达到上限时阻塞
		go func(idx int, t string) {
			defer wg.Done()
			defer func() { <-sem }() // 释放信号量

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
