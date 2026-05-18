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

// WordFingerprint 是 active 词条集合的"指纹/版本号"，用于热更新轮询比对。
//
// 由 (Count, MaxID, MaxUpdatedUnixMicro) 三元组构成：
//   - Count:                INSERT/DELETE 时变化
//   - MaxID:                INSERT 时单调递增（兜底"同秒 INSERT+DELETE"导致 Count 回归原值的边界）
//   - MaxUpdatedUnixMicro:  UPDATE 时变化（autoUpdateTime）；用 Unix 微秒避免时区/精度坑
//
// 仅当三元组完全相等时认为词库未变；任意一项不同即触发重建。
type WordFingerprint struct {
	Count               int64
	MaxID               uint64
	MaxUpdatedUnixMicro int64
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
	// FindInBatches 分批查询词条，每批调用 fn 处理
	FindInBatches(ctx context.Context, category *valueobject.Category, batchSize int, fn func(words []*entity.SensitiveWord) error) error
	// ActiveFingerprint 返回 status=active 词条集合的指纹，用于热更新 poll
	ActiveFingerprint(ctx context.Context) (WordFingerprint, error)
}
