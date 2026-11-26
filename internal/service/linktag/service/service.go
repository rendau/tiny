package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	localConstant "github.com/rendau/tiny/internal/service/linktag/service/constant"
)

// event — у записанной ссылки появились метки added и пропали removed
type event struct {
	added   []string
	removed []string
}

type Service struct {
	linkService linkServiceI

	events chan event

	recountFirstDelay time.Duration
	recountInterval   time.Duration

	wg sync.WaitGroup
}

func New(linkService linkServiceI) *Service {
	return &Service{
		linkService:       linkService,
		events:            make(chan event, localConstant.QueueSize),
		recountFirstDelay: localConstant.RecountFirstDelay,
		recountInterval:   localConstant.RecountInterval,
	}
}

func (s *Service) Changed(added, removed []string) {
	if len(added) == 0 && len(removed) == 0 {
		return
	}

	select {
	case s.events <- event{added: added, removed: removed}:
	default:
		// очередь полна: запись ссылки не ждёт, метки подберёт пересчёт
		slog.Warn("link tag: queue is full, event dropped", "added", added, "removed", removed)
	}
}

func (s *Service) Start(ctx context.Context) {
	s.wg.Go(func() {
		recountTimer := time.NewTimer(s.recountFirstDelay)
		defer recountTimer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case e := <-s.events:
				s.handle(ctx, e)
			case <-recountTimer.C:
				s.recount(ctx)
				recountTimer.Reset(s.recountInterval)
			}
		}
	})
}

func (s *Service) Wait() {
	s.wg.Wait()
}

// handle — новые метки дописываются в справочник; пропавшие убираются, если их не осталось ни у
// одной ссылки
func (s *Service) handle(ctx context.Context, e event) {
	ctx, cancel := context.WithTimeout(ctx, localConstant.EventTimeout)
	defer cancel()

	if len(e.added) > 0 {
		if err := s.linkService.TagAdd(ctx, e.added); err != nil {
			s.logFailure(ctx, "link tag: add failed", err, "tags", e.added)
		}
	}

	if len(e.removed) > 0 {
		if _, err := s.linkService.TagDropUnused(ctx, e.removed); err != nil {
			s.logFailure(ctx, "link tag: drop failed", err, "tags", e.removed)
		}
	}
}

func (s *Service) recount(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, localConstant.RecountTimeout)
	defer cancel()

	startedAt := time.Now()

	added, dropped, done, err := s.linkService.TagRecount(ctx)
	if err != nil {
		s.logFailure(ctx, "link tag: recount failed", err)
		return
	}
	if !done {
		slog.Info("link tag: recount skipped, another instance is running it")
		return
	}

	took := time.Since(startedAt).Round(time.Millisecond).String()
	if len(added) == 0 && len(dropped) == 0 {
		slog.Info("link tag: recount done, tags match", "took", took)
		return
	}
	// справочник разошёлся с таблицей ссылок: сообщение о метках потерялось либо ссылки писали
	// мимо сервиса
	slog.Warn("link tag: recount done, tags fixed", "took", took, "added", added, "dropped", dropped)
}

// logFailure — сбой пишется в журнал и ни на что не влияет: пропущенное подберёт пересчёт.
// Сервис останавливается — оборванное обращение не сбой.
func (s *Service) logFailure(ctx context.Context, msg string, err error, args ...any) {
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return
	}
	slog.Error(msg, append([]any{"error", err}, args...)...)
}
