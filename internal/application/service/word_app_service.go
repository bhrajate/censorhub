package service

import (
	"context"
	"encoding/csv"
	"io"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/bhrajate/censorhub/internal/application/assembler"
	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/repository"
	"github.com/bhrajate/censorhub/internal/domain/service"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
	"github.com/bhrajate/censorhub/internal/infrastructure/algorithm"
	pkgerrors "github.com/bhrajate/censorhub/pkg/errors"
	"github.com/bhrajate/censorhub/pkg/metrics"
)

// pollConfig 控制指纹轮询循环的时序参数。生产路径用 defaultPollConfig；
// 测试可注入更短的值。
type pollConfig struct {
	interval        time.Duration // 两次指纹检查的基础间隔
	jitter          time.Duration // 每次间隔上叠加的随机抖动 [0, jitter]，避免多实例同时 poll
	queryTimeout    time.Duration // 单次指纹/重建调用的总超时
	retryBackoff    time.Duration // 重建失败后首次重试退避（同一个 tick 内）；之后翻倍
	maxAttempts     int           // 一次 reconcile 中 FindAllActive+Rebuild 的最大尝试数
	shutdownTimeout time.Duration // Close 等待 pollLoop 退出的最长时间
}

func defaultPollConfig() pollConfig {
	return pollConfig{
		interval:        500 * time.Millisecond,
		jitter:          250 * time.Millisecond,
		queryTimeout:    10 * time.Second,
		retryBackoff:    200 * time.Millisecond,
		maxAttempts:     3,
		shutdownTimeout: 5 * time.Second,
	}
}

type prefixInvalidator interface {
	InvalidateByPrefix(ctx context.Context, prefix string) error
}

// WordAppService 词条管理应用服务。
//
// 引擎热更新采用"指纹 + 轮询"模型：写入路径（Create/Update/Delete/Import）只关心 DB 事务，
// 不主动通知任何人；后台 pollLoop 周期性查询 ActiveFingerprint，发现变化则重建引擎并清缓存。
//
// 这套设计相对于"PubSub + debounce"的好处：
//   - 失败自愈：任何环节失败下个 tick 自然重试，无需"重试 + 上报"的状态机
//   - 跨实例一致性：每个实例独立判断 DB 状态，不依赖 PubSub 投递
//   - 关停安全：进程崩溃不丢窗口数据，新实例第一次 poll 自然拉到最新
//
// 生命周期：NewWordAppService → Start(ctx) → ... → Close()
type WordAppService struct {
	repo   repository.WordRepository
	engine service.FilterEngine
	cache  prefixInvalidator
	logger *zap.Logger
	cfg    pollConfig

	cancel    context.CancelFunc // pollLoop 的退出信号
	wg        sync.WaitGroup     // 跟踪 pollLoop
	closeOnce sync.Once          // 保证 Close 幂等

	// lastFingerprint 仅由 pollLoop 单 goroutine 读写，无需锁
	lastFingerprint repository.WordFingerprint
}

// NewWordAppService 创建词条管理应用服务。需要调用 Start 才会开始 poll。
func NewWordAppService(
	repo repository.WordRepository,
	engine service.FilterEngine,
	cache prefixInvalidator,
	logger *zap.Logger,
) *WordAppService {
	return &WordAppService{
		repo:   repo,
		engine: engine,
		cache:  cache,
		logger: logger,
		cfg:    defaultPollConfig(),
	}
}

// Start 启动指纹轮询循环。多次调用安全（仅第一次起作用）。
func (s *WordAppService) Start(ctx context.Context) {
	if s.cancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	go s.pollLoop(loopCtx)
}

// Close 停止后台循环。带超时兜底避免阻塞优雅关停。多次调用安全。
//
// 不需要"flush 待执行重建"——poll 模型下没有 debounce 窗口；进程退出后下次启动
// InitEngine 会自动加载最新词库，新实例第一次 poll 也会拉到最新指纹。
func (s *WordAppService) Close() {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	exited := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(s.cfg.shutdownTimeout):
		s.logger.Warn("Close timed out waiting for pollLoop to exit")
	}
}

func (s *WordAppService) Create(ctx context.Context, req *dto.CreateWordRequest) (*dto.WordResponse, error) {
	word := assembler.CreateDTOToEntity(req)
	if err := word.Validate(); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInvalidRequest, err.Error())
	}

	existing, err := s.repo.FindByText(ctx, word.Text)
	if err == nil && existing != nil {
		return nil, pkgerrors.ErrWordAlreadyExists
	}

	if err := s.repo.Create(ctx, word); err != nil {
		return nil, err
	}

	s.invalidatePrefix(ctx, "words:")
	return assembler.EntityToDTO(word), nil
}

