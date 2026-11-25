// Package public — публичный сервер, совместимый с Shlink 5.1.8: переход по короткой ссылке
// (GET и HEAD /{код}) и /rest/v3/*. Ответы повторяют настоящий Shlink вплоть до кодов, заголовков
// и порядка полей JSON: эталон снят с shlinkio/shlink:5.1.8-roadrunner (см. test/compat и
// testdata/shlink). Клиенты при замене Shlink ничего не меняют.
package public

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	usecase "github.com/rendau/tiny/internal/usecase/link"
)

// Имена маршрутов для учёта запросов (Config.Observe)
const (
	RouteRedirect       = "redirect"
	RouteRobots         = "robots"
	RouteFavicon        = "favicon"
	RouteNotFound       = "not_found"
	RouteHealth         = "rest_health"
	RouteShortUrlCreate = "rest_short_url_create"
	RouteShortUrlGet    = "rest_short_url_get"
	RouteRestOptions    = "rest_options"
	RouteRestNotFound   = "rest_not_found"
)

// maxBodySize — предел тела запроса создания ссылки
const maxBodySize = 1 << 20

var (
	//go:embed assets/404_root.html
	pageNotFound []byte
	//go:embed assets/404_code.html
	pageInvalidShortUrl []byte
	//go:embed assets/favicon.ico
	favicon []byte

	// faviconModTime — Last-Modified значка у Shlink 5.1.8
	faviconModTime = time.Date(2026, time.October, 22, 9, 54, 23, 0, time.UTC)
)

type Config struct {
	// ApiKeys — ключи для /rest/* (заголовок X-Api-Key) открытым текстом
	ApiKeys []string
	// ApiKeyHashes — те же ключи, заданные хешем: SHA-256 в hex, как Shlink хранит их в колонке
	// api_keys.key. Так ключи переезжают из Shlink без переноса самих значений. Строки, которые
	// не являются таким хешем, пропускаются — их до запуска ловит CheckApiKeyHashes.
	// Оба списка пусты — /rest/* отвечает 401 всем
	ApiKeyHashes []string
	// TimeZone — пояс дат в ответах /rest/* и дат без пояса в запросах: у Shlink это его настройка
	// TIMEZONE, а не пояс пода. nil — UTC (у Shlink без TIMEZONE даты идут с +00:00).
	TimeZone *time.Location
	// Observe — учёт запроса (метрики): маршрут, код ответа, длительность; nil — не учитывать
	Observe func(route string, status int, duration time.Duration)
	// LogCreateRequests — писать в журнал каждый запрос создания ссылки вместе с телом (reqlog.go).
	// Отказы на создании и чтении пишутся всегда, без тела запроса
	LogCreateRequests bool
}

type Handler struct {
	usecase *usecase.Usecase

	// apiKeyHashes — SHA-256 ключей: сравнение хешей не выдаёт временем ни длину, ни начало ключа
	apiKeyHashes [][sha256.Size]byte
	timeZone     *time.Location
	observe      func(route string, status int, duration time.Duration)

	logCreateRequests bool
}

func New(uc *usecase.Usecase, cfg Config) *Handler {
	h := &Handler{usecase: uc, timeZone: cfg.TimeZone, observe: cfg.Observe, logCreateRequests: cfg.LogCreateRequests}
	if h.timeZone == nil {
		h.timeZone = time.UTC
	}

	for _, key := range cfg.ApiKeys {
		if key = strings.TrimSpace(key); key != "" {
			h.apiKeyHashes = append(h.apiKeyHashes, sha256.Sum256([]byte(key)))
		}
	}
	for _, v := range cfg.ApiKeyHashes {
		if hash, ok := decodeApiKeyHash(v); ok {
			h.apiKeyHashes = append(h.apiKeyHashes, hash)
		}
	}

	return h
}

// CheckApiKeyHashes — проверка настройки до запуска: каждая непустая строка должна быть SHA-256
// в hex (64 знака). Значения в текст ошибки не попадают — только номер строки в списке.
func CheckApiKeyHashes(hashes []string) error {
	for i, v := range hashes {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if _, ok := decodeApiKeyHash(v); !ok {
			return fmt.Errorf("api key hash #%d is not a sha-256 hex string (64 hex chars)", i+1)
		}
	}
	return nil
}

func decodeApiKeyHash(v string) ([sha256.Size]byte, bool) {
	var hash [sha256.Size]byte
	raw, err := hex.DecodeString(strings.TrimSpace(v))
	if err != nil || len(raw) != sha256.Size {
		return hash, false
	}
	copy(hash[:], raw)
	return hash, true
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	route := h.serve(rec, r)
	duration := time.Since(start)

	if h.observe != nil {
		h.observe(route, rec.status, duration)
	}
	h.logRest(r, rec, route, duration)
}

// serve разбирает адрес как Shlink: повторные «/» схлопываются, путь сравнивается раскодированным
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) string {
	path := collapseSlashes(r.URL.Path)
	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead

	if path == "/rest" || strings.HasPrefix(path, "/rest/") {
		return h.serveRest(w, r, strings.TrimPrefix(path, "/rest"))
	}

	switch {
	case !isRead:
	case path == "/robots.txt":
		return h.robots(w, r)
	case path == "/favicon.ico":
		return h.favicon(w, r)
	case len(path) > 1 && !strings.Contains(path[1:], "/"):
		return h.redirect(w, r, path[1:])
	}

	// страница «не найдено»: в отличие от прочих ответов, на HEAD отдаёт настоящую длину
	h.respond(w, r, http.StatusNotFound, "text/html; charset=utf-8", pageNotFound, true)
	return RouteNotFound
}

// respond пишет ответ с заголовками как у Shlink под RoadRunner: X-Request-Id у каждого ответа,
// на HEAD — Content-Length: 0 (кроме страницы «не найдено»)
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte, headKeepsLength bool) {
	header := w.Header()
	header.Set("X-Request-Id", newRequestId())
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}

	if r.Method == http.MethodHead {
		length := 0
		if headKeepsLength {
			length = len(body)
		}
		header.Set("Content-Length", strconv.Itoa(length))
		w.WriteHeader(status)
		return
	}

	header.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (h *Handler) favicon(w http.ResponseWriter, r *http.Request) string {
	// как раздача файла у RoadRunner: Accept-Ranges, Last-Modified, 304 по If-Modified-Since
	w.Header().Set("Content-Type", "image/vnd.microsoft.icon")
	http.ServeContent(w, r, "", faviconModTime, bytes.NewReader(favicon))
	return RouteFavicon
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	// body — начало тела ответа для журнала (reqlog.go)
	body []byte
	note requestNote
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if room := logResponseLimit + 1 - len(w.body); room > 0 {
		w.body = append(w.body, p[:min(room, len(p))]...)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func collapseSlashes(path string) string {
	if path == "" {
		return "/"
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	return path
}

// newRequestId — случайный UUID четвёртой версии, как X-Request-Id у Shlink
func newRequestId() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])

	return string(out[:])
}
