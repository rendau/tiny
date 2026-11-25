package link

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonModel "github.com/rendau/tiny/internal/domain/common/model"
	"github.com/rendau/tiny/internal/domain/link/model"
	sessionModel "github.com/rendau/tiny/internal/domain/session/model"
	"github.com/rendau/tiny/internal/errs"
)

// fakeSvc — доменный сервис в памяти: запоминает, с чем его вызвали
type fakeSvc struct {
	ServiceI

	links     map[int64]*model.Main
	nextId    int64
	createErr error

	existing    *model.Main // что вернёт FindExisting
	findReq     *model.FindExistingReq
	lastEdit    *model.Edit
	lastLength  int
	lastListReq *model.ListReq
	redirect    map[string]*model.Main // "домен/код"
	tags        []string
}

func (s *fakeSvc) List(_ context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	s.lastListReq = pars
	return []*model.Main{{Id: 1, ShortCode: "abc"}, {Id: 2, ShortCode: "def", Domain: "sl.demo.test"}}, 2, nil
}

func (s *fakeSvc) Get(_ context.Context, id int64, _ bool) (*model.Main, bool, error) {
	link, ok := s.links[id]
	if !ok {
		return nil, false, errs.ObjectNotFound
	}
	return link, true, nil
}

func (s *fakeSvc) Create(_ context.Context, obj *model.Edit, codeLength int) (int64, error) {
	s.lastEdit, s.lastLength = obj, codeLength
	if s.createErr != nil {
		return 0, s.createErr
	}

	code := strings.Repeat("x", codeLength)
	if obj.ShortCode != nil {
		code = *obj.ShortCode
	}

	s.nextId++
	if s.links == nil {
		s.links = map[int64]*model.Main{}
	}
	s.links[s.nextId] = &model.Main{
		Id: s.nextId, Domain: *obj.Domain, ShortCode: code, LongUrl: *obj.LongUrl, Title: *obj.Title, Tags: *obj.Tags,
		ValidSince: *obj.ValidSince, ValidUntil: *obj.ValidUntil, MaxVisits: *obj.MaxVisits,
		Crawlable: *obj.Crawlable, ForwardQuery: *obj.ForwardQuery,
	}
	return s.nextId, nil
}

func (s *fakeSvc) Update(_ context.Context, _ int64, obj *model.Edit) error {
	s.lastEdit = obj
	return nil
}

func (s *fakeSvc) Delete(context.Context, int64) error { return nil }

func (s *fakeSvc) FindExisting(_ context.Context, pars *model.FindExistingReq) (*model.Main, bool, error) {
	s.findReq = pars
	return s.existing, s.existing != nil, nil
}

func (s *fakeSvc) FindForRedirect(_ context.Context, shortCode, domain string) (*model.Main, bool, error) {
	link, ok := s.redirect[domain+"/"+shortCode]
	return link, ok, nil
}

func (s *fakeSvc) GetByCode(_ context.Context, shortCode, domain string, _ bool) (*model.Main, bool, error) {
	link, ok := s.redirect[domain+"/"+shortCode]
	return link, ok, nil
}

func (s *fakeSvc) TagList(context.Context) ([]string, error) { return s.tags, nil }

// fakeSession — вошедший человек берётся из значения контекста
type fakeSession struct{}

type authKey struct{}

func (fakeSession) FromContext(ctx context.Context) *sessionModel.Session {
	if id, ok := ctx.Value(authKey{}).(int64); ok {
		return sessionModel.New(id)
	}
	return sessionModel.New(0)
}

var (
	anonCtx = context.Background()
	usrCtx  = context.WithValue(context.Background(), authKey{}, int64(7))
)

func newUsecase(svc *fakeSvc) *Usecase {
	return New(svc, fakeSession{}, Config{DefaultDomain: "L.Demo.test", HttpsEnabled: true, DefaultCodeLength: 5})
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	require.ErrorIs(t, err, errs.InvalidRequest)
	fullErr, ok := errs.AsErrFull(err)
	require.True(t, ok)
	return fullErr.Fields
}

