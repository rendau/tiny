//go:build integration

package db

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	commonModel "github.com/rendau/tiny/internal/domain/common/model"
	"github.com/rendau/tiny/internal/domain/link/linktest"
	"github.com/rendau/tiny/internal/domain/link/model"
	"github.com/rendau/tiny/internal/errs"
)

func newEdit(code, longUrl string) *model.Edit {
	return &model.Edit{
		ShortCode: &code,
		LongUrl:   &longUrl,
	}
}

func codes(items []*model.Main) []string {
	return lo.Map(items, func(item *model.Main, _ int) string { return item.ShortCode })
}

func TestCrud(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	until := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)

	id, err := repo.Create(ctx, &model.Edit{
		Domain:       new("sl.demo.test"),
		ShortCode:    new("aBc12"),
		LongUrl:      new("https://example.com/a?b=1"),
		Title:        new("Название"),
		Tags:         new([]string{"a", "b"}),
		ValidUntil:   new(&until),
		MaxVisits:    new(new(int32(7))),
		ForwardQuery: new(false),
	})
	require.NoError(t, err)
	require.Positive(t, id)

	item, found, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "sl.demo.test", item.Domain)
	assert.Equal(t, "aBc12", item.ShortCode)
	assert.Equal(t, "https://example.com/a?b=1", item.LongUrl)
	assert.Equal(t, "Название", item.Title)
	assert.Equal(t, []string{"a", "b"}, item.Tags)
	assert.Nil(t, item.ValidSince)
	require.NotNil(t, item.ValidUntil)
	assert.True(t, until.Equal(*item.ValidUntil))
	require.NotNil(t, item.MaxVisits)
	assert.EqualValues(t, 7, *item.MaxVisits)
	assert.False(t, item.ForwardQuery)
	assert.WithinDuration(t, time.Now(), item.CreatedAt, time.Minute)

	// значения по умолчанию из БД
	id2, err := repo.Create(ctx, newEdit("def", "https://example.com/def"))
	require.NoError(t, err)
	item2, _, err := repo.Get(ctx, id2)
	require.NoError(t, err)
	assert.Empty(t, item2.Domain)
	assert.Empty(t, item2.Title)
	assert.Equal(t, []string{}, item2.Tags)
	assert.Nil(t, item2.ValidUntil)
	assert.Nil(t, item2.MaxVisits)
	assert.True(t, item2.ForwardQuery, "параметры запроса по умолчанию дописываются")

	// правка: незаданное не меняется, указатель на nil снимает ограничение
	require.NoError(t, repo.Update(ctx, id, &model.Edit{
		LongUrl:    new("https://example.com/new"),
		Tags:       new([]string{}),
		ValidUntil: new((*time.Time)(nil)),
		MaxVisits:  new((*int32)(nil)),
	}))
	item, _, err = repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/new", item.LongUrl)
	assert.Equal(t, "Название", item.Title, "незаданное поле не тронуто")
	assert.Equal(t, []string{}, item.Tags)
	assert.Nil(t, item.ValidUntil)
	assert.Nil(t, item.MaxVisits)

	require.NoError(t, repo.Delete(ctx, id))
	_, found, err = repo.Get(ctx, id)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestShortCodeUnique(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	_, err := repo.Create(ctx, newEdit("same", "https://example.com/1"))
	require.NoError(t, err)

	_, err = repo.Create(ctx, newEdit("same", "https://example.com/2"))
	assert.ErrorIs(t, err, errs.ShortCodeExists, "тот же код в том же домене занят")

	_, err = repo.Create(ctx, newEdit("SAME", "https://example.com/3"))
	assert.NoError(t, err, "код чувствителен к регистру")

	other := newEdit("same", "https://example.com/4")
	other.Domain = new("sl.demo.test")
	_, err = repo.Create(ctx, other)
	assert.NoError(t, err, "тот же код в другом домене свободен")
}

