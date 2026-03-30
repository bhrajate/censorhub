package cache

import (
	"sync"
	"time"
)

// circuitState 熔断器状态
type circuitState int

const (
	stateClosed   circuitState = iota // 正常（放行）
	stateOpen                         // 熔断（拒绝）
	stateHalfOpen                     // 半开（探测）
)

// CircuitBreaker 轻量级熔断器
type CircuitBreaker struct {
	mu               sync.Mutex
	state            circuitState
	failCount        int
	failThreshold    int           // 连续失败次数触发熔断
	resetTimeout     time.Duration // 熔断后多久尝试半开
	lastFailTime     time.Time
	halfOpenSuccess  int
	halfOpenRequired int // 半开状态需要连续成功几次才恢复
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(failThreshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            stateClosed,
		failThreshold:    failThreshold,
		resetTimeout:     resetTimeout,
		halfOpenRequired: 2,
	}
}

// Allow 是否允许请求通过
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateClosed:
		return true
	case stateOpen:
		if time.Since(cb.lastFailTime) > cb.resetTimeout {
			cb.state = stateHalfOpen
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

	switch cb.state {
	case stateHalfOpen:
		cb.halfOpenSuccess++
		if cb.halfOpenSuccess >= cb.halfOpenRequired {
			cb.state = stateClosed
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

	switch cb.state {
	case stateHalfOpen:
		cb.state = stateOpen
	case stateClosed:
		cb.failCount++
		if cb.failCount >= cb.failThreshold {
			cb.state = stateOpen
		}
	}
}

// IsOpen 返回熔断器是否处于开路状态
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state == stateOpen
}