// операции админки — только вошедшему человеку; публичные — без входа
func TestAdminRequiresLogin(t *testing.T) {
	svc := &fakeSvc{tags: []string{"a"}}
	u := newUsecase(svc)

	_, _, err := u.List(anonCtx, &model.ListReq{ListParams: commonModel.ListParams{PageSize: 10}})
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, err = u.Get(anonCtx, 1)
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, err = u.Create(anonCtx, &model.CreateReq{LongUrl: "https://example.com"})
	assert.ErrorIs(t, err, errs.NotAuthorized)
	assert.ErrorIs(t, u.Update(anonCtx, 1, &model.Edit{}), errs.NotAuthorized)
	assert.ErrorIs(t, u.Delete(anonCtx, 1), errs.NotAuthorized)
	_, err = u.TagList(anonCtx, nil)
	assert.ErrorIs(t, err, errs.NotAuthorized)
	assert.Nil(t, svc.lastEdit, "без входа до хранилища дело не доходит")

	_, err = u.Create(usrCtx, &model.CreateReq{LongUrl: "https://example.com"})
	assert.NoError(t, err)
	_, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com"})
	assert.NoError(t, err, "публичное создание закрыто ключом API, а не входом")
}

func TestListAndIds(t *testing.T) {
	svc := &fakeSvc{}
	u := newUsecase(svc)

	_, _, err := u.List(usrCtx, &model.ListReq{})
	assert.ErrorIs(t, err, errs.IncorrectPageSize)

	items, total, err := u.List(usrCtx, &model.ListReq{ListParams: commonModel.ListParams{PageSize: 10}, Domain: new("L.DEMO.TeST")})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Equal(t, "https://l.demo.test/abc", items[0].ShortUrl)
	assert.Equal(t, "https://sl.demo.test/def", items[1].ShortUrl, "у ссылки своего домена — свой адрес")
	assert.Equal(t, "", *svc.lastListReq.Domain, "основной домен в фильтре — пустая строка")

	items, total, err = u.List(usrCtx, &model.ListReq{ListParams: commonModel.ListParams{PageSize: 10}, Domain: new("не домен!")})
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Zero(t, total)

	assert.ErrorIs(t, u.Update(usrCtx, 0, &model.Edit{}), errs.IdRequired)
	assert.ErrorIs(t, u.Delete(usrCtx, 0), errs.IdRequired)
}

