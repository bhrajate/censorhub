package grpc

import (
	"context"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// RecoveryInterceptor panic 恢复拦截器
func RecoveryInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("gRPC panic recovered",
					zap.String("method", info.FullMethod),
					zap.Any("panic", r),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// LoggingInterceptor 请求日志拦截器
func LoggingInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start)

		code := codes.OK
		if err != nil {
			if s, ok := status.FromError(err); ok {
				code = s.Code()
			}
		}

		logger.Info("gRPC request",
			zap.String("method", info.FullMethod),
			zap.String("code", code.String()),
			zap.Duration("duration", duration),
		)
		return resp, err
	}
}

// AuthInterceptor API Key 认证拦截器
func AuthInterceptor(apiKeys []string) grpc.UnaryServerInterceptor {
	keySet := make(map[string]bool, len(apiKeys))
	for _, k := range apiKeys {
		keySet[k] = true
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// 未配置 API Key 时跳过认证
		if len(keySet) == 0 {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		keys := md.Get("x-api-key")
		if len(keys) == 0 || !keySet[keys[0]] {
			return nil, status.Error(codes.Unauthenticated, "invalid or missing x-api-key")
		}

		return handler(ctx, req)
	}
}

// RateLimitInterceptor 限流拦截器
func RateLimitInterceptor(rps int, burst int) grpc.UnaryServerInterceptor {
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !limiter.Allow() {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}
