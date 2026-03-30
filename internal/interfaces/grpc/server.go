package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	censorv1 "github.com/bhrajate/censorhub/api/proto/censor/v1"
	"github.com/bhrajate/censorhub/internal/application/dto"
	"github.com/bhrajate/censorhub/internal/application/service"
)

// CensorServiceServer gRPC 过滤服务实现
type CensorServiceServer struct {
	censorv1.UnimplementedCensorServiceServer
	filterService *service.FilterAppService
}

// NewCensorServiceServer 创建 gRPC 服务
func NewCensorServiceServer(filterService *service.FilterAppService) *CensorServiceServer {
	return &CensorServiceServer{filterService: filterService}
}

// RegisterServer 注册 gRPC 服务到 server
func (s *CensorServiceServer) RegisterServer(srv *grpc.Server) {
	censorv1.RegisterCensorServiceServer(srv, s)
}

func (s *CensorServiceServer) Detect(ctx context.Context, req *censorv1.FilterRequest) (*censorv1.FilterResponse, error) {
	if req.Text == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}

	result, err := s.filterService.Detect(ctx, &dto.FilterRequest{Text: req.Text})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "filter error: %v", err)
	}

	return toProtoResponse(result), nil
}

func (s *CensorServiceServer) Replace(ctx context.Context, req *censorv1.FilterRequest) (*censorv1.FilterResponse, error) {
	if req.Text == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}

	result, err := s.filterService.Replace(ctx, &dto.FilterRequest{Text: req.Text})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "filter error: %v", err)
	}

	return toProtoResponse(result), nil
}

func (s *CensorServiceServer) BatchDetect(ctx context.Context, req *censorv1.BatchFilterRequest) (*censorv1.BatchFilterResponse, error) {
	if len(req.Texts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "texts is required")
	}

	result, err := s.filterService.BatchDetect(ctx, &dto.BatchFilterRequest{Texts: req.Texts})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "batch filter error: %v", err)
	}

	protoResults := make([]*censorv1.FilterResponse, len(result.Results))
	for i, r := range result.Results {
		protoResults[i] = toProtoResponse(r)
	}

	return &censorv1.BatchFilterResponse{
		Results: protoResults,
		Total:   int32(result.Total),
		HitNum:  int32(result.HitNum),
	}, nil
}

func toProtoResponse(r *dto.FilterResponse) *censorv1.FilterResponse {
	if r == nil {
		return &censorv1.FilterResponse{}
	}
	matches := make([]*censorv1.MatchItem, len(r.Matches))
	for i, m := range r.Matches {
		matches[i] = &censorv1.MatchItem{
			Word:        m.Word,
			Position:    int32(m.Position),
			EndPosition: int32(m.EndPos),
			Category:    m.Category,
			Level:       int32(m.Level),
		}
	}
	return &censorv1.FilterResponse{
		Original:  r.Original,
		Filtered:  r.Filtered,
		IsHit:     r.IsHit,
		HitCount:  int32(r.HitCount),
		Matches:   matches,
		RiskLevel: int32(r.RiskLevel),
		CostMs:    r.CostMs,
	}
}
