package shlinkimport

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// dest — база tiny, таблица link.
type dest struct {
	conn     *pgx.Conn
	readOnly bool
}

var batchTable = pgx.Identifier{"pg_temp", "shlink_import_batch"}

var batchColumns = []string{
	"src_id", "created_at", "domain", "short_code", "long_url", "title", "tags",
	"valid_since", "valid_until", "max_visits", "crawlable", "forward_query",
}

// openDest подключается к приёмнику. readOnly — пробный режим и проверка: соединение помечается
// «только чтение», записать через него нельзя.
func openDest(ctx context.Context, dsn string, readOnly bool) (*dest, error) {
	conn, err := connect(ctx, dsn, roleDst, false)
	if err != nil {
		return nil, err
	}

	d := &dest{conn: conn, readOnly: readOnly}

	var ok bool
	err = conn.QueryRow(ctx, "select to_regclass('link') is not null").Scan(&ok)
	if err != nil {
		closeConn(ctx, conn)
		return nil, fmt.Errorf("%s: проверка таблицы link: %w", roleDst, err)
	}
	if !ok {
		closeConn(ctx, conn)
		return nil, fmt.Errorf("%s: в базе нет таблицы link — сначала примените миграции tiny (их применяет сам сервис при запуске)", roleDst)
	}

	if readOnly {
		if _, err = conn.Exec(ctx, "set default_transaction_read_only = on"); err != nil {
			closeConn(ctx, conn)
			return nil, fmt.Errorf("%s: не удалось перевести соединение в режим «только чтение»: %w", roleDst, err)
		}
		return d, nil
	}

	// временная таблица живёт только в этом соединении и схему базы не меняет
	_, err = conn.Exec(ctx, `create temp table if not exists shlink_import_batch (
		src_id        bigint      not null,
		created_at    timestamptz not null,
		domain        text        not null,
		short_code    text        not null,
		long_url      text        not null,
		title         text        not null,
		tags          text[]      not null,
		valid_since   timestamptz,
		valid_until   timestamptz,
		max_visits    integer,
		crawlable     boolean     not null,
		forward_query boolean     not null
	) on commit delete rows`)
	if err != nil {
		closeConn(ctx, conn)
		return nil, fmt.Errorf("%s: создание временной таблицы: %w", roleDst, err)
	}

	return d, nil
}

func (d *dest) close(ctx context.Context) {
	closeConn(ctx, d.conn)
}

// count — сколько ссылок в приёмнике.
func (d *dest) count(ctx context.Context) (int64, error) {
	var n int64
	if err := d.conn.QueryRow(ctx, "select count(*) from link").Scan(&n); err != nil {
		return 0, fmt.Errorf("приёмник: подсчёт ссылок: %w", err)
	}
	return n, nil
}

// batchOutcome — что стало с пачкой.
type batchOutcome struct {
	Inserted  int64 // записано новых
	Existing  int64 // пропущено: такая ссылка уже есть
	Conflicts []Key // пропущено: пара «код + домен» занята ссылкой с другим длинным адресом
}

