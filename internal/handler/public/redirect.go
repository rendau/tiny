package public

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/rendau/tiny/internal/errs"
)

// redirect — переход по короткой ссылке: 302 и Location; ссылки нет, она ещё не началась или
// истекла — 404 со страницей Shlink. Посещения не считаются.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, shortCode string) string {
	location, err := h.usecase.Redirect(r.Context(), r.Host, shortCode, r.URL.RawQuery)
	if err != nil {
		if errors.Is(err, errs.ObjectNotFound) {
			h.respond(w, r, http.StatusNotFound, "text/html; charset=utf-8", pageInvalidShortUrl, false)
			return RouteRedirect
		}

		slog.Error("public: redirect failed", "error", err, "short_code", shortCode)
		h.respond(w, r, http.StatusInternalServerError, "", nil, false)
		return RouteRedirect
	}

	// тела и Content-Type у ответа нет — как у Shlink (http.Redirect добавил бы HTML-тело)
	w.Header().Set("Location", location)
	h.respond(w, r, http.StatusFound, "", nil, false)

	return RouteRedirect
}