func TestCreateDefaultsAndNormalization(t *testing.T) {
	svc := &fakeSvc{}
	u := newUsecase(svc)

	link, err := u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/a"})
	require.NoError(t, err)
	assert.Equal(t, 5, svc.lastLength, "длина кода по умолчанию")
	assert.Nil(t, svc.lastEdit.ShortCode, "код не задан — генерируется")
	assert.Equal(t, "https://l.demo.test/xxxxx", link.ShortUrl)
	assert.True(t, link.ForwardQuery, "по умолчанию параметры запроса дописываются")
	assert.False(t, link.Crawlable)
	assert.Empty(t, link.Domain)
	assert.Equal(t, []string{}, link.Tags)

	until := time.Date(2026, 12, 5, 10, 1, 0, 123456789, time.UTC)
	link, err = u.PublicCreate(anonCtx, &model.CreateReq{
		LongUrl:         "  https://example.com/b  ",
		ShortCode:       new("  My Code/1 "),
		ShortCodeLength: new(8),
		Domain:          new("SL.Demo.teST"),
		Title:           new("  Название  "),
		Tags:            []string{" Zeta ", "mail sms", "ZETA", "", "альфа"},
		ValidUntil:      &until,
		MaxVisits:       new(int32(3)),
		Crawlable:       new(true),
		ForwardQuery:    new(false),
	})
	require.NoError(t, err)
	assert.Equal(t, "  https://example.com/b  ", link.LongUrl, "длинный адрес не обрезается — как у Shlink")
	assert.Equal(t, "My-Code-1", link.ShortCode, "края обрезаны, пробел и / стали дефисом")
	assert.Equal(t, "sl.demo.test", link.Domain)
	assert.Equal(t, "https://sl.demo.test/My-Code-1", link.ShortUrl)
	assert.Equal(t, "Название", link.Title)
	assert.Equal(t, []string{"zeta", "mail-sms", "альфа"}, link.Tags, "в ответе на создание — порядок запроса")
	assert.Equal(t, []string{"mail-sms", "zeta", "альфа"}, *svc.lastEdit.Tags, "в базе — по алфавиту")
	assert.Equal(t, time.Date(2026, 12, 5, 10, 1, 0, 0, time.UTC), *link.ValidUntil, "доли секунды отброшены")
	assert.False(t, link.ForwardQuery)
	assert.True(t, link.Crawlable)

	// основной домен, названный явно, — та же ссылка основного домена
	link, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/c", Domain: new("l.demo.test")})
	require.NoError(t, err)
	assert.Empty(t, link.Domain)

	// пустой свой код — генерируется (Shlink завёл бы ссылку с пустым кодом)
	_, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/d", ShortCode: new("  ")})
	require.NoError(t, err)
	assert.Nil(t, svc.lastEdit.ShortCode)

	// название длиннее 512 байт обрезается по границе символа
	link, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/e", Title: new(strings.Repeat("я", 600))})
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("я", 256), link.Title)
	link, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/f", Title: new("a" + strings.Repeat("я", 600))})
	require.NoError(t, err)
	assert.Equal(t, "a"+strings.Repeat("я", 255), link.Title)
}

func TestStripTags(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                     "plain",
		"<b>bold</b>":               "bold",
		"a <i>b</i> c":              "a b c",
		`x<a href="1>2">y</a>z`:     "xyz",
		"пр<&'\"\\":                 "пр",
		"a < b":                     "a < b",
		"a > b":                     "a > b",
		"a<b":                       "a",
		"<<b>>x":                    "x",
		"<script>alert(1)</script>": "alert(1)",
	} {
		assert.Equal(t, want, phpStripTags(in), "строка %q", in)
	}

	assert.Equal(t, []string{"compat-bold", "x"}, normalizeTags([]string{" <b>Compat Bold</b> ", "<i></i>", "X"}))

	code, msg := normalizeShortCode(" <b>My Code</b> ")
	assert.Empty(t, msg)
	assert.Equal(t, "My-Code", code)
}

