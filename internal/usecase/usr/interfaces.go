package usr

import (
	"context"

	sessionModel "github.com/rendau/tiny/internal/domain/session/model"
	"github.com/rendau/tiny/internal/domain/usr/model"
)

type ServiceI interface {
	List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error)
	Get(ctx context.Context, id int64, errNE bool) (*model.Main, bool, error)
	AuthByUsernamePassword(ctx context.Context, username, password string) (*model.Main, bool, error)
	HasAny(ctx context.Context) (bool, error)
	Create(ctx context.Context, obj *model.Edit) (int64, error)
	Update(ctx context.Context, id int64, obj *model.Edit) error
	Delete(ctx context.Context, id int64) error
}

type SessionServiceI interface {
	FromContext(ctx context.Context) *sessionModel.Session
	CtxIsAuthorized(ctx context.Context) bool
	CtxRequireAdmin(ctx context.Context) error
	CreateToken(usrId int64, isAdmin bool) (string, error)
	CreateRefreshToken(usrId int64, passwordHash string) (string, error)
	RefreshTokenUserId(tokenStr string) (int64, error)
	CheckRefreshToken(tokenStr, currentPasswordHash string) error
}

// LoginLimiterI — счёт неудачных входов (internal/service/loginlimit). limits — предел по каждому
// ключу; ok=false — хотя бы один исчерпан. release возвращает занятое место: его зовут, если вход
// удался или попытки не было.
type LoginLimiterI interface {
	Reserve(limits map[string]int) (release func(), ok bool)
}
