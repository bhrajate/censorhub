package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// circuitState 熔断器状态
type circuitState uint32

const (
	stateClosed   circuitState = iota // 正常（放行）
	stateOpen                         // 熔断（拒绝）
	stateHalfOpen                     // 半开（探测）
)

// CircuitBreaker 轻量级熔断器
//
// 快速路径：Allow() 在最常见的 closed 状态下仅做一次 atomic.Load，不获取互斥锁，
// 让 L1 缓存命中路径（~10 ns）不被 ~100 ns 的互斥锁吞掉。
// 状态转换（closed↔open↔halfOpen）仍然用 mutex 保持多字段一致性。
type CircuitBreaker struct {
	state atomic.Uint32 // 原子化的状态，供 Allow 快速路径读取

	mu               sync.Mutex
	failCount        int
	failThreshold    int           // 连续失败次数触发熔断
	resetTimeout     time.Duration // 熔断后多久尝试半开
	lastFailTime     time.Time
	halfOpenSuccess  int
	halfOpenRequired int // 半开状态需要连续成功几次才恢复
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(failThreshold int, resetTimeout time.Duration) *CircuitBreaker {
	cb := &CircuitBreaker{
		failThreshold:    failThreshold,
		resetTimeout:     resetTimeout,
		halfOpenRequired: 2,
	}
	cb.state.Store(uint32(stateClosed))
	return cb
}

// Allow 是否允许请求通过。
// Closed 态走无锁快速路径；非 Closed 态才进入互斥路径做状态迁移判断。
func (cb *CircuitBreaker) Allow() bool {
	if circuitState(cb.state.Load()) == stateClosed {
		return true
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch circuitState(cb.state.Load()) {
	case stateClosed:
		return true
	case stateOpen:
		if time.Since(cb.lastFailTime) > cb.resetTimeout {
			cb.state.Store(uint32(stateHalfOpen))
			cb.halfOpenSuccess = 0
			return true
		}
		return false
	case stateHalfOpen:
		return true
	}
	return true
}

// RecordSuccess 记录成功
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch circuitState(cb.state.Load()) {
	case stateHalfOpen:
		cb.halfOpenSuccess++
		if cb.halfOpenSuccess >= cb.halfOpenRequired {
			cb.state.Store(uint32(stateClosed))
			cb.failCount = 0
		}
	case stateClosed:
		cb.failCount = 0
	}
}

// RecordFailure 记录失败
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailTime = time.Now()

	switch circuitState(cb.state.Load()) {
	case stateHalfOpen:
		cb.state.Store(uint32(stateOpen))
	case stateClosed:
		cb.failCount++
		if cb.failCount >= cb.failThreshold {
			cb.state.Store(uint32(stateOpen))
		}
	}
}

// IsOpen 返回熔断器是否处于开路状态。
// 同样走 atomic 快路径，调用方不需要互斥等待。
func (cb *CircuitBreaker) IsOpen() bool {
	return circuitState(cb.state.Load()) == stateOpen
}
