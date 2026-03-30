package service

import (
	"bytes"
	"context"
	"encoding/csv"
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
	"github.com/bhrajate/censorhub/internal/infrastructure/cache"
	"github.com/bhrajate/censorhub/internal/infrastructure/mq"
	pkgerrors "github.com/bhrajate/censorhub/pkg/errors"
)

// WordAppService 词条管理应用服务
type WordAppService struct {
	repo   repository.WordRepository
	engine service.FilterEngine
	cache  *cache.MultiLevelCache
	pubsub *mq.RedisPubSub
	logger *zap.Logger
}

// NewWordAppService 创建词条管理应用服务
func NewWordAppService(
	repo repository.WordRepository,
	engine service.FilterEngine,
	cache *cache.MultiLevelCache,
	pubsub *mq.RedisPubSub,
	logger *zap.Logger,
) *WordAppService {
	return &WordAppService{
		repo:   repo,
		engine: engine,
		cache:  cache,
		pubsub: pubsub,
		logger: logger,
	}
}

func (s *WordAppService) Create(ctx context.Context, req *dto.CreateWordRequest) (*dto.WordResponse, error) {
	// DTO -> Entity
	word := assembler.CreateDTOToEntity(req)
	if err := word.Validate(); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInvalidRequest, err.Error())
	}

	// 检查重复
	existing, err := s.repo.FindByText(ctx, word.Text)
	if err == nil && existing != nil {
		return nil, pkgerrors.ErrWordAlreadyExists
	}

	// 持久化
	if err := s.repo.Create(ctx, word); err != nil {
		return nil, err
	}

	// 触发热更新
	s.triggerRebuild(ctx)

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

	// 应用更新
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

	s.triggerRebuild(ctx)
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

	s.triggerRebuild(ctx)
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

	s.triggerRebuild(ctx)

	return &dto.ImportResponse{
		Total:    len(req.Words),
		Imported: imported,
		Skipped:  len(failures),
		Failures: failures,
	}, nil
}

func (s *WordAppService) Export(ctx context.Context, category string) ([]byte, error) {
	var words []*entity.SensitiveWord
	var err error

	if category != "" {
		cat := valueobject.Category(category)
		q := repository.WordQuery{Category: &cat, Page: 1, PageSize: 100000}
		words, _, err = s.repo.List(ctx, q)
	} else {
		words, err = s.repo.FindAllActive(ctx)
	}
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write([]string{"text", "category", "level", "tag"})

	for _, word := range words {
		w.Write([]string{
			word.Text,
			string(word.Category),
			valueobject.RiskLevel(word.Level).String(),
			word.Tag,
		})
	}
	w.Flush()

	return buf.Bytes(), nil
}

// triggerRebuild 触发引擎热更新
func (s *WordAppService) triggerRebuild(ctx context.Context) {
	go func() {
		// 使用带超时的 context，确保优雅关停时不会无限等待
		rebuildCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		words, err := s.repo.FindAllActive(rebuildCtx)
		if err != nil {
			s.logger.Error("failed to load words for rebuild", zap.Error(err))
			return
		}
		if err := s.engine.Rebuild(words); err != nil {
			s.logger.Error("failed to rebuild engine", zap.Error(err))
			return
		}
		s.logger.Info("engine rebuilt", zap.Int("word_count", s.engine.WordCount()))
	}()

	// 通知其他实例
	if err := s.pubsub.PublishWordUpdate(ctx); err != nil {
		s.logger.Error("failed to publish word update", zap.Error(err))
	}

	// 清除缓存
	s.cache.InvalidateByPrefix(ctx, "words:")
}

// InitEngine 应用启动时初始化引擎
func (s *WordAppService) InitEngine(ctx context.Context) error {
	words, err := s.repo.FindAllActive(ctx)
	if err != nil {
		return err
	}
	if err := s.engine.Rebuild(words); err != nil {
		return err
	}
	s.logger.Info("engine initialized", zap.Int("word_count", s.engine.WordCount()))
	return nil
}
