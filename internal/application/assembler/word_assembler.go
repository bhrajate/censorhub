package assembler

import (
	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
	"github.com/bhrajate/censorhub/internal/infrastructure/algorithm"
)

// CreateDTOToEntity 创建请求 DTO 转实体
func CreateDTOToEntity(req *dto.CreateWordRequest) *entity.SensitiveWord {
	return &entity.SensitiveWord{
		Text:     algorithm.NormalizeForIndex(req.Text),
		Category: valueobject.Category(req.Category),
		Level:    valueobject.RiskLevel(req.Level),
		Status:   valueobject.WordStatusActive,
		Tag:      req.Tag,
	}
}

// EntityToDTO 实体转响应 DTO
func EntityToDTO(e *entity.SensitiveWord) *dto.WordResponse {
	return &dto.WordResponse{
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

// EntitiesToDTOs 实体列表转 DTO 列表
func EntitiesToDTOs(entities []*entity.SensitiveWord) []*dto.WordResponse {
	result := make([]*dto.WordResponse, len(entities))
	for i, e := range entities {
		result[i] = EntityToDTO(e)
	}
	return result
}

// FilterResultToDTO 过滤结果转 DTO
func FilterResultToDTO(result *valueobject.FilterResult) *dto.FilterResponse {
	matches := make([]dto.MatchDTO, len(result.Matches))
	for i, m := range result.Matches {
		matches[i] = dto.MatchDTO{
			Word:     m.Word,
			Position: m.Position,
			EndPos:   m.EndPos,
			Category: string(m.Category),
			Level:    int(m.Level),
		}
	}
	return &dto.FilterResponse{
		Original:  result.Original,
		Filtered:  result.Filtered,
		IsHit:     result.IsHit,
		HitCount:  result.HitCount,
		Matches:   matches,
		RiskLevel: int(result.RiskLevel),
		CostMs:    result.CostMs,
	}
}
