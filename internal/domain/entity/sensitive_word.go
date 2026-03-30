package entity

import (
	"errors"
	"time"
	"unicode/utf8"

	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

const (
	MaxWordLength = 255
	MinWordLength = 1
)

// SensitiveWord 敏感词实体
type SensitiveWord struct {
	ID        uint64
	Text      string                 // 敏感词文本
	Category  valueobject.Category   // 分类
	Level     valueobject.RiskLevel  // 风险等级
	Status    valueobject.WordStatus // 状态
	Tag       string                 // 自定义标签
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate 实体业务校验
func (w *SensitiveWord) Validate() error {
	if utf8.RuneCountInString(w.Text) < MinWordLength {
		return errors.New("word text cannot be empty")
	}
	if utf8.RuneCountInString(w.Text) > MaxWordLength {
		return errors.New("word text exceeds maximum length")
	}
	if !w.Category.IsValid() {
		return errors.New("invalid category")
	}
	if !w.Level.IsValid() {
		return errors.New("invalid risk level")
	}
	return nil
}

// Activate 启用词条
func (w *SensitiveWord) Activate() {
	w.Status = valueobject.WordStatusActive
	w.UpdatedAt = time.Now()
}

// Deactivate 禁用词条
func (w *SensitiveWord) Deactivate() {
	w.Status = valueobject.WordStatusInactive
	w.UpdatedAt = time.Now()
}

// IsActive 是否启用
func (w *SensitiveWord) IsActive() bool {
	return w.Status.IsActive()
}
