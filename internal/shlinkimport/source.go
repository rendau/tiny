package shlinkimport

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// source — база Shlink. Из неё только читаем: каждый запрос идёт в отдельной короткой транзакции
// «только чтение» (длинных транзакций на живой базе не держим), других обращений к соединению нет.
//
// Настроек на само соединение (set …) не ставим нарочно: к боевой базе обычно ходят через пулер,
// а он может отдать то же соединение с сервером самому Shlink — и настройка «только чтение»,
// оставшаяся на нём, сломала бы Shlink запись.
type source struct {
	conn *pgx.Conn
	loc  *time.Location // пояс, в котором Shlink хранит время
}

func openSource(ctx context.Context, dsn string, loc *time.Location) (*source, error) {
	conn, err := connect(ctx, dsn, roleSrc, true)
	if err != nil {
		return nil, err
	}

	s := &source{conn: conn, loc: loc}

	var ok bool
	err = s.readTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select to_regclass('short_urls') is not null
			and to_regclass('domains') is not null
			and to_regclass('tags') is not null
			and to_regclass('short_urls_in_tags') is not null`).Scan(&ok)
	})
	if err != nil {
		closeConn(ctx, conn)
		return nil, fmt.Errorf("%s: проверка таблиц Shlink: %w", roleSrc, err)
	}
	if !ok {
		closeConn(ctx, conn)
		return nil, fmt.Errorf("%s: в базе нет таблиц Shlink (short_urls, domains, tags, short_urls_in_tags) — это точно база Shlink?", roleSrc)
	}

	return s, nil
}

func (s *source) close(ctx context.Context) {
	closeConn(ctx, s.conn)
}

// readTx выполняет fn в транзакции «только чтение».
func (s *source) readTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return fmt.Errorf("begin read only: %w", err)
	}
	defer rollback(ctx, tx)

	return fn(tx)
}

// scope — какие ссылки источника берём: id в (afterID, maxID], при since — созданные не раньше.
type scope struct {
	afterID int64
	maxID   int64
	since   *time.Time
}

const (
	linkColumns = `s.id, s.short_code, coalesce(d.authority, '')::text, s.original_url, coalesce(s.title, '')::text,
       array(select t.name::text
             from short_urls_in_tags sit
             join tags t on t.id = sit.tag_id
             where sit.short_url_id = s.id
             order by t.name),
       s.valid_since, s.valid_until, s.max_visits, s.forward_query, s.date_created`

	// дубль — строка, у которой тот же код и домен, что у более ранней строки. Shlink сам такого
	// не допускает, но база от этого защищает только ссылки с доменом (у основного домена
	// domain_id пуст, а пустые значения в уникальном индексе не сравниваются).
	dupColumn = `exists(select 1 from short_urls p
              where p.short_code = s.short_code and p.domain_id is not distinct from s.domain_id and p.id < s.id)`

	linkFrom = ` from short_urls s left join domains d on d.id = s.domain_id`

	naiveTimeLayout = "2006-01-02 15:04:05"
)

// where собирает условие отбора; номера параметров продолжают уже занятые.
func (s *source) where(sc scope, afterID int64, args []any) (string, []any) {
	args = append(args, afterID)
	cond := fmt.Sprintf(" where s.id > $%d", len(args))
	args = append(args, sc.maxID)
	cond += fmt.Sprintf(" and s.id <= $%d", len(args))
	if sc.since != nil {
		// date_created хранится без пояса, в поясе Shlink — сравниваем в тех же цифрах на часах
		args = append(args, sc.since.In(s.loc).Format(naiveTimeLayout))
		cond += fmt.Sprintf(" and s.date_created >= $%d::timestamp", len(args))
	}
	return cond, args
}

// maxID — наибольший id ссылки на сейчас. Перенос идёт до него: всё, что появится в Shlink позже,
// забирает докачка.
func (s *source) maxID(ctx context.Context) (int64, error) {
	var id int64
	err := s.readTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select coalesce(max(id), 0) from short_urls").Scan(&id)
	})
	if err != nil {
		return 0, fmt.Errorf("источник: наибольший id: %w", err)
	}
	return id, nil
}

// count — сколько ссылок попадает в отбор.
func (s *source) count(ctx context.Context, sc scope) (int64, error) {
	cond, args := s.where(sc, sc.afterID, nil)
	var n int64
	err := s.readTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select count(*) from short_urls s"+cond, args...).Scan(&n)
	})
	if err != nil {
		return 0, fmt.Errorf("источник: подсчёт ссылок: %w", err)
	}
	return n, nil
}

// srcRow — ссылка источника плюс признак дубля (заполняется только при проверке).
type srcRow struct {
	Link
	IsDup bool
}

func (s *source) scanRows(rows pgx.Rows, withDup bool, buf []srcRow) ([]srcRow, error) {
	defer rows.Close()

	for rows.Next() {
		var (
			r          srcRow
			created    time.Time
			validSince *time.Time
			validUntil *time.Time
		)
		dest := []any{
			&r.SrcID, &r.ShortCode, &r.Domain, &r.LongURL, &r.Title, &r.Tags,
			&validSince, &validUntil, &r.MaxVisits, &r.ForwardQuery, &created,
		}
		if withDup {
			dest = append(dest, &r.IsDup)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if r.Tags == nil {
			r.Tags = []string{}
		}
		r.CreatedAt = wallClockIn(created, s.loc)
		r.ValidSince = wallClockPtrIn(validSince, s.loc)
		r.ValidUntil = wallClockPtrIn(validUntil, s.loc)
		buf = append(buf, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return buf, nil
}

// readBatch читает очередную пачку ссылок с id больше afterID, по возрастанию id.
// buf переиспользуется между пачками, чтобы память не росла с числом ссылок.
func (s *source) readBatch(ctx context.Context, sc scope, afterID int64, limit int, withDup bool, buf []srcRow) ([]srcRow, error) {
	cond, args := s.where(sc, afterID, nil)
	args = append(args, limit)

	query := "select " + linkColumns
	if withDup {
		query += ", " + dupColumn
	}
	query += linkFrom + cond + fmt.Sprintf(" order by s.id limit $%d", len(args))

	buf = buf[:0]
	err := s.readTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		buf, err = s.scanRows(rows, withDup, buf)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("источник: чтение пачки после id %d: %w", afterID, err)
	}
	return buf, nil
}

// readByIDs читает ссылки по списку id (для выборочной проверки), по возрастанию id.
func (s *source) readByIDs(ctx context.Context, ids []int64, buf []srcRow) ([]srcRow, error) {
	query := "select " + linkColumns + ", " + dupColumn + linkFrom + " where s.id = any($1) order by s.id"

	buf = buf[:0]
	err := s.readTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, ids)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		buf, err = s.scanRows(rows, true, buf)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("источник: чтение ссылок по id: %w", err)
	}
	return buf, nil
}

// sampleIDs — случайная выборка id ссылок.
func (s *source) sampleIDs(ctx context.Context, n int) ([]int64, error) {
	var ids []int64
	err := s.readTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			"select id from (select id from short_urls order by random() limit $1) x order by id", n)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[int64])
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("источник: случайная выборка: %w", err)
	}
	return ids, nil
}

// srcKey — ключ ссылки источника с её id.
type srcKey struct {
	ID  int64
	Key Key
}

// keysBefore читает ключи ссылок с id меньше beforeID, от новых к старым.
func (s *source) keysBefore(ctx context.Context, beforeID int64, limit int) ([]srcKey, error) {
	var keys []srcKey
	err := s.readTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`select s.id, s.short_code, coalesce(d.authority, '')::text`+linkFrom+
				` where s.id < $1 order by s.id desc limit $2`, beforeID, limit)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		keys, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (srcKey, error) {
			var k srcKey
			return k, row.Scan(&k.ID, &k.Key.ShortCode, &k.Key.Domain)
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("источник: чтение ключей перед id %d: %w", beforeID, err)
	}
	return keys, nil
}

// Survey — обзор базы Shlink: что в ней есть и что из этого не переносится.
type Survey struct {
	Links             int64        // всего ссылок
	DistinctKeys      int64        // разных пар «код + домен»
	ByDomain          []NamedCount // ссылок по доменам ('' — основной)
	CodeLengths       []NamedCount // ссылок по длине кода
	NonAlnumCodes     int64        // коды не только из 0-9a-zA-Z (свои коды с дефисами, кириллицей…)
	WithTags          int64
	WithTitle         int64
	WithValidSince    int64
	WithValidUntil    int64
	WithMaxVisits     int64
	MaxVisitsReached  int64 // предел посещений уже исчерпан — в Shlink такая ссылка отдаёт 404
	WithRedirectRules int64 // ссылок с правилами перенаправления (не переносятся)
	Visits            int64 // учтённых посещений (не переносятся)
	Tags              int64 // разных меток
	DomainRedirects   int64 // доменов, у которых в Shlink настроены свои перенаправления (не переносятся)
	MaxLongURLLen     int64 // самый длинный адрес, в символах
}

// NamedCount — число ссылок с подписью.
type NamedCount struct {
	Name  string
	Count int64
}

func (s *source) survey(ctx context.Context) (*Survey, error) {
	res := &Survey{}
	err := s.readTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			select count(*),
			       count(*) filter (where s.short_code !~ '^[0-9a-zA-Z]+$'),
			       count(*) filter (where exists(select 1 from short_urls_in_tags sit where sit.short_url_id = s.id)),
			       count(*) filter (where coalesce(s.title, '') <> ''),
			       count(*) filter (where s.valid_since is not null),
			       count(*) filter (where s.valid_until is not null),
			       count(*) filter (where s.max_visits is not null),
			       coalesce(max(length(s.original_url)), 0)
			from short_urls s`).Scan(
			&res.Links, &res.NonAlnumCodes, &res.WithTags, &res.WithTitle,
			&res.WithValidSince, &res.WithValidUntil, &res.WithMaxVisits, &res.MaxLongURLLen)
		if err != nil {
			return fmt.Errorf("общие числа: %w", err)
		}

		err = tx.QueryRow(ctx,
			"select count(*) from (select 1 from short_urls group by short_code, domain_id) x").Scan(&res.DistinctKeys)
		if err != nil {
			return fmt.Errorf("разные ключи: %w", err)
		}

		res.ByDomain, err = queryNamedCounts(ctx, tx,
			`select coalesce(d.authority, '')::text, count(*)`+linkFrom+` group by 1 order by 2 desc, 1 limit 20`)
		if err != nil {
			return fmt.Errorf("по доменам: %w", err)
		}

		res.CodeLengths, err = queryNamedCounts(ctx, tx,
			`select length(short_code)::text, count(*) from short_urls group by length(short_code) order by 2 desc, length(short_code) limit 10`)
		if err != nil {
			return fmt.Errorf("по длине кода: %w", err)
		}

		err = tx.QueryRow(ctx, "select count(*) from tags").Scan(&res.Tags)
		if err != nil {
			return fmt.Errorf("метки: %w", err)
		}

		err = tx.QueryRow(ctx, `select count(*) from domains
			where coalesce(base_url_redirect, '') <> ''
			   or coalesce(regular_not_found_redirect, '') <> ''
			   or coalesce(invalid_short_url_redirect, '') <> ''`).Scan(&res.DomainRedirects)
		if err != nil {
			return fmt.Errorf("перенаправления доменов: %w", err)
		}

		// таблиц правил и счётчиков в очень старой базе может не быть — тогда считаем нулём
		var hasRules, hasCounts bool
		err = tx.QueryRow(ctx, `select to_regclass('short_url_redirect_rules') is not null,
			to_regclass('short_url_visits_counts') is not null`).Scan(&hasRules, &hasCounts)
		if err != nil {
			return fmt.Errorf("наличие таблиц: %w", err)
		}
		if hasRules {
			err = tx.QueryRow(ctx,
				"select count(distinct short_url_id) from short_url_redirect_rules").Scan(&res.WithRedirectRules)
			if err != nil {
				return fmt.Errorf("правила перенаправления: %w", err)
			}
		}
		if hasCounts {
			err = tx.QueryRow(ctx, `
				select coalesce((select sum(c.count) from short_url_visits_counts c), 0)::bigint,
				       (select count(*) from short_urls s
				        where s.max_visits is not null
				          and (select coalesce(sum(c.count), 0) from short_url_visits_counts c
				               where c.short_url_id = s.id) >= s.max_visits)`).Scan(&res.Visits, &res.MaxVisitsReached)
			if err != nil {
				return fmt.Errorf("посещения: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("источник: обзор: %w", err)
	}
	return res, nil
}

func queryNamedCounts(ctx context.Context, tx pgx.Tx, query string) ([]NamedCount, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (NamedCount, error) {
		var c NamedCount
		return c, row.Scan(&c.Name, &c.Count)
	})
}

