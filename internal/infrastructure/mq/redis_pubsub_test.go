package mq

import (
	"testing"

	"go.uber.org/zap"
)

func TestRedisPubSub_ShouldHandleWordUpdate_LegacyPayload(t *testing.T) {
	pubsub := &RedisPubSub{
		logger:     zap.NewNop(),
		instanceID: "self",
	}

	if !pubsub.shouldHandleWordUpdate("rebuild") {
		t.Fatal("expected legacy rebuild payload to be handled")
	}
}

func TestRedisPubSub_ShouldHandleWordUpdate_SelfPublished(t *testing.T) {
	pubsub := &RedisPubSub{
		logger:     zap.NewNop(),
		instanceID: "self",
	}

	payload := `{"type":"rebuild","source":"self"}`
	if pubsub.shouldHandleWordUpdate(payload) {
		t.Fatal("expected self-published payload to be ignored")
	}
}

func TestRedisPubSub_ShouldHandleWordUpdate_RemotePayload(t *testing.T) {
	pubsub := &RedisPubSub{
		logger:     zap.NewNop(),
		instanceID: "self",
	}

	payload := `{"type":"rebuild","source":"other-instance"}`
	if !pubsub.shouldHandleWordUpdate(payload) {
		t.Fatal("expected remote payload to be handled")
	}
}
