package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/rendau/tiny/internal/domain/link/model"
	repoModel "github.com/rendau/tiny/internal/domain/link/repo/db/model"
)

var allowedSortFields = map[string]string{
	"id":          "id",
	"created_at":  "created_at",
	"modified_at": "modified_at",
	"short_code":  "short_code",
	"valid_until": "valid_until",
}

// likeEscaper — подстрока поиска не должна работать как шаблон LIKE
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (r *Repo) getConditions(pars *model.ListReq) (map[string]any, map[string][]any) {
	conditions := make(map[string]any, 4)
	conditionExps := make(map[string][]any, 4)

	if pars == nil {
		return conditions, conditionExps
	}

	// точный код — по индексу link_short_code_domain_key
	if pars.ShortCode != nil {
		conditions["short_code"] = *pars.ShortCode
	}
	if pars.Domain != nil {
		conditions["domain"] = *pars.Domain
	}
	// метка — по GIN-индексу link_tags_idx
	if pars.Tag != nil {
		conditionExps["tags @> ?"] = []any{[]string{*pars.Tag}}
	}
	// истёкшие — по частичному индексу link_valid_until_idx
	if pars.Expired != nil {
		if *pars.Expired {
			conditionExps["valid_until is not null and valid_until < now()"] = nil
		} else {
			conditionExps["(valid_until is null or valid_until >= now())"] = nil
		}
	}
	if pars.Search != nil && *pars.Search != "" {
		pattern := "%" + likeEscaper.Replace(*pars.Search) + "%"
		conditionExps["(long_url ilike ? or title ilike ?)"] = []any{pattern, pattern}
	}

	return conditions, conditionExps
}

// GetByCode — ссылка ровно этого домена (пустая строка — основной)
func (r *Repo) GetByCode(ctx context.Context, shortCode, domain string) (*model.Main, bool, error) {
	return r.queryOne(ctx,
		`select `+repoModel.SelectColumns+` from `+tableName+` where short_code = $1 and domain = $2`,
		shortCode, domain,
	)
}

// FindForRedirect — ссылка для перехода: сначала ссылка именно этого домена, иначе — основного (пустая строка).
// Оба варианта берутся одним проходом по индексу link_short_code_domain_key.
func (r *Repo) FindForRedirect(ctx context.Context, shortCode, domain string) (*model.Main, bool, error) {
	if domain == "" {
		return r.GetByCode(ctx, shortCode, "")
	}

	return r.queryOne(ctx,
		`select `+repoModel.SelectColumns+` from `+tableName+`
		where short_code = $1 and domain in ($2, '')
		order by (domain = '')
		limit 1`,
		shortCode, domain,
	)
}

// FindExisting — самая старая ссылка с этим длинным адресом и переданными параметрами.
// Длинный адрес ищется по индексу link_long_url_md5_idx.
func (r *Repo) FindExisting(ctx context.Context, pars *model.FindExistingReq) (*model.Main, bool, error) {
	qb := r.QB.Select(repoModel.SelectColumns).From(tableName).
		Where("md5(long_url) = md5(?)", pars.LongUrl).
		Where(sq.Eq{"long_url": pars.LongUrl}).
		OrderBy("id").
		Limit(1)

	if pars.ShortCode != nil {
		qb = qb.Where(sq.Eq{"short_code": *pars.ShortCode})
	}
	if pars.Domain != nil {
		qb = qb.Where(sq.Eq{"domain": *pars.Domain})
	}
	if pars.ValidSince != nil {
		qb = qb.Where(sq.Eq{"valid_since": *pars.ValidSince})
	}
	if pars.ValidUntil != nil {
		qb = qb.Where(sq.Eq{"valid_until": *pars.ValidUntil})
	}
	if pars.MaxVisits != nil {
		qb = qb.Where(sq.Eq{"max_visits": *pars.MaxVisits})
	}
	if len(pars.Tags) > 0 {
		// ровно этот набор: метки в строке хранятся без повторов
		qb = qb.Where("tags @> ? and cardinality(tags) = ?", pars.Tags, len(pars.Tags))
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, false, fmt.Errorf("FindExisting build query: %w", err)
	}

	return r.queryOne(ctx, query, args...)
}

// TagList — все метки, которые встречаются у ссылок, по алфавиту. Проходит всю таблицу
// (индекс по массиву не умеет отдавать список значений) — вызывающий кэширует результат.
func (r *Repo) TagList(ctx context.Context) ([]string, error) {
	return r.queryStrings(ctx,
		`select t from (select distinct unnest(tags) as t from `+tableName+`) x order by t`,
	)
}

// CrawlableCodes — коды ссылок, которые разрешено обходить поисковикам (для robots.txt)
func (r *Repo) CrawlableCodes(ctx context.Context) ([]string, error) {
	return r.queryStrings(ctx,
		`select short_code from `+tableName+` where crawlable order by short_code collate "C"`,
	)
}

// Ping — база отвечает
func (r *Repo) Ping(ctx context.Context) error {
	var one int
	if err := r.Con.QueryRow(ctx, `select 1`).Scan(&one); err != nil {
		return fmt.Errorf("Ping: %w", err)
	}
	return nil
}

func (r *Repo) queryOne(ctx context.Context, query string, args ...any) (*model.Main, bool, error) {
	m := &repoModel.Select{}

	err := r.Con.QueryRow(ctx, query, args...).Scan(m.ScanDest()...)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("query: %w", err)
	}

	return repoModel.EncodeSelect(m, 0), true, nil
}

func (r *Repo) queryStrings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := r.Con.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	result, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("collect rows: %w", err)
	}

	return result, nil
}
