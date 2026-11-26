package service

import (
	"context"

	"github.com/rendau/tiny/internal/domain/link/model"
)

type RepoDbI interface {
	List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error)
	Get(ctx context.Context, id int64) (*model.Main, bool, error)
	Create(ctx context.Context, obj *model.Edit) (int64, error)
	Update(ctx context.Context, id int64, obj *model.Edit) error
	Delete(ctx context.Context, id int64) error

	GetByCode(ctx context.Context, shortCode, domain string) (*model.Main, bool, error)
	FindForRedirect(ctx context.Context, shortCode, domain string) (*model.Main, bool, error)
	FindExisting(ctx context.Context, pars *model.FindExistingReq) (*model.Main, bool, error)
	CountAll(ctx context.Context) (int64, error)
	CountExpired(ctx context.Context) (int64, error)
	// CountUpTo — сколько ссылок подходит под фильтр, но не больше limit
	CountUpTo(ctx context.Context, pars *model.ListReq, limit int64) (int64, error)

	// справочник меток
	TagList(ctx context.Context) ([]string, error)
	TagAdd(ctx context.Context, names []string) error
	TagDropUnused(ctx context.Context, names []string) ([]string, error)
	TagRecount(ctx context.Context) (added, dropped []string, done bool, err error)
	Ping(ctx context.Context) error
}
