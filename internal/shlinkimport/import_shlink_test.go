package shlinkimport

// Тесты на настоящем Shlink 5.1.8 стенда: источник — его база, наполненная через его же API
// (см. TestSeedStand), а правильность перенесённого сверяется ещё и с ответами самого Shlink.
// Во время этих тестов наполнение стенда запускать нельзя: тесты считают новые ссылки поштучно.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readSrcIndependent читает ссылки источника запросом, написанным отдельно от кода переноса:
// время пересчитывает сам Postgres (at time zone), метки собирает array_agg.
func readSrcIndependent(t testing.TB, conn *pgx.Conn, maxID int64) map[Key]Link {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		select s.id, s.short_code, coalesce(d.authority, ''), s.original_url, coalesce(s.title, ''),
		       coalesce((select array_agg(t.name::text order by t.name)
		                 from short_urls_in_tags x join tags t on t.id = x.tag_id
		                 where x.short_url_id = s.id), '{}'),
		       s.valid_since at time zone $2, s.valid_until at time zone $2, s.max_visits,
		       s.forward_query, s.date_created at time zone $2
		from short_urls s
		left join domains d on d.id = s.domain_id
		where s.id <= $1
		order by s.id`, maxID, standSrcTZ)
	require.NoError(t, err)
	defer rows.Close()

	res := map[Key]Link{}
	for rows.Next() {
		var l Link
		require.NoError(t, rows.Scan(&l.SrcID, &l.ShortCode, &l.Domain, &l.LongURL, &l.Title, &l.Tags,
			&l.ValidSince, &l.ValidUntil, &l.MaxVisits, &l.ForwardQuery, &l.CreatedAt))
		if _, dup := res[l.Key()]; dup {
			continue // переносится самая ранняя строка
		}
		res[l.Key()] = l
	}
	require.NoError(t, rows.Err())
	return res
}

// assertSameAsShlinkAPI сверяет ссылку приёмника с тем, что про неё отвечает сам Shlink.
func assertSameAsShlinkAPI(t testing.TB, got Link) {
	t.Helper()
	api, err := shlinkGet(context.Background(), got.ShortCode, got.Domain)
	require.NoError(t, err, "ссылка %s", got.Key())

	parse := func(s *string) *time.Time {
		if s == nil {
			return nil
		}
		res, err := time.Parse(time.RFC3339, *s)
		require.NoError(t, err)
		return &res
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	if api.Tags == nil {
		api.Tags = []string{}
	}

	want := Link{
		ShortCode:    api.ShortCode,
		Domain:       deref(api.Domain),
		LongURL:      api.LongURL,
		Title:        deref(api.Title),
		Tags:         api.Tags,
		CreatedAt:    *parse(&api.DateCreated),
		ValidSince:   parse(api.Meta.ValidSince),
		ValidUntil:   parse(api.Meta.ValidUntil),
		MaxVisits:    api.Meta.MaxVisits,
		ForwardQuery: api.ForwardQuery,
	}
	assert.Empty(t, want.diff(&got), "ссылка %s в приёмнике отличается от ответа Shlink", got.Key())
}

// Несколько тысяч настоящих ссылок Shlink: перенос, сверка поле в поле с базой и с API Shlink,
// повторный запуск, пробный режим, проверка.
func TestImport_RealShlink(t *testing.T) {
	shlinkOrSkip(t)
	srcConn := connectOrSkip(t, standSrcDSN())
	dstDSN, dstConn := newDstSchema(t)
	ctx := context.Background()

	var srcTotal int64
	require.NoError(t, srcConn.QueryRow(ctx, "select count(*) from short_urls").Scan(&srcTotal))
	if srcTotal < 1000 {
		t.Skipf("в Shlink стенда всего %d ссылок — сначала наполните его (TestSeedStand)", srcTotal)
	}

	// пробный запуск: обещает перенести всё и ничего не пишет
	dry := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, DryRun: true, BatchSize: 700})
	assert.Equal(t, srcTotal, dry.Read)
	assert.Equal(t, srcTotal, dry.Inserted)
	assert.EqualValues(t, 0, dstCount(t, dstConn))

	// перенос
	res := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, Survey: true, BatchSize: 700})
	assert.Equal(t, srcTotal, res.Total)
	assert.Equal(t, srcTotal, res.Read)
	assert.Equal(t, srcTotal, res.Inserted)
	assert.EqualValues(t, 0, res.Existing)
	assert.EqualValues(t, 0, res.Conflicts)
	assert.Equal(t, srcTotal, dstCount(t, dstConn))

	// поле в поле — с базой Shlink, запросом, не зависящим от кода переноса
	want := readSrcIndependent(t, srcConn, res.MaxID)
	got := readDst(t, dstConn)
	require.Len(t, got, len(want))

	var withTags, withDomain, withSince, withUntil, withMax, withTitle, noForward, longURLs int
	codeLens := map[int]int{}
	for key, w := range want {
		g, ok := got[key]
		if !assert.True(t, ok, "нет ссылки %s", key) {
			continue
		}
		if diff := w.diff(&g.Link); len(diff) > 0 {
			assert.Fail(t, "ссылка отличается", "%s: %v", key, diff)
		}
		assert.True(t, g.ModifiedAt.Equal(g.CreatedAt))

		codeLens[len(g.ShortCode)]++
		count := func(cond bool, n *int) {
			if cond {
				*n++
			}
		}
		count(len(g.Tags) > 0, &withTags)
		count(g.Domain != "", &withDomain)
		count(g.ValidSince != nil, &withSince)
		count(g.ValidUntil != nil, &withUntil)
		count(g.MaxVisits != nil, &withMax)
		count(g.Title != "", &withTitle)
		count(!g.ForwardQuery, &noForward)
		count(len(g.LongURL) > 5000, &longURLs)
	}
	// стенд действительно разнообразен — иначе сверка мало что доказывает
	for name, n := range map[string]int{
		"с метками": withTags, "со вторым доменом": withDomain, "со сроком «с»": withSince, "со сроком «до»": withUntil,
		"с пределом посещений": withMax, "с названием": withTitle,
		"без forward_query": noForward, "с очень длинным адресом": longURLs,
		"с кодом из 4 символов": codeLens[4], "с кодом из 5 символов": codeLens[5], "с кодом из 6 символов": codeLens[6],
	} {
		assert.Greater(t, n, 10, "на стенде мало ссылок %s", name)
	}

	// поле в поле — с ответами самого Shlink (каждая пятидесятая ссылка и все особые случаи вразброс)
	i, checked := 0, 0
	for _, g := range got {
		i++
		special := g.Domain != "" || g.ValidSince != nil || g.MaxVisits != nil || !g.ForwardQuery
		if i%50 != 0 && !(special && i%7 == 0) {
			continue
		}
		assertSameAsShlinkAPI(t, g.Link)
		checked++
	}
	t.Logf("с ответами Shlink сверено %d ссылок", checked)
	assert.Greater(t, checked, 100)

	// повторный запуск ничего не добавляет и ничего не меняет
	again := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, BatchSize: 700})
	assert.Equal(t, srcTotal, again.Read)
	assert.EqualValues(t, 0, again.Inserted)
	assert.Equal(t, srcTotal, again.Existing)
	assert.EqualValues(t, 0, again.Conflicts)
	assert.Equal(t, srcTotal, dstCount(t, dstConn))
	gotAgain := readDst(t, dstConn)
	for key, g := range got {
		a := gotAgain[key]
		assert.Empty(t, g.Link.diff(&a.Link), "ссылка %s изменилась при повторном запуске", key)
	}

	// проверка: выборка и все ссылки
	for _, opts := range []VerifyOptions{{Sample: 500}, {All: true, BatchSize: 700}} {
		opts.SrcDSN, opts.DstDSN, opts.SrcLocation = standSrcDSN(), dstDSN, standLocation(t)
		opts.Logf = newTestLog(t).Logf
		v, err := Verify(ctx, opts)
		require.NoError(t, err)
		assert.True(t, v.OK)
		assert.Equal(t, srcTotal, v.SrcLinks)
		assert.Equal(t, srcTotal, v.DstLinks)
		assert.EqualValues(t, 0, v.Missing)
		assert.EqualValues(t, 0, v.Mismatched)
		if opts.All {
			assert.Equal(t, srcTotal, v.Checked)
			assert.Equal(t, srcTotal, v.Matched)
		} else {
			assert.EqualValues(t, 500, v.Checked)
		}
	}
}

// Докачка: после основного переноса в Shlink появляются новые ссылки — переносятся только они.
func TestImport_CatchUp(t *testing.T) {
	shlinkOrSkip(t)
	srcConn := connectOrSkip(t, standSrcDSN())
	dstDSN, dstConn := newDstSchema(t)
	ctx := context.Background()
	run := randSuffix(t)

	create := func(n int, kind string) []*shlinkShortURL {
		res := make([]*shlinkShortURL, 0, n)
		for i := range n {
			body := map[string]any{
				"longUrl": fmt.Sprintf("https://www.demo.test/catchup/%s/%s/%d?q=привет мир", run, kind, i),
				"tags":    []string{"mail_sms", "courier"},
			}
			switch i % 3 {
			case 1:
				body["domain"] = "sl.demo.test"
				body["validUntil"] = "2027-01-15T10:01:00+05:00"
				body["title"] = "Докачка " + kind
			case 2:
				body["maxVisits"] = 5
				body["forwardQuery"] = false
				body["validSince"] = "2026-01-01T00:00:00Z"
			}
			link, err := shlinkCreate(ctx, body)
			require.NoError(t, err)
			res = append(res, link)
		}
		return res
	}
	assertImported := func(links []*shlinkShortURL) {
		got := readDst(t, dstConn)
		for _, l := range links {
			key := Key{ShortCode: l.ShortCode}
			if l.Domain != nil {
				key.Domain = *l.Domain
			}
			g, ok := got[key]
			if assert.True(t, ok, "новая ссылка %s не перенесена", key) {
				assertSameAsShlinkAPI(t, g.Link)
			}
		}
	}
	srcCount := func() int64 {
		var n int64
		require.NoError(t, srcConn.QueryRow(ctx, "select count(*) from short_urls").Scan(&n))
		return n
	}

	// основной перенос
	base := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN})
	require.Positive(t, base.Inserted)
	require.Equal(t, srcCount(), dstCount(t, dstConn))

	// 1. «только новое»: находит последнюю перенесённую сам
	fresh := create(7, "new")
	dry := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, New: true, Overlap: 5, DryRun: true})
	assert.EqualValues(t, 7, dry.Inserted)
	assert.Equal(t, base.Inserted, dstCount(t, dstConn), "пробная докачка ничего не пишет")

	byNew := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, New: true, Overlap: 5})
	assert.EqualValues(t, base.MaxID-5, byNew.FromID, "докачка начинается от последней перенесённой с запасом")
	var overlapRows int64 // сколько уже перенесённых ссылок попало в запас (id могут идти с пропусками)
	require.NoError(t, srcConn.QueryRow(ctx, "select count(*) from short_urls where id > $1 and id <= $2",
		base.MaxID-5, base.MaxID).Scan(&overlapRows))
	assert.Equal(t, 7+overlapRows, byNew.Read, "читаются только новые и запас, а не весь источник")
	assert.EqualValues(t, 7, byNew.Inserted)
	assert.Equal(t, overlapRows, byNew.Existing)
	assert.EqualValues(t, 0, byNew.Conflicts)
	assertImported(fresh)

	// повтор докачки ничего не добавляет
	byNewAgain := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, New: true, Overlap: 5})
	assert.EqualValues(t, 0, byNewAgain.Inserted)
	assert.EqualValues(t, 5, byNewAgain.Read)

	// 2. с момента времени (Shlink хранит время с точностью до секунды — ждём начала новой секунды)
	time.Sleep(1100 * time.Millisecond)
	since := time.Now()
	fresh = create(4, "since")
	bySince := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, Since: &since})
	assert.EqualValues(t, 4, bySince.Read, "читаются только ссылки, созданные не раньше момента")
	assert.EqualValues(t, 4, bySince.Inserted)
	assertImported(fresh)

	// 3. после указанного id источника
	fresh = create(3, "afterid")
	byID := runImport(t, ImportOptions{SrcDSN: standSrcDSN(), DstDSN: dstDSN, AfterID: bySince.MaxID})
	assert.EqualValues(t, 3, byID.Read)
	assert.EqualValues(t, 3, byID.Inserted)
	assertImported(fresh)

	// в итоге приёмник равен источнику
	assert.Equal(t, srcCount(), dstCount(t, dstConn))
	v, err := Verify(ctx, VerifyOptions{
		SrcDSN: standSrcDSN(), DstDSN: dstDSN, SrcLocation: standLocation(t), All: true, Logf: newTestLog(t).Logf,
	})
	require.NoError(t, err)
	assert.True(t, v.OK)
}

// «Только новое» на пустом приёмнике переносит всё.
func TestImport_NewOnEmptyDestination(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}

	for i := int64(1); i <= 25; i++ {
		f.link(srcLink{ID: i, Code: fmt.Sprintf("n%02d", i), URL: fmt.Sprintf("https://example.com/%d", i)})
	}

	res := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, New: true, BatchSize: 10})
	assert.EqualValues(t, 0, res.FromID)
	assert.EqualValues(t, 25, res.Inserted)
	assert.EqualValues(t, 25, dstCount(t, dstConn))

	// последняя перенесённая ищется от новых к старым, в том числе через несколько пачек
	for i := int64(26); i <= 60; i++ {
		f.link(srcLink{ID: i, Code: fmt.Sprintf("n%02d", i), URL: fmt.Sprintf("https://example.com/%d", i)})
	}
	res = runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, New: true, BatchSize: 10, Overlap: 3})
	assert.EqualValues(t, 22, res.FromID)
	assert.EqualValues(t, 38, res.Read)
	assert.EqualValues(t, 35, res.Inserted)
	assert.EqualValues(t, 3, res.Existing)
	assert.EqualValues(t, 60, dstCount(t, dstConn))
}
