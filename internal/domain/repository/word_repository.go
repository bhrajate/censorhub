package repository

import (
	"context"

	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// WordQuery 查询条件
type WordQuery struct {
	Category *valueobject.Category
	Level    *valueobject.RiskLevel
	Status   *valueobject.WordStatus
	Keyword  string
	Page     int
	PageSize int
}

// WordRepository 敏感词仓储接口
type WordRepository interface {
	Create(ctx context.Context, word *entity.SensitiveWord) error
	Update(ctx context.Context, word *entity.SensitiveWord) error
	Delete(ctx context.Context, id uint64) error
	FindByID(ctx context.Context, id uint64) (*entity.SensitiveWord, error)
	FindByText(ctx context.Context, text string) (*entity.SensitiveWord, error)
	List(ctx context.Context, query WordQuery) ([]*entity.SensitiveWord, int64, error)
	FindAllActive(ctx context.Context) ([]*entity.SensitiveWord, error)
	BatchCreate(ctx context.Context, words []*entity.SensitiveWord) (int, error)
}
