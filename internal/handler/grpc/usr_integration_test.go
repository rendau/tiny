//go:build integration

package grpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	domainSessionServiceP "github.com/rendau/tiny/internal/domain/session/service"
	domainUsrRepoDbP "github.com/rendau/tiny/internal/domain/usr/repo/db"
	domainUsrServiceP "github.com/rendau/tiny/internal/domain/usr/service"
	"github.com/rendau/tiny/internal/domain/usr/usrtest"
	"github.com/rendau/tiny/internal/errs"
	handlerGrpcP "github.com/rendau/tiny/internal/handler/grpc"
	serviceLoginlimitServiceP "github.com/rendau/tiny/internal/service/loginlimit/service"
	usecaseUsrP "github.com/rendau/tiny/internal/usecase/usr"
	"github.com/rendau/tiny/pkg/proto/common"
	proto "github.com/rendau/tiny/pkg/proto/tiny_v1"
)

// usrClient — настоящий gRPC-сервер в памяти (с перехватчиком входа) поверх живой базы стенда,
// собранный так же, как в app.go: repo → service → usecase → handler
func usrClient(t *testing.T) proto.UsrServiceClient {
	t.Helper()

	sessionService := domainSessionServiceP.New("test-secret")
	usrRepo := domainUsrRepoDbP.New(usrtest.PgPool(t))
	usrService := domainUsrServiceP.New(usrRepo)
	usrUsecase := usecaseUsrP.New(usrService, sessionService, serviceLoginlimitServiceP.New(5, time.Minute))
	usrHandler := handlerGrpcP.NewUsr(usrUsecase)

	server := grpc.NewServer(grpc.ChainUnaryInterceptor(handlerGrpcP.NewAuthInterceptor(sessionService)))
	proto.RegisterUsrServiceServer(server, usrHandler)

	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return proto.NewUsrServiceClient(conn)
}

