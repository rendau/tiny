package shlinkimport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Адрес базы секретен: в тексте ошибок не должно быть ни хоста, ни логина, ни пароля, ни имени базы.
func TestConnect_ErrorsDoNotLeakAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	secrets := []string{"s3cretPassw0rd", "secretuser", "secretdb", "127.0.0.1", "secret-host.invalid", "5439"}
	assertClean := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		t.Log(err)
		for _, s := range secrets {
			assert.NotContains(t, err.Error(), s)
		}
	}

	t.Run("никто не слушает", func(t *testing.T) {
		_, err := connect(ctx, "postgres://secretuser:s3cretPassw0rd@127.0.0.1:1/secretdb?sslmode=disable", roleSrc)
		assertClean(t, err)
		assert.Contains(t, err.Error(), "соединение отклонено")
		assert.Contains(t, err.Error(), "SRC_DSN")
	})

	t.Run("имя сервера не найдено", func(t *testing.T) {
		_, err := connect(ctx, "postgres://secretuser:s3cretPassw0rd@secret-host.invalid:5439/secretdb?sslmode=disable", roleDst)
		assertClean(t, err)
		assert.Contains(t, err.Error(), "DST_DSN")
	})

	t.Run("адрес не разобран", func(t *testing.T) {
		_, err := connect(ctx, "postgres://secretuser:s3cretPassw0rd@127.0.0.1:port/secretdb", roleSrc)
		assertClean(t, err)
		assert.Contains(t, err.Error(), "не разобран")
	})

	t.Run("адрес не задан", func(t *testing.T) {
		_, err := connect(ctx, "", roleDst)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "не задан")
	})

	t.Run("перенос с недоступным источником", func(t *testing.T) {
		_, err := Import(ctx, ImportOptions{
			SrcDSN:      "postgres://secretuser:s3cretPassw0rd@127.0.0.1:1/secretdb?sslmode=disable",
			DstDSN:      "postgres://secretuser:s3cretPassw0rd@127.0.0.1:1/secretdb?sslmode=disable",
			SrcLocation: time.UTC,
		})
		assertClean(t, err)
	})

	t.Run("неверный пароль на стенде", func(t *testing.T) {
		connectOrSkip(t, standSrcDSN())
		_, err := connect(ctx, "postgres://shlink:s3cretPassw0rd@localhost:54344/shlink?sslmode=disable", roleSrc)
		require.Error(t, err)
		t.Log(err)
		assert.Contains(t, err.Error(), "неверный логин или пароль")
		assert.NotContains(t, err.Error(), "s3cretPassw0rd")
		assert.NotContains(t, err.Error(), "54344")
		assert.NotContains(t, err.Error(), "localhost")
	})
}

// Через соединение с источником записать ничего нельзя — ни в транзакции, ни вне её.
func TestSource_IsReadOnly(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	ctx := context.Background()

	src, err := openSource(ctx, srcDSN, time.UTC)
	require.NoError(t, err)
	defer src.close(ctx)

	assertReadOnlyError := func(err error) {
		t.Helper()
		require.Error(t, err)
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		require.True(t, ok, "ожидалась ошибка Postgres, получено: %v", err)
		assert.Equal(t, "25006", pgErr.Code, "read_only_sql_transaction")
	}

	_, err = src.conn.Exec(ctx, "insert into tags (name) values ('нельзя')")
	assertReadOnlyError(err)

	_, err = src.conn.Exec(ctx, "delete from short_urls")
	assertReadOnlyError(err)

	err = src.readTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "insert into tags (name) values ('нельзя')")
		return err
	})
	assertReadOnlyError(err)

	var tags int
	require.NoError(t, srcConn.QueryRow(ctx, "select count(*) from tags").Scan(&tags))
	assert.Zero(t, tags)
}

