package usr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sessionModel "github.com/rendau/tiny/internal/domain/session/model"
	sessionService "github.com/rendau/tiny/internal/domain/session/service"
	"github.com/rendau/tiny/internal/domain/usr/model"
	usrService "github.com/rendau/tiny/internal/domain/usr/service"
	"github.com/rendau/tiny/internal/errs"
	loginlimitService "github.com/rendau/tiny/internal/service/loginlimit/service"
)

// repoFake — хранилище людей в памяти (domain service настоящий: хеширование и проверка пароля)
type repoFake struct {
	items  map[int64]*model.Main
	nextId int64
}

func (r *repoFake) List(context.Context, *model.ListReq) ([]*model.Main, int64, error) {
	result := make([]*model.Main, 0, len(r.items))
	for _, v := range r.items {
		result = append(result, v)
	}
	return result, int64(len(result)), nil
}

func (r *repoFake) Get(_ context.Context, id int64) (*model.Main, bool, error) {
	v, ok := r.items[id]
	if !ok {
		return nil, false, nil
	}
	return new(*v), true, nil
}

func (r *repoFake) GetByUsername(_ context.Context, username string) (*model.Main, bool, error) {
	for _, v := range r.items {
		if v.Username == username {
			return new(*v), true, nil
		}
	}
	return nil, false, nil
}

func (r *repoFake) HasAny(context.Context) (bool, error) { return len(r.items) > 0, nil }

func (r *repoFake) Create(_ context.Context, obj *model.Edit) (int64, error) {
	for _, v := range r.items {
		if obj.Username != nil && v.Username == *obj.Username {
			return 0, errs.UsrUsernameExists
		}
	}
	r.nextId++
	v := &model.Main{Id: r.nextId, Active: true}
	apply(v, obj)
	r.items[v.Id] = v
	return v.Id, nil
}

func (r *repoFake) Update(_ context.Context, id int64, obj *model.Edit) error {
	if v, ok := r.items[id]; ok {
		apply(v, obj)
	}
	return nil
}

func (r *repoFake) Delete(_ context.Context, id int64) error {
	delete(r.items, id)
	return nil
}

func apply(v *model.Main, obj *model.Edit) {
	if obj.Active != nil {
		v.Active = *obj.Active
	}
	if obj.IsAdmin != nil {
		v.IsAdmin = *obj.IsAdmin
	}
	if obj.Name != nil {
		v.Name = *obj.Name
	}
	if obj.Username != nil {
		v.Username = *obj.Username
	}
	if obj.Password != nil {
		v.Password = *obj.Password
	}
}

type env struct {
	uc       *Usecase
	sessions *sessionService.Service
	repo     *repoFake
}

func newEnv() *env {
	sessions := sessionService.New("test-secret")
	repo := &repoFake{items: map[int64]*model.Main{}}
	return &env{
		uc:       New(usrService.New(repo), sessions, loginlimitService.New(time.Minute)),
		sessions: sessions,
		repo:     repo,
	}
}

func (e *env) as(id int64, admin bool) context.Context {
	return e.sessions.WithContext(context.Background(), &sessionModel.Session{Id: id, Admin: admin})
}

// admins — сколько действующих админов осталось
func (e *env) admins() int {
	n := 0
	for _, v := range e.repo.items {
		if v.Active && v.IsAdmin {
			n++
		}
	}
	return n
}

