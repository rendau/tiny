package link

import (
	"context"

	"github.com/rendau/tiny/internal/domain/link/model"
	sessionModel "github.com/rendau/tiny/internal/domain/session/model"
)

type ServiceI interface {
	List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error)
	Get(ctx context.Context, id int64, errNE bool) (*model.Main, bool, error)
	Create(ctx context.Context, obj *model.Edit, codeLength int) (int64, error)
	Update(ctx context.Context, id int64, obj *model.Edit) error
	Delete(ctx context.Context, id int64) error

	GetByCode(ctx context.Context, shortCode, domain string, errNE bool) (*model.Main, bool, error)
	FindForRedirect(ctx context.Context, shortCode, domain string) (*model.Main, bool, error)
	FindExisting(ctx context.Context, pars *model.FindExistingReq) (*model.Main, bool, error)
	TagList(ctx context.Context) ([]string, error)
	CrawlableCodes(ctx context.Context) ([]string, error)
	Ping(ctx context.Context) error
}

type SessionServiceI interface {
	FromContext(ctx context.Context) *sessionModel.Session
}
