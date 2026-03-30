package database

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/bhrajate/censorhub/internal/infrastructure/config"
)

// NewRedis 创建 Redis 客户端
func NewRedis(cfg *config.Config, logger *zap.Logger) (*redis.Client, func(), error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, nil, err
	}

	logger.Info("Redis connected", zap.String("addr", cfg.Redis.Addr))

	cleanup := func() {
		if err := client.Close(); err != nil {
			logger.Error("failed to close Redis", zap.Error(err))
		}
	}

	return client, cleanup, nil
}