// В пробном режиме и при проверке приёмник тоже открыт только на чтение.
func TestDest_ReadOnlyMode(t *testing.T) {
	dstDSN, _ := newDstSchema(t)
	ctx := context.Background()

	dst, err := openDest(ctx, dstDSN, true)
	require.NoError(t, err)
	defer dst.close(ctx)

	_, err = dst.conn.Exec(ctx, "insert into link (short_code, long_url) values ('x', 'https://example.com')")
	require.Error(t, err)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	require.True(t, ok)
	assert.Equal(t, "25006", pgErr.Code)
}

func TestOpen_WrongDatabase(t *testing.T) {
	ctx := context.Background()
	connectOrSkip(t, standDstDSN())
	connectOrSkip(t, standSrcDSN())

	// базы перепутаны местами: в базе tiny нет таблиц Shlink и наоборот
	_, err := openSource(ctx, standDstDSN(), time.UTC)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "нет таблиц Shlink")

	_, err = openDest(ctx, standSrcDSN(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "нет таблицы link")
}

// При обрыве связи посреди работы в тексте ошибки нет адреса сервера базы.
func TestSafeError(t *testing.T) {
	assert.NoError(t, safeError(nil))

	plain := errors.New("обычная ошибка")
	assert.Same(t, plain, safeError(plain))

	opErr := &net.OpError{
		Op: "write", Net: "tcp",
		Source: &net.TCPAddr{IP: net.IPv4(10, 1, 2, 3), Port: 51234},
		Addr:   &net.TCPAddr{IP: net.IPv4(10, 9, 8, 7), Port: 5432},
		Err:    errors.New("broken pipe"),
	}
	err := safeError(fmt.Errorf("перенос прерван после id источника 100: приёмник: copy: %w", opErr))
	t.Log(err)
	assert.NotContains(t, err.Error(), "10.9.8.7")
	assert.NotContains(t, err.Error(), "10.1.2.3")
	assert.NotContains(t, err.Error(), "5432")
	assert.Contains(t, err.Error(), "broken pipe")
	assert.Contains(t, err.Error(), "перенос прерван после id источника 100")
	_, isOpErr := errors.AsType[*net.OpError](err)
	assert.True(t, isOpErr, "исходная причина остаётся доступной коду")

	dnsErr := &net.DNSError{Err: "no such host", Name: "pg.secret.internal", Server: "10.0.0.2:53"}
	err = safeError(fmt.Errorf("источник: чтение пачки: %w", dnsErr))
	assert.NotContains(t, err.Error(), "pg.secret.internal")
	assert.NotContains(t, err.Error(), "10.0.0.2")
}

// Отмена (Ctrl+C) останавливает перенос; записанное остаётся, продолжить можно повтором.
func TestImport_Cancel(t *testing.T) {
	srcDSN, srcConn := newSrcSchema(t)
	dstDSN, dstConn := newDstSchema(t)
	f := &srcFixture{t: t, conn: srcConn}

	for i := int64(1); i <= 30; i++ {
		f.link(srcLink{ID: i, Code: fmt.Sprintf("x%02d", i), URL: fmt.Sprintf("https://example.com/%d", i)})
	}

	ctx, cancel := context.WithCancel(context.Background())
	batches := 0
	res, err := Import(ctx, ImportOptions{
		SrcDSN: srcDSN, DstDSN: dstDSN, SrcLocation: time.UTC, BatchSize: 10, ProgressEvery: time.Nanosecond,
		Logf: func(format string, args ...any) {
			t.Logf(format, args...)
			if strings.HasPrefix(format, "ход:") {
				if batches++; batches == 2 {
					cancel() // останавливаем после второй пачки
				}
			}
		},
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "-after-id=20")
	require.NotNil(t, res)
	assert.EqualValues(t, 20, res.LastID)
	assert.EqualValues(t, 20, dstCount(t, dstConn), "две пачки записаны, третья не начата")

	// повтор доводит перенос до конца
	done := runImport(t, ImportOptions{SrcDSN: srcDSN, DstDSN: dstDSN, SrcLocation: time.UTC, BatchSize: 10})
	assert.EqualValues(t, 10, done.Inserted)
	assert.EqualValues(t, 20, done.Existing)
	assert.EqualValues(t, 30, dstCount(t, dstConn))
}
