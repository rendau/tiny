//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonModel "github.com/rendau/tiny/internal/domain/common/model"
	"github.com/rendau/tiny/internal/domain/usr/model"
	"github.com/rendau/tiny/internal/domain/usr/repo/db"
	"github.com/rendau/tiny/internal/domain/usr/usrtest"
	"github.com/rendau/tiny/internal/errs"
)

func usernames(items []*model.Main) []string {
	return lo.Map(items, func(v *model.Main, _ int) string { return v.Username })
}

// Стенд: контейнер tiny-pg-usr (см. usrtest). Таблицы — из migrations/000001_init.up.sql.
func TestRepoCrud(t *testing.T) {
	ctx := context.Background()
	repo := db.New(usrtest.PgPool(t))

	hasAny, err := repo.HasAny(ctx)
	require.NoError(t, err)
	assert.False(t, hasAny)

	// Create: непереданные поля берут значения по умолчанию из таблицы
	started := time.Now().Add(-time.Second)
	id, err := repo.Create(ctx, &model.Edit{Username: new("admin"), Password: new("hash-1")})
	require.NoError(t, err)
	assert.Positive(t, id)

	hasAny, err = repo.HasAny(ctx)
	require.NoError(t, err)
	assert.True(t, hasAny)

	item, found, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, id, item.Id)
	assert.Equal(t, "admin", item.Username)
	assert.Equal(t, "hash-1", item.Password)
	assert.Empty(t, item.Name)
	assert.True(t, item.Active, "по умолчанию действующий")
	assert.False(t, item.IsAdmin, "по умолчанию не админ")
	assert.WithinDuration(t, time.Now(), item.CreatedAt, time.Minute)
	assert.False(t, item.CreatedAt.Before(started))
	assert.Equal(t, item.CreatedAt, item.ModifiedAt)

	_, found, err = repo.Get(ctx, id+1000)
	require.NoError(t, err)
	assert.False(t, found)

	// GetByUsername — точное совпадение
	item, found, err = repo.GetByUsername(ctx, "admin")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, id, item.Id)
	_, found, err = repo.GetByUsername(ctx, "Admin")
	require.NoError(t, err)
	assert.False(t, found, "логин сравнивается как есть: к нижнему регистру приводит usecase")
	_, found, err = repo.GetByUsername(ctx, "nobody")
	require.NoError(t, err)
	assert.False(t, found)

	// Update: меняется только переданное
	modifiedAt := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	require.NoError(t, repo.Update(ctx, id, &model.Edit{
		ModifiedAt: &modifiedAt,
		IsAdmin:    new(true),
		Name:       new("Админ"),
	}))
	item, found, err = repo.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, item.IsAdmin)
	assert.Equal(t, "Админ", item.Name)
	assert.Equal(t, "admin", item.Username)
	assert.Equal(t, "hash-1", item.Password)
	assert.True(t, item.Active)
	assert.True(t, modifiedAt.Equal(item.ModifiedAt))

	// занятый логин — отказ по делу, и при создании, и при изменении
	otherId, err := repo.Create(ctx, &model.Edit{Username: new("oleg"), Password: new("hash-2"), Active: new(false)})
	require.NoError(t, err)
	_, err = repo.Create(ctx, &model.Edit{Username: new("admin"), Password: new("hash-3")})
	assert.ErrorIs(t, err, errs.UsrUsernameExists)
	assert.ErrorIs(t, repo.Update(ctx, otherId, &model.Edit{Username: new("admin")}), errs.UsrUsernameExists)

	// Delete
	require.NoError(t, repo.Delete(ctx, otherId))
	_, found, err = repo.Get(ctx, otherId)
	require.NoError(t, err)
	assert.False(t, found)
	require.NoError(t, repo.Delete(ctx, otherId), "повторное удаление — не ошибка")
}

func TestRepoList(t *testing.T) {
	ctx := context.Background()
	repo := db.New(usrtest.PgPool(t))

	ids := map[string]int64{}
	for _, v := range []struct {
		username string
		name     string
		active   bool
	}{
		{"dana", "Дана", true},
		{"berik", "Берик", false},
		{"aigul", "Айгуль", true},
		{"chingiz", "Чингиз", true},
	} {
		id, err := repo.Create(ctx, &model.Edit{Username: new(v.username), Name: new(v.name), Active: new(v.active), Password: new("hash")})
		require.NoError(t, err)
		ids[v.username] = id
	}

	// без сортировки — по логину
	items, total, err := repo.List(ctx, &model.ListReq{ListParams: commonModel.ListParams{PageSize: 10, WithTotalCount: true}})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Equal(t, []string{"aigul", "berik", "chingiz", "dana"}, usernames(items))

	// страницы с нуля
	items, total, err = repo.List(ctx, &model.ListReq{ListParams: commonModel.ListParams{Page: 1, PageSize: 3, WithTotalCount: true}})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Equal(t, []string{"dana"}, usernames(items))

	// сортировка
	items, _, err = repo.List(ctx, &model.ListReq{ListParams: commonModel.ListParams{PageSize: 10, Sort: []string{"-id"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"chingiz", "aigul", "berik", "dana"}, usernames(items))

	// сортировка по неразрешённой колонке не проходит в запрос
	items, _, err = repo.List(ctx, &model.ListReq{ListParams: commonModel.ListParams{PageSize: 10, Sort: []string{"password; drop table usr"}}})
	require.NoError(t, err)
	assert.Len(t, items, 4)

	// фильтры
	items, total, err = repo.List(ctx, &model.ListReq{ListParams: commonModel.ListParams{PageSize: 10, WithTotalCount: true}, Active: new(false)})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, []string{"berik"}, usernames(items))

	items, _, err = repo.List(ctx, &model.ListReq{
		ListParams: commonModel.ListParams{PageSize: 10},
		Active:     new(true),
		IDs:        new([]int64{ids["berik"], ids["dana"], ids["aigul"]}),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"aigul", "dana"}, usernames(items))

	// только количество
	items, total, err = repo.List(ctx, &model.ListReq{ListParams: commonModel.ListParams{OnlyCount: true}, Active: new(true)})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Empty(t, items)
}
