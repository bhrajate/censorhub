package cache

import (
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)

	// 初始状态：closed，允许通过
	if !cb.Allow() {
		t.Fatal("expected Allow=true in closed state")
	}
	if cb.IsOpen() {
		t.Fatal("expected closed state initially")
	}

	// 连续 3 次失败触发熔断
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsOpen() {
		t.Fatal("should not be open after 2 failures")
	}
	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("expected open state after 3 failures")
	}

	// 熔断状态下不允许通过
	if cb.Allow() {
		t.Fatal("expected Allow=false in open state")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	cb.RecordFailure() // 触发熔断
	if !cb.IsOpen() {
		t.Fatal("expected open state")
	}

	// 等待超过 resetTimeout
	time.Sleep(60 * time.Millisecond)

	// 应该进入 half-open，允许一次探测
	if !cb.Allow() {
		t.Fatal("expected Allow=true in half-open state")
	}
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	cb.RecordFailure() // 触发熔断
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // 进入 half-open

	// 连续成功恢复到 closed
	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.IsOpen() {
		t.Fatal("expected closed state after recovery")
	}
	if !cb.Allow() {
		t.Fatal("expected Allow=true after recovery")
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	cb.RecordFailure() // 触发熔断
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // 进入 half-open

	// half-open 状态下失败，回到 open
	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("expected open state after half-open failure")
	}
}

func TestCircuitBreaker_SuccessResetCounter(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()
	// 成功应重置计数
	cb.RecordSuccess()
	cb.RecordFailure()
	cb.RecordFailure()

	// 应该还没熔断（因为成功重置了计数器）
	if cb.IsOpen() {
		t.Fatal("expected closed state, success should have reset counter")
	}
}
