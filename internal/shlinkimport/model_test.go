package shlinkimport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWallClockIn(t *testing.T) {
	almaty, err := time.LoadLocation("Asia/Almaty")
	require.NoError(t, err)

	// pgx отдаёт время без пояса как UTC с теми же цифрами на часах
	naive := func(s string) time.Time {
		res, err := time.Parse("2006-01-02 15:04:05", s)
		require.NoError(t, err)
		return res
	}

	cases := []struct {
		name, in string
		loc      *time.Location
		wantUTC  string
	}{
		{"Алматы сейчас (+05)", "2026-11-25 10:01:05", almaty, "2026-11-25T05:01:05Z"},
		{"Алматы до 1 марта 2024 (+06)", "2023-06-01 12:00:00", almaty, "2023-06-01T06:00:00Z"},
		{"Алматы сразу после перевода часов", "2024-03-01 12:00:00", almaty, "2024-03-01T07:00:00Z"},
		{"Shlink в UTC", "2026-11-25 10:01:05", time.UTC, "2026-11-25T10:01:05Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.wantUTC, wallClockIn(naive(c.in), c.loc).UTC().Format(time.RFC3339))
		})
	}

	assert.Nil(t, wallClockPtrIn(nil, almaty))
	assert.Equal(t, "2026-11-25T05:01:05Z",
		wallClockPtrIn(new(naive("2026-11-25 10:01:05")), almaty).UTC().Format(time.RFC3339))
}

func TestLinkDiff(t *testing.T) {
	base := func() Link {
		return Link{
			CreatedAt: time.Date(2026, 11, 25, 5, 0, 0, 0, time.UTC),
			Domain:    "sl.demo.test", ShortCode: "aBc", LongURL: "https://example.com", Title: "t",
			Tags:       []string{"a", "b"},
			ValidSince: new(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			MaxVisits:  new(int32(3)),
			Crawlable:  true, ForwardQuery: true,
		}
	}

	same := base()
	// тот же момент в другом поясе — не расхождение
	same.CreatedAt = same.CreatedAt.In(time.FixedZone("", 5*3600))
	same.ValidSince = new(same.ValidSince.In(time.FixedZone("", 5*3600)))
	src := base()
	assert.Empty(t, src.diff(&same))

	other := base()
	other.ShortCode = "abc"
	other.Domain = ""
	other.LongURL += "/"
	other.Title = ""
	other.Tags = []string{"b", "a"}
	other.CreatedAt = other.CreatedAt.Add(time.Second)
	other.ValidSince = nil
	other.ValidUntil = new(time.Now())
	other.MaxVisits = new(int32(4))
	other.Crawlable = false
	other.ForwardQuery = false
	assert.Equal(t, []string{
		"short_code", "domain", "long_url", "title", "tags", "created_at",
		"valid_since", "valid_until", "max_visits", "crawlable", "forward_query",
	}, src.diff(&other))
}

func TestKeyString(t *testing.T) {
	assert.Equal(t, "aBc", Key{ShortCode: "aBc"}.String())
	assert.Equal(t, "sl.demo.test/aBc", Key{ShortCode: "aBc", Domain: "sl.demo.test"}.String())
}

func TestImportOptionsNormalize(t *testing.T) {
	opts := ImportOptions{SrcLocation: time.UTC}
	require.NoError(t, opts.normalize())
	assert.Equal(t, DefaultBatchSize, opts.BatchSize)
	assert.EqualValues(t, DefaultOverlap, opts.Overlap)
	assert.Equal(t, DefaultProgressEvery, opts.ProgressEvery)
	assert.NotNil(t, opts.Logf)

	assert.Error(t, (&ImportOptions{}).normalize(), "пояс источника обязателен")
	assert.Error(t, (&ImportOptions{SrcLocation: time.UTC, New: true, AfterID: 5}).normalize())
	assert.Error(t, (&ImportOptions{SrcLocation: time.UTC, New: true, Since: new(time.Now())}).normalize())
	assert.Error(t, (&ImportOptions{SrcLocation: time.UTC, BatchSize: -1}).normalize())
}
