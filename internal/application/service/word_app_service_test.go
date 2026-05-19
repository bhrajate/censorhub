package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/repository"
	domainservice "github.com/bhrajate/censorhub/internal/domain/service"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
)

type fakeWordRepo struct {
	mu          sync.Mutex
	activeWords []*entity.SensitiveWord
	fingerprint repository.WordFingerprint

	// 失败注入
	findCalls     int
	findFailUntil int // 前 N 次 FindAllActive 返回 findErr
	findErr       error

	fpCalls     int
	fpFailUntil int // 前 N 次 ActiveFingerprint 返回 fpErr
	fpErr       error
}

func (f *fakeWordRepo) Create(context.Context, *entity.SensitiveWord) error { return nil }
func (f *fakeWordRepo) Update(context.Context, *entity.SensitiveWord) error { return nil }
func (f *fakeWordRepo) Delete(context.Context, uint64) error                { return nil }
func (f *fakeWordRepo) FindByID(context.Context, uint64) (*entity.SensitiveWord, error) {
	return nil, nil
}
func (f *fakeWordRepo) FindByText(context.Context, string) (*entity.SensitiveWord, error) {
	return nil, nil
}
func (f *fakeWordRepo) List(context.Context, repository.WordQuery) ([]*entity.SensitiveWord, int64, error) {
	return nil, 0, nil
}
func (f *fakeWordRepo) FindAllActive(context.Context) ([]*entity.SensitiveWord, error) {
	f.mu.Lock()
	f.findCalls++
	calls := f.findCalls
	failUntil := f.findFailUntil
	err := f.findErr
	words := f.activeWords
	f.mu.Unlock()
	if calls <= failUntil {
		return nil, err
	}
	return words, nil
}
func (f *fakeWordRepo) BatchCreate(context.Context, []*entity.SensitiveWord) (int, error) {
	return 0, nil
}
func (f *fakeWordRepo) FindInBatches(context.Context, *valueobject.Category, int, func(words []*entity.SensitiveWord) error) error {
	return nil
}
func (f *fakeWordRepo) ActiveFingerprint(context.Context) (repository.WordFingerprint, error) {
	f.mu.Lock()
	f.fpCalls++
	calls := f.fpCalls
	failUntil := f.fpFailUntil
	err := f.fpErr
	fp := f.fingerprint
	f.mu.Unlock()
	if calls <= failUntil {
		return repository.WordFingerprint{}, err
	}
	return fp, nil
}

func (f *fakeWordRepo) findCallsObserved() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.findCalls
}

func (f *fakeWordRepo) fpCallsObserved() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fpCalls
}

type fakeFilterEngine struct {
	mu         sync.Mutex
	rebuilds   int
	rebuildErr error // 若非 nil，Rebuild 始终返回该错误
	version    uint64
}

func (f *fakeFilterEngine) Match(string) domainservice.MatchResult {
	return domainservice.MatchResult{}
}

func (f *fakeFilterEngine) Rebuild([]*entity.SensitiveWord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebuilds++
	if f.rebuildErr != nil {
		return f.rebuildErr
	}
	f.version++
	return nil
}

func (f *fakeFilterEngine) WordCount() int { return 0 }

func (f *fakeFilterEngine) Version() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version
}

func (f *fakeFilterEngine) rebuildsObserved() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rebuilds
}

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *eventRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	copy(out, r.events)
	return out
}

type fakeInvalidator struct {
	recorder *eventRecorder
}

func (f *fakeInvalidator) InvalidateByPrefix(_ context.Context, prefix string) error {
	f.recorder.add(prefix)
	return nil
}

// fastPollConfig 返回适合单元测试的 pollConfig：间隔 10ms，无 jitter，重试退避 2ms。
func fastPollConfig() pollConfig {
	return pollConfig{
		interval:        10 * time.Millisecond,
		jitter:          0,
		queryTimeout:    500 * time.Millisecond,
		retryBackoff:    2 * time.Millisecond,
		maxAttempts:     3,
		shutdownTimeout: 500 * time.Millisecond,
	}
}