func TestBootstrapFirstAdmin(t *testing.T) {
	e := newEnv()
	ctx := context.Background()

	required, err := e.uc.BootstrapStatus(ctx)
	require.NoError(t, err)
	assert.True(t, required)

	// первый человек: что бы ни прислали, он действующий админ
	id, err := e.uc.Create(ctx, &model.Edit{Username: new(" Admin "), Password: new("password-1"), IsAdmin: new(false), Active: new(false)})
	require.NoError(t, err)

	admin, err := e.uc.Get(e.as(id, true), id)
	require.NoError(t, err)
	assert.True(t, admin.IsAdmin, "первый человек — всегда админ")
	assert.True(t, admin.Active, "и всегда действующий")
	assert.Equal(t, "admin", admin.Username, "логин приводится к нижнему регистру")
	assert.NotEqual(t, "password-1", admin.Password, "хранится хеш, а не пароль")

	required, err = e.uc.BootstrapStatus(ctx)
	require.NoError(t, err)
	assert.False(t, required)

	_, err = e.uc.Create(ctx, &model.Edit{Username: new("intruder"), Password: new("password-1")})
	assert.ErrorIs(t, err, errs.NotAuthorized, "без входа второго не создать")
}

func TestLoginAndRefresh(t *testing.T) {
	e := newEnv()
	adminId, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)
	adminCtx := e.as(adminId, true)
	olegId, err := e.uc.Create(adminCtx, &model.Edit{Username: new("oleg"), Password: new("temp-pass-1"), Active: new(true)})
	require.NoError(t, err)

	pair, err := e.uc.Login(context.Background(), "OLEG", "temp-pass-1", "10.0.0.1")
	require.NoError(t, err)
	session, err := e.sessions.FromToken(pair.Jwt)
	require.NoError(t, err)
	assert.Equal(t, olegId, session.Id)
	assert.False(t, session.IsAdmin())

	_, err = e.sessions.FromToken(pair.RefreshToken)
	assert.Error(t, err, "долгий токен не годится вместо короткого")

	_, err = e.uc.Login(context.Background(), "oleg", "wrong", "10.0.0.1")
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, err = e.uc.Login(context.Background(), "nobody", "temp-pass-1", "10.0.0.1")
	assert.ErrorIs(t, err, errs.NotAuthorized, "нет такого логина — тот же отказ, что и при неверном пароле")

	next, err := e.uc.RefreshToken(context.Background(), pair.RefreshToken)
	require.NoError(t, err)
	assert.NotEmpty(t, next.Jwt)
	assert.NotEmpty(t, next.RefreshToken)

	// роль поменяли — новая пара несёт новую роль
	require.NoError(t, e.uc.Update(adminCtx, olegId, &model.Edit{IsAdmin: new(true)}))
	next, err = e.uc.RefreshToken(context.Background(), pair.RefreshToken)
	require.NoError(t, err)
	session, err = e.sessions.FromToken(next.Jwt)
	require.NoError(t, err)
	assert.True(t, session.IsAdmin())

	// пароль сменили — старые refresh-токены отозваны
	require.NoError(t, e.uc.UpdateProfile(e.as(olegId, false), &UpdateProfileReq{Password: new("new-pass-1"), CurrentPassword: new("temp-pass-1")}))
	_, err = e.uc.RefreshToken(context.Background(), pair.RefreshToken)
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, err = e.uc.Login(context.Background(), "oleg", "temp-pass-1", "10.0.0.3")
	assert.ErrorIs(t, err, errs.NotAuthorized, "старый пароль больше не подходит")

	// пароль сменил админ — тоже отзывает
	pair, err = e.uc.Login(context.Background(), "oleg", "new-pass-1", "10.0.0.1")
	require.NoError(t, err)
	require.NoError(t, e.uc.Update(adminCtx, olegId, &model.Edit{Password: new("by-admin-1")}))
	_, err = e.uc.RefreshToken(context.Background(), pair.RefreshToken)
	assert.ErrorIs(t, err, errs.NotAuthorized)

	// отключили — ни входа, ни обновления
	pair, err = e.uc.Login(context.Background(), "oleg", "by-admin-1", "10.0.0.1")
	require.NoError(t, err)
	require.NoError(t, e.uc.Update(adminCtx, olegId, &model.Edit{Active: new(false)}))
	_, err = e.uc.RefreshToken(context.Background(), pair.RefreshToken)
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, err = e.uc.Login(context.Background(), "oleg", "by-admin-1", "10.0.0.2")
	assert.ErrorIs(t, err, errs.NotAuthorized)

	// удалили — обновления тоже нет
	require.NoError(t, e.uc.Delete(adminCtx, olegId))
	_, err = e.uc.RefreshToken(context.Background(), pair.RefreshToken)
	assert.ErrorIs(t, err, errs.NotAuthorized)

	_, err = e.uc.RefreshToken(context.Background(), next.Jwt)
	assert.ErrorIs(t, err, errs.NotAuthorized, "access-токен — не refresh")
	_, err = e.uc.RefreshToken(context.Background(), "  ")
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, err = e.uc.RefreshToken(context.Background(), "not-a-token")
	assert.ErrorIs(t, err, errs.NotAuthorized)
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv()
	_, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)

	for range loginMaxPerUsrIp {
		_, err = e.uc.Login(context.Background(), "admin", "wrong", "10.0.0.1")
		assert.ErrorIs(t, err, errs.NotAuthorized)
	}
	_, err = e.uc.Login(context.Background(), "admin", "password-1", "10.0.0.1")
	assert.ErrorIs(t, err, errs.LoginRateLimited, "с этого адреса логин закрыт на минуту даже с верным паролем")

	_, err = e.uc.Login(context.Background(), "admin", "password-1", "10.0.0.9")
	assert.NoError(t, err, "чужие неудачи с другого адреса не мешают войти со своего")

	_, err = e.uc.Login(context.Background(), "other", "wrong", "10.0.0.1")
	assert.ErrorIs(t, err, errs.NotAuthorized, "другой логин с того же адреса ещё можно пробовать")
}

