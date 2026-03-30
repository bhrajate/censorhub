package mysql

import (
	"time"

	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

// SensitiveWordModel GORM 数据模型
type SensitiveWordModel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	Text      string    `gorm:"type:varchar(255);uniqueIndex;not null"`
	Category  string    `gorm:"type:varchar(50);index;not null"`
	Level     int       `gorm:"type:tinyint;default:1"`
	Status    int       `gorm:"type:tinyint;default:1;index"`
	Tag       string    `gorm:"type:varchar(100);index"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (SensitiveWordModel) TableName() string {
	return "sensitive_words"
}

// ToEntity 转换为领域实体
func (m *SensitiveWordModel) ToEntity() *entity.SensitiveWord {
	return &entity.SensitiveWord{
		ID:        m.ID,
		Text:      m.Text,
		Category:  valueobject.Category(m.Category),
		Level:     valueobject.RiskLevel(m.Level),
		Status:    valueobject.WordStatus(m.Status),
		Tag:       m.Tag,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// FromEntity 从领域实体转换为数据模型
func FromEntity(e *entity.SensitiveWord) *SensitiveWordModel {
	return &SensitiveWordModel{
		ID:        e.ID,
		Text:      e.Text,
		Category:  string(e.Category),
		Level:     int(e.Level),
		Status:    int(e.Status),
		Tag:       e.Tag,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}