func TestCreateValidation(t *testing.T) {
	u := newUsecase(&fakeSvc{})

	for _, longUrl := range []string{"", "not a url", "example.com/x", ":b", "a:", "\xff\xfe:x"} {
		err := u.ValidateCreate(&model.CreateReq{LongUrl: longUrl})
		assert.Contains(t, fieldsOf(t, err), FieldLongUrl, "longUrl %q", longUrl)
	}
	for _, longUrl := range []string{"https://example.com", "ftp://x/y", "myapp://open/1", "a:b", "mailto:a@b.c", "tel:+77001234567"} {
		assert.NoError(t, u.ValidateCreate(&model.CreateReq{LongUrl: longUrl}), "longUrl %q", longUrl)
	}

	for _, code := range []string{"a?b", "a#b", "a:b", "a@b", "a[b]", "a\tb", strings.Repeat("x", 256)} {
		err := u.ValidateCreate(&model.CreateReq{LongUrl: "https://e.com", ShortCode: &code})
		assert.Contains(t, fieldsOf(t, err), FieldShortCode, "код %q", code)
	}
	for _, code := range []string{"abc", "a.b_c-d~e", "привет", "a&b", "12345"} {
		assert.NoError(t, u.ValidateCreate(&model.CreateReq{LongUrl: "https://e.com", ShortCode: &code}), "код %q", code)
	}

	for _, length := range []int{-1, 0, 3, 256} {
		err := u.ValidateCreate(&model.CreateReq{LongUrl: "https://e.com", ShortCodeLength: &length})
		assert.Contains(t, fieldsOf(t, err), FieldShortCodeLength, "длина %d", length)
	}
	for _, length := range []int{4, 5, 6, 100} {
		assert.NoError(t, u.ValidateCreate(&model.CreateReq{LongUrl: "https://e.com", ShortCodeLength: &length}), "длина %d", length)
	}

	for _, domain := range []string{"", "bad domain!", "-a.kz", "a..kz", "a.kz:port", "a.kz/x"} {
		err := u.ValidateCreate(&model.CreateReq{LongUrl: "https://e.com", Domain: &domain})
		assert.Contains(t, fieldsOf(t, err), FieldDomain, "домен %q", domain)
	}
	for _, domain := range []string{"sl.demo.test", "SL.Demo.teST", "localhost", "a.kz:8080", "пример.рф"} {
		assert.NoError(t, u.ValidateCreate(&model.CreateReq{LongUrl: "https://e.com", Domain: &domain}), "домен %q", domain)
	}

	// все неверные поля называются сразу; причины — слова, которые понимает интерфейс админки
	err := u.ValidateCreate(&model.CreateReq{LongUrl: "", ShortCode: new("a?b"), ShortCodeLength: new(2), Domain: new("b d")})
	assert.Equal(t, map[string]string{
		FieldLongUrl: "required", FieldShortCode: "invalid", FieldShortCodeLength: "invalid", FieldDomain: "invalid",
	}, fieldsOf(t, err))
	err = u.ValidateCreate(&model.CreateReq{LongUrl: "nope", ShortCode: new(strings.Repeat("x", 256))})
	assert.Equal(t, map[string]string{FieldLongUrl: "invalid", FieldShortCode: "too_long"}, fieldsOf(t, err))

	// неверный запрос ничего не создаёт
	svc := &fakeSvc{}
	_, err = newUsecase(svc).PublicCreate(anonCtx, &model.CreateReq{LongUrl: "nope"})
	require.ErrorIs(t, err, errs.InvalidRequest)
	assert.Nil(t, svc.lastEdit)
}

func TestCreateFindIfExists(t *testing.T) {
	existing := &model.Main{Id: 42, ShortCode: "old", LongUrl: "https://example.com/a", Tags: []string{"a"}}
	svc := &fakeSvc{existing: existing}
	u := newUsecase(svc)

	until := time.Date(2026, 12, 5, 10, 1, 0, 5, time.UTC)

	// без findIfExists существующая не ищется
	link, err := u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/a"})
	require.NoError(t, err)
	assert.Nil(t, svc.findReq)
	assert.NotEqual(t, int64(42), link.Id)

	link, err = u.PublicCreate(anonCtx, &model.CreateReq{
		LongUrl: "https://example.com/a", FindIfExists: true,
		Tags: []string{"B", "a"}, ValidUntil: &until, MaxVisits: new(int32(2)),
		Title: new("другое"), ShortCodeLength: new(6), Domain: new("l.demo.test"),
	})
	require.NoError(t, err)
	assert.EqualValues(t, 42, link.Id, "найдена — новая не создаётся")
	assert.Equal(t, "https://l.demo.test/old", link.ShortUrl)

	// сравнивается только названное в запросе; основной домен — не ограничение
	assert.Equal(t, "https://example.com/a", svc.findReq.LongUrl)
	assert.Equal(t, []string{"b", "a"}, svc.findReq.Tags)
	assert.Equal(t, until.Truncate(time.Second), *svc.findReq.ValidUntil)
	assert.EqualValues(t, 2, *svc.findReq.MaxVisits)
	assert.Nil(t, svc.findReq.ValidSince)
	assert.Nil(t, svc.findReq.Domain)
	assert.Nil(t, svc.findReq.ShortCode)

	_, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/a", FindIfExists: true, Domain: new("SL.demo.test"), ShortCode: new("mine")})
	require.NoError(t, err)
	assert.Equal(t, "sl.demo.test", *svc.findReq.Domain)
	assert.Equal(t, "mine", *svc.findReq.ShortCode)

	// не найдена — создаётся
	svc.existing = nil
	link, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com/new", FindIfExists: true})
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/new", link.LongUrl)
}

