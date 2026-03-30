package mq

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const WordUpdateChannel = "censorhub:word_update"

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

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

// SubscribeWordUpdate 订阅词条更新通知，断线自动重连（指数退避）
func (p *RedisPubSub) SubscribeWordUpdate(ctx context.Context, handler func()) {
	go func() {
		backoff := initialBackoff
		for {
			err := p.runSubscription(ctx, handler)

			// context 取消属于正常关停，直接退出
			if ctx.Err() != nil {
				p.logger.Info("PubSub subscriber stopped")
				return
			}

			p.logger.Error("PubSub subscription lost, reconnecting...",
				zap.Error(err),
				zap.Duration("backoff", backoff),
			)

			// 等待退避时间或 context 取消
			select {
			case <-ctx.Done():
				p.logger.Info("PubSub subscriber stopped during backoff")
				return
			case <-time.After(backoff):
			}

			// 指数退避，上限 30s
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}()

	p.logger.Info("PubSub subscriber started", zap.String("channel", WordUpdateChannel))
}

// runSubscription 执行一次订阅循环，返回时表示订阅已断开
func (p *RedisPubSub) runSubscription(ctx context.Context, handler func()) error {
	sub := p.client.Subscribe(ctx, WordUpdateChannel)
	defer sub.Close()

	// 验证订阅是否建立成功
	_, err := sub.Receive(ctx)
	if err != nil {
		return err
	}

	p.logger.Info("PubSub subscription established")

	ch := sub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return errChannelClosed
			}
			if msg.Payload == "rebuild" {
				p.logger.Info("Received word update notification, triggering rebuild")
				handler()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

var errChannelClosed = &subscriptionError{msg: "PubSub channel closed unexpectedly"}

type subscriptionError struct {
	msg string
}

func (e *subscriptionError) Error() string {
	return e.msg
}