func (s *WordAppService) Update(ctx context.Context, id uint64, req *dto.UpdateWordRequest) (*dto.WordResponse, error) {
	word, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, pkgerrors.ErrWordNotFound
		}
		return nil, err
	}

	if req.Text != nil {
		word.Text = algorithm.NormalizeForIndex(*req.Text)
	}
	if req.Category != nil {
		cat, err := valueobject.ParseCategory(*req.Category)
		if err != nil {
			return nil, pkgerrors.ErrInvalidCategory
		}
		word.Category = cat
	}
	if req.Level != nil {
		level, err := valueobject.ParseRiskLevel(*req.Level)
		if err != nil {
			return nil, pkgerrors.ErrInvalidRiskLevel
		}
		word.Level = level
	}
	if req.Status != nil {
		word.Status = valueobject.WordStatus(*req.Status)
	}
	if req.Tag != nil {
		word.Tag = *req.Tag
	}

	if err := word.Validate(); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInvalidRequest, err.Error())
	}

	if err := s.repo.Update(ctx, word); err != nil {
		return nil, err
	}

	s.invalidatePrefix(ctx, "words:")
	return assembler.EntityToDTO(word), nil
}

func (s *WordAppService) Delete(ctx context.Context, id uint64) error {
	_, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return pkgerrors.ErrWordNotFound
		}
		return err
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.invalidatePrefix(ctx, "words:")
	return nil
}

func (s *WordAppService) Get(ctx context.Context, id uint64) (*dto.WordResponse, error) {
	word, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, pkgerrors.ErrWordNotFound
		}
		return nil, err
	}
	return assembler.EntityToDTO(word), nil
}

func (s *WordAppService) List(ctx context.Context, req *dto.WordListRequest) (*dto.WordListResponse, error) {
	query := repository.WordQuery{
		Keyword:  req.Keyword,
		Page:     req.Page,
		PageSize: req.PageSize,
	}

	if req.Category != "" {
		cat := valueobject.Category(req.Category)
		query.Category = &cat
	}
	if req.Level > 0 {
		level := valueobject.RiskLevel(req.Level)
		query.Level = &level
	}

	words, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, err
	}

	return &dto.WordListResponse{
		Items:    assembler.EntitiesToDTOs(words),
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}, nil
}

func (s *WordAppService) Import(ctx context.Context, req *dto.ImportRequest) (*dto.ImportResponse, error) {
	words := make([]*entity.SensitiveWord, 0, len(req.Words))
	var failures []dto.ImportFailure

	for i, w := range req.Words {
		word := assembler.CreateDTOToEntity(&w)
		if err := word.Validate(); err != nil {
			failures = append(failures, dto.ImportFailure{
				Index:  i,
				Word:   w.Text,
				Reason: err.Error(),
			})
			continue
		}
		words = append(words, word)
	}

	imported, err := s.repo.BatchCreate(ctx, words)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrImportFailed, err.Error())
	}

	s.invalidatePrefix(ctx, "words:")
	return &dto.ImportResponse{
		Total:    len(req.Words),
		Imported: imported,
		Skipped:  len(failures),
		Failures: failures,
	}, nil
}

// ExportToWriter 分批流式导出词条为 CSV，直接写入 io.Writer
func (s *WordAppService) ExportToWriter(ctx context.Context, category string, w io.Writer) error {
	csvWriter := csv.NewWriter(w)
	csvWriter.Write([]string{"text", "category", "level", "tag"})

	var cat *valueobject.Category
	if category != "" {
		c := valueobject.Category(category)
		cat = &c
	}

	err := s.repo.FindInBatches(ctx, cat, 1000, func(words []*entity.SensitiveWord) error {
		for _, word := range words {
			csvWriter.Write([]string{
				word.Text,
				string(word.Category),
				valueobject.RiskLevel(word.Level).String(),
				word.Tag,
			})
		}
		csvWriter.Flush()
		return csvWriter.Error()
	})
	if err != nil {
		return err
	}

	csvWriter.Flush()
	return csvWriter.Error()
}

// pollLoop 是单 goroutine 指纹轮询循环。每个 tick 查一次指纹，变化则重建。
// 所有状态都是循环局部变量，无共享内存。
func (s *WordAppService) pollLoop(ctx context.Context) {
	defer s.wg.Done()

	// 启动时引入随机相位偏移，避免多实例同时启动后整齐同步打 DB
	jitterRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	initialDelay := time.Duration(jitterRand.Int63n(int64(s.cfg.interval)))
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	for {
		s.reconcileOnce(ctx)

		nextWait := s.cfg.interval
		if s.cfg.jitter > 0 {
			nextWait += time.Duration(jitterRand.Int63n(int64(s.cfg.jitter)))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(nextWait):
		}
	}
}

