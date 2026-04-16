package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// CacheOpsTotal 缓存操作计数（L1/L2 命中/未命中）
	CacheOpsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "censorhub_cache_operations_total",
			Help: "Total cache operations by level and result",
		},
		[]string{"level", "result"}, // level=l1/l2, result=hit/miss
	)

	// CircuitBreakerStateGauge 熔断器状态（0=closed, 1=open, 2=half-open）
	CircuitBreakerStateGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "censorhub_circuit_breaker_state",
			Help: "Circuit breaker state: 0=closed, 1=open, 2=half-open",
		},
	)

	// FilterHitsTotal 过滤命中/未命中计数
	FilterHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "censorhub_filter_hits_total",
			Help: "Total number of filter hits",
		},
		[]string{"strategy", "is_hit"},
	)

	// EngineWordCount 引擎当前词条数
	EngineWordCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "censorhub_engine_word_count",
			Help: "Number of words loaded in the filter engine",
		},
	)

	// EngineRebuildTotal 引擎重建次数
	EngineRebuildTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "censorhub_engine_rebuild_total",
			Help: "Total number of engine rebuilds",
		},
	)
)