// Одновременные запросы не обходят предел: место под попытку занимается до проверки пароля
func TestLoginRateLimitParallel(t *testing.T) {
	e := newEnv()
	_, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)

	const requests = 40

	var (
		wg               sync.WaitGroup
		rejected, limits atomic.Int32
	)
	for range requests {
		wg.Go(func() {
			_, err := e.uc.Login(context.Background(), "admin", "wrong", "10.0.0.1")
			switch {
			case errors.Is(err, errs.NotAuthorized):
				rejected.Add(1)
			case errors.Is(err, errs.LoginRateLimited):
				limits.Add(1)
			}
		})
	}
	wg.Wait()

	assert.EqualValues(t, loginMaxPerUsrIp, rejected.Load(), "пароль проверен ровно столько раз, сколько разрешено")
	assert.EqualValues(t, requests-loginMaxPerUsrIp, limits.Load())
}

// Удачный вход места в счёте не занимает
func TestLoginRateLimitSuccessNotCounted(t *testing.T) {
	e := newEnv()
	_, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)

	for range loginMaxPerUsrIp + 2 {
		_, err = e.uc.Login(context.Background(), "admin", "password-1", "10.0.0.1")
		require.NoError(t, err)
	}
}

// С одного адреса по разным логинам — свой предел (перебор логинов)
func TestLoginRateLimitPerIp(t *testing.T) {
	e := newEnv()

	for i := range loginMaxPerIp {
		// по одному логину с адреса — не больше loginMaxPerUsrIp, поэтому логины меняются
		_, err := e.uc.Login(context.Background(), fmt.Sprintf("user-%d", i/loginMaxPerUsrIp), "wrong", "10.0.0.1")
		require.ErrorIs(t, err, errs.NotAuthorized)
	}

	_, err := e.uc.Login(context.Background(), "fresh-user", "wrong", "10.0.0.1")
	assert.ErrorIs(t, err, errs.LoginRateLimited, "адрес исчерпал свой предел")

	_, err = e.uc.Login(context.Background(), "fresh-user", "wrong", "10.0.0.2")
	assert.ErrorIs(t, err, errs.NotAuthorized, "другой адрес не затронут")
}

