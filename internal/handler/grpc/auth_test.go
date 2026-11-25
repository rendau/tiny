package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	sessionModel "github.com/rendau/tiny/internal/domain/session/model"
	sessionService "github.com/rendau/tiny/internal/domain/session/service"
	"github.com/rendau/tiny/internal/errs"
)

func TestAuthInterceptor(t *testing.T) {
	sessions := sessionService.New("test-secret")
	interceptor := NewAuthInterceptor(sessions)

	access, err := sessions.CreateToken(7, true)
	require.NoError(t, err)
	refresh, err := sessions.CreateRefreshToken(7, "hash")
	require.NoError(t, err)
	foreign, err := sessionService.New("other-secret").CreateToken(7, true)
	require.NoError(t, err)

	// run — сессия, которую увидит обработчик при таком заголовке authorization
	run := func(header ...string) *sessionModel.Session {
		ctx := context.Background()
		if len(header) > 0 {
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", header[0]))
		}
		var seen *sessionModel.Session
		_, err := interceptor(ctx, nil, nil, func(ctx context.Context, _ any) (any, error) {
			seen = sessions.FromContext(ctx)
			return nil, nil
		})
		require.NoError(t, err, "негодный токен — не ошибка запроса")
		return seen
	}

	for name, header := range map[string]string{
		"Bearer":         "Bearer " + access,
		"bearer":         "bearer " + access,
		"без слова":      access,
		"лишние пробелы": "  Bearer   " + access + " ",
	} {
		session := run(header)
		assert.Equal(t, int64(7), session.Id, name)
		assert.True(t, session.IsAdmin(), name)
	}

	assert.False(t, run().IsAuthorized(), "заголовка нет")
	for name, header := range map[string]string{
		"пусто":         "",
		"мусор":         "Bearer not-a-token",
		"чужая подпись": "Bearer " + foreign,
		"долгий токен":  "Bearer " + refresh,
		"другая схема":  "Basic " + access,
		"три слова":     "Bearer " + access + " extra",
	} {
		assert.False(t, run(header).IsAuthorized(), name)
	}
}

func TestAuthGrpcCode(t *testing.T) {
	assert.Equal(t, codes.Unauthenticated, AuthGrpcCode(errs.NotAuthorized.Error()))
	assert.Equal(t, codes.PermissionDenied, AuthGrpcCode(errs.NoPermission.Error()))
	assert.Equal(t, codes.InvalidArgument, AuthGrpcCode(errs.InvalidRequest.Error()))
	assert.Equal(t, codes.InvalidArgument, AuthGrpcCode(errs.ServiceNA.Error()))
}

func TestUsrClientIp(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-forwarded-for", "203.0.113.7, 10.0.0.1"))
	assert.Equal(t, "203.0.113.7", usrClientIp(ctx), "первый адрес из x-forwarded-for")

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-forwarded-for", "not-an-ip"))
	assert.Empty(t, usrClientIp(ctx), "мусор в заголовке и нет соединения — адрес неизвестен")

	assert.Empty(t, usrClientIp(context.Background()))
}
