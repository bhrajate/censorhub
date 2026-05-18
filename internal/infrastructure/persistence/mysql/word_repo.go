package mysql

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/repository"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

type wordRepo struct {
	db *gorm.DB
}

// NewWordRepository 创建 MySQL 词条仓储
func NewWordRepository(db *gorm.DB) repository.WordRepository {
	return &wordRepo{db: db}
}

func (r *wordRepo) Create(ctx context.Context, word *entity.SensitiveWord) error {
	model := FromEntity(word)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return err
	}
	word.ID = model.ID
	word.CreatedAt = model.CreatedAt
	word.UpdatedAt = model.UpdatedAt
	return nil
}

func (r *wordRepo) Update(ctx context.Context, word *entity.SensitiveWord) error {
	model := FromEntity(word)
	return r.db.WithContext(ctx).Save(model).Error
}

func (r *wordRepo) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&SensitiveWordModel{}, id).Error
}

func (r *wordRepo) FindByID(ctx context.Context, id uint64) (*entity.SensitiveWord, error) {
	var model SensitiveWordModel
	if err := r.db.WithContext(ctx).First(&model, id).Error; err != nil {
		return nil, err
	}
	return model.ToEntity(), nil
}

func (r *wordRepo) FindByText(ctx context.Context, text string) (*entity.SensitiveWord, error) {
	var model SensitiveWordModel
	if err := r.db.WithContext(ctx).Where("text = ?", text).First(&model).Error; err != nil {
		return nil, err
	}
	return model.ToEntity(), nil
}

func (r *wordRepo) List(ctx context.Context, query repository.WordQuery) ([]*entity.SensitiveWord, int64, error) {
	db := r.db.WithContext(ctx).Model(&SensitiveWordModel{})

	if query.Category != nil {
		db = db.Where("category = ?", string(*query.Category))
	}
	if query.Level != nil {
		db = db.Where("level = ?", int(*query.Level))
	}
	if query.Status != nil {
		db = db.Where("status = ?", int(*query.Status))
	}
	if query.Keyword != "" {
		db = db.Where("text LIKE ?", "%"+query.Keyword+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var models []SensitiveWordModel
	offset := (page - 1) * pageSize
	if err := db.Offset(offset).Limit(pageSize).Order("id DESC").Find(&models).Error; err != nil {
		return nil, 0, err
	}

	words := make([]*entity.SensitiveWord, len(models))
	for i, m := range models {
		words[i] = m.ToEntity()
	}
	return words, total, nil
}

func (r *wordRepo) FindAllActive(ctx context.Context) ([]*entity.SensitiveWord, error) {
	var models []SensitiveWordModel
	if err := r.db.WithContext(ctx).Where("status = ?", 1).Find(&models).Error; err != nil {
		return nil, err
	}
	words := make([]*entity.SensitiveWord, len(models))
	for i, m := range models {
		words[i] = m.ToEntity()
	}
	return words, nil
}

func (r *wordRepo) FindInBatches(ctx context.Context, category *valueobject.Category, batchSize int, fn func(words []*entity.SensitiveWord) error) error {
	db := r.db.WithContext(ctx).Model(&SensitiveWordModel{})
	if category != nil {
		db = db.Where("category = ?", string(*category))
	} else {
		db = db.Where("status = ?", 1)
	}

	var models []SensitiveWordModel
	result := db.FindInBatches(&models, batchSize, func(tx *gorm.DB, batch int) error {
		words := make([]*entity.SensitiveWord, len(models))
		for i, m := range models {
			words[i] = m.ToEntity()
		}
		return fn(words)
	})
	return result.Error
}

// ActiveFingerprint 在 idx_status_updated covering index 上一次完成 (count, max_id, max_updated)。
// 即使 100 万行级别也应在亚毫秒返回。
func (r *wordRepo) ActiveFingerprint(ctx context.Context) (repository.WordFingerprint, error) {
	var row struct {
		Cnt       int64
		MaxID     *uint64
		MaxUpdMic *int64
	}
	// COALESCE 兜底空表场景；UNIX_TIMESTAMP 用 6 位精度避免同秒误判
	err := r.db.WithContext(ctx).
		Model(&SensitiveWordModel{}).
		Select(`COUNT(*) AS cnt,
		        MAX(id) AS max_id,
		        CAST(COALESCE(UNIX_TIMESTAMP(MAX(updated_at)) * 1000000, 0) AS SIGNED) AS max_upd_mic`).
		Where("status = ?", 1).
		Scan(&row).Error
	if err != nil {
		return repository.WordFingerprint{}, err
	}
	fp := repository.WordFingerprint{Count: row.Cnt}
	if row.MaxID != nil {
		fp.MaxID = *row.MaxID
	}
	if row.MaxUpdMic != nil {
		fp.MaxUpdatedUnixMicro = *row.MaxUpdMic
	}
	return fp, nil
}

func (r *wordRepo) BatchCreate(ctx context.Context, words []*entity.SensitiveWord) (int, error) {
	if len(words) == 0 {
		return 0, nil
	}

	models := make([]SensitiveWordModel, len(words))
	for i, w := range words {
		models[i] = *FromEntity(w)
	}

	// 使用 ON DUPLICATE KEY 忽略重复
	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(models, 500)

	if result.Error != nil {
		return 0, result.Error
	}
	return int(result.RowsAffected), nil
}
