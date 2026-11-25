package public

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/tiny/internal/errs"
	usecase "github.com/rendau/tiny/internal/usecase/link"
)

const (
	healthBodyPass = `{"status":"pass","version":"5.1.8","links":{"about":"https://shlink.io","project":"https://github.com/shlinkio/shlink"}}`
	healthBodyFail = `{"status":"fail","version":"5.1.8","links":{"about":"https://shlink.io","project":"https://github.com/shlinkio/shlink"}}`

	healthTimeout = 2 * time.Second

	shortUrlsPath = "/short-urls"
)

// restVersions — версии API в адресе, которые принимает Shlink 5 (ответы у них одинаковые)
var restVersions = []string{"/v1", "/v2", "/v3"}

// serveRest — /rest/*; restPath — путь без «/rest». Как у Shlink: сначала ищется маршрут
// (неизвестный — 404 без проверки ключа), потом проверяется ключ.
func (h *Handler) serveRest(w http.ResponseWriter, r *http.Request, restPath string) string {
	// запросы из браузера с другого сайта Shlink разрешает всем
	if r.Header.Get("Origin") != "" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	path, versioned := restPath, false
	for _, version := range restVersions {
		if rest, ok := strings.CutPrefix(restPath, version); ok && (rest == "" || rest[0] == '/') {
			path, versioned = rest, true
			break
		}
	}

	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead

	switch {
	case path == "/health" && isRead:
		return h.health(w, r)

	case restPath == "" && isRead:
		// «/rest» без продолжения: у Shlink сначала проверяется ключ, потом — что такого адреса нет
		return h.authorized(w, r, RouteRestNotFound, func(w http.ResponseWriter, r *http.Request) {
			h.problemNotFound(w, r, restPath)
		})

	case versioned && path == shortUrlsPath:
		switch r.Method {
		case http.MethodPost:
			return h.authorized(w, r, RouteShortUrlCreate, h.shortUrlCreate)
		case http.MethodOptions:
			return h.restOptions(w, r, "GET,POST")
		}

	case versioned && isShortUrlPath(path):
		switch {
		case isRead:
			return h.authorized(w, r, RouteShortUrlGet, h.shortUrlGet(path[len(shortUrlsPath)+1:]))
		case r.Method == http.MethodOptions:
			return h.restOptions(w, r, "GET,DELETE,PATCH")
		}
	}

	h.problemNotFound(w, r, restPath)
	return RouteRestNotFound
}

// isShortUrlPath — «/short-urls/{код}»: код — один непустой отрезок пути
func isShortUrlPath(path string) bool {
	code, ok := strings.CutPrefix(path, shortUrlsPath+"/")
	return ok && code != "" && !strings.Contains(code, "/")
}

// authorized пускает к обработчику только с верным ключом API
func (h *Handler) authorized(w http.ResponseWriter, r *http.Request, route string, next func(http.ResponseWriter, *http.Request)) string {
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		h.problemMissingAuth(w, r)
		return route
	}

	// сравнение за постоянное время: сверяются хеши, причём со всеми ключами, без раннего выхода
	keyHash := sha256.Sum256([]byte(key))
	matched := 0
	for i := range h.apiKeyHashes {
		matched |= subtle.ConstantTimeCompare(keyHash[:], h.apiKeyHashes[i][:])
	}

	// для журнала — отпечаток ключа (и неверного тоже: по нему видно, чей ключ мы не знаем)
	if note := noteOf(w); note != nil {
		note.keyFingerprint, note.keyValid = keyFingerprint(keyHash[:]), matched == 1
	}

	if matched != 1 {
		h.problemInvalidApiKey(w, r)
		return route
	}

	next(w, r)
	return route
}

