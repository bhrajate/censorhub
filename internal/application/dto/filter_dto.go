package dto

// FilterRequest 过滤请求
type FilterRequest struct {
	Text     string `json:"text" binding:"required,max=50000"`
	Strategy string `json:"strategy,omitempty"` // detect/replace/highlight
}

// FilterResponse 过滤响应
type FilterResponse struct {
	Original  string     `json:"original"`
	Filtered  string     `json:"filtered,omitempty"`
	IsHit     bool       `json:"is_hit"`
	HitCount  int        `json:"hit_count"`
	Matches   []MatchDTO `json:"matches,omitempty"`
	RiskLevel int        `json:"risk_level"`
	CostMs    int64      `json:"cost_ms"`
}

// MatchDTO 匹配项 DTO
type MatchDTO struct {
	Word     string `json:"word"`
	Position int    `json:"position"`
	EndPos   int    `json:"end_position"`
	Category string `json:"category"`
	Level    int    `json:"level"`
}

// BatchFilterRequest 批量过滤请求
type BatchFilterRequest struct {
	Texts    []string `json:"texts" binding:"required,min=1,max=100,dive,max=50000"`
	Strategy string   `json:"strategy,omitempty"`
}

// BatchFilterResponse 批量过滤响应
type BatchFilterResponse struct {
	Results []*FilterResponse `json:"results"`
	Total   int               `json:"total"`
	HitNum  int               `json:"hit_num"`
}
