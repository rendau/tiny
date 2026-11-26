package service

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReserve(t *testing.T) {
	s := New(50 * time.Millisecond)
	ivan := map[string]int{"ivan": 3}

	for range 3 {
		_, ok := s.Reserve(ivan)
		assert.True(t, ok)
	}
	_, ok := s.Reserve(ivan)
	assert.False(t, ok, "3 попытки — дальше нельзя")

	_, ok = s.Reserve(map[string]int{"olga": 3})
	assert.True(t, ok, "другой ключ не затронут")

	time.Sleep(60 * time.Millisecond)
	_, ok = s.Reserve(ivan)
	assert.True(t, ok, "окно прошло")
}

func TestReserveRelease(t *testing.T) {
	s := New(time.Minute)
	limits := map[string]int{"ivan": 1}

	release, ok := s.Reserve(limits)
	require.True(t, ok)
	_, ok = s.Reserve(limits)
	assert.False(t, ok)

	release()
	assert.Empty(t, s.attempts, "возвращённое место в счёте не остаётся")

	release2, ok := s.Reserve(limits)
	require.True(t, ok, "место вернули — можно снова")

	release() // повторный вызов чужое место не трогает
	_, ok = s.Reserve(limits)
	assert.False(t, ok)

	release2()
	_, ok = s.Reserve(limits)
	assert.True(t, ok)
}

// Предел исчерпан по одному из ключей — место не занято ни по одному
func TestReserveAllOrNothing(t *testing.T) {
	s := New(time.Minute)

	_, ok := s.Reserve(map[string]int{"ip": 1})
	require.True(t, ok)

	_, ok = s.Reserve(map[string]int{"ip": 1, "ivan": 5})
	require.False(t, ok)
	assert.NotContains(t, s.attempts, "ivan")

	for range 5 {
		_, ok = s.Reserve(map[string]int{"ivan": 5})
		assert.True(t, ok)
	}
}

// Одновременные попытки: место достаётся ровно стольким, сколько разрешено
func TestReserveParallel(t *testing.T) {
	s := New(time.Minute)

	var (
		wg     sync.WaitGroup
		passed atomic.Int32
	)
	for range 200 {
		wg.Go(func() {
			if _, ok := s.Reserve(map[string]int{"ivan": 5, "ip": 20}); ok {
				passed.Add(1)
			}
		})
	}
	wg.Wait()

	assert.EqualValues(t, 5, passed.Load())
}

// Ключи без свежих попыток убираются, когда их накопилось много
func TestReserveSweep(t *testing.T) {
	s := New(time.Millisecond)

	for i := range sweepThreshold + 1 {
		s.attempts[fmt.Sprintf("old-%d", i)] = []time.Time{time.Now().Add(-time.Hour)}
	}

	_, ok := s.Reserve(map[string]int{"ivan": 1})
	require.True(t, ok)
	assert.Len(t, s.attempts, 1)
}
