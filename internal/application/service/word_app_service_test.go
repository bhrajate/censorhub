package service

import (
	"context"
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
	activeWords []*entity.SensitiveWord
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
	return f.activeWords, nil
}
func (f *fakeWordRepo) BatchCreate(context.Context, []*entity.SensitiveWord) (int, error) {
	return 0, nil
}
func (f *fakeWordRepo) FindInBatches(context.Context, *valueobject.Category, int, func(words []*entity.SensitiveWord) error) error {
	return nil
}

type fakeFilterEngine struct {
	mu          sync.Mutex
	rebuilds    int
	rebuildDone chan struct{}
}

func (f *fakeFilterEngine) Match(string) domainservice.MatchResult {
	return domainservice.MatchResult{}
}

func (f *fakeFilterEngine) Rebuild([]*entity.SensitiveWord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebuilds++
	if f.rebuildDone != nil {
		select {
		case <-f.rebuildDone:
		default:
			close(f.rebuildDone)
		}
	}
	return nil
}

func (f *fakeFilterEngine) WordCount() int { return 0 }

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

type fakePublisher struct {
	recorder    *eventRecorder
	publishDone chan struct{}
}

func (f *fakePublisher) PublishWordUpdate(context.Context) error {
	f.recorder.add("publish")
	if f.publishDone != nil {
		select {
		case <-f.publishDone:
		default:
			close(f.publishDone)
		}
	}
	return nil
}

func TestWordAppService_TriggerRebuildInvalidatesFilterCacheAfterRebuild(t *testing.T) {
	recorder := &eventRecorder{}
	repo := &fakeWordRepo{
		activeWords: []*entity.SensitiveWord{
			{
				ID:       1,
				Text:     "赌博",
				Category: valueobject.CategoryCustom,
				Level:    valueobject.RiskMedium,
				Status:   valueobject.WordStatusActive,
			},
		},
	}
	engine := &fakeFilterEngine{rebuildDone: make(chan struct{})}
	cache := &fakeInvalidator{recorder: recorder}
	pubsub := &fakePublisher{recorder: recorder, publishDone: make(chan struct{})}
	app := &WordAppService{
		repo:   repo,
		engine: engine,
		cache:  cache,
		pubsub: pubsub,
		logger: zap.NewNop(),
	}

	app.triggerRebuild(context.Background())

	initialEvents := recorder.snapshot()
	if len(initialEvents) != 1 || initialEvents[0] != "words:" {
		t.Fatalf("expected only words cache invalidation before rebuild, got %v", initialEvents)
	}

	select {
	case <-engine.rebuildDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rebuild")
	}

	select {
	case <-pubsub.publishDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for publish")
	}

	events := recorder.snapshot()
	want := []string{"words:", "filter:", "publish"}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d: %v", len(want), len(events), events)
	}
	for i, event := range want {
		if events[i] != event {
			t.Fatalf("expected event %d to be %q, got %q (all events: %v)", i, event, events[i], events)
		}
	}
}
