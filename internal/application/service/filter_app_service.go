package service

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
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

// batchJob 单条批量任务，传递索引和原文。
type batchJob struct {
	idx  int
	text string
}

// BatchDetect 批量检测
//
// 使用固定 worker pool + 任务 channel 代替"每 text 一个 goroutine + 信号量"，
// 消除高 batch 下的 goroutine churn 并降低 hitNum 的互斥锁压力。
func (s *FilterAppService) BatchDetect(ctx context.Context, req *dto.BatchFilterRequest) (*dto.BatchFilterResponse, error) {
	strategyStr := req.Strategy
	if strategyStr == "" {
		strategyStr = string(valueobject.StrategyDetect)
	}

	n := len(req.Texts)
	results := make([]*dto.FilterResponse, n)
	if n == 0 {
		return &dto.BatchFilterResponse{Results: results, Total: 0, HitNum: 0}, nil
	}

	// worker 数：受 CPU 和 batch 大小双重约束，避免小 batch 启太多闲 worker
	workers := runtime.NumCPU()
	if workers > n {
		workers = n
	}

	jobs := make(chan batchJob, n)
	var wg sync.WaitGroup
	var hitNum int64

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				// 提前检查 ctx，避免 ctx 取消后仍继续过滤
				if ctx.Err() != nil {
					return
				}
				r, err := s.Filter(ctx, &dto.FilterRequest{Text: job.text, Strategy: strategyStr})
				if err != nil {
					s.logger.Error("batch filter error", zap.Int("index", job.idx), zap.Error(err))
					continue
				}
				results[job.idx] = r
				if r.IsHit {
					atomic.AddInt64(&hitNum, 1)
				}
			}
		}()
	}

	for i, text := range req.Texts {
		jobs <- batchJob{idx: i, text: text}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &dto.BatchFilterResponse{
		Results: results,
		Total:   n,
		HitNum:  int(hitNum),
	}, nil
}

// EngineWordCount 获取当前引擎词条数
func (s *FilterAppService) EngineWordCount() int {
	return s.engine.WordCount()
}
