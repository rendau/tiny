package public

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/samber/lo"
)

// Ошибки /rest/* — application/problem+json в том виде, в каком их отдаёт Shlink: сначала свои поля
// ошибки, затем title, type, status, detail.

const problemTypePrefix = "https://shlink.io/api/error/"

// problemField — своё поле ошибки: имя и уже готовое значение JSON
type problemField struct {
	name  string
	value string
}

func (h *Handler) problem(w http.ResponseWriter, r *http.Request, status int, kind, title, detail string, fields ...problemField) {
	var sb strings.Builder

	sb.WriteByte('{')
	for _, field := range fields {
		sb.WriteString(problemString(field.name))
		sb.WriteByte(':')
		sb.WriteString(field.value)
		sb.WriteByte(',')
	}
	sb.WriteString(`"title":`)
	sb.WriteString(problemString(title))
	sb.WriteString(`,"type":`)
	sb.WriteString(problemString(problemTypePrefix + kind))
	sb.WriteString(`,"status":`)
	sb.WriteString(strconv.Itoa(status))
	sb.WriteString(`,"detail":`)
	sb.WriteString(problemString(detail))
	sb.WriteByte('}')

	h.respond(w, r, status, "application/problem+json", []byte(sb.String()), false)
}

func (h *Handler) problemMissingAuth(w http.ResponseWriter, r *http.Request) {
	h.problem(w, r, http.StatusUnauthorized, "missing-authentication", "Invalid authorization",
		`Expected one of the following authentication headers, ["X-Api-Key"], but none were provided`,
		problemField{name: "expectedHeaders", value: `["X-Api-Key"]`},
	)
}

func (h *Handler) problemInvalidApiKey(w http.ResponseWriter, r *http.Request) {
	h.problem(w, r, http.StatusUnauthorized, "invalid-api-key", "Invalid API key",
		"Provided API key does not exist or is invalid.",
	)
}

func (h *Handler) problemMalformedBody(w http.ResponseWriter, r *http.Request) {
	h.problem(w, r, http.StatusBadRequest, "malformed-request-body", "Malformed request body",
		"Provided request does not contain a valid JSON body.",
	)
}

func (h *Handler) problemInvalidData(w http.ResponseWriter, r *http.Request, invalidElements []string) {
	elements := lo.Map(invalidElements, func(name string, _ int) string { return problemString(name) })

	h.problem(w, r, http.StatusBadRequest, "invalid-data", "Invalid data", "Provided data is not valid",
		problemField{name: "invalidElements", value: "[" + strings.Join(elements, ",") + "]"},
	)
}

// problemNonUniqueSlug — свой код уже занят; domain — пусто для основного домена
func (h *Handler) problemNonUniqueSlug(w http.ResponseWriter, r *http.Request, slug, domain string) {
	fields := []problemField{{name: "customSlug", value: problemString(slug)}}
	detail := `Provided slug "` + slug + `" is already in use`
	if domain != "" {
		fields = append(fields, problemField{name: "domain", value: problemString(domain)})
		detail += ` for domain "` + domain + `"`
	}

	h.problem(w, r, http.StatusBadRequest, "non-unique-slug", "Invalid custom slug", detail+".", fields...)
}

// problemShortUrlNotFound — ссылки с таким кодом нет; domain — nil, если домен в запросе не назван
func (h *Handler) problemShortUrlNotFound(w http.ResponseWriter, r *http.Request, shortCode string, domain *string) {
	fields := []problemField{{name: "shortCode", value: problemString(shortCode)}}
	detail := `No URL found with short code "` + shortCode + `"`
	if domain != nil {
		fields = append(fields, problemField{name: "domain", value: problemString(*domain)})
		detail += ` for domain "` + *domain + `"`
	}

	h.problem(w, r, http.StatusNotFound, "short-url-not-found", "Short URL not found", detail, fields...)
}

// problemNotFound — такого адреса в /rest/* нет (или метод не тот)
func (h *Handler) problemNotFound(w http.ResponseWriter, r *http.Request, restPath string) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	target := scheme + "://" + r.Host + restPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	h.problem(w, r, http.StatusNotFound, "not-found", "Not Found", "Cannot "+r.Method+" "+target+"!")
}

func (h *Handler) problemInternal(w http.ResponseWriter, r *http.Request) {
	h.problem(w, r, http.StatusInternalServerError, "internal-server-error", "Internal Server Error",
		"An unknown error occurred.",
	)
}
