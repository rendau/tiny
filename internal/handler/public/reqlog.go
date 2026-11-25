package public

import (
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Журнал запросов к /rest/*: нужен, чтобы после переключения с Shlink видеть, что именно присылают
// клиенты и на чём они получают отказ.
//
// Пишется:
//   - каждый отказ (код ответа от 400) на создании и чтении ссылки — всегда, вместе с ответом, в
//     котором Shlink-формат ошибки называет причину и неверные поля;
//   - каждый запрос создания ссылки целиком, с телом, — только при LOG_CREATE_REQUESTS=true:
//     в длинных адресах бывают телефоны, поэтому тела включаются осознанно и на время.
//
// Сам ключ API в журнал не попадает — только его отпечаток: первые 8 знаков SHA-256, то есть начало
// значения api_keys.key у Shlink и строки в API_KEY_HASHES. По нему видно, чей это ключ.
const (
	logBodyLimit     = 4096
	logResponseLimit = 512
	// у успешного создания ответ длинный и повторяет запрос — хватает начала с shortUrl и shortCode
	logCreatedResponseLimit = 160
)

// requestNote — что обработчик запомнил о запросе для журнала
type requestNote struct {
	keyFingerprint string
	keyValid       bool
	body           []byte
	hasBody        bool
}

// noteOf — заметки запроса; nil, если обработчик вызван не через ServeHTTP
func noteOf(w http.ResponseWriter) *requestNote {
	if rec, ok := w.(*statusRecorder); ok {
		return &rec.note
	}
	return nil
}

func keyFingerprint(keyHash []byte) string {
	return hex.EncodeToString(keyHash[:4])
}

func (h *Handler) logRest(r *http.Request, rec *statusRecorder, route string, duration time.Duration) {
	isCreate := route == RouteShortUrlCreate
	rejected := rec.status >= http.StatusBadRequest

	switch {
	case isCreate && (rejected || h.logCreateRequests):
	case route == RouteShortUrlGet && rejected:
	default:
		// остальное (неизвестные адреса /rest/*, health, OPTIONS) — это сканеры и пробы, в журнал не идёт
		return
	}

	note := &rec.note
	attrs := []any{
		"route", route,
		"status", rec.status,
		"method", r.Method,
		"path", r.URL.RequestURI(),
		"host", r.Host,
		"key", note.keyFingerprint,
		"key_valid", note.keyValid,
		"client", clientAddr(r),
		"user_agent", r.UserAgent(),
		"duration_ms", duration.Milliseconds(),
	}

	if isCreate {
		attrs = append(attrs, "content_type", r.Header.Get("Content-Type"))
		if note.hasBody {
			attrs = append(attrs, "body_bytes", len(note.body))
			if h.logCreateRequests {
				attrs = append(attrs, "body", cut(note.body, logBodyLimit))
			}
		}
	}

	responseLimit := logResponseLimit
	if !rejected {
		responseLimit = logCreatedResponseLimit
	}
	attrs = append(attrs, "response", cut(rec.body, responseLimit))

	switch {
	case rec.status >= http.StatusInternalServerError:
		// сама ошибка уже записана обработчиком — здесь то, с чем пришёл клиент
		slog.Warn("public: rest request failed", attrs...)
	case rejected:
		slog.Info("public: rest request rejected", attrs...)
	default:
		slog.Info("public: short url created", attrs...)
	}
}

// clientAddr — адрес клиента для журнала: сервис обычно стоит за CDN и обратным прокси
func clientAddr(r *http.Request) string {
	if v := r.Header.Get("Cf-Connecting-Ip"); v != "" {
		return v
	}
	if v, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ","); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		return v
	}
	return r.RemoteAddr
}

func cut(b []byte, limit int) string {
	if len(b) <= limit {
		return string(b)
	}
	return strings.ToValidUTF8(string(b[:limit]), "") + "…"
}