func TestGetByCodeAndFindForRedirect(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	mk := func(code, domain, longUrl string) {
		edit := newEdit(code, longUrl)
		edit.Domain = &domain
		_, err := repo.Create(ctx, edit)
		require.NoError(t, err)
	}
	mk("both", "", "https://example.com/default")
	mk("both", "sl.demo.test", "https://example.com/sl")
	mk("onlydef", "", "https://example.com/onlydef")
	mk("onlysl", "sl.demo.test", "https://example.com/onlysl")

	longUrl := func(item *model.Main, found bool, err error) string {
		require.NoError(t, err)
		if !found {
			return ""
		}
		return item.LongUrl
	}

	// ровно этот домен
	assert.Equal(t, "https://example.com/default", longUrl(repo.GetByCode(ctx, "both", "")))
	assert.Equal(t, "https://example.com/sl", longUrl(repo.GetByCode(ctx, "both", "sl.demo.test")))
	assert.Empty(t, longUrl(repo.GetByCode(ctx, "onlydef", "sl.demo.test")), "запасного поиска нет")
	assert.Empty(t, longUrl(repo.GetByCode(ctx, "onlysl", "")))
	assert.Empty(t, longUrl(repo.GetByCode(ctx, "BOTH", "")), "регистр важен")

	// переход: сначала ссылка домена запроса, иначе основного
	assert.Equal(t, "https://example.com/sl", longUrl(repo.FindForRedirect(ctx, "both", "sl.demo.test")))
	assert.Equal(t, "https://example.com/default", longUrl(repo.FindForRedirect(ctx, "both", "")))
	assert.Equal(t, "https://example.com/default", longUrl(repo.FindForRedirect(ctx, "both", "unknown.example")))
	assert.Equal(t, "https://example.com/onlydef", longUrl(repo.FindForRedirect(ctx, "onlydef", "sl.demo.test")))
	assert.Equal(t, "https://example.com/onlysl", longUrl(repo.FindForRedirect(ctx, "onlysl", "sl.demo.test")))
	assert.Empty(t, longUrl(repo.FindForRedirect(ctx, "onlysl", "")), "ссылка домена на основном не открывается")
	assert.Empty(t, longUrl(repo.FindForRedirect(ctx, "onlysl", "unknown.example")))
	assert.Empty(t, longUrl(repo.FindForRedirect(ctx, "nope", "sl.demo.test")))
}

func TestFindExisting(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	const longUrl = "https://example.com/fie?x=1"
	until := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	firstId, err := repo.Create(ctx, &model.Edit{
		ShortCode:  new("first"),
		LongUrl:    new(longUrl),
		Tags:       new([]string{"a", "b"}),
		ValidUntil: new(&until),
		MaxVisits:  new(new(int32(5))),
	})
	require.NoError(t, err)

	second := newEdit("second", longUrl)
	second.Domain = new("sl.demo.test")
	secondId, err := repo.Create(ctx, second)
	require.NoError(t, err)

	_, err = repo.Create(ctx, newEdit("other", longUrl+"&y=2"))
	require.NoError(t, err)

	find := func(req model.FindExistingReq) int64 {
		req.LongUrl = lo.Ternary(req.LongUrl == "", longUrl, req.LongUrl)
		item, found, err := repo.FindExisting(ctx, &req)
		require.NoError(t, err)
		if !found {
			return 0
		}
		return item.Id
	}

	assert.Equal(t, firstId, find(model.FindExistingReq{}), "только адрес — самая старая ссылка")
	assert.Zero(t, find(model.FindExistingReq{LongUrl: "https://example.com/none"}))

	assert.Equal(t, firstId, find(model.FindExistingReq{Tags: []string{"b", "a"}}), "порядок меток не важен")
	assert.Zero(t, find(model.FindExistingReq{Tags: []string{"a"}}), "часть меток — не совпадение")
	assert.Zero(t, find(model.FindExistingReq{Tags: []string{"a", "b", "c"}}))

	assert.Equal(t, firstId, find(model.FindExistingReq{ValidUntil: &until}))
	assert.Equal(t, firstId, find(model.FindExistingReq{ValidUntil: new(until.In(time.FixedZone("x", 5*3600)))}), "то же время в другом поясе")
	assert.Zero(t, find(model.FindExistingReq{ValidUntil: new(until.Add(time.Hour))}))
	assert.Zero(t, find(model.FindExistingReq{ValidSince: &until}))

	assert.Equal(t, firstId, find(model.FindExistingReq{MaxVisits: new(int32(5))}))
	assert.Zero(t, find(model.FindExistingReq{MaxVisits: new(int32(6))}))

	assert.Equal(t, secondId, find(model.FindExistingReq{Domain: new("sl.demo.test")}))
	assert.Equal(t, firstId, find(model.FindExistingReq{Domain: new("")}))
	assert.Zero(t, find(model.FindExistingReq{Domain: new("x.example")}))

	assert.Equal(t, secondId, find(model.FindExistingReq{ShortCode: new("second")}))
	assert.Zero(t, find(model.FindExistingReq{ShortCode: new("other")}), "код есть, но адрес другой")
}