// restOptions — ответ на OPTIONS (в том числе предварительный запрос браузера), как у Shlink
func (h *Handler) restOptions(w http.ResponseWriter, r *http.Request, allow string) string {
	header := w.Header()
	header.Set("Allow", allow)
	if r.Header.Get("Origin") != "" {
		header.Set("Access-Control-Allow-Methods", allow)
		header.Set("Access-Control-Max-Age", "3600")
		if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
			header.Set("Access-Control-Allow-Headers", requested)
		}
	}
	header.Set("X-Request-Id", newRequestId())
	w.WriteHeader(http.StatusNoContent)

	return RouteRestOptions
}

// health — GET /rest/health: пробы kubernetes. База недоступна — 503, как у Shlink.
func (h *Handler) health(w http.ResponseWriter, r *http.Request) string {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	if err := h.usecase.Health(ctx); err != nil {
		slog.Error("public: health check failed", "error", err)
		h.respond(w, r, http.StatusServiceUnavailable, "application/health+json", []byte(healthBodyFail), false)
		return RouteHealth
	}

	h.respond(w, r, http.StatusOK, "application/health+json", []byte(healthBodyPass), false)

	return RouteHealth
}

// shortUrlCreate — POST /rest/v3/short-urls
func (h *Handler) shortUrlCreate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if note := noteOf(w); note != nil {
		// для журнала: что успели прочитать, даже если тело слишком большое или оборвалось
		note.body, note.hasBody = body, true
	}
	if err != nil {
		h.problemMalformedBody(w, r)
		return
	}

	fields, malformed := parseBody(r.Header.Get("Content-Type"), body)
	if malformed {
		h.problemMalformedBody(w, r)
		return
	}

	req, invalid := decodeCreateReq(fields, h.timeZone)
	if len(invalid) > 0 {
		// неверные поля перечисляются все сразу: к неразобранным добавляются те, что не прошли проверку
		invalid = append(invalid, invalidElementsOf(h.usecase.ValidateCreate(req))...)
		h.problemInvalidData(w, r, sortInvalidElements(invalid))
		return
	}

	link, err := h.usecase.PublicCreate(r.Context(), req)
	if err != nil {
		codeErr, _ := errors.AsType[usecase.CodeError](err)

		switch {
		case errors.Is(err, errs.InvalidRequest):
			h.problemInvalidData(w, r, sortInvalidElements(invalidElementsOf(err)))
		case errors.Is(err, errs.ShortCodeExists):
			h.problemNonUniqueSlug(w, r, codeErr.ShortCode, codeErr.Domain)
		default:
			slog.Error("public: short url create failed", "error", err)
			h.problemInternal(w, r)
		}
		return
	}

	h.respond(w, r, http.StatusOK, "application/json", encodeShortUrl(link, h.timeZone), false)
}

// shortUrlGet — GET /rest/v3/short-urls/{код}; домен ссылки — в параметре запроса domain
func (h *Handler) shortUrlGet(shortCode string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var domain *string
		if query := r.URL.Query(); query.Has(fieldDomain) {
			domain = new(query.Get(fieldDomain))
		}

		link, err := h.usecase.PublicGet(r.Context(), shortCode, domain)
		if err != nil {
			if errors.Is(err, errs.ObjectNotFound) {
				// домен в ошибке называется, только если искали не в основном
				codeErr, _ := errors.AsType[usecase.CodeError](err)
				h.problemShortUrlNotFound(w, r, shortCode, lo.EmptyableToPtr(codeErr.Domain))
				return
			}

			slog.Error("public: short url get failed", "error", err, "short_code", shortCode)
			h.problemInternal(w, r)
			return
		}

		h.respond(w, r, http.StatusOK, "application/json", encodeShortUrl(link, h.timeZone), false)
	}
}

// invalidElementsOf — имена неверных полей Shlink из ошибки проверки usecase
func invalidElementsOf(err error) []string {
	fullErr, ok := errs.AsErrFull(err)
	if !ok || !errors.Is(fullErr.Err, errs.InvalidRequest) {
		return nil
	}

	return lo.FilterMap(lo.Keys(fullErr.Fields), func(name string, _ int) (string, bool) {
		shlinkName, known := usecaseFieldNames[name]
		return shlinkName, known
	})
}
