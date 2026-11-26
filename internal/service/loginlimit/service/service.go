package service

import (
	"slices"
	"sync"
	"time"
)

// sweepThreshold — с какого числа ключей при очередной попытке убираются ключи без свежих попыток
const sweepThreshold = 10_000

type Service struct {
	window time.Duration

	mu       sync.Mutex
	attempts map[string][]time.Time
}

// New — счётчик попыток входа за window
func New(window time.Duration) *Service {
	return &Service{
		window:   window,
		attempts: map[string][]time.Time{},
	}
}

// Reserve занимает место под попытку входа сразу по всем ключам. limits — предел по каждому ключу:
// сколько попыток за окно по нему можно. Хотя бы по одному ключу предел исчерпан — ok=false, и
// место не занято нигде.
//
// Место занимается до проверки пароля, а не после: иначе одновременные запросы все видят счётчик
// ещё пустым и проходят мимо предела. Вход удался (или попытки не было — сбой базы) — место
// возвращается вызовом release; при неверном пароле release не зовут, и попытка остаётся в счёте
// до конца окна. Повторный вызов release ничего не делает.
func (s *Service) Reserve(limits map[string]int) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	for key, limit := range limits {
		if len(s.recent(key, now)) >= limit {
			return nil, false
		}
	}

	keys := make([]string, 0, len(limits))
	for key := range limits {
		s.attempts[key] = append(s.attempts[key], now)
		keys = append(keys, key)
	}

	// карта не растёт бесконечно от перебора разных логинов и адресов
	if len(s.attempts) > sweepThreshold {
		for key := range s.attempts {
			s.recent(key, now)
		}
	}

	var once sync.Once
	return func() { once.Do(func() { s.release(keys, now) }) }, true
}

// release убирает из счёта попытку, занятую в момент at
func (s *Service) release(keys []string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, key := range keys {
		list := s.attempts[key]
		i := slices.IndexFunc(list, at.Equal)
		if i < 0 {
			continue
		}
		if len(list) == 1 {
			delete(s.attempts, key)
			continue
		}
		s.attempts[key] = slices.Delete(list, i, i+1)
	}
}

// recent — попытки по ключу в окне; устаревшие забываются. Вызывается под mu
func (s *Service) recent(key string, now time.Time) []time.Time {
	list := s.attempts[key]
	i := 0
	for i < len(list) && now.Sub(list[i]) > s.window {
		i++
	}
	if i == len(list) {
		delete(s.attempts, key)
		return nil
	}
	list = list[i:]
	s.attempts[key] = list
	return list
}