// writeBatch пишет пачку одной транзакцией: COPY во временную таблицу и вставка оттуда.
// Занятые пары «код + домен» не трогаются (on conflict do nothing), поэтому повторный запуск
// ничего не дублирует и не затирает то, что в приёмнике успели поправить.
func (d *dest) writeBatch(ctx context.Context, rows []srcRow) (*batchOutcome, error) {
	tx, err := d.conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("приёмник: begin: %w", err)
	}
	defer rollback(ctx, tx)

	_, err = tx.CopyFrom(ctx, batchTable, batchColumns, pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		l := &rows[i].Link
		return []any{
			l.SrcID, l.CreatedAt, l.Domain, l.ShortCode, l.LongURL, l.Title, l.Tags,
			l.ValidSince, l.ValidUntil, l.MaxVisits, l.Crawlable, l.ForwardQuery,
		}, nil
	}))
	if err != nil {
		return nil, fmt.Errorf("приёмник: copy: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		insert into link (created_at, modified_at, domain, short_code, long_url, title, tags,
		                  valid_since, valid_until, max_visits, crawlable, forward_query)
		select created_at, created_at, domain, short_code, long_url, title, tags,
		       valid_since, valid_until, max_visits, crawlable, forward_query
		from pg_temp.shlink_import_batch
		order by src_id
		on conflict (short_code, domain) do nothing`)
	if err != nil {
		return nil, fmt.Errorf("приёмник: insert: %w", err)
	}

	out := &batchOutcome{Inserted: tag.RowsAffected()}

	conflictRows, err := tx.Query(ctx, `
		select b.short_code, b.domain
		from pg_temp.shlink_import_batch b
		join link l on l.short_code = b.short_code and l.domain = b.domain
		where l.long_url <> b.long_url
		order by b.src_id`)
	if err != nil {
		return nil, fmt.Errorf("приёмник: поиск конфликтов: %w", err)
	}
	out.Conflicts, err = pgx.CollectRows(conflictRows, func(row pgx.CollectableRow) (Key, error) {
		var k Key
		return k, row.Scan(&k.ShortCode, &k.Domain)
	})
	if err != nil {
		return nil, fmt.Errorf("приёмник: поиск конфликтов: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("приёмник: commit: %w", err)
	}

	out.Existing = int64(len(rows)) - out.Inserted - int64(len(out.Conflicts))
	return out, nil
}

// classifyBatch — пробный режим: считает, что стало бы с пачкой, ничего не записывая.
// Дубли кода между разными пачками источника здесь не видны (в настоящем переносе их отсекает
// сама база) — их число показывает обзор источника.
func (d *dest) classifyBatch(ctx context.Context, rows []srcRow) (*batchOutcome, error) {
	keys := make([]Key, len(rows))
	for i := range rows {
		keys[i] = rows[i].Key()
	}
	found, err := d.lookup(ctx, keys)
	if err != nil {
		return nil, err
	}

	out := &batchOutcome{}
	winners := make(map[Key]string, len(rows)) // чей длинный адрес занял ключ внутри пачки
	for i := range rows {
		l := &rows[i].Link
		key := l.Key()

		taken, busy := winners[key]
		if !busy {
			if dst, ok := found[key]; ok {
				taken, busy = dst.LongURL, true
			}
		}
		switch {
		case !busy:
			winners[key] = l.LongURL
			out.Inserted++
		case taken == l.LongURL:
			out.Existing++
		default:
			out.Conflicts = append(out.Conflicts, key)
		}
	}
	return out, nil
}

// lookup находит в приёмнике ссылки по списку ключей.
func (d *dest) lookup(ctx context.Context, keys []Key) (map[Key]*Link, error) {
	codes := make([]string, len(keys))
	domains := make([]string, len(keys))
	for i, k := range keys {
		codes[i], domains[i] = k.ShortCode, k.Domain
	}

	rows, err := d.conn.Query(ctx, `
		select l.short_code, l.domain, l.long_url, l.title, l.tags, l.valid_since, l.valid_until,
		       l.max_visits, l.crawlable, l.forward_query, l.created_at
		from (select distinct * from unnest($1::text[], $2::text[]) as u(short_code, domain)) k
		join link l on l.short_code = k.short_code and l.domain = k.domain`, codes, domains)
	if err != nil {
		return nil, fmt.Errorf("приёмник: поиск ссылок: %w", err)
	}
	defer rows.Close()

	res := make(map[Key]*Link, len(keys))
	for rows.Next() {
		l := &Link{}
		err = rows.Scan(&l.ShortCode, &l.Domain, &l.LongURL, &l.Title, &l.Tags, &l.ValidSince, &l.ValidUntil,
			&l.MaxVisits, &l.Crawlable, &l.ForwardQuery, &l.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("приёмник: поиск ссылок: scan: %w", err)
		}
		if l.Tags == nil {
			l.Tags = []string{}
		}
		res[l.Key()] = l
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("приёмник: поиск ссылок: %w", err)
	}
	return res, nil
}
