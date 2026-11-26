package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/tiny/internal/domain/link/model"
	"github.com/rendau/tiny/internal/errs"
)

// fakeRepo — хранилище в памяти: только то, что нужно сервису
type fakeRepo struct {
	RepoDbI

	taken     map[string]bool // занятые коды
	created   []string
	createErr error

	// подсчёты и то, что отвечает List (подсчёты идут одновременно)
	countAll     int64
	countExpired int64
	countErr     error
	countCalls   atomic.Int32
	listCount    int64
	listCalls    int
	listPars     model.ListReq

	// подсчёт с потолком: сколько ссылок подходит под фильтр на самом деле
	matched     int64
	upToCalls   atomic.Int32
	upToLimit   atomic.Int64
	upToCountOf model.ListReq
}

func (r *fakeRepo) Create(_ context.Context, obj *model.Edit) (int64, error) {
	if r.createErr != nil {
		return 0, r.createErr
	}
	code := *obj.ShortCode
	if r.taken[code] {
		return 0, errs.ShortCodeExists
	}
	if r.taken == nil {
		r.taken = map[string]bool{}
	}
	r.taken[code] = true
	r.created = append(r.created, code)
	return int64(len(r.created)), nil
}

func (r *fakeRepo) Update(context.Context, int64, *model.Edit) error { return nil }

func (r *fakeRepo) List(_ context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	r.listCalls++
	r.listPars = *pars

	var count int64
	if pars.WithTotalCount || pars.OnlyCount {
		count = r.listCount
	}
	if pars.OnlyCount {
		return []*model.Main{}, count, nil
	}
	return []*model.Main{{Id: 1}}, count, nil
}

func (r *fakeRepo) CountUpTo(_ context.Context, pars *model.ListReq, limit int64) (int64, error) {
	r.upToCalls.Add(1)
	r.upToLimit.Store(limit)
	r.upToCountOf = *pars
	return min(r.matched, limit), r.countErr
}

func (r *fakeRepo) CountAll(context.Context) (int64, error) {
	r.countCalls.Add(1)
	return r.countAll, r.countErr
}

func (r *fakeRepo) CountExpired(context.Context) (int64, error) {
	r.countCalls.Add(1)
	return r.countExpired, r.countErr
}

func TestGenerateCode(t *testing.T) {
	seen := map[string]bool{}
	chars := map[rune]bool{}

	for range 2000 {
		code, err := GenerateCode(5)
		require.NoError(t, err)
		require.Len(t, code, 5)
		for _, c := range code {
			require.True(t, strings.ContainsRune(codeAlphabet, c), "символ %q не из набора", c)
			chars[c] = true
		}
		seen[code] = true
	}

	assert.Greater(t, len(seen), 1990, "коды не повторяются (916 млн вариантов)")
	assert.Len(t, chars, len(codeAlphabet), "встречаются все 62 символа: цифры, строчные и прописные")

	for _, length := range []int{1, 4, 6, 100} {
		code, err := GenerateCode(length)
		require.NoError(t, err)
		assert.Len(t, code, length)
	}

	_, err := GenerateCode(0)
	assert.Error(t, err)
}

func TestCreateGeneratesFreeCode(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{}
	svc := New(repo)

	edit := &model.Edit{LongUrl: new("https://example.com"), ModifiedAt: new(time.Now())}
	id, err := svc.Create(ctx, edit, 6)
	require.NoError(t, err)
	assert.EqualValues(t, 1, id)
	require.Len(t, repo.created, 1)
	assert.Len(t, repo.created[0], 6)
	assert.Nil(t, edit.ModifiedAt, "время правки при создании ставит БД")
}

// при длине 1 кодов всего 62: когда почти все заняты, сервис перебирает, пока не попадёт в свободный
func TestCreateRetriesOnCollision(t *testing.T) {
	ctx := context.Background()

	repo := &fakeRepo{taken: map[string]bool{}}
	for _, c := range codeAlphabet[:40] {
		repo.taken[string(c)] = true
	}
	svc := New(repo)

	created := 0
	for range 5 {
		if _, err := svc.Create(ctx, &model.Edit{LongUrl: new("https://example.com")}, 1); err == nil {
			created++
		}
	}
	assert.Equal(t, 5, created, "20 попыток хватает, когда свободна треть кодов")
	for _, code := range repo.created {
		assert.NotContains(t, codeAlphabet[:40], code, "занятый код не выдан")
	}

	// свободных кодов нет совсем — ошибка, а не вечный цикл
	for _, c := range codeAlphabet {
		repo.taken[string(c)] = true
	}
	_, err := svc.Create(ctx, &model.Edit{LongUrl: new("https://example.com")}, 1)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errs.ShortCodeExists, "это сбой, а не «код занят»")
}

func TestCreateWithOwnCode(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{taken: map[string]bool{"busy": true}}
	svc := New(repo)

	_, err := svc.Create(ctx, &model.Edit{ShortCode: new("mine"), LongUrl: new("https://example.com")}, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"mine"}, repo.created, "свой код берётся как есть")

	_, err = svc.Create(ctx, &model.Edit{ShortCode: new("busy"), LongUrl: new("https://example.com")}, 5)
	assert.ErrorIs(t, err, errs.ShortCodeExists, "занятый свой код не подменяется случайным")

	repo.createErr = errors.New("db down")
	_, err = svc.Create(ctx, &model.Edit{LongUrl: new("https://example.com")}, 5)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errs.ShortCodeExists)
}

