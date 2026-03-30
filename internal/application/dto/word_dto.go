package dto

import "time"

// CreateWordRequest 创建词条请求
type CreateWordRequest struct {
	Text     string `json:"text" binding:"required,min=1,max=255"`
	Category string `json:"category" binding:"required"`
	Level    int    `json:"level" binding:"required,min=1,max=4"`
	Tag      string `json:"tag,omitempty"`
}

// UpdateWordRequest 更新词条请求
type UpdateWordRequest struct {
	Text     *string `json:"text,omitempty" binding:"omitempty,min=1,max=255"`
	Category *string `json:"category,omitempty"`
	Level    *int    `json:"level,omitempty" binding:"omitempty,min=1,max=4"`
	Status   *int    `json:"status,omitempty" binding:"omitempty,min=0,max=1"`
	Tag      *string `json:"tag,omitempty"`
}

// WordResponse 词条响应
type WordResponse struct {
	ID        uint64    `json:"id"`
	Text      string    `json:"text"`
	Category  string    `json:"category"`
	Level     int       `json:"level"`
	Status    int       `json:"status"`
	Tag       string    `json:"tag"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WordListRequest 词条列表请求
type WordListRequest struct {
	Category string `form:"category"`
	Level    int    `form:"level"`
	Status   int    `form:"status" binding:"omitempty,min=0,max=1"`
	Keyword  string `form:"keyword"`
	Page     int    `form:"page,default=1" binding:"min=1"`
	PageSize int    `form:"page_size,default=20" binding:"min=1,max=100"`
}

// WordListResponse 词条列表响应
type WordListResponse struct {
	Items    []*WordResponse `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// ImportRequest 批量导入请求
type ImportRequest struct {
	Words []CreateWordRequest `json:"words" binding:"required,min=1,max=10000"`
}

// ImportResponse 批量导入响应
type ImportResponse struct {
	Total    int             `json:"total"`
	Imported int             `json:"imported"`
	Skipped  int             `json:"skipped"`
	Failures []ImportFailure `json:"failures,omitempty"`
}

// ImportFailure 导入失败详情
type ImportFailure struct {
	Index  int    `json:"index"`
	Word   string `json:"word"`
	Reason string `json:"reason"`
}