// withToken — запрос с заголовком входа, как его шлёт интерфейс
func withToken(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

// assertCode — запрос отклонён с этим кодом ошибки сервиса
func assertCode(t *testing.T, err error, code errs.Err, msg string) {
	t.Helper()
	if assert.Error(t, err, msg) {
		assert.Contains(t, status.Convert(err).Message(), code.Error(), msg)
	}
}

// TestUsrFullPath — первый админ → вход → токен → профиль → люди → смена пароля, на живой базе
func TestUsrFullPath(t *testing.T) {
	client := usrClient(t)
	anon := context.Background()

	// людей нет: первый создаётся без входа и становится админом
	status1, err := client.BootstrapStatus(anon, &emptypb.Empty{})
	require.NoError(t, err)
	assert.True(t, status1.GetBootstrapRequired())

	created, err := client.Create(anon, &proto.UsrCreateReq{Username: new("Admin"), Password: new("password-1"), Name: new("Админ")})
	require.NoError(t, err)
	adminId := created.GetId()
	assert.Positive(t, adminId)

	status1, err = client.BootstrapStatus(anon, &emptypb.Empty{})
	require.NoError(t, err)
	assert.False(t, status1.GetBootstrapRequired())

	_, err = client.Create(anon, &proto.UsrCreateReq{Username: new("intruder"), Password: new("password-1")})
	assertCode(t, err, errs.NotAuthorized, "второго без входа не создать")

	// вход
	_, err = client.Login(anon, &proto.UsrLoginReq{Username: "admin", Password: "wrong-pass"})
	assertCode(t, err, errs.NotAuthorized, "неверный пароль")

	adminPair, err := client.Login(anon, &proto.UsrLoginReq{Username: " ADMIN ", Password: "password-1"})
	require.NoError(t, err)
	require.NotEmpty(t, adminPair.GetJwt())
	require.NotEmpty(t, adminPair.GetRefreshToken())
	admin := withToken(adminPair.GetJwt())

	// профиль: только с коротким токеном
	_, err = client.GetProfile(anon, &emptypb.Empty{})
	assertCode(t, err, errs.NotAuthorized, "без токена")
	_, err = client.GetProfile(withToken(adminPair.GetRefreshToken()), &emptypb.Empty{})
	assertCode(t, err, errs.NotAuthorized, "долгий токен вместо короткого")
	_, err = client.GetProfile(withToken("not-a-token"), &emptypb.Empty{})
	assertCode(t, err, errs.NotAuthorized, "мусор вместо токена")

	profile, err := client.GetProfile(admin, &emptypb.Empty{})
	require.NoError(t, err)
	assert.Equal(t, adminId, profile.GetId())
	assert.Equal(t, "admin", profile.GetUsername())
	assert.Equal(t, "Админ", profile.GetName())
	assert.True(t, profile.GetIsAdmin())
	assert.True(t, profile.GetActive())
	assert.WithinDuration(t, time.Now(), profile.GetCreatedAt().AsTime(), time.Minute)

	// админ заводит человека
	created, err = client.Create(admin, &proto.UsrCreateReq{Username: new("oleg"), Password: new("temp-pass-1"), Name: new("Олег")})
	require.NoError(t, err)
	olegId := created.GetId()

	_, err = client.Create(admin, &proto.UsrCreateReq{Username: new("OLEG"), Password: new("temp-pass-1")})
	assertCode(t, err, errs.UsrUsernameExists, "логин занят")
	_, err = client.Create(admin, &proto.UsrCreateReq{Username: new("dana"), Password: new("short")})
	assertCode(t, err, errs.InvalidRequest, "короткий пароль")

	list, err := client.List(admin, &proto.UsrListReq{ListParams: &common.ListParamsSt{PageSize: 10, WithTotalCount: true}})
	require.NoError(t, err)
	assert.Equal(t, int64(2), list.GetPaginationInfo().GetTotalCount())
	assert.Equal(t, []string{"admin", "oleg"}, lo.Map(list.GetResults(), func(v *proto.UsrMain, _ int) string { return v.GetUsername() }))

	got, err := client.Get(admin, &proto.UsrGetReq{Id: olegId})
	require.NoError(t, err)
	assert.Equal(t, "Олег", got.GetName())
	assert.False(t, got.GetIsAdmin())

	// обычный человек: свой профиль — да, чужие люди — нет
	olegPair, err := client.Login(anon, &proto.UsrLoginReq{Username: "oleg", Password: "temp-pass-1"})
	require.NoError(t, err)
	oleg := withToken(olegPair.GetJwt())

	profile, err = client.GetProfile(oleg, &emptypb.Empty{})
	require.NoError(t, err)
	assert.Equal(t, olegId, profile.GetId())
	assert.False(t, profile.GetIsAdmin())

	_, err = client.List(oleg, &proto.UsrListReq{ListParams: &common.ListParamsSt{PageSize: 10}})
	assertCode(t, err, errs.NoPermission, "список людей — только админу")
	_, err = client.Update(oleg, &proto.UsrUpdateReq{Id: olegId, IsAdmin: new(true)})
	assertCode(t, err, errs.NoPermission, "сам себе права не выдаст")
	_, err = client.Delete(oleg, &proto.UsrGetReq{Id: adminId})
	assertCode(t, err, errs.NoPermission, "админа не удалит")

	// смена своего пароля: нужен текущий; старые долгие токены отзываются
	_, err = client.UpdateProfile(oleg, &proto.UsrUpdateProfileReq{Password: new("new-pass-1"), CurrentPassword: new("wrong-pass")})
	assertCode(t, err, errs.InvalidRequest, "неверный текущий пароль")

	refreshed, err := client.RefreshToken(anon, &proto.UsrRefreshTokenReq{RefreshToken: olegPair.GetRefreshToken()})
	require.NoError(t, err, "пока пароль прежний — долгий токен годится")
	require.NotEmpty(t, refreshed.GetJwt())

	_, err = client.UpdateProfile(oleg, &proto.UsrUpdateProfileReq{Name: new("Олег О."), Password: new("new-pass-1"), CurrentPassword: new("temp-pass-1")})
	require.NoError(t, err)

	_, err = client.RefreshToken(anon, &proto.UsrRefreshTokenReq{RefreshToken: olegPair.GetRefreshToken()})
	assertCode(t, err, errs.NotAuthorized, "после смены пароля старый долгий токен отозван")
	_, err = client.RefreshToken(anon, &proto.UsrRefreshTokenReq{RefreshToken: refreshed.GetRefreshToken()})
	assertCode(t, err, errs.NotAuthorized, "и выданный по нему — тоже")
	_, err = client.Login(anon, &proto.UsrLoginReq{Username: "oleg", Password: "temp-pass-1"})
	assertCode(t, err, errs.NotAuthorized, "старый пароль не подходит")

	olegPair, err = client.Login(anon, &proto.UsrLoginReq{Username: "oleg", Password: "new-pass-1"})
	require.NoError(t, err)
	profile, err = client.GetProfile(withToken(olegPair.GetJwt()), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Equal(t, "Олег О.", profile.GetName())
	assert.True(t, profile.GetModifiedAt().AsTime().After(profile.GetCreatedAt().AsTime()), "modified_at обновился")

	// админ: себя не тронуть, другого — можно
	_, err = client.Update(admin, &proto.UsrUpdateReq{Id: adminId, IsAdmin: new(false)})
	assertCode(t, err, errs.UsrSelfChange, "себя прав не лишить")
	_, err = client.Update(admin, &proto.UsrUpdateReq{Id: adminId, Active: new(false)})
	assertCode(t, err, errs.UsrSelfChange, "себя не отключить")
	_, err = client.Delete(admin, &proto.UsrGetReq{Id: adminId})
	assertCode(t, err, errs.UsrSelfChange, "себя не удалить")

	// отключённый человек не входит и не обновляет токен
	_, err = client.Update(admin, &proto.UsrUpdateReq{Id: olegId, Active: new(false)})
	require.NoError(t, err)
	_, err = client.Login(anon, &proto.UsrLoginReq{Username: "oleg", Password: "new-pass-1"})
	assertCode(t, err, errs.NotAuthorized, "отключён — входа нет")
	_, err = client.RefreshToken(anon, &proto.UsrRefreshTokenReq{RefreshToken: olegPair.GetRefreshToken()})
	assertCode(t, err, errs.NotAuthorized, "отключён — обновления нет")

	list, err = client.List(admin, &proto.UsrListReq{ListParams: &common.ListParamsSt{PageSize: 10}, Active: new(true)})
	require.NoError(t, err)
	assert.Len(t, list.GetResults(), 1)

	_, err = client.Delete(admin, &proto.UsrGetReq{Id: olegId})
	require.NoError(t, err)
	_, err = client.Get(admin, &proto.UsrGetReq{Id: olegId})
	assertCode(t, err, errs.ObjectNotFound, "удалён")
}

// TestUsrLoginRateLimit — после 5 неудачных входов логин закрыт на минуту даже с верным паролем
func TestUsrLoginRateLimit(t *testing.T) {
	client := usrClient(t)
	anon := context.Background()

	_, err := client.Create(anon, &proto.UsrCreateReq{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)

	for range 5 {
		_, err = client.Login(anon, &proto.UsrLoginReq{Username: "admin", Password: "wrong-pass"})
		assertCode(t, err, errs.NotAuthorized, "неверный пароль")
	}
	_, err = client.Login(anon, &proto.UsrLoginReq{Username: "admin", Password: "password-1"})
	assertCode(t, err, errs.LoginRateLimited, "слишком много попыток")
}
