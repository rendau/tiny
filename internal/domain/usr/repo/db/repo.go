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
	"github.com/rendau/tiny/internal/domain/usr/model"
	repoModel "github.com/rendau/tiny/internal/domain/usr/repo/db/model"
	"github.com/rendau/tiny/internal/errs"
)

// имя ограничения уникальности логина (migrations/000001_init.up.sql: username text not null unique)
const usernameUniqueConstraint = "usr_username_key"

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
			TableName: "usr",
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

func (r *Repo) Create(ctx context.Context, obj *model.Edit) (int64, error) {
	m := repoModel.DecodeUpsert(obj)
	if err := r.ModelStore.Create(ctx, m); err != nil {
		return 0, wrapWriteErr("ModelStore.Create", err)
	}
	return m.NewId, nil
}

func (r *Repo) Update(ctx context.Context, id int64, obj *model.Edit) error {
	m := repoModel.DecodeUpsert(obj)
	m.PKId = id
	if err := r.ModelStore.Update(ctx, m); err != nil {
		return wrapWriteErr("ModelStore.Update", err)
	}
	return nil
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	if err := r.ModelStore.Delete(ctx, &repoModel.Upsert{PKId: id}); err != nil {
		return fmt.Errorf("ModelStore.Delete: %w", err)
	}
	return nil
}

// wrapWriteErr — занятый логин отдаётся отказом по делу, а не сбоем
func wrapWriteErr(op string, err error) error {
	if isUniqueViolation(err, usernameUniqueConstraint) {
		return errs.UsrUsernameExists
	}
	return fmt.Errorf("%s: %w", op, err)
}

// isUniqueViolation — ошибка нарушения уникальности (23505) на ограничении constraint
func isUniqueViolation(err error, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