// log печатает обзор в журнал.
func (v *Survey) log(logf Logf) {
	namedList := func(items []NamedCount, emptyName string) string {
		parts := make([]string, 0, len(items))
		for _, it := range items {
			name := it.Name
			if name == "" {
				name = emptyName
			}
			parts = append(parts, fmt.Sprintf("%s — %d", name, it.Count))
		}
		return strings.Join(parts, "; ")
	}

	logf("источник: ссылок %d, разных пар «код + домен» %d, разных меток %d", v.Links, v.DistinctKeys, v.Tags)
	logf("источник: по доменам: %s", namedList(v.ByDomain, "основной домен"))
	logf("источник: по длине кода (символов — ссылок): %s", namedList(v.CodeLengths, "?"))
	logf("источник: с метками %d, с названием %d, со сроком «с» %d, со сроком «до» %d, с пределом посещений %d; самый длинный адрес — %d символов",
		v.WithTags, v.WithTitle, v.WithValidSince, v.WithValidUntil, v.WithMaxVisits, v.MaxLongURLLen)
	if v.NonAlnumCodes > 0 {
		logf("источник: у %d ссылок код не только из цифр и латинских букв (свои коды: дефисы, кириллица и т.п.) — переносятся как есть", v.NonAlnumCodes)
	}
	if dups := v.Links - v.DistinctKeys; dups > 0 {
		logf("ВНИМАНИЕ: в источнике %d лишних строк с уже занятой парой «код + домен» — переносится самая ранняя, остальные пропускаются", dups)
	}
	if v.WithRedirectRules > 0 {
		logf("ВНИМАНИЕ: у %d ссылок есть правила перенаправления — правила не переносятся, такие ссылки будут вести на свой основной адрес", v.WithRedirectRules)
	}
	if v.DomainRedirects > 0 {
		logf("ВНИМАНИЕ: у %d доменов в Shlink настроены свои перенаправления (для корня домена, для несуществующих ссылок) — они не переносятся", v.DomainRedirects)
	}
	if v.MaxVisitsReached > 0 {
		logf("ВНИМАНИЕ: у %d ссылок предел посещений уже исчерпан — в Shlink они отдают 404, а после переноса снова заработают (счётчики посещений не переносятся)", v.MaxVisitsReached)
	}
	if v.Visits > 0 {
		logf("источник: учтено посещений %d — счётчики и история посещений не переносятся", v.Visits)
	}
}