func newTestApp(t *testing.T, repo *fakeWordRepo, engine *fakeFilterEngine, cache prefixInvalidator) *WordAppService {
	t.Helper()
	app := &WordAppService{
		repo:   repo,
		engine: engine,
		cache:  cache,
		logger: zap.NewNop(),
		cfg:    fastPollConfig(),
	}
	return app
}

// waitFor 在 deadline 内反复调用 cond，true 即返回。超时返回 false。
func waitFor(deadline time.Duration, cond func() bool) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// 指纹未变 → 不重建、不清缓存
func TestPollLoop_FingerprintUnchanged_NoRebuild(t *testing.T) {
	recorder := &eventRecorder{}
	repo := &fakeWordRepo{
		fingerprint: repository.WordFingerprint{Count: 5, MaxID: 5, MaxUpdatedUnixMicro: 1000},
	}
	engine := &fakeFilterEngine{}
	app := newTestApp(t, repo, engine, &fakeInvalidator{recorder: recorder})

	// 模拟 Init 已经把指纹同步过了（与 repo 当前指纹一致）
	app.lastFingerprint = repo.fingerprint

	app.Start(context.Background())
	t.Cleanup(app.Close)

	// 等到至少 3 次 poll 完成
	if !waitFor(500*time.Millisecond, func() bool { return repo.fpCallsObserved() >= 3 }) {
		t.Fatalf("expected at least 3 fp checks, got %d", repo.fpCallsObserved())
	}

	if got := engine.rebuildsObserved(); got != 0 {
		t.Fatalf("expected no rebuild when fingerprint unchanged, got %d", got)
	}
	for _, ev := range recorder.snapshot() {
		if ev == "filter:" {
			t.Fatalf("filter cache should not be invalidated when fingerprint unchanged, events: %v", recorder.snapshot())
		}
	}
}

// 指纹变化 → 触发重建 + 清 filter 缓存,且后续 poll 不再重复重建
func TestPollLoop_FingerprintChanged_TriggersRebuild(t *testing.T) {
	recorder := &eventRecorder{}
	repo := &fakeWordRepo{
		activeWords: []*entity.SensitiveWord{{ID: 1, Text: "赌博", Category: valueobject.CategoryCustom, Level: valueobject.RiskMedium, Status: valueobject.WordStatusActive}},
		fingerprint: repository.WordFingerprint{Count: 1, MaxID: 1, MaxUpdatedUnixMicro: 1000},
	}
	engine := &fakeFilterEngine{}
	app := newTestApp(t, repo, engine, &fakeInvalidator{recorder: recorder})
	// Init 时指纹是 {0,0,0}，所以第一次 poll 必然触发重建
	app.lastFingerprint = repository.WordFingerprint{}

	app.Start(context.Background())
	t.Cleanup(app.Close)

	if !waitFor(500*time.Millisecond, func() bool { return engine.rebuildsObserved() >= 1 }) {
		t.Fatalf("expected rebuild to happen, got %d", engine.rebuildsObserved())
	}

	// 给 loop 再跑几轮，确保不会重复重建
	time.Sleep(100 * time.Millisecond)
	if got := engine.rebuildsObserved(); got != 1 {
		t.Fatalf("expected exactly 1 rebuild after fingerprint stabilizes, got %d", got)
	}

	events := recorder.snapshot()
	found := false
	for _, ev := range events {
		if ev == "filter:" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected filter cache invalidation after rebuild, got %v", events)
	}
}

