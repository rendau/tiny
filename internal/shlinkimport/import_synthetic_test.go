package shlinkimport

// Тесты на «рукотворном» источнике: таблицы с настоящей структурой Shlink 5.1.8 (копия со стенда),
// а строки вставлены напрямую. Так проверяются случаи, которые через API Shlink не создать:
// старые даты, дубли кода, правила перенаправления, исчерпанный предел посещений.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// srcFixture вставляет строки в таблицы Shlink.
type srcFixture struct {
	t    testing.TB
	conn *pgx.Conn
}

// srcLink — строка short_urls; пустые поля — как у Shlink по умолчанию.
type srcLink struct {
	ID           int64
	Code         string
	DomainID     *int64
	URL          string
	Created      string // время без пояса, как хранит Shlink
	ValidSince   *string
	ValidUntil   *string
	MaxVisits    *int32
	Title        *string
	Crawlable    bool
	NoForwardQry bool
	Tags         []string
}

func (f *srcFixture) domain(authority string) *int64 {
	f.t.Helper()
	var id int64
	err := f.conn.QueryRow(context.Background(),
		"insert into domains (authority) values ($1) returning id", authority).Scan(&id)
	require.NoError(f.t, err)
	return &id
}

func (f *srcFixture) link(l srcLink) {
	f.t.Helper()
	ctx := context.Background()
	if l.Created == "" {
		l.Created = "2026-11-25 10:00:00"
	}
	_, err := f.conn.Exec(ctx, `
		insert into short_urls (id, original_url, long_url_hash, short_code, date_created, valid_since, valid_until,
		                        max_visits, title, crawlable, forward_query, domain_id)
		values ($1, $2, sha256(convert_to($2, 'UTF8')), $3, $4::timestamp, $5::timestamp, $6::timestamp,
		        $7, $8, $9, $10, $11)`,
		l.ID, l.URL, l.Code, l.Created, l.ValidSince, l.ValidUntil, l.MaxVisits, l.Title, l.Crawlable, !l.NoForwardQry, l.DomainID)
	require.NoError(f.t, err)

	for _, name := range l.Tags {
		var tagID int64
		err = f.conn.QueryRow(ctx, `
			with ins as (insert into tags (name) values ($1) on conflict (name) do nothing returning id)
			select id from ins union all select id from tags where name = $1 limit 1`, name).Scan(&tagID)
		require.NoError(f.t, err)
		_, err = f.conn.Exec(ctx, "insert into short_urls_in_tags (short_url_id, tag_id) values ($1, $2)", l.ID, tagID)
		require.NoError(f.t, err)
	}
}

// dstRow — строка link целиком.
type dstRow struct {
	Link
	ModifiedAt time.Time
}