// По одному логину со многих адресов — общий предел: подбор с разных адресов тоже упирается
func TestLoginRateLimitPerUsr(t *testing.T) {
	e := newEnv()

	for i := range loginMaxPerUsr {
		// с одного адреса по логину — не больше loginMaxPerUsrIp, поэтому адреса меняются
		_, err := e.uc.Login(context.Background(), "admin", "wrong", fmt.Sprintf("10.0.1.%d", i/loginMaxPerUsrIp))
		require.ErrorIs(t, err, errs.NotAuthorized)
	}

	_, err := e.uc.Login(context.Background(), "admin", "wrong", "10.0.2.1")
	assert.ErrorIs(t, err, errs.LoginRateLimited, "логин закрыт для всех адресов")

	_, err = e.uc.Login(context.Background(), "other", "wrong", "10.0.2.1")
	assert.ErrorIs(t, err, errs.NotAuthorized, "другой логин не затронут")
}

// Логин, которого в базе быть не может, отклоняется сразу и в счётчики не попадает
func TestLoginBadUsernameNotCounted(t *testing.T) {
	e := newEnv()
	_, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)

	for _, username := range []string{"", "ab", "with space", "кириллица", strings.Repeat("a", 65)} {
		for range loginMaxPerIp + 1 {
			_, err = e.uc.Login(context.Background(), username, "wrong", "10.0.0.1")
			require.ErrorIs(t, err, errs.NotAuthorized, "логин %q", username)
		}
	}

	_, err = e.uc.Login(context.Background(), "admin", "password-1", "10.0.0.1")
	assert.NoError(t, err, "адрес не исчерпал предел")
}

func TestPermissions(t *testing.T) {
	e := newEnv()
	adminId, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)
	adminCtx := e.as(adminId, true)
	memberId, err := e.uc.Create(adminCtx, &model.Edit{Username: new("member"), Password: new("password-1")})
	require.NoError(t, err)
	memberCtx := e.as(memberId, false)

	member, err := e.uc.Get(adminCtx, memberId)
	require.NoError(t, err)
	assert.False(t, member.IsAdmin, "созданный админом человек — не админ, пока это не указано явно")

	_, err = e.uc.Create(memberCtx, &model.Edit{Username: new("xyz"), Password: new("password-1")})
	assert.ErrorIs(t, err, errs.NoPermission)
	assert.ErrorIs(t, e.uc.Update(memberCtx, adminId, &model.Edit{Name: new("x")}), errs.NoPermission)
	assert.ErrorIs(t, e.uc.Update(memberCtx, memberId, &model.Edit{IsAdmin: new(true)}), errs.NoPermission, "сам себе права не выдаст")
	assert.ErrorIs(t, e.uc.Delete(memberCtx, adminId), errs.NoPermission)
	// без входа (в том числе с истёкшим токеном) — 401, а не 403: по 401 интерфейс обновляет токен
	assert.ErrorIs(t, e.uc.Update(context.Background(), memberId, &model.Edit{Name: new("x")}), errs.NotAuthorized)
	assert.ErrorIs(t, e.uc.Delete(context.Background(), memberId), errs.NotAuthorized)
	_, err = e.uc.Get(context.Background(), adminId)
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, err = e.uc.GetProfile(context.Background())
	assert.ErrorIs(t, err, errs.NotAuthorized)
	assert.ErrorIs(t, e.uc.UpdateProfile(context.Background(), &UpdateProfileReq{Name: new("x")}), errs.NotAuthorized)

	_, _, err = e.uc.List(context.Background(), &model.ListReq{})
	assert.ErrorIs(t, err, errs.NotAuthorized)
	_, _, err = e.uc.List(memberCtx, &model.ListReq{})
	assert.ErrorIs(t, err, errs.NoPermission, "список людей — только админу")
	_, _, err = e.uc.List(adminCtx, &model.ListReq{})
	assert.ErrorIs(t, err, errs.IncorrectPageSize, "размер страницы обязателен")
	_, err = e.uc.Get(memberCtx, adminId)
	assert.ErrorIs(t, err, errs.NoPermission)
	_, err = e.uc.GetProfile(memberCtx)
	assert.NoError(t, err, "свой профиль — можно")

	_, err = e.uc.Get(adminCtx, 999)
	assert.ErrorIs(t, err, errs.ObjectNotFound)
	assert.ErrorIs(t, e.uc.Update(adminCtx, 0, &model.Edit{Name: new("x")}), errs.IdRequired)
	assert.ErrorIs(t, e.uc.Delete(adminCtx, 0), errs.IdRequired)

	_, err = e.uc.Create(adminCtx, &model.Edit{Username: new("Member"), Password: new("password-1")})
	assert.ErrorIs(t, err, errs.UsrUsernameExists, "логин занят (регистр не важен)")
}

