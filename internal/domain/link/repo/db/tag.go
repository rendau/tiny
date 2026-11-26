package db

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/samber/lo"
)

// Справочник меток (таблица tag): какие метки встречаются у ссылок. С записью ссылок он не связан:
// Create, Update и Delete его не трогают. Его дописывают и чистят отдельные вызовы отсюда —
// их делает фоновый сервис internal/service/linktag.

const (
	tagTableName = "tag"

	// tagRecountLock — замок пересчёта (pg_advisory_lock): две копии сервиса не пересчитывают разом
	tagRecountLock = 7411002
)

// TagList — метки из справочника, по алфавиту
func (r *Repo) TagList(ctx context.Context) ([]string, error) {
	return r.queryStrings(ctx, `select name from `+tagTableName+` order by name`)
}

// TagAdd дописывает метки в справочник; те, что уже есть, не трогает
func (r *Repo) TagAdd(ctx context.Context, names []string) error {
	return tagAdd(ctx, r.Con, names)
}

// TagDropUnused убирает из справочника те из names, которых не осталось ни у одной ссылки.
// Возвращает убранные.
func (r *Repo) TagDropUnused(ctx context.Context, names []string) ([]string, error) {
	return tagDropUnused(ctx, r.Con, names)
}

// TagRecount сверяет справочник меток с таблицей ссылок, читая её целиком: дописывает метки,
// которых в справочнике нет, и убирает те, которых нет у ссылок. Возвращает, что дописано и что
// убрано. done=false — пересчёт уже идёт в другой копии сервиса, ничего не делалось.
func (r *Repo) TagRecount(ctx context.Context) (added, dropped []string, done bool, err error) {
	conn, err := r.Con.Acquire(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	var locked bool
	if err = conn.QueryRow(ctx, `select pg_try_advisory_lock($1)`, tagRecountLock).Scan(&locked); err != nil {
		return nil, nil, false, fmt.Errorf("advisory lock: %w", err)
	}
	if !locked {
		return nil, nil, false, nil
	}
	defer func() {
		// замок снимается, даже если запрос отменили; не снялся — его снимет закрытие соединения
		if _, err := conn.Exec(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, tagRecountLock); err != nil {
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
		}
	}()

	actual, err := queryStrings(ctx, conn, `select distinct unnest(tags) from `+tableName)
	if err != nil {
		return nil, nil, false, fmt.Errorf("read link tags: %w", err)
	}
	stored, err := queryStrings(ctx, conn, `select name from `+tagTableName)
	if err != nil {
		return nil, nil, false, fmt.Errorf("read tags: %w", err)
	}

	missing, extra := lo.Difference(actual, stored)

	if err = tagAdd(ctx, conn, missing); err != nil {
		return nil, nil, false, err
	}

	// лишние убираются с повторной проверкой по ссылкам: за время чтения таблицы ссылка с такой
	// меткой могла появиться
	dropped, err = tagDropUnused(ctx, conn, extra)
	if err != nil {
		return nil, nil, false, err
	}

	slices.Sort(missing)
	return missing, dropped, true, nil
}

// tagConI — пул или соединение: то, чем выполняются запросы справочника
type tagConI interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	execI
}

// tagAdd — имена идут в одном, побайтном, порядке у всех, кто пишет в справочник (и у переноса из
// Shlink): иначе две вставки одних и тех же новых меток могли бы взаимно заблокироваться
func tagAdd(ctx context.Context, con execI, names []string) error {
	names = lo.Uniq(names)
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)

	_, err := con.Exec(ctx,
		`insert into `+tagTableName+` (name) select unnest($1::text[]) on conflict do nothing`, names,
	)
	if err != nil {
		return fmt.Errorf("add tags: %w", err)
	}
	return nil
}

func tagDropUnused(ctx context.Context, con tagConI, names []string) ([]string, error) {
	names = lo.Uniq(names)
	if len(names) == 0 {
		return nil, nil
	}

	tx, err := con.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// «Есть ли ещё ссылки с меткой» — только по индексу link_tags_idx. Без этого для метки,
	// которой у ссылок уже нет (а это и есть случай удаления), планировщик читает всю таблицу:
	// сотни миллисекунд вместо сотых долей.
	if _, err = tx.Exec(ctx, `set local enable_seqscan = off`); err != nil {
		return nil, fmt.Errorf("set enable_seqscan: %w", err)
	}

	dropped, err := queryStrings(ctx, tx,
		`delete from `+tagTableName+` t
		where t.name = any($1)
		  and not exists (select 1 from `+tableName+` l where l.tags @> array[t.name])
		returning t.name`,
		names,
	)
	if err != nil {
		return nil, fmt.Errorf("drop tags: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	slices.Sort(dropped)
	return dropped, nil
}
