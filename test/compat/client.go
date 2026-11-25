package compat

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// ClientConfig — настройки http-клиента прогона; нулевые поля заменяются значениями по умолчанию.
type ClientConfig struct {
	DialTimeout         time.Duration // установка соединения, по умолчанию 2s
	TLSHandshakeTimeout time.Duration // TLS-рукопожатие, по умолчанию 2s
	MaxConnsPerHost     int           // предел соединений на хост, по умолчанию 20
	Timeout             time.Duration // весь запрос целиком, по умолчанию 15s
}

// NewClient — единственное место, где создаётся http-клиент прогона.
//
// Переходы по перенаправлениям выключены: ответ 302 нужно видеть как есть. Сжатие тоже
// выключено — клиент не добавляет от себя Accept-Encoding и не прячет Content-Encoding.
func NewClient(cfg ClientConfig) *http.Client {
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 2 * time.Second
	}
	if cfg.TLSHandshakeTimeout == 0 {
		cfg.TLSHandshakeTimeout = 2 * time.Second
	}
	if cfg.MaxConnsPerHost == 0 {
		cfg.MaxConnsPerHost = 20
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}

	dialer := &net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: 30 * time.Second}

	return &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
			TLSHandshakeTimeout: cfg.TLSHandshakeTimeout,
			// стенды и внутренние сервисы ходят по самоподписанным сертификатам
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			MaxConnsPerHost:       cfg.MaxConnsPerHost,
			MaxIdleConnsPerHost:   cfg.MaxConnsPerHost,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: cfg.Timeout,
			DisableCompression:    true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
