package service

import (
	"context"
	"slices"
	"sync"
	"time"
)

// cachedList — список, ради которого читается вся таблица: помнится ttl и сбрасывается при
// изменении ссылок в этом же процессе. Загрузка идёт под блокировкой: одновременные запросы
// ждут одну выборку, а не запускают каждый свою.
type cachedList struct {
	ttl time.Duration

	mu       sync.Mutex
	value    []string
	loadedAt time.Time
	valid    bool
}

func newCachedList(ttl time.Duration) *cachedList {
	return &cachedList{ttl: ttl}
}

func (c *cachedList) get(ctx context.Context, load func(context.Context) ([]string, error)) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid && time.Since(c.loadedAt) < c.ttl {
		return slices.Clone(c.value), nil
	}

	value, err := load(ctx)
	if err != nil {
		return nil, err
	}

	c.value, c.loadedAt, c.valid = value, time.Now(), true

	return slices.Clone(value), nil
}

func (c *cachedList) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.valid = false
}
