package shlinkimport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	roleSrc = "источник (SRC_DSN)"
	roleDst = "приёмник (DST_DSN)"

	appName        = "shlink-import"
	connectTimeout = 15 * time.Second
)

// connect открывает одно соединение с базой. Адрес базы секретен: ни сам DSN, ни исходный текст
// ошибки подключения (в нём есть хост, логин и имя базы) наружу не отдаются — только причина
// обычными словами.
func connect(ctx context.Context, dsn, role string) (*pgx.Conn, error) {
	if dsn == "" {
		return nil, fmt.Errorf("%s: адрес базы не задан", role)
	}

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		// текст ошибки разбора содержит сам адрес — не оборачиваем
		return nil, fmt.Errorf("%s: адрес базы не разобран, ожидается вид postgres://логин:пароль@хост:порт/база", role)
	}
	cfg.RuntimeParams["application_name"] = appName
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = connectTimeout
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%s: подключение прервано: %w", role, ctxErr)
		}
		return nil, fmt.Errorf("%s: не удалось подключиться: %s", role, connectErrorReason(err))
	}
	return conn, nil
}

// connectErrorReason описывает причину отказа в подключении, не раскрывая адрес базы.
func connectErrorReason(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "28P01", "28000":
			return "неверный логин или пароль (код " + pgErr.Code + ")"
		case "3D000":
			return "такой базы на сервере нет (код 3D000)"
		case "53300":
			return "на сервере кончились свободные соединения (код 53300)"
		default:
			return "сервер отказал, код ошибки Postgres " + pgErr.Code
		}
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "имя сервера не найдено"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "соединение отклонено: по этому адресу и порту никто не слушает"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "сервер не ответил за отведённое время"
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return "сервер не ответил за отведённое время"
	}
	return "причина не распознана (подробности скрыты, чтобы не показывать адрес базы); проверьте адрес, сеть и режим sslmode"
}

// closeConn закрывает соединение, даже если рабочий контекст уже отменён.
func closeConn(ctx context.Context, conn *pgx.Conn) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = conn.Close(closeCtx)
}

func rollback(ctx context.Context, tx pgx.Tx) {
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(rbCtx)
}

// redactedError — ошибка, из текста которой убраны сетевые адреса.
type redactedError struct {
	text  string
	cause error
}

func (e *redactedError) Error() string { return e.text }
func (e *redactedError) Unwrap() error { return e.cause }

const hiddenAddr = "<адрес скрыт>"

// safeError убирает из текста ошибки сетевые адреса: при обрыве связи посреди работы в ошибке
// оказываются адрес и порт сервера базы, а адреса баз показывать нельзя.
func safeError(err error) error {
	if err == nil {
		return nil
	}

	var secrets []string
	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		if opErr.Addr != nil {
			secrets = append(secrets, opErr.Addr.String())
		}
		if opErr.Source != nil {
			secrets = append(secrets, opErr.Source.String())
		}
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
		secrets = append(secrets, dnsErr.Name, dnsErr.Server)
	}

	text := err.Error()
	changed := false
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			text = strings.ReplaceAll(text, secret, hiddenAddr)
			changed = true
		}
	}
	if !changed {
		return err
	}
	return &redactedError{text: text, cause: err}
}