func TestCreateShortCodeExists(t *testing.T) {
	u := newUsecase(&fakeSvc{createErr: errs.ShortCodeExists})

	_, err := u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com", ShortCode: new("My Slug"), Domain: new("sl.demo.test")})
	require.ErrorIs(t, err, errs.ShortCodeExists)
	codeErr, ok := errors.AsType[CodeError](err)
	require.True(t, ok)
	assert.Equal(t, "My-Slug", codeErr.ShortCode, "код — в приведённом виде")
	assert.Equal(t, "sl.demo.test", codeErr.Domain)

	// интерфейсу админки — код ошибки и слово-причина у поля
	fullErr, ok := errs.AsErrFull(err)
	require.True(t, ok)
	assert.Equal(t, errs.ShortCodeExists.Error(), errs.Code(err))
	assert.Equal(t, map[string]string{FieldShortCode: "exists"}, fullErr.Fields)

	_, err = u.PublicCreate(anonCtx, &model.CreateReq{LongUrl: "https://example.com", ShortCode: new("x")})
	codeErr, _ = errors.AsType[CodeError](err)
	assert.Equal(t, "x", codeErr.ShortCode)
	assert.Empty(t, codeErr.Domain, "основной домен — пусто")
}

func TestUpdateNormalizes(t *testing.T) {
	svc := &fakeSvc{}
	u := newUsecase(svc)

	until := time.Date(2030, 1, 1, 0, 0, 0, 999, time.UTC)

	require.NoError(t, u.Update(usrCtx, 5, &model.Edit{
		ShortCode:  new("hack"),
		Domain:     new("other.kz"),
		CreatedAt:  new(time.Now()),
		LongUrl:    new("https://example.com/new"),
		Title:      new("  заголовок "),
		Tags:       new([]string{"Z", "a b", "z"}),
		ValidUntil: new(&until),
		ValidSince: new((*time.Time)(nil)),
	}))
	assert.Nil(t, svc.lastEdit.ShortCode, "код ссылки после создания не меняется")
	assert.Nil(t, svc.lastEdit.Domain, "домен тоже")
	assert.Nil(t, svc.lastEdit.CreatedAt)
	assert.Equal(t, "заголовок", *svc.lastEdit.Title)
	assert.Equal(t, []string{"a-b", "z"}, *svc.lastEdit.Tags)
	assert.Equal(t, until.Truncate(time.Second), **svc.lastEdit.ValidUntil)
	assert.Nil(t, *svc.lastEdit.ValidSince, "ограничение снято")

	err := u.Update(usrCtx, 5, &model.Edit{LongUrl: new("")})
	assert.Contains(t, fieldsOf(t, err), FieldLongUrl)
}

func TestTagListSearch(t *testing.T) {
	u := newUsecase(&fakeSvc{tags: []string{"mail_sms", "promo", "sms-akcia", "акция"}})

	tags, err := u.TagList(usrCtx, nil)
	require.NoError(t, err)
	assert.Len(t, tags, 4)

	tags, err = u.TagList(usrCtx, new(" SMS "))
	require.NoError(t, err)
	assert.Equal(t, []string{"mail_sms", "sms-akcia"}, tags)

	tags, err = u.TagList(usrCtx, new("АКЦ"))
	require.NoError(t, err)
	assert.Equal(t, []string{"акция"}, tags)
}