// 指纹查询连续失败,lastFingerprint 不被更新;恢复后重建
func TestPollLoop_FingerprintErrorRecoversNextTick(t *testing.T) {
	recorder := &eventRecorder{}
	repo := &fakeWordRepo{
		activeWords: []*entity.SensitiveWord{{ID: 1, Text: "赌博", Category: valueobject.CategoryCustom, Level: valueobject.RiskMedium, Status: valueobject.WordStatusActive}},
		fingerprint: repository.WordFingerprint{Count: 1, MaxID: 1, MaxUpdatedUnixMicro: 1000},
		fpFailUntil: 2,
		fpErr:       errors.New("transient db error"),
	}
	engine := &fakeFilterEngine{}
	app := newTestApp(t, repo, engine, &fakeInvalidator{recorder: recorder})
	app.lastFingerprint = repository.WordFingerprint{}

	app.Start(context.Background())
	t.Cleanup(app.Close)

	// 第 3 次 fp 才成功 → 第 3 次 tick 才会触发重建
	if !waitFor(500*time.Millisecond, func() bool { return engine.rebuildsObserved() >= 1 }) {
		t.Fatalf("expected rebuild after fingerprint recovers, fp_calls=%d rebuilds=%d",
			repo.fpCallsObserved(), engine.rebuildsObserved())
	}
	if got := repo.fpCallsObserved(); got < 3 {
		t.Fatalf("expected at least 3 fp calls (2 fail + 1 succeed), got %d", got)
	}
}

// FindAllActive 失败导致重建失败,lastFingerprint 不更新,下个 tick 自然重试
func TestPollLoop_RebuildFailureSelfHealsNextTick(t *testing.T) {
	recorder := &eventRecorder{}
	repo := &fakeWordRepo{
		activeWords:   []*entity.SensitiveWord{{ID: 1, Text: "赌博", Category: valueobject.CategoryCustom, Level: valueobject.RiskMedium, Status: valueobject.WordStatusActive}},
		fingerprint:   repository.WordFingerprint{Count: 1, MaxID: 1, MaxUpdatedUnixMicro: 1000},
		findFailUntil: 3, // 第一次 reconcile 内重试 3 次都失败,下个 tick 重新尝试
		findErr:       errors.New("transient db error"),
	}
	engine := &fakeFilterEngine{}
	app := newTestApp(t, repo, engine, &fakeInvalidator{recorder: recorder})
	app.lastFingerprint = repository.WordFingerprint{}

	app.Start(context.Background())
	t.Cleanup(app.Close)

	// 第二个 tick 才能成功（第一次 reconcile 用掉 3 次 find,都失败;第二次 reconcile 第 4 次 find 成功）
	if !waitFor(800*time.Millisecond, func() bool { return engine.rebuildsObserved() >= 1 }) {
		t.Fatalf("expected eventual rebuild after self-heal, find_calls=%d rebuilds=%d",
			repo.findCallsObserved(), engine.rebuildsObserved())
	}
	if got := repo.findCallsObserved(); got < 4 {
		t.Fatalf("expected at least 4 find calls (3 fail in tick1 + 1 succeed in tick2), got %d", got)
	}
}

// Close 应能在合理时间内停止 pollLoop,且幂等
func TestPollLoop_CloseStopsLoopIdempotent(t *testing.T) {
	recorder := &eventRecorder{}
	repo := &fakeWordRepo{}
	engine := &fakeFilterEngine{}
	app := newTestApp(t, repo, engine, &fakeInvalidator{recorder: recorder})
	app.Start(context.Background())

	// 让 loop 跑一会儿
	time.Sleep(50 * time.Millisecond)

	closed := make(chan struct{})
	go func() {
		app.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(1 * time.Second):
		t.Fatal("Close did not return within 1s")
	}

	// 二次 Close 应幂等
	app.Close()

	// Close 之后 fpCalls 应停止增长
	beforeCalls := repo.fpCallsObserved()
	time.Sleep(50 * time.Millisecond)
	if got := repo.fpCallsObserved(); got != beforeCalls {
		t.Fatalf("expected fp calls to stop after Close, before=%d after=%d", beforeCalls, got)
	}
}