func TestListFilters(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	mk := func(edit *model.Edit) {
		_, err := repo.Create(ctx, edit)
		require.NoError(t, err)
	}
	mk(&model.Edit{ShortCode: new("c1"), LongUrl: new("https://shop.example/phone"), Title: new("Телефон"), Tags: new([]string{"sms"})})
	mk(&model.Edit{ShortCode: new("c2"), LongUrl: new("https://shop.example/tv"), Tags: new([]string{"sms", "promo"}), ValidUntil: new(&past)})
	mk(&model.Edit{ShortCode: new("c3"), LongUrl: new("https://other.example/100%_off"), Domain: new("sl.demo.test"), ValidUntil: new(&future)})
	mk(&model.Edit{ShortCode: new("c4"), LongUrl: new("https://other.example/100x_off"), CreatedAt: new(time.Now().Add(time.Minute))})

	list := func(pars model.ListReq) ([]string, int64) {
		pars.ListParams.PageSize = lo.Ternary(pars.ListParams.PageSize == 0, int64(10), pars.ListParams.PageSize)
		pars.ListParams.WithTotalCount = true
		items, total, err := repo.List(ctx, &pars)
		require.NoError(t, err)
		return codes(items), total
	}

	got, total := list(model.ListReq{})
	assert.Equal(t, []string{"c4", "c3", "c2", "c1"}, got, "новые первыми")
	assert.EqualValues(t, 4, total)

	got, total = list(model.ListReq{ListParams: commonModel.ListParams{Page: 1, PageSize: 3}})
	assert.Equal(t, []string{"c1"}, got, "страницы с нуля")
	assert.EqualValues(t, 4, total)

	got, _ = list(model.ListReq{ListParams: commonModel.ListParams{Sort: []string{"short_code"}}})
	assert.Equal(t, []string{"c1", "c2", "c3", "c4"}, got)

	got, _ = list(model.ListReq{ListParams: commonModel.ListParams{Sort: []string{"long_url; drop table link"}}})
	assert.Len(t, got, 4, "чужое поле сортировки отброшено")

	got, _ = list(model.ListReq{Search: new("SHOP.example")})
	assert.ElementsMatch(t, []string{"c1", "c2"}, got, "подстрока в адресе, регистр не важен")

	got, _ = list(model.ListReq{Search: new("телеф")})
	assert.Equal(t, []string{"c1"}, got, "подстрока в названии")

	got, _ = list(model.ListReq{Search: new("100%_off")})
	assert.Equal(t, []string{"c3"}, got, "% и _ в поиске — обычные символы")

	got, _ = list(model.ListReq{ShortCode: new("c2")})
	assert.Equal(t, []string{"c2"}, got)

	got, _ = list(model.ListReq{Tag: new("sms")})
	assert.ElementsMatch(t, []string{"c1", "c2"}, got)

	got, _ = list(model.ListReq{Tag: new("promo")})
	assert.Equal(t, []string{"c2"}, got)

	got, _ = list(model.ListReq{Expired: new(true)})
	assert.Equal(t, []string{"c2"}, got)

	got, _ = list(model.ListReq{Expired: new(false)})
	assert.ElementsMatch(t, []string{"c1", "c3", "c4"}, got)

	got, _ = list(model.ListReq{Domain: new("sl.demo.test")})
	assert.Equal(t, []string{"c3"}, got)

	got, _ = list(model.ListReq{Domain: new("")})
	assert.ElementsMatch(t, []string{"c1", "c2", "c4"}, got, "пустой домен — основной")

	got, total = list(model.ListReq{Tag: new("sms"), Expired: new(false), Search: new("shop")})
	assert.Equal(t, []string{"c1"}, got, "фильтры складываются")
	assert.EqualValues(t, 1, total)

	// подсчёт с потолком: тот же фильтр, что у списка, но не дальше limit
	upTo := func(pars model.ListReq, limit int64) int64 {
		n, err := repo.CountUpTo(ctx, &pars, limit)
		require.NoError(t, err)
		return n
	}
	assert.EqualValues(t, 4, upTo(model.ListReq{}, 100), "без фильтра")
	assert.EqualValues(t, 3, upTo(model.ListReq{}, 3), "не больше limit")
	assert.EqualValues(t, 2, upTo(model.ListReq{Tag: new("sms")}, 100))
	assert.EqualValues(t, 1, upTo(model.ListReq{Tag: new("sms")}, 1))
	assert.EqualValues(t, 0, upTo(model.ListReq{Tag: new("нет-такой")}, 100))
	assert.EqualValues(t, 2, upTo(model.ListReq{Search: new("SHOP.example")}, 100), "два параметра в одном условии")
	assert.EqualValues(t, 3, upTo(model.ListReq{Domain: new("")}, 100))
	assert.EqualValues(t, 1, upTo(model.ListReq{Tag: new("sms"), Expired: new(false), Search: new("shop"), Domain: new("")}, 100),
		"фильтры складываются")
}

