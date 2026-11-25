package shlinkimport

// Общее для тестов на живом стенде (раздел «Тестовый стенд» в CLAUDE.md, зона «Перенос»):
// настоящий Shlink 5.1.8, его Postgres (источник) и Postgres со схемой tiny (приёмник).
// Стенда нет — тесты пропускаются, а не падают.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

const (
	standSrcDSNDefault = "postgres://shlink:shlink@localhost:54344/shlink?sslmode=disable"
	standDstDSNDefault = "postgres://tiny:tiny@localhost:54345/tiny?sslmode=disable"
	standShlinkDefault = "http://localhost:18082"
	standShlinkKey     = "imp-api-key"
	// часовой пояс, в котором работает Shlink стенда (переменная TIMEZONE контейнера)
	standSrcTZ = "Asia/Almaty"
)

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func standSrcDSN() string { return envOr("SHLINK_IMPORT_TEST_SRC_DSN", standSrcDSNDefault) }
func standDstDSN() string { return envOr("SHLINK_IMPORT_TEST_DST_DSN", standDstDSNDefault) }
func standShlink() string { return envOr("SHLINK_IMPORT_TEST_SHLINK_URL", standShlinkDefault) }

func standLocation(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(standSrcTZ)
	require.NoError(t, err)
	return loc
}

// connectOrSkip подключается к базе стенда; если стенд не поднят — пропускает тест.
func connectOrSkip(t testing.TB, dsn string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skip("стенд переноса не поднят (см. «Тестовый стенд» в CLAUDE.md) — тест пропущен")
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func randSuffix(t testing.TB) string {
	t.Helper()
	b := make([]byte, 6)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// withSearchPath добавляет к DSN схему по умолчанию.
func withSearchPath(t testing.TB, dsn, schema string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// newDstSchema создаёт в приёмнике отдельную схему со свежей структурой tiny (из миграции
// проекта) и возвращает DSN, смотрящий в неё. Так тесты не мешают друг другу и показу на стенде.
func newDstSchema(t testing.TB) (dsn string, conn *pgx.Conn) {
	t.Helper()
	admin := connectOrSkip(t, standDstDSN())
	ctx := context.Background()

	schema := "imp_test_" + randSuffix(t)
	_, err := admin.Exec(ctx, "create schema "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
	})

	dsn = withSearchPath(t, standDstDSN(), schema)
	conn = connectOrSkip(t, dsn)

	migration, err := os.ReadFile("../../migrations/000001_init.up.sql")
	require.NoError(t, err)
	_, err = conn.Exec(ctx, string(migration), pgx.QueryExecModeSimpleProtocol)
	require.NoError(t, err)

	return dsn, conn
}

// newSrcSchema создаёт в базе Shlink отдельную схему с пустыми копиями его таблиц (структура —
// от настоящего Shlink) и возвращает DSN, смотрящий в неё. Нужна для случаев, которые через API
// Shlink не создать: дубли кода, старые даты, правила перенаправления.
func newSrcSchema(t testing.TB) (dsn string, conn *pgx.Conn) {
	t.Helper()
	admin := connectOrSkip(t, standSrcDSN())
	ctx := context.Background()

	schema := "imp_src_" + randSuffix(t)
	_, err := admin.Exec(ctx, "create schema "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
	})

	for _, table := range []string{
		"domains", "tags", "short_urls", "short_urls_in_tags",
		"short_url_redirect_rules", "short_url_visits_counts", "visits",
	} {
		_, err = admin.Exec(ctx, fmt.Sprintf(
			"create table %s.%s (like public.%s including all)", schema, table, table))
		require.NoError(t, err)
	}

	dsn = withSearchPath(t, standSrcDSN(), schema)
	conn = connectOrSkip(t, dsn)
	return dsn, conn
}

// --- Shlink API стенда ---

var (
	standHTTPOnce   sync.Once
	standHTTPClient *http.Client
)

// standHTTP — http-клиент для Shlink стенда (настройки — по общему правилу для http-клиентов).
func standHTTP() *http.Client {
	standHTTPOnce.Do(func() {
		standHTTPClient = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext:         (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
				TLSHandshakeTimeout: 2 * time.Second,
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // стенд
				MaxConnsPerHost:     20,
				MaxIdleConnsPerHost: 20,
			},
		}
	})
	return standHTTPClient
}

// shlinkShortURL — ссылка в том виде, в каком её отдаёт API Shlink.
type shlinkShortURL struct {
	ShortURL    string   `json:"shortUrl"`
	ShortCode   string   `json:"shortCode"`
	LongURL     string   `json:"longUrl"`
	DateCreated string   `json:"dateCreated"`
	Tags        []string `json:"tags"`
	Meta        struct {
		ValidSince *string `json:"validSince"`
		ValidUntil *string `json:"validUntil"`
		MaxVisits  *int32  `json:"maxVisits"`
	} `json:"meta"`
	Domain       *string `json:"domain"`
	Title        *string `json:"title"`
	Crawlable    bool    `json:"crawlable"`
	ForwardQuery bool    `json:"forwardQuery"`
}

func shlinkDo(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("json.Marshal: %w", err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, standShlink()+path, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("http.NewRequest: %w", err)
	}
	req.Header.Set("X-Api-Key", standShlinkKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := standHTTP().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// shlinkCreate создаёт ссылку через настоящий Shlink.
func shlinkCreate(ctx context.Context, body map[string]any) (*shlinkShortURL, error) {
	code, raw, err := shlinkDo(ctx, http.MethodPost, "/rest/v3/short-urls", body)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("shlink ответил %d: %s", code, truncate(string(raw), 300))
	}
	res := &shlinkShortURL{}
	if err = json.Unmarshal(raw, res); err != nil {
		return nil, fmt.Errorf("json.Unmarshal: %w", err)
	}
	return res, nil
}

// shlinkGet читает ссылку через настоящий Shlink.
func shlinkGet(ctx context.Context, shortCode, domain string) (*shlinkShortURL, error) {
	path := "/rest/v3/short-urls/" + url.PathEscape(shortCode)
	if domain != "" {
		path += "?domain=" + url.QueryEscape(domain)
	}
	code, raw, err := shlinkDo(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("shlink ответил %d: %s", code, truncate(string(raw), 300))
	}
	res := &shlinkShortURL{}
	if err = json.Unmarshal(raw, res); err != nil {
		return nil, fmt.Errorf("json.Unmarshal: %w", err)
	}
	return res, nil
}

func shlinkOrSkip(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, _, err := shlinkDo(ctx, http.MethodGet, "/rest/health", nil)
	if err != nil || code != http.StatusOK {
		t.Skip("Shlink стенда не отвечает (см. «Тестовый стенд» в CLAUDE.md) — тест пропущен")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// testLog собирает журнал переноса в тесте и отдаёт его в вывод теста.
type testLog struct {
	t  testing.TB
	mu sync.Mutex
	sb strings.Builder
}

func newTestLog(t testing.TB) *testLog { return &testLog{t: t} }

func (l *testLog) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf(format, args...)
	l.sb.WriteString(line)
	l.sb.WriteByte('\n')
	l.t.Log(line)
}

func (l *testLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sb.String()
}