// reconcileOnce 拉取指纹，发现变化则重建引擎、清 filter 缓存。
// 任何环节失败：不更新 lastFingerprint，下个 tick 自然重试。
//
// 关键不变式：lastFingerprint 必须 ≤ 引擎当前已加载词条对应的指纹（即 lastFingerprint
// 反映的是"引擎里最迟也包含哪些词"）。如果 lastFingerprint 反而比引擎"领先"，下次
// 比对时就会跳过重建，造成漏更新。
//
// 实现做法：
//   1. 取一次指纹 fp_before
//   2. fp_before == lastFingerprint → 跳过
//   3. rebuild（FindAllActive + engine.Rebuild）
//   4. lastFingerprint = fp_before
//
// 第 4 步只用 fp_before 而不是 rebuild 之后再取一次新指纹：
// 因为 rebuild 期间可能有并发 INSERT（fp_after 可能 > fp_before），但这些新数据
// **不在** FindAllActive 拿到的快照里。如果用 fp_after 当 lastFingerprint，下次比对
// 就会跳过这些新数据 → 漏更新。
//
// 用 fp_before 是保守的下界：可能轻微"低估"引擎实际包含的数据，但下次 reconcile
// 会因 fp_after != fp_before 立即触发重建，把刚漏掉的写入扫进来。
func (s *WordAppService) reconcileOnce(parentCtx context.Context) {
	ctx, cancel := context.WithTimeout(parentCtx, s.cfg.queryTimeout)
	defer cancel()

	fpBefore, err := s.repo.ActiveFingerprint(ctx)
	if err != nil {
		metrics.EngineFingerprintChecksTotal.WithLabelValues("error").Inc()
		metrics.EngineRebuildFailuresTotal.WithLabelValues("fingerprint").Inc()
		s.logger.Warn("fingerprint query failed, will retry next tick", zap.Error(err))
		return
	}
	if fpBefore == s.lastFingerprint {
		metrics.EngineFingerprintChecksTotal.WithLabelValues("unchanged").Inc()
		return
	}
	metrics.EngineFingerprintChecksTotal.WithLabelValues("changed").Inc()

	if err := s.rebuildWithRetry(ctx); err != nil {
		// 失败：不更新 lastFingerprint，下个 tick 自然再触发
		return
	}
	// 关键：用 fp_before 而非 rebuild 之后重新拿的指纹，避免漏掉 rebuild 期间的并发写入
	s.lastFingerprint = fpBefore

	s.logger.Info("engine rebuilt via fingerprint poll",
		zap.Int64("count", fpBefore.Count),
		zap.Uint64("max_id", fpBefore.MaxID),
		zap.Int("word_count", s.engine.WordCount()),
	)
	s.invalidatePrefix(ctx, "filter:")
}

// rebuildWithRetry 在同一个 reconcile 内最多尝试 maxAttempts 次。
// 跨 tick 的"自愈"由外层 pollLoop 通过保留 lastFingerprint 不变实现。
func (s *WordAppService) rebuildWithRetry(ctx context.Context) error {
	var (
		words []*entity.SensitiveWord
		err   error
	)
	backoff := s.cfg.retryBackoff
	for attempt := 1; attempt <= s.cfg.maxAttempts; attempt++ {
		words, err = s.repo.FindAllActive(ctx)
		if err == nil {
			if err = s.engine.Rebuild(words); err == nil {
				metrics.EngineRebuildTotal.Inc()
				metrics.EngineWordCount.Set(float64(s.engine.WordCount()))
				return nil
			}
			metrics.EngineRebuildFailuresTotal.WithLabelValues("rebuild").Inc()
			s.logger.Error("failed to rebuild engine",
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", s.cfg.maxAttempts),
				zap.Error(err),
			)
		} else {
			metrics.EngineRebuildFailuresTotal.WithLabelValues("load_words").Inc()
			s.logger.Error("failed to load words for rebuild",
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", s.cfg.maxAttempts),
				zap.Error(err),
			)
		}

		if attempt == s.cfg.maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return err
}

func (s *WordAppService) invalidatePrefix(ctx context.Context, prefix string) {
	if s.cache == nil {
		return
	}
	if err := s.cache.InvalidateByPrefix(ctx, prefix); err != nil {
		s.logger.Warn("failed to invalidate cache by prefix",
			zap.String("prefix", prefix),
			zap.Error(err),
		)
	}
}

// InitEngine 应用启动时初始化引擎。同时把指纹记入 lastFingerprint，
// 使得首次 poll 不会立即重复一次重建。
func (s *WordAppService) InitEngine(ctx context.Context) error {
	words, err := s.repo.FindAllActive(ctx)
	if err != nil {
		return err
	}
	if err := s.engine.Rebuild(words); err != nil {
		return err
	}
	s.logger.Info("engine initialized", zap.Int("word_count", s.engine.WordCount()))
	metrics.EngineWordCount.Set(float64(s.engine.WordCount()))

	// 用一次指纹查询同步基线；失败不致命（下次 poll 会自然 reconcile）
	if fp, err := s.repo.ActiveFingerprint(ctx); err != nil {
		s.logger.Warn("init fingerprint query failed, first poll will reconcile", zap.Error(err))
	} else {
		s.lastFingerprint = fp
	}
	return nil
}