// Запись ссылки справочник меток не трогает: это отдельные вызовы, которые делает фоновый сервис
func TestTagDirectory(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	tagList := func() []string {
		tags, err := repo.TagList(ctx)
		require.NoError(t, err)
		return tags
	}
	mk := func(code string, tags ...string) int64 {
		id, err := repo.Create(ctx, &model.Edit{ShortCode: &code, LongUrl: new("https://example.com/" + code), Tags: &tags})
		require.NoError(t, err)
		return id
	}

	first := mk("a", "sms", "promo")
	second := mk("b", "sms")
	assert.Empty(t, tagList(), "создание ссылки в справочник не пишет")

	// дописать метки: повторы и уже известные — без ошибок
	require.NoError(t, repo.TagAdd(ctx, []string{"sms", "promo", "sms"}))
	require.NoError(t, repo.TagAdd(ctx, []string{"promo", "акция", "ghost"}))
	require.NoError(t, repo.TagAdd(ctx, nil))
	assert.Equal(t, []string{"ghost", "promo", "sms", "акция"}, tagList(), "по алфавиту, каждая один раз")

	// убрать неиспользуемые: уходят только те, которых нет ни у одной ссылки
	dropped, err := repo.TagDropUnused(ctx, []string{"sms", "ghost", "акция", "нет-такой"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ghost", "акция"}, dropped)
	assert.Equal(t, []string{"promo", "sms"}, tagList())

	// метку сняли с одной ссылки — у другой она осталась
	require.NoError(t, repo.Update(ctx, first, &model.Edit{Tags: new([]string{"promo"})}))
	dropped, err = repo.TagDropUnused(ctx, []string{"sms"})
	require.NoError(t, err)
	assert.Empty(t, dropped)

	// последняя ссылка с меткой удалена — метка уходит
	require.NoError(t, repo.Delete(ctx, second))
	assert.Equal(t, []string{"promo", "sms"}, tagList(), "удаление ссылки справочник не трогает")
	dropped, err = repo.TagDropUnused(ctx, []string{"sms"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sms"}, dropped)
	assert.Equal(t, []string{"promo"}, tagList())

	dropped, err = repo.TagDropUnused(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, dropped)

	require.NoError(t, repo.Ping(ctx))
}

func TestCounts(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	mk := func(code string, validUntil *time.Time) {
		_, err := repo.Create(ctx, &model.Edit{ShortCode: &code, LongUrl: new("https://example.com/" + code), ValidUntil: &validUntil})
		require.NoError(t, err)
	}
	mk("old1", &past)
	mk("old2", &past)
	mk("new", &future)
	mk("forever", nil)

	all, err := repo.CountAll(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 4, all)

	expired, err := repo.CountExpired(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 2, expired)
}

// Пересчёт приводит справочник меток к тому, что есть у ссылок
func TestTagRecount(t *testing.T) {
	ctx := context.Background()
	pool := linktest.PgPool(t)
	repo := New(pool)

	// 30 ссылок с метками t0, t1, t2 и одна с двумя метками — в справочнике о них ничего нет
	_, err := pool.Exec(ctx, `insert into link (short_code, long_url, tags)
		select 'p' || g, 'https://example.com/p' || g, array['t' || g % 3] from generate_series(1, 30) g`)
	require.NoError(t, err)
	_, err = repo.Create(ctx, &model.Edit{ShortCode: new("a"), LongUrl: new("https://example.com/a"), Tags: new([]string{"sms", "promo"})})
	require.NoError(t, err)
	// а в справочнике — одна верная метка и две, которых у ссылок нет
	require.NoError(t, repo.TagAdd(ctx, []string{"sms", "ghost", "старая"}))

	added, dropped, done, err := repo.TagRecount(ctx)
	require.NoError(t, err)
	require.True(t, done)
	assert.Equal(t, []string{"promo", "t0", "t1", "t2"}, added)
	assert.Equal(t, []string{"ghost", "старая"}, dropped)

	tags, err := repo.TagList(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"promo", "sms", "t0", "t1", "t2"}, tags)

	// всё сходится — исправлять нечего
	added, dropped, done, err = repo.TagRecount(ctx)
	require.NoError(t, err)
	require.True(t, done)
	assert.Empty(t, added)
	assert.Empty(t, dropped)

	// пустая таблица ссылок — пустой справочник
	_, err = pool.Exec(ctx, `truncate link`)
	require.NoError(t, err)
	_, dropped, _, err = repo.TagRecount(ctx)
	require.NoError(t, err)
	assert.Len(t, dropped, 5)
	tags, err = repo.TagList(ctx)
	require.NoError(t, err)
	assert.Empty(t, tags)
}

// Пересчёт один на все копии сервиса: пока идёт один, второй не начинается
func TestTagRecountOneAtATime(t *testing.T) {
	ctx := context.Background()
	pool := linktest.PgPool(t)
	repo := New(pool)

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `select pg_advisory_lock($1)`, tagRecountLock)
	require.NoError(t, err)

	_, _, done, err := repo.TagRecount(ctx)
	require.NoError(t, err)
	assert.False(t, done, "замок занят — пересчёт пропускается")

	_, err = conn.Exec(ctx, `select pg_advisory_unlock($1)`, tagRecountLock)
	require.NoError(t, err)

	_, _, done, err = repo.TagRecount(ctx)
	require.NoError(t, err)
	assert.True(t, done)
}

// Справочник пишут одновременно со всех сторон — одни и те же новые метки в разном порядке:
// никто никого не блокирует и ничего не теряется
func TestTagDirectoryConcurrent(t *testing.T) {
	ctx := context.Background()
	repo := New(linktest.PgPool(t))

	const workers, perWorker = 8, 30

	g, gctx := errgroup.WithContext(ctx)
	for w := range workers {
		g.Go(func() error {
			for i := range perWorker {
				code := fmt.Sprintf("w%d-%d", w, i)
				tags := []string{fmt.Sprintf("t%d", i), "общая", fmt.Sprintf("a%d", i%7)}
				if w%2 == 0 {
					slices.Reverse(tags)
				}
				if _, err := repo.Create(gctx, &model.Edit{ShortCode: &code, LongUrl: new("https://example.com/" + code), Tags: &tags}); err != nil {
					return err
				}
				if err := repo.TagAdd(gctx, tags); err != nil {
					return err
				}
				if _, err := repo.TagDropUnused(gctx, []string{fmt.Sprintf("t%d", i), "нет-такой"}); err != nil {
					return err
				}
			}
			return nil
		})
	}
	g.Go(func() error {
		for range 5 {
			if _, _, _, err := repo.TagRecount(gctx); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, g.Wait())

	added, dropped, done, err := repo.TagRecount(ctx)
	require.NoError(t, err)
	require.True(t, done)
	assert.Empty(t, added, "метки ссылок потерялись")
	assert.Empty(t, dropped)

	tags, err := repo.TagList(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, perWorker+7+1)
}
