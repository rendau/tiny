package service

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/samber/lo"
)

// cachedList — список, ради которого читается вся таблица: помнится ttl и сбрасывается при
// изменении ссылок в этом же процессе.
//
// Выборка идёт дольше всего остального в сервисе (секунды, на занятой базе — десятки секунд),
// поэтому замков два:
//   - loading — одна выборка за раз: одновременные запросы ждут её, а не запускают каждый свою;
//   - mu — только состояние, без обращений к базе. Сброс берёт лишь его и выборку не ждёт: иначе
//     создание ссылки стояло бы в очереди за чтением всей таблицы.
type cachedList struct {
	ttl time.Duration

	loading chan struct{}

	mu       sync.Mutex
	value    []string
	loadedAt time.Time
	valid    bool
	gen      uint64 // номер сброса: выборка, начатая до сброса, не запоминается
}

func newCachedList(ttl time.Duration) *cachedList {
	return &cachedList{ttl: ttl, loading: make(chan struct{}, 1)}
}

func (c *cachedList) get(ctx context.Context, load func(context.Context) ([]string, error)) ([]string, error) {
	if value, ok := c.fresh(); ok {
		return value, nil
	}

	select {
	case c.loading <- struct{}{}:
		defer func() { <-c.loading }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// пока ждали очереди, список мог загрузить другой запрос
	if value, ok := c.fresh(); ok {
		return value, nil
	}

	c.mu.Lock()
	gen := c.gen
	c.mu.Unlock()

	value, err := load(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.gen == gen {
		c.value, c.loadedAt, c.valid = value, time.Now(), true
	}
	c.mu.Unlock()

	return slices.Clone(value), nil
}

// fresh — список из памяти, если он загружен и не устарел
func (c *cachedList) fresh() ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.valid || time.Since(c.loadedAt) >= c.ttl {
		return nil, false
	}
	return slices.Clone(c.value), true
}

// invalidate — забыть список. Идущую выборку не ждёт: её результат просто не запомнится.
func (c *cachedList) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.valid = false
	c.gen++
}

// invalidateIfMissing — забыть список, только если в нём нет хотя бы одного из values. Для
// добавления строк: появление ссылки с уже известными значениями список не меняет, и читать
// таблицу заново незачем.
func (c *cachedList) invalidateIfMissing(values []string) {
	if len(values) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid && lo.Every(c.value, values) {
		return
	}

	c.valid = false
	c.gen++
}