func readDst(t testing.TB, conn *pgx.Conn) map[Key]dstRow {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		select short_code, domain, long_url, title, tags, valid_since, valid_until, max_visits,
		       crawlable, forward_query, created_at, modified_at
		from link`)
	require.NoError(t, err)
	defer rows.Close()

	res := map[Key]dstRow{}
	for rows.Next() {
		var r dstRow
		require.NoError(t, rows.Scan(&r.ShortCode, &r.Domain, &r.LongURL, &r.Title, &r.Tags, &r.ValidSince, &r.ValidUntil,
			&r.MaxVisits, &r.Crawlable, &r.ForwardQuery, &r.CreatedAt, &r.ModifiedAt))
		res[r.Key()] = r
	}
	require.NoError(t, rows.Err())
	return res
}

func dstCount(t testing.TB, conn *pgx.Conn) int64 {
	t.Helper()
	var n int64
	require.NoError(t, conn.QueryRow(context.Background(), "select count(*) from link").Scan(&n))
	return n
}

func mustTime(t testing.TB, s string) time.Time {
	t.Helper()
	res, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return res
}

func runImport(t testing.TB, opts ImportOptions) *ImportResult {
	t.Helper()
	if opts.SrcLocation == nil {
		opts.SrcLocation = standLocation(t)
	}
	opts.Logf = newTestLog(t).Logf
	res, err := Import(context.Background(), opts)
	require.NoError(t, err)
	return res
}

func TestImport_FieldMapping(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}

	sl := f.domain("sl.demo.test")
	longURL := "https://www.demo.test/корзина?товары=" + strings.Repeat("холодильник двухкамерный, ", 8000)

	// 1: всё по умолчанию
	f.link(srcLink{ID: 1, Code: "aBc", URL: "https://example.com/1"})
	// 2: тот же код в другом регистре — другая ссылка
	f.link(srcLink{ID: 2, Code: "abc", URL: "https://example.com/2"})
	// 3: тот же код на втором домене — другая ссылка
	f.link(srcLink{ID: 3, Code: "aBc", DomainID: sl, URL: "https://example.com/3"})
	// 4: все поля заполнены; даты до и после 1 марта 2024 (до него в Алматы было +06, после — +05)
	f.link(srcLink{
		ID: 4, Code: "full4", DomainID: sl, URL: "https://example.com/4?a=1&b=привет мир",
		Created:    "2024-02-29 12:00:00",
		ValidSince: new("2023-06-01 12:00:00"),
		ValidUntil: new("2026-12-05 10:01:00"),
		MaxVisits:  new(int32(7)),
		Title:      new("Привет, «мир»"),
		Crawlable:  true, NoForwardQry: true,
		Tags: []string{"zeta", "alpha", "бета", "mail_sms"},
	})
	// 5: пустое название (не то же, что отсутствующее, но в link это одно значение)
	f.link(srcLink{ID: 5, Code: "empty", URL: "https://example.com/5", Title: new(""), Created: "2024-03-01 12:00:00"})
	// 6: очень длинный адрес с пробелами и кириллицей
	f.link(srcLink{ID: 6, Code: "long06", URL: longURL})
	// 7 и 8: дубль кода на основном домене (база Shlink это допускает: domain_id пуст)
	f.link(srcLink{ID: 7, Code: "dup", URL: "https://example.com/first"})
	f.link(srcLink{ID: 8, Code: "dup", URL: "https://example.com/second"})
	// 9: свой код с кириллицей, пробелом и знаками
	f.link(srcLink{ID: 9, Code: "Акция-Зима/2026 %", URL: "https://example.com/9", Tags: []string{"mail_sms"}})

	res := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, Survey: true})

	assert.EqualValues(t, 9, res.Total)
	assert.EqualValues(t, 9, res.Read)
	assert.EqualValues(t, 8, res.Inserted)
	assert.EqualValues(t, 0, res.Existing)
	assert.EqualValues(t, 1, res.Conflicts)
	assert.Equal(t, []Key{{ShortCode: "dup"}}, res.ConflictKeys)
	assert.EqualValues(t, 9, res.MaxID)
	assert.EqualValues(t, 9, res.LastID)

	got := readDst(t, dstConn)
	require.Len(t, got, 8)

	defaultCreated := mustTime(t, "2026-11-25T10:00:00+05:00")

	want := map[Key]Link{
		{ShortCode: "aBc"}: {
			ShortCode: "aBc", LongURL: "https://example.com/1", Tags: []string{}, ForwardQuery: true, CreatedAt: defaultCreated,
		},
		{ShortCode: "abc"}: {
			ShortCode: "abc", LongURL: "https://example.com/2", Tags: []string{}, ForwardQuery: true, CreatedAt: defaultCreated,
		},
		{ShortCode: "aBc", Domain: "sl.demo.test"}: {
			ShortCode: "aBc", Domain: "sl.demo.test", LongURL: "https://example.com/3", Tags: []string{}, ForwardQuery: true,
			CreatedAt: defaultCreated,
		},
		{ShortCode: "full4", Domain: "sl.demo.test"}: {
			ShortCode: "full4", Domain: "sl.demo.test", LongURL: "https://example.com/4?a=1&b=привет мир",
			Title: "Привет, «мир»", Tags: []string{"alpha", "mail_sms", "zeta", "бета"},
			CreatedAt:  mustTime(t, "2024-02-29T12:00:00+06:00"),
			ValidSince: new(mustTime(t, "2023-06-01T12:00:00+06:00")),
			ValidUntil: new(mustTime(t, "2026-12-05T10:01:00+05:00")),
			MaxVisits:  new(int32(7)),
			Crawlable:  true, ForwardQuery: false,
		},
		{ShortCode: "empty"}: {
			ShortCode: "empty", LongURL: "https://example.com/5", Tags: []string{}, ForwardQuery: true,
			CreatedAt: mustTime(t, "2024-03-01T12:00:00+05:00"),
		},
		{ShortCode: "long06"}: {
			ShortCode: "long06", LongURL: longURL, Tags: []string{}, ForwardQuery: true, CreatedAt: defaultCreated,
		},
		{ShortCode: "dup"}: { // из двух строк с одним кодом переносится более ранняя
			ShortCode: "dup", LongURL: "https://example.com/first", Tags: []string{}, ForwardQuery: true, CreatedAt: defaultCreated,
		},
		{ShortCode: "Акция-Зима/2026 %"}: {
			ShortCode: "Акция-Зима/2026 %", LongURL: "https://example.com/9", Tags: []string{"mail_sms"}, ForwardQuery: true,
			CreatedAt: defaultCreated,
		},
	}
	for key, w := range want {
		g, ok := got[key]
		if !assert.True(t, ok, "нет ссылки %s", key) {
			continue
		}
		assert.Empty(t, w.diff(&g.Link), "ссылка %s отличается", key)
		assert.True(t, g.ModifiedAt.Equal(g.CreatedAt), "modified_at = created_at у %s", key)
	}

	// точные моменты — отдельно и явно, в UTC
	full := got[Key{ShortCode: "full4", Domain: "sl.demo.test"}]
	assert.Equal(t, "2024-02-29T06:00:00Z", full.CreatedAt.UTC().Format(time.RFC3339))
	assert.Equal(t, "2023-06-01T06:00:00Z", full.ValidSince.UTC().Format(time.RFC3339))
	assert.Equal(t, "2026-12-05T05:01:00Z", full.ValidUntil.UTC().Format(time.RFC3339))
	assert.Equal(t, "2024-03-01T07:00:00Z", got[Key{ShortCode: "empty"}].CreatedAt.UTC().Format(time.RFC3339))
}

func TestImport_SourceTimezoneUTC(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}

	f.link(srcLink{ID: 1, Code: "utc01", URL: "https://example.com/1",
		Created: "2026-11-25 10:00:00", ValidUntil: new("2026-12-05 10:01:00")})

	runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, SrcLocation: time.UTC})

	got := readDst(t, dstConn)[Key{ShortCode: "utc01"}]
	assert.Equal(t, "2026-11-25T10:00:00Z", got.CreatedAt.UTC().Format(time.RFC3339))
	require.NotNil(t, got.ValidUntil)
	assert.Equal(t, "2026-12-05T10:01:00Z", got.ValidUntil.UTC().Format(time.RFC3339))
}

// Маленькие пачки: границы пачек ничего не теряют, а дубль, попавший в другую пачку, отсекает база.
func TestImport_SmallBatches(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}

	for i := int64(1); i <= 10; i++ {
		f.link(srcLink{ID: i * 10, Code: "c" + string(rune('a'+i)), URL: "https://example.com/" + string(rune('a'+i))})
	}
	f.link(srcLink{ID: 200, Code: "cb", URL: "https://example.com/other"}) // дубль кода первой ссылки, далеко от неё
	f.link(srcLink{ID: 201, Code: "cc", URL: "https://example.com/c"})     // дубль с тем же адресом

	res := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, BatchSize: 3})

	assert.EqualValues(t, 12, res.Read)
	assert.EqualValues(t, 10, res.Inserted)
	assert.EqualValues(t, 1, res.Existing)
	assert.EqualValues(t, 1, res.Conflicts)
	assert.EqualValues(t, 201, res.LastID)
	assert.EqualValues(t, 10, dstCount(t, dstConn))
	assert.Equal(t, "https://example.com/b", readDst(t, dstConn)[Key{ShortCode: "cb"}].LongURL)
}

func TestImport_DryRun(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}

	f.link(srcLink{ID: 1, Code: "one", URL: "https://example.com/1", Tags: []string{"a"}})
	f.link(srcLink{ID: 2, Code: "two", URL: "https://example.com/2"})
	f.link(srcLink{ID: 3, Code: "two", URL: "https://example.com/2-other"}) // дубль в той же пачке
	f.link(srcLink{ID: 4, Code: "busy", URL: "https://example.com/4"})
	f.link(srcLink{ID: 5, Code: "same", URL: "https://example.com/5"})

	// в приёмнике код busy уже занят другой ссылкой, а same — такой же
	_, err := dstConn.Exec(context.Background(), `insert into link (short_code, long_url) values
		('busy', 'https://example.com/occupied'), ('same', 'https://example.com/5')`)
	require.NoError(t, err)

	dry := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, DryRun: true})
	assert.True(t, dry.DryRun)
	assert.EqualValues(t, 5, dry.Read)
	assert.EqualValues(t, 2, dry.Inserted)
	assert.EqualValues(t, 1, dry.Existing)
	assert.EqualValues(t, 2, dry.Conflicts)
	assert.EqualValues(t, 2, dstCount(t, dstConn), "пробный режим ничего не пишет")

	// настоящий перенос даёт те же числа, что обещал пробный
	actual := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN})
	assert.Equal(t, dry.Inserted, actual.Inserted)
	assert.Equal(t, dry.Existing, actual.Existing)
	assert.Equal(t, dry.Conflicts, actual.Conflicts)
	assert.ElementsMatch(t, dry.ConflictKeys, actual.ConflictKeys)
	assert.EqualValues(t, 4, dstCount(t, dstConn))

	// после переноса пробный режим обещает 0 новых
	again := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, DryRun: true})
	assert.EqualValues(t, 0, again.Inserted)
	assert.EqualValues(t, 3, again.Existing)
	assert.EqualValues(t, 2, again.Conflicts)
}

// Повторный запуск не дублирует ссылки и не затирает то, что в приёмнике успели поправить.
func TestImport_RerunKeepsDestinationEdits(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}
	ctx := context.Background()

	for i := int64(1); i <= 20; i++ {
		f.link(srcLink{ID: i, Code: "r" + string(rune('a'+i)), URL: "https://example.com/" + string(rune('a'+i)), Tags: []string{"x"}})
	}

	first := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, BatchSize: 7})
	assert.EqualValues(t, 20, first.Inserted)
	before := readDst(t, dstConn)

	// в админке поправили название одной ссылки и длинный адрес другой
	_, err := dstConn.Exec(ctx, "update link set title = 'поправлено', modified_at = now() where short_code = 'rb'")
	require.NoError(t, err)
	_, err = dstConn.Exec(ctx, "update link set long_url = 'https://example.com/new' where short_code = 'rc'")
	require.NoError(t, err)

	second := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, BatchSize: 7})
	assert.EqualValues(t, 20, second.Read)
	assert.EqualValues(t, 0, second.Inserted, "повторный запуск ничего не добавляет")
	assert.EqualValues(t, 19, second.Existing)
	assert.EqualValues(t, 1, second.Conflicts, "ссылка с изменённым адресом показана как расхождение")
	assert.EqualValues(t, 20, dstCount(t, dstConn))

	after := readDst(t, dstConn)
	assert.Equal(t, "поправлено", after[Key{ShortCode: "rb"}].Title)
	assert.Equal(t, "https://example.com/new", after[Key{ShortCode: "rc"}].LongURL)
	for key, b := range before {
		if key.ShortCode == "rb" || key.ShortCode == "rc" {
			continue
		}
		a := after[key]
		assert.Empty(t, b.Link.diff(&a.Link), "ссылка %s изменилась при повторном запуске", key)
		assert.True(t, a.ModifiedAt.Equal(b.ModifiedAt))
	}
}

func TestVerify_DetectsProblems(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}
	ctx := context.Background()

	sl := f.domain("sl.demo.test")
	for i := int64(1); i <= 30; i++ {
		l := srcLink{ID: i, Code: fmt.Sprintf("v%02d", i), URL: fmt.Sprintf("https://example.com/%d", i)}
		if i%3 == 0 {
			l.DomainID = sl
			l.Tags = []string{"t1", "t2"}
			l.ValidUntil = new("2026-12-05 10:01:00")
		}
		f.link(l)
	}
	f.link(srcLink{ID: 31, Code: "v01", URL: "https://example.com/dup"}) // дубль в источнике

	runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN})

	verify := func(opts VerifyOptions) *VerifyResult {
		opts.SrcDSN, opts.DstDSN, opts.SrcLocation = srcDSN, dstDSN, standLocation(t)
		opts.Logf = newTestLog(t).Logf
		res, err := Verify(ctx, opts)
		require.NoError(t, err)
		return res
	}

	ok := verify(VerifyOptions{All: true, BatchSize: 8})
	assert.True(t, ok.OK)
	assert.EqualValues(t, 31, ok.SrcLinks)
	assert.EqualValues(t, 30, ok.SrcDistinctKeys)
	assert.EqualValues(t, 30, ok.DstLinks)
	assert.EqualValues(t, 30, ok.Checked)
	assert.EqualValues(t, 30, ok.Matched)
	assert.EqualValues(t, 1, ok.SrcDuplicates)

	sample := verify(VerifyOptions{Sample: 10})
	assert.True(t, sample.OK)
	assert.EqualValues(t, 10, sample.Checked+sample.SrcDuplicates)

	// портим приёмник: одну ссылку удаляем, у другой меняем метки и срок, у третьей — время создания
	_, err := dstConn.Exec(ctx, "delete from link where short_code = 'v02'")
	require.NoError(t, err)
	_, err = dstConn.Exec(ctx, "update link set tags = '{t2,t1}', valid_until = valid_until + interval '1 hour' where short_code = 'v03'")
	require.NoError(t, err)
	_, err = dstConn.Exec(ctx, "update link set created_at = created_at + interval '1 second', forward_query = false where short_code = 'v04'")
	require.NoError(t, err)

	bad := verify(VerifyOptions{All: true})
	assert.False(t, bad.OK)
	assert.EqualValues(t, 1, bad.Missing)
	assert.EqualValues(t, 2, bad.Mismatched)
	assert.EqualValues(t, 27, bad.Matched)

	problems := map[string]VerifyProblem{}
	for _, p := range bad.Problems {
		problems[p.Key.ShortCode] = p
	}
	require.Len(t, problems, 3)
	assert.True(t, problems["v02"].Missing)
	assert.ElementsMatch(t, []string{"tags", "valid_until"}, problems["v03"].Fields)
	assert.ElementsMatch(t, []string{"created_at", "forward_query"}, problems["v04"].Fields)

	// выборка больше числа ссылок — то же, что полная сверка
	badSample := verify(VerifyOptions{Sample: 1000})
	assert.False(t, badSample.OK)
	assert.EqualValues(t, 1, badSample.Missing)
	assert.EqualValues(t, 2, badSample.Mismatched)
}

func TestSurvey_ShowsWhatIsNotImported(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	f := &srcFixture{t: t, conn: srcConn}
	ctx := context.Background()

	sl := f.domain("sl.demo.test")
	f.link(srcLink{ID: 1, Code: "abcd", URL: "https://example.com/1", MaxVisits: new(int32(2)), Tags: []string{"a", "b"}})
	f.link(srcLink{ID: 2, Code: "abcde", URL: "https://example.com/2", MaxVisits: new(int32(5)), Title: new("t")})
	f.link(srcLink{ID: 3, Code: "my-slug", DomainID: sl, URL: "https://example.com/3", ValidSince: new("2026-01-01 00:00:00")})
	f.link(srcLink{ID: 4, Code: "abcd", URL: "https://example.com/4"}) // дубль

	_, err := srcConn.Exec(ctx, `insert into short_url_redirect_rules (priority, long_url, short_url_id) values
		(1, 'https://example.com/ios', 2), (2, 'https://example.com/android', 2)`)
	require.NoError(t, err)
	_, err = srcConn.Exec(ctx, `insert into short_url_visits_counts (potential_bot, count, slot_id, short_url_id) values
		(false, 2, 1, 1), (true, 1, 2, 1), (false, 3, 1, 2)`)
	require.NoError(t, err)

	src, err := openSource(ctx, srcDSN, standLocation(t))
	require.NoError(t, err)
	defer src.close(ctx)

	s, err := src.survey(ctx)
	require.NoError(t, err)
	s.log(newTestLog(t).Logf)

	assert.EqualValues(t, 4, s.Links)
	assert.EqualValues(t, 3, s.DistinctKeys)
	assert.Equal(t, []NamedCount{{Name: "", Count: 3}, {Name: "sl.demo.test", Count: 1}}, s.ByDomain)
	assert.EqualValues(t, 1, s.NonAlnumCodes)
	assert.EqualValues(t, 1, s.WithTags)
	assert.EqualValues(t, 1, s.WithTitle)
	assert.EqualValues(t, 1, s.WithValidSince)
	assert.EqualValues(t, 0, s.WithValidUntil)
	assert.EqualValues(t, 2, s.WithMaxVisits)
	assert.EqualValues(t, 1, s.MaxVisitsReached)
	assert.EqualValues(t, 1, s.WithRedirectRules)
	assert.EqualValues(t, 6, s.Visits)
	assert.EqualValues(t, 2, s.Tags)
}