func TestRedirect(t *testing.T) {
	now := time.Now()
	link := func(longUrl string) *model.Main { return &model.Main{LongUrl: longUrl, ForwardQuery: true} }

	expired := link("https://example.com/expired")
	expired.ValidUntil = new(now.Add(-time.Minute))
	future := link("https://example.com/future")
	future.ValidSince = new(now.Add(time.Minute))
	active := link("https://example.com/active")
	active.ValidSince, active.ValidUntil = new(now.Add(-time.Minute)), new(now.Add(time.Minute))
	spent := link("https://example.com/spent")
	spent.MaxVisits = new(int32(0))
	limited := link("https://example.com/limited")
	limited.MaxVisits = new(int32(1))
	noForward := &model.Main{LongUrl: "https://example.com/nf?a=1"}

	u := newUsecase(&fakeSvc{redirect: map[string]*model.Main{
		"/abc":             link("https://example.com/default?a=1"),
		"sl.demo.test/abc": link("https://example.com/sl"),
		"/expired":         expired,
		"/future":          future,
		"/active":          active,
		"/spent":           spent,
		"/limited":         limited,
		"/nf":              noForward,
	}})

	go_ := func(host, code, query string) string {
		location, err := u.Redirect(anonCtx, host, code, query)
		if err != nil {
			require.ErrorIs(t, err, errs.ObjectNotFound)
			return ""
		}
		return location
	}

	assert.Equal(t, "https://example.com/default?a=1", go_("l.demo.test", "abc", ""))
	assert.Equal(t, "https://example.com/default?a=1", go_("L.DEMO.TeST", "abc", ""), "регистр хоста не важен")
	assert.Equal(t, "https://example.com/sl", go_("sl.demo.test", "abc", ""))
	assert.Equal(t, "https://example.com/default?a=2&utm=sms", go_("l.demo.test", "abc", "utm=sms&a=2"), "параметры запроса дописаны")
	assert.Equal(t, "https://example.com/nf?a=1", go_("l.demo.test", "nf", "utm=sms"), "forward_query выключен")
	assert.Empty(t, go_("l.demo.test", "nope", ""))
	assert.Empty(t, go_("l.demo.test", "expired", ""), "истекла")
	assert.Empty(t, go_("l.demo.test", "future", ""), "ещё не началась")
	assert.Equal(t, "https://example.com/active", go_("l.demo.test", "active", ""))
	assert.Empty(t, go_("l.demo.test", "spent", ""), "предел переходов 0 — закрыта, как у Shlink")
	assert.Equal(t, "https://example.com/limited", go_("l.demo.test", "limited", ""), "посещения не считаются")
}

func TestPublicGet(t *testing.T) {
	u := newUsecase(&fakeSvc{redirect: map[string]*model.Main{
		"/abc":             {ShortCode: "abc", LongUrl: "https://example.com/default", ValidUntil: new(time.Now().Add(-time.Hour))},
		"sl.demo.test/abc": {ShortCode: "abc", Domain: "sl.demo.test", LongUrl: "https://example.com/sl"},
	}})

	link, err := u.PublicGet(anonCtx, "abc", nil)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/default", link.LongUrl, "истёкшая ссылка читается")
	assert.Equal(t, "https://l.demo.test/abc", link.ShortUrl)

	link, err = u.PublicGet(anonCtx, "abc", new("L.demo.test"))
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/default", link.LongUrl)

	link, err = u.PublicGet(anonCtx, "abc", new("SL.demo.test"))
	require.NoError(t, err)
	assert.Equal(t, "https://sl.demo.test/abc", link.ShortUrl)

	_, err = u.PublicGet(anonCtx, "nope", nil)
	require.ErrorIs(t, err, errs.ObjectNotFound)
	codeErr, _ := errors.AsType[CodeError](err)
	assert.Equal(t, CodeError{ErrFull: codeErr.ErrFull, ShortCode: "nope"}, codeErr)

	_, err = u.PublicGet(anonCtx, "nope", new("SL.demo.test"))
	require.ErrorIs(t, err, errs.ObjectNotFound)
	codeErr, _ = errors.AsType[CodeError](err)
	assert.Equal(t, "sl.demo.test", codeErr.Domain)

	_, err = u.PublicGet(anonCtx, "nope", new("l.demo.test"))
	codeErr, _ = errors.AsType[CodeError](err)
	assert.Empty(t, codeErr.Domain, "основной домен в ошибке не называется")
}
