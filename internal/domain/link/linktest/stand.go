//go:build integration

// Package linktest — подключение к основной тестовой базе для тестов на живой базе
// (CLAUDE.md, «Тестовый стенд»: контейнер tiny-pg, порт 54340):
//
//	docker run --rm -d --name tiny-pg -p 54340:5432 -e TZ=Asia/Almaty \
//	  -e POSTGRES_USER=tiny -e POSTGRES_PASSWORD=tiny -e POSTGRES_DB=tiny postgres:17
//	go test -tags integration ./internal/domain/link/... ./internal/handler/public/...
package linktest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// PgDsn — адрес базы основного стенда
const PgDsn = "postgres://tiny:tiny@localhost:54340/tiny?sslmode=disable"

// PgPool — пул к базе стенда со схемой из migrations/*.up.sql. У каждого вызова своя временная
// схема Postgres (она же search_path пула): тесты начинают с пустых таблиц, не мешают ни друг
// другу, ни сервису, запущенному на той же базе, и ничего после себя не оставляют.
func PgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	schema := fmt.Sprintf("link_test_%d", rand.Int64N(1<<40))
	execOnce(t, `create schema `+schema)
	t.Cleanup(func() { execOnce(t, `drop schema if exists `+schema+` cascade`) })

	cfg, err := pgxpool.ParseConfig(PgDsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close) // выполнится раньше удаления схемы

	files, err := filepath.Glob(filepath.Join(repoRoot(t), "migrations", "*.up.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "migrations/*.up.sql not found")
	slices.Sort(files)
	for _, file := range files {
		script, err := os.ReadFile(file)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, string(script))
		require.NoError(t, err, file)
	}

	return pool
}

// execOnce — одна команда отдельным соединением
func execOnce(t *testing.T, query string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, PgDsn)
	require.NoError(t, err, "стенд tiny-pg не запущен? (CLAUDE.md, «Тестовый стенд»)")
	defer func() { _ = conn.Close(ctx) }()

	_, err = conn.Exec(ctx, query)
	require.NoError(t, err)
}

// repoRoot — корень репозитория (каталог с go.mod)
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err = os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "go.mod not found")
		dir = parent
	}
}
