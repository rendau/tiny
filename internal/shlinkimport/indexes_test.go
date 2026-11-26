package shlinkimport

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// необходимые для переноса индексы: держат ограничения таблицы link и не снимаются никогда
var requiredIndexes = []string{"link_pkey", "link_short_code_domain_key"}

// linkIndexes — индексы таблицы link приёмника: имя → определение
func linkIndexes(t testing.TB, conn *pgx.Conn) map[string]string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		select c.relname, pg_get_indexdef(i.indexrelid)
		from pg_index i join pg_class c on c.oid = i.indexrelid
		where i.indrelid = to_regclass('link')`)
	require.NoError(t, err)

	res := map[string]string{}
	var name, def string
	_, err = pgx.ForEachRow(rows, []any{&name, &def}, func() error {
		res[name] = def
		return nil
	})
	require.NoError(t, err)
	return res
}

func savedIndexTableExists(t testing.TB, conn *pgx.Conn) bool {
	t.Helper()
	var exists bool
	require.NoError(t, conn.QueryRow(context.Background(), `select to_regclass($1) is not null`, savedIndexTable).Scan(&exists))
	return exists
}

func fillSrc(t testing.TB, conn *pgx.Conn, n int64) {
	t.Helper()
	f := &srcFixture{t: t, conn: conn}
	for i := int64(1); i <= n; i++ {
		f.link(srcLink{ID: i, Code: fmt.Sprintf("x%02d", i), URL: fmt.Sprintf("https://example.com/%d", i)})
	}
}

// duringImport — что было с индексами приёмника в разгар переноса (на каждой строке «ход:»)
func duringImport(t testing.TB, dstConn *pgx.Conn, seen *[][]string) Logf {
	return func(format string, args ...any) {
		t.Logf(format, args...)
		if strings.HasPrefix(format, "ход:") {
			*seen = append(*seen, slices.Sorted(maps.Keys(linkIndexes(t, dstConn))))
		}
	}
}

// Основной перенос идёт без лишних индексов и в конце возвращает их такими же, какими они были
func TestImport_DropsAndRestoresIndexes(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	fillSrc(t, srcConn, 30)

	before := linkIndexes(t, dstConn)
	require.Greater(t, len(before), len(requiredIndexes), "у таблицы link должны быть индексы сверх необходимых")

	var seen [][]string
	res, err := Import(context.Background(), ImportOptions{
		SrcDSN: srcDSN, DstDSN: dstDSN, SrcLocation: time.UTC, BatchSize: 10, ProgressEvery: time.Nanosecond,
		Logf: duringImport(t, dstConn, &seen),
	})
	require.NoError(t, err)
	assert.EqualValues(t, 30, res.Inserted)

	require.NotEmpty(t, seen)
	for _, names := range seen {
		assert.Equal(t, requiredIndexes, names, "во время переноса — только необходимые индексы")
	}

	assert.Equal(t, before, linkIndexes(t, dstConn), "после переноса индексы те же: и имена, и определения")
	assert.False(t, savedIndexTableExists(t, dstConn), "служебная таблица с определениями убрана")

	// поиск уже существующей ссылки после возврата индексов работает
	var n int
	require.NoError(t, dstConn.QueryRow(context.Background(),
		`select count(*) from link where md5(long_url) = md5($1)`, "https://example.com/7").Scan(&n))
	assert.Equal(t, 1, n)
}

// С -keep-indexes, при докачке и в пробном режиме индексы не трогаются
func TestImport_KeepsIndexesWhenAsked(t *testing.T) {
	cases := map[string]ImportOptions{
		"keep-indexes":      {KeepIndexes: true},
		"докачка -new":      {New: true},
		"докачка -after-id": {AfterID: 5},
		"докачка -since":    {Since: new(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))},
		"пробный режим":     {DryRun: true},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			srcDSN, srcConn := newSrcSchema(t)
			dstDSN, dstConn := newDstSchema(t)
			fillSrc(t, srcConn, 30)

			before := slices.Sorted(maps.Keys(linkIndexes(t, dstConn)))

			var seen [][]string
			opts.SrcDSN, opts.DstDSN, opts.SrcLocation = srcDSN, dstDSN, time.UTC
			opts.BatchSize, opts.ProgressEvery = 10, time.Nanosecond
			opts.Logf = duringImport(t, dstConn, &seen)
			_, err := Import(context.Background(), opts)
			require.NoError(t, err)

			require.NotEmpty(t, seen)
			for _, names := range seen {
				assert.Equal(t, before, names, "индексы на месте всё время переноса")
			}
			assert.False(t, savedIndexTableExists(t, dstConn))
		})
	}
}

// Перенос оборвали (Ctrl+C) — индексы всё равно возвращаются
func TestImport_RestoresIndexesAfterCancel(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	fillSrc(t, srcConn, 30)

	before := linkIndexes(t, dstConn)

	ctx, cancel := context.WithCancel(context.Background())
	_, err := Import(ctx, ImportOptions{
		SrcDSN: srcDSN, DstDSN: dstDSN, SrcLocation: time.UTC, BatchSize: 10, ProgressEvery: time.Nanosecond,
		Logf: func(format string, args ...any) {
			t.Logf(format, args...)
			if strings.HasPrefix(format, "ход:") {
				cancel()
			}
		},
	})
	require.ErrorIs(t, err, context.Canceled)

	assert.Equal(t, before, linkIndexes(t, dstConn))
	assert.False(t, savedIndexTableExists(t, dstConn))
}

// Процесс убили, когда индексы были сняты: их определения лежат в базе, и следующий запуск —
// даже докачка — возвращает их
func TestImport_RestoresIndexesLeftByKilledRun(t *testing.T) {
	ctx := context.Background()
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	fillSrc(t, srcConn, 30)

	before := linkIndexes(t, dstConn)

	// «убитый» запуск: индексы сняты, до возврата дело не дошло
	killed, err := openDest(ctx, dstDSN, false)
	require.NoError(t, err)
	dropped, err := killed.dropIndexes(ctx)
	require.NoError(t, err)
	killed.close(ctx)

	assert.Len(t, dropped, len(before)-len(requiredIndexes))
	assert.Equal(t, requiredIndexes, slices.Sorted(maps.Keys(linkIndexes(t, dstConn))))
	require.True(t, savedIndexTableExists(t, dstConn))

	// один индекс успели вернуть руками — повторно он не создаётся, остальные достраиваются
	_, err = dstConn.Exec(ctx, before["link_tags_idx"])
	require.NoError(t, err)

	runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, SrcLocation: time.UTC, New: true})

	assert.Equal(t, before, linkIndexes(t, dstConn))
	assert.False(t, savedIndexTableExists(t, dstConn))
}

// Перенос оборвали посреди запроса: соединение с приёмником при этом закрывается, и индексы
// возвращаются через новое
func TestRestoreIndexes_ReconnectsIfConnectionIsLost(t *testing.T) {
	ctx := context.Background()
	dstDSN, dstConn := newDstSchema(t)
	before := linkIndexes(t, dstConn)

	d, err := openDest(ctx, dstDSN, false)
	require.NoError(t, err)
	_, err = d.dropIndexes(ctx)
	require.NoError(t, err)
	d.close(ctx) // соединение потеряно

	cancelled, cancel := context.WithCancel(ctx)
	cancel() // и сам перенос уже отменён
	require.NoError(t, restoreIndexes(cancelled, dstDSN, d, t.Logf))

	assert.Equal(t, before, linkIndexes(t, dstConn))
	assert.False(t, savedIndexTableExists(t, dstConn))
}

// Снятие индексов повторяемо: второй вызов подряд ничего не теряет
func TestDest_DropIndexesTwice(t *testing.T) {
	ctx := context.Background()
	dstDSN, dstConn := newDstSchema(t)
	before := linkIndexes(t, dstConn)

	d, err := openDest(ctx, dstDSN, false)
	require.NoError(t, err)
	defer d.close(ctx)

	_, err = d.dropIndexes(ctx)
	require.NoError(t, err)
	again, err := d.dropIndexes(ctx)
	require.NoError(t, err)
	assert.Empty(t, again, "снимать больше нечего")

	n, err := d.restoreIndexes(ctx, func(string, ...any) {})
	require.NoError(t, err)
	assert.Equal(t, len(before)-len(requiredIndexes), n)
	assert.Equal(t, before, linkIndexes(t, dstConn))

	n, err = d.restoreIndexes(ctx, func(string, ...any) {})
	require.NoError(t, err)
	assert.Zero(t, n)
}