// Откуда берётся «Найдено: N»: действующие — «все минус истёкшие» (подсчёт в лоб читает всю
// таблицу); без фильтра и «только истёкшие» — точное число из запроса списка (один индекс);
// с любым другим фильтром — подсчёт с потолком
func TestListCountSource(t *testing.T) {
	ctx := context.Background()

	const (
		byList   = "запрос списка"
		bySub    = "все минус истёкшие"
		byCapped = "с потолком"
	)

	cases := map[string]struct {
		pars      model.ListReq
		wantCount int64
		source    string
	}{
		"действующие":              {model.ListReq{Expired: new(false)}, 700, bySub},
		"пустой поиск — не фильтр": {model.ListReq{Expired: new(false), Search: new("")}, 700, bySub},
		"без фильтра":              {model.ListReq{}, 7, byList},
		"истёкшие":                 {model.ListReq{Expired: new(true)}, 7, byList},
		"метка":                    {model.ListReq{Tag: new("sms")}, 42, byCapped},
		"поиск":                    {model.ListReq{Search: new("shop")}, 42, byCapped},
		"код":                      {model.ListReq{ShortCode: new("abc")}, 42, byCapped},
		"домен":                    {model.ListReq{Domain: new("")}, 42, byCapped},
		"истёкшие с меткой":        {model.ListReq{Expired: new(true), Tag: new("sms")}, 42, byCapped},
		"действующие с меткой":     {model.ListReq{Expired: new(false), Tag: new("sms")}, 42, byCapped},
		"действующие с поиском":    {model.ListReq{Expired: new(false), Search: new("shop")}, 42, byCapped},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{countAll: 1000, countExpired: 300, listCount: 7, matched: 42}
			svc := New(repo)

			pars := c.pars
			pars.WithTotalCount = true
			items, count, capped, err := svc.List(ctx, &pars)
			require.NoError(t, err)
			assert.Len(t, items, 1, "сами ссылки читаются всегда")
			assert.Equal(t, c.wantCount, count)
			assert.False(t, capped)
			assert.Equal(t, c.source == byList, repo.listPars.WithTotalCount, "считал ли строки запрос списка")
			assert.Equal(t, c.source == byCapped, repo.upToCalls.Load() == 1, "был ли подсчёт с потолком")
			assert.Equal(t, c.source == bySub, repo.countCalls.Load() == 2, "были ли подсчёты «все» и «истёкшие»")
			assert.True(t, pars.WithTotalCount, "параметры вызывающего не меняются")
			if c.source == byCapped {
				assert.Equal(t, c.pars.Tag, repo.upToCountOf.Tag, "считается тот же фильтр, что и в списке")
			}

			// только число, без ссылок
			repo.listCalls = 0
			pars.OnlyCount = true
			items, count, capped, err = svc.List(ctx, &pars)
			require.NoError(t, err)
			assert.Empty(t, items)
			assert.Equal(t, c.wantCount, count)
			assert.False(t, capped)
			assert.Equal(t, c.source != byList, repo.listCalls == 0, "число не из запроса списка — он не нужен")
		})
	}

	t.Run("найдено больше потолка — потолок и признак", func(t *testing.T) {
		for matched, want := range map[int64]struct {
			count  int64
			capped bool
		}{
			0:                 {0, false},
			totalCountCap - 1: {totalCountCap - 1, false},
			totalCountCap:     {totalCountCap, false},
			totalCountCap + 1: {totalCountCap, true},
			450_000:           {totalCountCap, true},
		} {
			repo := &fakeRepo{matched: matched}
			pars := model.ListReq{Tag: new("delivery")}
			pars.WithTotalCount = true
			_, count, capped, err := New(repo).List(ctx, &pars)
			require.NoError(t, err)
			assert.Equal(t, want.count, count, "подходит %d", matched)
			assert.Equal(t, want.capped, capped, "подходит %d", matched)
			assert.Equal(t, int64(totalCountCap+1), repo.upToLimit.Load(), "на одну больше потолка — иначе «ровно» не отличить от «больше»")
		}
	})

	t.Run("с меткой без общего числа ничего не считается", func(t *testing.T) {
		repo := &fakeRepo{matched: 42}
		_, count, capped, err := New(repo).List(ctx, &model.ListReq{Tag: new("sms")})
		require.NoError(t, err)
		assert.Zero(t, count)
		assert.False(t, capped)
		assert.Zero(t, repo.upToCalls.Load())
	})

	t.Run("сбой подсчёта с потолком — ошибка, а не неверное число", func(t *testing.T) {
		repo := &fakeRepo{matched: 42, countErr: errors.New("db down")}
		pars := model.ListReq{Tag: new("sms")}
		pars.WithTotalCount = true
		_, _, _, err := New(repo).List(ctx, &pars)
		require.Error(t, err)
	})

	t.Run("без общего числа ничего не считается", func(t *testing.T) {
		repo := &fakeRepo{countAll: 1000, listCount: 7}
		_, count, _, err := New(repo).List(ctx, &model.ListReq{Expired: new(false)})
		require.NoError(t, err)
		assert.Zero(t, count)
		assert.Zero(t, repo.countCalls.Load())
	})

	t.Run("ссылка истекла между двумя подсчётами — не меньше нуля", func(t *testing.T) {
		repo := &fakeRepo{countAll: 5, countExpired: 9}
		pars := model.ListReq{Expired: new(false)}
		pars.WithTotalCount = true
		_, count, _, err := New(repo).List(ctx, &pars)
		require.NoError(t, err)
		assert.Zero(t, count)
	})

	t.Run("сбой подсчёта — ошибка, а не неверное число", func(t *testing.T) {
		repo := &fakeRepo{countAll: 1000, countErr: errors.New("db down")}
		pars := model.ListReq{Expired: new(false)}
		pars.WithTotalCount = true
		_, _, _, err := New(repo).List(ctx, &pars)
		require.Error(t, err)
	})
}
