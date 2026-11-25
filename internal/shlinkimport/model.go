// Package shlinkimport переносит короткие ссылки из базы Shlink 5.1.8 (Postgres) в таблицу link
// сервиса tiny. Разовый инструмент: к сервису не подключается, слоёв сервиса не использует.
//
// Соответствие полей (short_urls Shlink → link tiny):
//
//	short_code                      → short_code
//	domains.authority (по domain_id) → domain; у ссылки основного домена (domain_id пуст) — ''
//	original_url                    → long_url
//	title                           → title; пусто (NULL) → ''
//	tags через short_urls_in_tags   → tags, по алфавиту (в таком порядке их отдаёт сам Shlink)
//	valid_since, valid_until        → valid_since, valid_until
//	max_visits                      → max_visits
//	crawlable, forward_query        → crawlable, forward_query
//	date_created                    → created_at и modified_at
//
// Время Shlink хранит без часового пояса — в поясе, в котором он сам работает (его настройка
// TIMEZONE). Поэтому пояс источника задаётся явно, и время пересчитывается в точный момент.
package shlinkimport

import (
	"slices"
	"time"
)

// Key — ключ ссылки: код + домен (пустой домен — основной). Код чувствителен к регистру.
type Key struct {
	ShortCode string
	Domain    string
}

func (k Key) String() string {
	if k.Domain == "" {
		return k.ShortCode
	}
	return k.Domain + "/" + k.ShortCode
}

// Link — ссылка в том виде, в каком она ложится в таблицу link.
type Link struct {
	SrcID        int64 // id строки в short_urls Shlink; в приёмник не пишется
	CreatedAt    time.Time
	Domain       string
	ShortCode    string
	LongURL      string
	Title        string
	Tags         []string
	ValidSince   *time.Time
	ValidUntil   *time.Time
	MaxVisits    *int32
	Crawlable    bool
	ForwardQuery bool
}

func (l *Link) Key() Key {
	return Key{ShortCode: l.ShortCode, Domain: l.Domain}
}

// diff возвращает имена полей, которыми ссылка в приёмнике отличается от ссылки источника.
func (l *Link) diff(dst *Link) []string {
	var fields []string
	add := func(same bool, name string) {
		if !same {
			fields = append(fields, name)
		}
	}
	add(l.ShortCode == dst.ShortCode, "short_code")
	add(l.Domain == dst.Domain, "domain")
	add(l.LongURL == dst.LongURL, "long_url")
	add(l.Title == dst.Title, "title")
	add(slices.Equal(l.Tags, dst.Tags), "tags")
	add(l.CreatedAt.Equal(dst.CreatedAt), "created_at")
	add(equalTimePtr(l.ValidSince, dst.ValidSince), "valid_since")
	add(equalTimePtr(l.ValidUntil, dst.ValidUntil), "valid_until")
	add(equalPtr(l.MaxVisits, dst.MaxVisits), "max_visits")
	add(l.Crawlable == dst.Crawlable, "crawlable")
	add(l.ForwardQuery == dst.ForwardQuery, "forward_query")
	return fields
}

func equalTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// wallClockIn переводит время без пояса (pgx отдаёт его как UTC с теми же цифрами на часах)
// в точный момент: те же цифры на часах, но в поясе loc.
func wallClockIn(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

func wallClockPtrIn(t *time.Time, loc *time.Location) *time.Time {
	if t == nil {
		return nil
	}
	return new(wallClockIn(*t, loc))
}
