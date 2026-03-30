package mq

import (
	"context"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const WordUpdateChannel = "censorhub:word_update"

// RedisPubSub Redis 发布/订阅，用于跨实例热更新通知
type RedisPubSub struct {
	client *redis.Client
	logger *zap.Logger
}

// NewRedisPubSub 创建 Redis PubSub
func NewRedisPubSub(client *redis.Client, logger *zap.Logger) *RedisPubSub {
	return &RedisPubSub{
		client: client,
		logger: logger,
	}
}

// PublishWordUpdate 发布词条更新通知
func (p *RedisPubSub) PublishWordUpdate(ctx context.Context) error {
	return p.client.Publish(ctx, WordUpdateChannel, "rebuild").Err()
}

// SubscribeWordUpdate 订阅词条更新通知
func (p *RedisPubSub) SubscribeWordUpdate(ctx context.Context, handler func()) {
	sub := p.client.Subscribe(ctx, WordUpdateChannel)

	go func() {
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					p.logger.Info("PubSub channel closed")
					return
				}
				if msg.Payload == "rebuild" {
					p.logger.Info("Received word update notification, triggering rebuild")
					handler()
				}
			case <-ctx.Done():
				p.logger.Info("PubSub subscriber stopped")
				return
			}
		}
	}()

	p.logger.Info("PubSub subscriber started", zap.String("channel", WordUpdateChannel))
}
