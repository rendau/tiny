package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLinkService struct {
	mu       sync.Mutex
	added    [][]string
	dropped  [][]string
	recounts int
	err      error

	// если задан — TagAdd ждёт, пока его закроют (справочник «завис»)
	block chan struct{}
}

func (f *fakeLinkService) TagAdd(ctx context.Context, names []string) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, names)
	return f.err
}

func (f *fakeLinkService) TagDropUnused(_ context.Context, names []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, names)
	return names, f.err
}

func (f *fakeLinkService) TagRecount(context.Context) ([]string, []string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recounts++
	return []string{"sms"}, nil, true, f.err
}

func (f *fakeLinkService) snapshot() (added, dropped [][]string, recounts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string{}, f.added...), append([][]string{}, f.dropped...), f.recounts
}

func start(t *testing.T, svc *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	t.Cleanup(func() {
		cancel()
		svc.Wait()
	})
}

func TestChangedAddsAndDropsInBackground(t *testing.T) {
	link := &fakeLinkService{}
	svc := New(link)
	start(t, svc)

	svc.Changed([]string{"sms", "promo"}, nil)
	svc.Changed(nil, []string{"old"})
	svc.Changed([]string{"news"}, []string{"sms"})
	svc.Changed(nil, nil) // нечего делать — в очередь не попадает

	require.Eventually(t, func() bool {
		added, dropped, _ := link.snapshot()
		return len(added) == 2 && len(dropped) == 2
	}, 2*time.Second, 5*time.Millisecond)

	added, dropped, recounts := link.snapshot()
	assert.Equal(t, [][]string{{"sms", "promo"}, {"news"}}, added)
	assert.Equal(t, [][]string{{"old"}, {"sms"}}, dropped)
	assert.Zero(t, recounts, "пересчёт — только по расписанию")
}

// Главное свойство: тот, кто записал ссылку, справочника не ждёт — ни когда тот завис, ни когда
// очередь переполнена
func TestChangedNeverBlocks(t *testing.T) {
	link := &fakeLinkService{block: make(chan struct{})}
	svc := New(link)
	svc.events = make(chan event, 2)
	start(t, svc)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			svc.Changed([]string{"sms"}, nil)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("сообщение о метках ждёт справочник")
	}

	close(link.block)
}

func TestFailuresDoNotStopTheWork(t *testing.T) {
	link := &fakeLinkService{err: errors.New("db down")}
	svc := New(link)
	svc.recountFirstDelay, svc.recountInterval = time.Millisecond, 5*time.Millisecond
	start(t, svc)

	svc.Changed([]string{"a"}, []string{"b"})
	svc.Changed([]string{"c"}, nil)

	require.Eventually(t, func() bool {
		added, dropped, recounts := link.snapshot()
		return len(added) == 2 && len(dropped) == 1 && recounts >= 2
	}, 2*time.Second, 5*time.Millisecond, "сбой одного обращения остальные не отменяет")
}

func TestRecountRunsAfterDelayAndThenByInterval(t *testing.T) {
	link := &fakeLinkService{}
	svc := New(link)
	svc.recountFirstDelay, svc.recountInterval = 30*time.Millisecond, 20*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)

	_, _, recounts := link.snapshot()
	assert.Zero(t, recounts, "сразу после запуска пересчёта нет")
	require.Eventually(t, func() bool {
		_, _, recounts := link.snapshot()
		return recounts >= 3
	}, 2*time.Second, 5*time.Millisecond)

	cancel()
	svc.Wait()

	_, _, stopped := link.snapshot()
	time.Sleep(60 * time.Millisecond)
	_, _, after := link.snapshot()
	assert.Equal(t, stopped, after, "после остановки пересчётов нет")
}
