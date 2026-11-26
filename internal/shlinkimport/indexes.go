package shlinkimport

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Индексы таблицы link на время переноса.
//
// При загрузке миллионов ссылок почти всё время уходит на обновление индексов (поиск по части
// адреса, по меткам и т. д.), а для самого переноса нужны только первичный ключ и уникальность
// пары «код + домен». Поэтому перед основным переносом остальные индексы снимаются, а после —
// строятся заново, один раз по готовой таблице: так выходит в разы быстрее.
//
// Postgres не умеет «выключить» индекс — его удаляют и создают снова. Чтобы определения не
// потерялись, если перенос оборвётся (пропала связь, убили процесс), они сначала записываются в
// таблицу savedIndexTable в самой базе приёмника. Любой следующий запуск переноса в конце
// вернёт всё, что там лежит, и уберёт таблицу.

const savedIndexTable = "shlink_import_saved_index"

type savedIndex struct {
	Name string
	Def  string // команда create index, как её отдаёт pg_get_indexdef
}

// dropIndexes снимает с таблицы link все индексы, кроме тех, что держат ограничения (первичный
// ключ и уникальность «код + домен»), сохранив их определения. Возвращает имена снятых.
func (d *dest) dropIndexes(ctx context.Context) ([]string, error) {
	tx, err := d.conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("приёмник: begin: %w", err)
	}
	defer rollback(ctx, tx)

	_, err = tx.Exec(ctx, `create table if not exists `+savedIndexTable+` (
		name text primary key,
		def  text not null
	)`)
	if err != nil {
		return nil, fmt.Errorf("приёмник: таблица для определений индексов: %w", err)
	}

	rows, err := tx.Query(ctx, `
		select n.nspname, c.relname, pg_get_indexdef(i.indexrelid)
		from pg_index i
		join pg_class c on c.oid = i.indexrelid
		join pg_namespace n on n.oid = c.relnamespace
		where i.indrelid = to_regclass('link')
		  and not exists (select 1 from pg_constraint k where k.conindid = i.indexrelid)
		order by c.relname`)
	if err != nil {
		return nil, fmt.Errorf("приёмник: список индексов: %w", err)
	}
	type found struct{ schema, name, def string }
	indexes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (found, error) {
		var f found
		return f, row.Scan(&f.schema, &f.name, &f.def)
	})
	if err != nil {
		return nil, fmt.Errorf("приёмник: список индексов: %w", err)
	}

	names := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		// определение, оставшееся от оборванного запуска, не затирается
		_, err = tx.Exec(ctx,
			`insert into `+savedIndexTable+` (name, def) values ($1, $2) on conflict (name) do nothing`,
			idx.name, idx.def)
		if err != nil {
			return nil, fmt.Errorf("приёмник: сохранение определения индекса %s: %w", idx.name, err)
		}
		if _, err = tx.Exec(ctx, `drop index `+pgx.Identifier{idx.schema, idx.name}.Sanitize()); err != nil {
			return nil, fmt.Errorf("приёмник: снятие индекса %s: %w", idx.name, err)
		}
		names = append(names, idx.name)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("приёмник: commit: %w", err)
	}
	return names, nil
}

// savedIndexes — определения снятых индексов, которые ещё не возвращены
func (d *dest) savedIndexes(ctx context.Context) ([]savedIndex, error) {
	var exists bool
	err := d.conn.QueryRow(ctx, `select to_regclass($1) is not null`, savedIndexTable).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("приёмник: проверка сохранённых индексов: %w", err)
	}
	if !exists {
		return nil, nil
	}

	rows, err := d.conn.Query(ctx, `select name, def from `+savedIndexTable+` order by name`)
	if err != nil {
		return nil, fmt.Errorf("приёмник: сохранённые индексы: %w", err)
	}
	saved, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (savedIndex, error) {
		var s savedIndex
		return s, row.Scan(&s.Name, &s.Def)
	})
	if err != nil {
		return nil, fmt.Errorf("приёмник: сохранённые индексы: %w", err)
	}
	return saved, nil
}

// restoreIndexes строит заново все снятые индексы и убирает таблицу с их определениями.
// Каждый индекс — отдельной командой: построенное не пропадает, если следующий не удался.
func (d *dest) restoreIndexes(ctx context.Context, logf Logf) (int, error) {
	saved, err := d.savedIndexes(ctx)
	if err != nil {
		return 0, err
	}
	if len(saved) == 0 {
		// таблица могла остаться пустой от оборванного запуска
		if _, err = d.conn.Exec(ctx, `drop table if exists `+savedIndexTable); err != nil {
			return 0, fmt.Errorf("приёмник: удаление таблицы определений индексов: %w", err)
		}
		return 0, nil
	}

	for i, idx := range saved {
		started := time.Now()

		var exists bool
		err = d.conn.QueryRow(ctx, `
			select exists (
				select 1 from pg_index i join pg_class c on c.oid = i.indexrelid
				where i.indrelid = to_regclass('link') and c.relname = $1
			)`, idx.Name).Scan(&exists)
		if err != nil {
			return i, fmt.Errorf("приёмник: проверка индекса %s: %w", idx.Name, err)
		}
		if !exists {
			if _, err = d.conn.Exec(ctx, idx.Def); err != nil {
				return i, fmt.Errorf("приёмник: построение индекса %s: %w", idx.Name, err)
			}
		}

		if _, err = d.conn.Exec(ctx, `delete from `+savedIndexTable+` where name = $1`, idx.Name); err != nil {
			return i, fmt.Errorf("приёмник: отметка индекса %s: %w", idx.Name, err)
		}
		logf("индексы: %s построен (%d из %d) за %s", idx.Name, i+1, len(saved), time.Since(started).Round(time.Millisecond))
	}

	if _, err = d.conn.Exec(ctx, `drop table if exists `+savedIndexTable); err != nil {
		return len(saved), fmt.Errorf("приёмник: удаление таблицы определений индексов: %w", err)
	}
	return len(saved), nil
}
