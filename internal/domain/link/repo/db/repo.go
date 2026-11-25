package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rendau/mobone/v2"
	moboneTools "github.com/rendau/mobone/v2/tools"
	"github.com/samber/lo"

	commonRepoPg "github.com/rendau/tiny/internal/domain/common/repo/pg"
	"github.com/rendau/tiny/internal/domain/link/model"
	repoModel "github.com/rendau/tiny/internal/domain/link/repo/db/model"
	"github.com/rendau/tiny/internal/errs"
)

const (
	tableName = "link"

	// shortCodeUniqueConstraint — ограничение уникальности пары (код, домен) из миграции
	shortCodeUniqueConstraint = "link_short_code_domain_key"
	pgUniqueViolation         = "23505"
)

type Repo struct {
	*commonRepoPg.Base
	ModelStore *mobone.ModelStore
}

func New(con *pgxpool.Pool) *Repo {
	base := commonRepoPg.NewBase(con)
	return &Repo{
		Base: base,
		ModelStore: &mobone.ModelStore{
			Con:       base.Con,
			QB:        base.QB,
			TableName: tableName,
		},
	}
}

func (r *Repo) List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	conditions, conditionExps := r.getConditions(pars)
	items := make([]*repoModel.Select, 0)

	totalCount, err := r.ModelStore.List(ctx, mobone.ListParams{
		Conditions:           conditions,
		ConditionExpressions: conditionExps,
		Page:                 pars.Page,
		PageSize:             pars.PageSize,
		WithTotalCount:       pars.WithTotalCount,
		OnlyCount:            pars.OnlyCount,
		Sort:                 moboneTools.ConstructSortColumns(allowedSortFields, pars.Sort),
	}, func(add bool) mobone.ListModelI {
		item := &repoModel.Select{}
		if add {
			items = append(items, item)
		}
		return item
	})
	if err != nil {
		return nil, 0, fmt.Errorf("ModelStore.List: %w", err)
	}
	return lo.Map(items, repoModel.EncodeSelect), totalCount, nil
}

func (r *Repo) Get(ctx context.Context, id int64) (*model.Main, bool, error) {
	m := &repoModel.Select{Id: id}
	found, err := r.ModelStore.Get(ctx, m)
	if err != nil {
		return nil, false, fmt.Errorf("ModelStore.Get: %w", err)
	}
	if !found {
		return nil, false, nil
	}
	return repoModel.EncodeSelect(m, 0), true, nil
}

// Create — занятая пара (код, домен) возвращается как errs.ShortCodeExists
func (r *Repo) Create(ctx context.Context, obj *model.Edit) (int64, error) {
	m := repoModel.DecodeUpsert(obj)
	if err := r.ModelStore.Create(ctx, m); err != nil {
		if isShortCodeConflict(err) {
			return 0, errs.ShortCodeExists
		}
		return 0, fmt.Errorf("ModelStore.Create: %w", err)
	}
	return m.NewId, nil
}

func (r *Repo) Update(ctx context.Context, id int64, obj *model.Edit) error {
	m := repoModel.DecodeUpsert(obj)
	m.PKId = id
	if err := r.ModelStore.Update(ctx, m); err != nil {
		if isShortCodeConflict(err) {
			return errs.ShortCodeExists
		}
		return fmt.Errorf("ModelStore.Update: %w", err)
	}
	return nil
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	if err := r.ModelStore.Delete(ctx, &repoModel.Upsert{PKId: id}); err != nil {
		return fmt.Errorf("ModelStore.Delete: %w", err)
	}
	return nil
}

func isShortCodeConflict(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == shortCodeUniqueConstraint
}
