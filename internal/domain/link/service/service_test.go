package service

import (
	"context"
	"errors"
	"strings"
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

	taken      map[string]bool // занятые коды
	created    []string
	createErr  error
	tagCalls   int
	tags       []string
	crawlCalls int

	// если заданы — TagList сообщает о начале выборки и ждёт разрешения закончить
	tagStarted chan struct{}
	tagRelease chan struct{}
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

func (r *fakeRepo) TagList(context.Context) ([]string, error) {
	r.tagCalls++
	if r.tagStarted != nil {
		close(r.tagStarted)
		<-r.tagRelease
		r.tagStarted = nil
	}
	return r.tags, nil
}

func (r *fakeRepo) CrawlableCodes(context.Context) ([]string, error) {
	r.crawlCalls++
	return []string{"a"}, nil
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

func TestListsAreCached(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{tags: []string{"a", "b"}}
	svc := New(repo)

	for range 3 {
		tags, err := svc.TagList(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, tags)
		tags[0] = "changed" // вызывающий получает копию
	}
	assert.Equal(t, 1, repo.tagCalls, "таблица читается один раз")

	// изменение ссылок сбрасывает списки
	require.NoError(t, svc.Update(ctx, 1, &model.Edit{}))
	_, err := svc.TagList(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, repo.tagCalls)

	_, err = svc.CrawlableCodes(ctx)
	require.NoError(t, err)
	_, err = svc.CrawlableCodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.crawlCalls)
}

func TestCachedListExpires(t *testing.T) {
	ctx := context.Background()
	calls := 0
	load := func(context.Context) ([]string, error) {
		calls++
		return []string{"x"}, nil
	}

	c := newCachedList(20 * time.Millisecond)
	_, _ = c.get(ctx, load)
	_, _ = c.get(ctx, load)
	assert.Equal(t, 1, calls)

	time.Sleep(30 * time.Millisecond)
	_, _ = c.get(ctx, load)
	assert.Equal(t, 2, calls, "после срока список перечитывается")

	// ошибка загрузки не запоминается
	failing := newCachedList(time.Minute)
	_, err := failing.get(ctx, func(context.Context) ([]string, error) { return nil, errors.New("boom") })
	require.Error(t, err)
	got, err := failing.get(ctx, load)
	require.NoError(t, err)
	assert.Equal(t, []string{"x"}, got)
}

// Создание ссылки не ждёт выборку списка: сброс берёт только замок состояния. Иначе создание
// стоит в очереди за чтением всей таблицы (так три запроса провисели по 40–55 секунд).
func TestCreateDoesNotWaitForListLoad(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{tags: []string{"a"}, tagStarted: make(chan struct{}), tagRelease: make(chan struct{})}
	svc := New(repo)

	loaded := make(chan []string)
	go func() {
		tags, _ := svc.TagList(ctx)
		loaded <- tags
	}()
	<-repo.tagStarted // выборка меток идёт и держит свой замок

	created := make(chan error)
	go func() {
		// новая метка и crawlable — сбрасываются оба списка
		_, err := svc.Create(ctx, &model.Edit{
			LongUrl: new("https://example.com"), Tags: new([]string{"b"}), Crawlable: new(true),
		}, 5)
		created <- err
	}()

	select {
	case err := <-created:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("создание ссылки ждёт выборку списка меток")
	}

	close(repo.tagRelease)
	assert.Equal(t, []string{"a"}, <-loaded, "запрос получает то, что прочитал")

	// выборка началась до сброса — её результат не запомнился
	_, err := svc.TagList(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, repo.tagCalls)
}

// Создание сбрасывает списки, только если меняет их: ссылки создаются постоянно, и сброс на
// каждую заставлял бы читать всю таблицу при каждом запросе списка
func TestCreateInvalidatesListsOnlyWhenChanged(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{tags: []string{"a", "b"}}
	svc := New(repo)

	lists := func() {
		_, err := svc.TagList(ctx)
		require.NoError(t, err)
		_, err = svc.CrawlableCodes(ctx)
		require.NoError(t, err)
	}
	create := func(obj *model.Edit) {
		obj.LongUrl = new("https://example.com")
		_, err := svc.Create(ctx, obj, 5)
		require.NoError(t, err)
	}

	lists()
	require.Equal(t, 1, repo.tagCalls)
	require.Equal(t, 1, repo.crawlCalls)

	// известная метка, без метки, свой код — списки те же
	create(&model.Edit{Tags: new([]string{"b"})})
	create(&model.Edit{})
	create(&model.Edit{ShortCode: new("mine"), Tags: new([]string{"a", "b"}), Crawlable: new(false)})
	lists()
	assert.Equal(t, 1, repo.tagCalls, "известные метки список не меняют")
	assert.Equal(t, 1, repo.crawlCalls)

	// новая метка — только список меток
	create(&model.Edit{Tags: new([]string{"a", "new"})})
	lists()
	assert.Equal(t, 2, repo.tagCalls)
	assert.Equal(t, 1, repo.crawlCalls)

	// ссылка открыта поисковикам — только список для robots.txt
	create(&model.Edit{ShortCode: new("open"), Crawlable: new(true)})
	lists()
	assert.Equal(t, 2, repo.tagCalls)
	assert.Equal(t, 2, repo.crawlCalls)

	// неудачное создание ничего не сбрасывает
	repo.createErr = errors.New("db down")
	_, err := svc.Create(ctx, &model.Edit{LongUrl: new("https://example.com"), Tags: new([]string{"zzz"})}, 5)
	require.Error(t, err)
	repo.createErr = nil
	lists()
	assert.Equal(t, 2, repo.tagCalls)
}

// Запрос, который ждёт чужую выборку, уходит, когда его отменили
func TestCachedListWaitHonorsContext(t *testing.T) {
	c := newCachedList(time.Minute)
	started, release := make(chan struct{}), make(chan struct{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.get(context.Background(), func(context.Context) ([]string, error) {
			close(started)
			<-release
			return []string{"x"}, nil
		})
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.get(ctx, func(context.Context) ([]string, error) {
		t.Error("вторая выборка не должна запускаться")
		return nil, nil
	})
	require.ErrorIs(t, err, context.Canceled)

	close(release)
	<-done
}
