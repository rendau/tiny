package public

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"

	"github.com/rendau/tiny/internal/errs"
)

const (
	robotsHeader = "# For more information about the robots.txt standard, see:\n" +
		"# https://www.robotstxt.org/orig.html\n\n" +
		"User-agent: *\n"
	robotsFooter = "Disallow: /"
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

// robots — robots.txt как у Shlink: поисковикам закрыто всё, кроме ссылок с признаком crawlable
func (h *Handler) robots(w http.ResponseWriter, r *http.Request) string {
	codes, err := h.usecase.CrawlableCodes(r.Context())
	if err != nil {
		slog.Error("public: robots.txt failed", "error", err)
		h.respond(w, r, http.StatusInternalServerError, "", nil, false)
		return RouteRobots
	}

	var body bytes.Buffer
	body.WriteString(robotsHeader)
	for _, code := range codes {
		body.WriteString("Allow: /")
		body.WriteString(code)
		body.WriteByte('\n')
	}
	body.WriteString(robotsFooter)

	h.respond(w, r, http.StatusOK, "text/plain", body.Bytes(), false)

	return RouteRobots
}
