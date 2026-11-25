package grpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	sessionModel "github.com/rendau/tiny/internal/domain/session/model"
	"github.com/rendau/tiny/internal/errs"
)

// AuthSessionServiceI — что перехватчику входа нужно от internal/domain/session/service
type AuthSessionServiceI interface {
	FromToken(tokenStr string) (*sessionModel.Session, error)
	WithContext(ctx context.Context, session *sessionModel.Session) context.Context
}

// NewAuthInterceptor кладёт в контекст сессию из `authorization: Bearer <jwt>`. Токена нет или он
// негодный (чужая подпись, истёк, это refresh) — пустая сессия, запрос идёт дальше: пускать или
// нет, решает usecase (errs.NotAuthorized / errs.NoPermission). В БД перехватчик не ходит.
//
// В цепочке gRPC-сервера ставится последним — после перехватчика ошибок.
func NewAuthInterceptor(sessionSvc AuthSessionServiceI) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		var session *sessionModel.Session

		if token := authBearerToken(ctx); token != "" {
			// негодный токен — не сбой: дальше пойдёт пустая сессия
			if s, err := sessionSvc.FromToken(token); err == nil && s.IsAuthorized() {
				session = s
			}
		}

		return handler(sessionSvc.WithContext(ctx, session), req)
	}
}

// AuthGrpcCode — gRPC-код ответа по коду ошибки сервиса: «нужен вход» и «нет прав» отличаются от
// прочих отказов, чтобы HTTP-шлюз отдал 401 и 403 (по 401 интерфейс обновляет токен и повторяет
// запрос). Нужен перехватчику ошибок gRPC-сервера.
func AuthGrpcCode(code string) codes.Code {
	switch errs.Err(code) {
	case errs.NotAuthorized:
		return codes.Unauthenticated
	case errs.NoPermission:
		return codes.PermissionDenied
	}
	return codes.InvalidArgument
}

// authBearerToken — токен из заголовка authorization: `Bearer <токен>` или просто `<токен>`
func authBearerToken(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	parts := strings.Fields(values[0])
	switch {
	case len(parts) == 1:
		return parts[0]
	case len(parts) == 2 && strings.EqualFold(parts[0], "bearer"):
		return parts[1]
	}
	return ""
}