// TestSelfAndLastAdmin — себя нельзя удалить, отключить и лишить прав; поэтому без админов
// остаться нельзя: единственный админ — всегда тот, кто выполняет действие
func TestSelfAndLastAdmin(t *testing.T) {
	e := newEnv()
	adminId, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1")})
	require.NoError(t, err)
	adminCtx := e.as(adminId, true)

	assert.ErrorIs(t, e.uc.Update(adminCtx, adminId, &model.Edit{IsAdmin: new(false)}), errs.UsrSelfChange)
	assert.ErrorIs(t, e.uc.Update(adminCtx, adminId, &model.Edit{Active: new(false)}), errs.UsrSelfChange)
	assert.ErrorIs(t, e.uc.Delete(adminCtx, adminId), errs.UsrSelfChange)
	assert.Equal(t, 1, e.admins(), "последний админ на месте")

	// остальное про себя менять можно, в том числе подтвердить уже имеющиеся флаги
	require.NoError(t, e.uc.Update(adminCtx, adminId, &model.Edit{Name: new("Главный"), IsAdmin: new(true), Active: new(true)}))

	// второй админ может снять первого — но тогда админом остаётся он сам
	secondId, err := e.uc.Create(adminCtx, &model.Edit{Username: new("second"), Password: new("password-1"), IsAdmin: new(true)})
	require.NoError(t, err)
	secondCtx := e.as(secondId, true)
	require.NoError(t, e.uc.Update(secondCtx, adminId, &model.Edit{IsAdmin: new(false)}))
	assert.Equal(t, 1, e.admins())
	assert.ErrorIs(t, e.uc.Delete(secondCtx, secondId), errs.UsrSelfChange)
	require.NoError(t, e.uc.Delete(secondCtx, adminId))
	assert.Equal(t, 1, e.admins())
}

