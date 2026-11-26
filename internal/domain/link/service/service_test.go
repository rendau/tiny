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

// Число действующих ссылок считается как «все минус истёкшие» (подсчёт в лоб читает всю таблицу);
// остальные фильтры считает сам запрос списка
func TestListActiveCount(t *testing.T) {
	ctx := context.Background()

	cases := map[string]struct {
		pars      model.ListReq
		wantCount int64
		fast      bool // число получено как «все минус истёкшие»: запрос списка строки не считал
	}{
		"действующие — все минус истёкшие": {model.ListReq{Expired: new(false)}, 700, true},
		"пустой поиск — не фильтр":         {model.ListReq{Expired: new(false), Search: new("")}, 700, true},
		"без фильтра — запрос списка":      {model.ListReq{}, 7, false},
		"истёкшие — запрос списка":         {model.ListReq{Expired: new(true)}, 7, false},
		"метка — запрос списка":            {model.ListReq{Tag: new("sms")}, 7, false},
		"действующие с меткой — запрос":    {model.ListReq{Expired: new(false), Tag: new("sms")}, 7, false},
		"действующие с поиском — запрос":   {model.ListReq{Expired: new(false), Search: new("shop")}, 7, false},
		"действующие с кодом — запрос":     {model.ListReq{Expired: new(false), ShortCode: new("abc")}, 7, false},
		"действующие с доменом — запрос":   {model.ListReq{Expired: new(false), Domain: new("")}, 7, false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{countAll: 1000, countExpired: 300, listCount: 7}
			svc := New(repo)

			pars := c.pars
			pars.WithTotalCount = true
			items, count, err := svc.List(ctx, &pars)
			require.NoError(t, err)
			assert.Len(t, items, 1, "сами ссылки читаются всегда")
			assert.Equal(t, c.wantCount, count)
			assert.Equal(t, !c.fast, repo.listPars.WithTotalCount, "считал ли строки запрос списка")
			assert.True(t, pars.WithTotalCount, "параметры вызывающего не меняются")

			// только число, без ссылок
			repo.listCalls = 0
			pars.OnlyCount = true
			items, count, err = svc.List(ctx, &pars)
			require.NoError(t, err)
			assert.Empty(t, items)
			assert.Equal(t, c.wantCount, count)
			assert.Equal(t, c.fast, repo.listCalls == 0, "при быстром подсчёте запрос списка не нужен")
		})
	}

	t.Run("без общего числа ничего не считается", func(t *testing.T) {
		repo := &fakeRepo{countAll: 1000, listCount: 7}
		_, count, err := New(repo).List(ctx, &model.ListReq{Expired: new(false)})
		require.NoError(t, err)
		assert.Zero(t, count)
		assert.Zero(t, repo.countCalls.Load())
	})

	t.Run("ссылка истекла между двумя подсчётами — не меньше нуля", func(t *testing.T) {
		repo := &fakeRepo{countAll: 5, countExpired: 9}
		pars := model.ListReq{Expired: new(false)}
		pars.WithTotalCount = true
		_, count, err := New(repo).List(ctx, &pars)
		require.NoError(t, err)
		assert.Zero(t, count)
	})

	t.Run("сбой подсчёта — ошибка, а не неверное число", func(t *testing.T) {
		repo := &fakeRepo{countAll: 1000, countErr: errors.New("db down")}
		pars := model.ListReq{Expired: new(false)}
		pars.WithTotalCount = true
		_, _, err := New(repo).List(ctx, &pars)
		require.Error(t, err)
	})
}