func TestProfile(t *testing.T) {
	e := newEnv()
	adminId, err := e.uc.Create(context.Background(), &model.Edit{Username: new("admin"), Password: new("password-1"), Name: new("Админ")})
	require.NoError(t, err)
	adminCtx := e.as(adminId, true)

	profile, err := e.uc.GetProfile(adminCtx)
	require.NoError(t, err)
	assert.Equal(t, "admin", profile.Username)
	assert.Equal(t, "Админ", profile.Name)

	// имя и логин — без текущего пароля
	require.NoError(t, e.uc.UpdateProfile(adminCtx, &UpdateProfileReq{Name: new("Новое имя"), Username: new(" Root ")}))
	profile, err = e.uc.GetProfile(adminCtx)
	require.NoError(t, err)
	assert.Equal(t, "Новое имя", profile.Name)
	assert.Equal(t, "root", profile.Username)
	assert.True(t, profile.IsAdmin, "профиль не меняет права")

	// пароль — только с верным текущим
	for name, current := range map[string]*string{"неверный": new("wrong-current"), "не передан": nil} {
		err = e.uc.UpdateProfile(adminCtx, &UpdateProfileReq{Password: new("new-pass-1"), CurrentPassword: current})
		full, ok := errs.AsErrFull(err)
		require.True(t, ok, name)
		assert.ErrorIs(t, full.Err, errs.InvalidRequest, name)
		assert.Equal(t, "invalid", full.Fields["current_password"], name)
	}
	_, err = e.uc.Login(context.Background(), "root", "password-1", "10.0.0.1")
	require.NoError(t, err, "пароль не изменился")

	require.NoError(t, e.uc.UpdateProfile(adminCtx, &UpdateProfileReq{Password: new("new-pass-1"), CurrentPassword: new("password-1")}))
	_, err = e.uc.Login(context.Background(), "root", "new-pass-1", "10.0.0.1")
	require.NoError(t, err)

	// новый пароль проверяется теми же правилами
	err = e.uc.UpdateProfile(adminCtx, &UpdateProfileReq{Password: new("short"), CurrentPassword: new("new-pass-1")})
	full, ok := errs.AsErrFull(err)
	require.True(t, ok)
	assert.Equal(t, "invalid", full.Fields["password"])
}

func TestValidation(t *testing.T) {
	e := newEnv()
	cases := map[string]struct {
		obj   *model.Edit
		field string
	}{
		"короткий пароль":      {&model.Edit{Username: new("admin"), Password: new("short")}, "password"},
		"пароль из пробелов":   {&model.Edit{Username: new("admin"), Password: new("            ")}, "password"},
		"длинный пароль":       {&model.Edit{Username: new("admin"), Password: new(strings.Repeat("x", 73))}, "password"},
		"без пароля":           {&model.Edit{Username: new("admin")}, "password"},
		"без логина":           {&model.Edit{Password: new("password-1")}, "username"},
		"плохой логин":         {&model.Edit{Username: new("a b"), Password: new("password-1")}, "username"},
		"короткий логин":       {&model.Edit{Username: new("ab"), Password: new("password-1")}, "username"},
		"логин с точки":        {&model.Edit{Username: new(".admin"), Password: new("password-1")}, "username"},
		"длинный логин":        {&model.Edit{Username: new(strings.Repeat("a", 65)), Password: new("password-1")}, "username"},
		"логин не на латинице": {&model.Edit{Username: new("админ"), Password: new("password-1")}, "username"},
	}
	for name, c := range cases {
		_, err := e.uc.Create(context.Background(), c.obj)
		full, ok := errs.AsErrFull(err)
		if assert.True(t, ok && errors.Is(full.Err, errs.InvalidRequest), name) {
			assert.Contains(t, full.Fields, c.field, name)
		}
	}
	assert.Empty(t, e.repo.items, "ни один негодный запрос человека не создал")

	// границы допустимого
	adminId, err := e.uc.Create(context.Background(), &model.Edit{Username: new("a.b_c-1"), Password: new(strings.Repeat("я", 36))})
	require.NoError(t, err, "72 байта (36 русских букв) — можно")
	adminCtx := e.as(adminId, true)
	_, err = e.uc.Create(adminCtx, &model.Edit{Username: new(strings.Repeat("a", 64)), Password: new("12345678")})
	require.NoError(t, err, "логин 64 символа и пароль 8 символов — можно")

	// при изменении проверяется только переданное
	require.NoError(t, e.uc.Update(adminCtx, adminId, &model.Edit{Name: new("Имя")}))
	err = e.uc.Update(adminCtx, adminId, &model.Edit{Username: new("x")})
	full, ok := errs.AsErrFull(err)
	require.True(t, ok)
	assert.Contains(t, full.Fields, "username")
	err = e.uc.Update(adminCtx, adminId, &model.Edit{Password: new("short")})
	full, ok = errs.AsErrFull(err)
	require.True(t, ok)
	assert.Contains(t, full.Fields, "password")
}
