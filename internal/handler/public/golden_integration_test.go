//go:build integration

package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/tiny/internal/domain/link/linktest"
	domainLinkRepoDb "github.com/rendau/tiny/internal/domain/link/repo/db"
	domainLinkService "github.com/rendau/tiny/internal/domain/link/service"
	domainSessionService "github.com/rendau/tiny/internal/domain/session/service"
	usecase "github.com/rendau/tiny/internal/usecase/link"
)

const testApiKey = "links-api-key"

// goldenFile — testdata/shlink_golden.json: запросы и то, что на них ответил настоящий
// Shlink 5.1.8 (shlinkio/shlink:5.1.8-roadrunner, DEFAULT_DOMAIN=l.demo.test, IS_HTTPS_ENABLED=true,
// DEFAULT_SHORT_CODES_LENGTH=5, DISABLE_TRACKING=true, TIMEZONE=Asia/Almaty). У Shlink по умолчанию
// TIMEZONE не задана (даты в UTC) — этот случай сверяет test/compat; здесь пояс задан нарочно,
// чтобы проверить и пересчёт дат в пояс сервера.
type goldenFile struct {
	Host  string       `json:"host"`
	Cases []goldenCase `json:"cases"`
}

type goldenCase struct {
	Name    string            `json:"name"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
	ApiKey  *string           `json:"apiKey"`
	// GeneratedCode — код в ответе случайный: сравнивается только его длина
	GeneratedCode   bool              `json:"generatedCode"`
	Status          int               `json:"status"`
	ResponseHeaders map[string]string `json:"responseHeaders"`
	RequestId       bool              `json:"requestId"`
	ResponseBody    string            `json:"responseBody"`
}

var (
	dateCreatedRe = regexp.MustCompile(`"dateCreated":"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d([+-]\d\d:\d\d)"`)
	shortCodeRe   = regexp.MustCompile(`"shortCode":"([^"]*)"`)
)

// comparedHeaders — заголовки ответа, которые должны совпасть с Shlink (и не появляться лишними)
var comparedHeaders = []string{
	"Content-Type", "Content-Length", "Location", "Allow",
	"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age",
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()

	almaty, err := time.LoadLocation("Asia/Almaty")
	require.NoError(t, err)

	uc := usecase.New(
		domainLinkService.New(domainLinkRepoDb.New(linktest.PgPool(t))),
		domainSessionService.New("test-secret"),
		usecase.Config{DefaultDomain: "l.demo.test", HttpsEnabled: true, DefaultCodeLength: 5},
	)

	return New(uc, Config{ApiKeys: []string{"other-key", testApiKey}, TimeZone: almaty})
}

func do(h http.Handler, method, target string, headers map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		if strings.EqualFold(k, "Host") {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// normalizeBody убирает из тела то, что не может совпасть: время создания и случайный код
func normalizeBody(body string, generatedCode bool) string {
	body = dateCreatedRe.ReplaceAllString(body, `"dateCreated":"<now$1>"`)
	if generatedCode {
		if m := shortCodeRe.FindStringSubmatch(body); m != nil {
			mask := strings.Repeat("#", len(m[1]))
			body = strings.ReplaceAll(body, `/`+m[1]+`"`, `/`+mask+`"`)
			body = strings.ReplaceAll(body, `"shortCode":"`+m[1]+`"`, `"shortCode":"`+mask+`"`)
		}
	}
	return body
}

// Все запросы эталона по порядку (они зависят друг от друга: ссылка создаётся, потом читается и
// открывается) — и на каждый наш ответ должен совпасть с ответом Shlink: код, заголовки, тело.
func TestMatchesShlink(t *testing.T) {
	raw, err := os.ReadFile("testdata/shlink_golden.json")
	require.NoError(t, err)

	var golden goldenFile
	require.NoError(t, json.Unmarshal(raw, &golden))
	require.Greater(t, len(golden.Cases), 200)

	h := newTestHandler(t)

	for _, c := range golden.Cases {
		headers := map[string]string{}
		for k, v := range c.Headers {
			headers[k] = v
		}
		if c.ApiKey != nil {
			headers["X-Api-Key"] = *c.ApiKey
		}
		body := ""
		if c.Body != nil {
			body = *c.Body
		}

		w := do(h, c.Method, "http://"+golden.Host+c.Path, headers, body)

		ok := assert.Equal(t, c.Status, w.Code, "%s: код ответа", c.Name)
		for _, name := range comparedHeaders {
			// пробелы по краям значения заголовка при передаче по HTTP отбрасываются
			got := strings.TrimSpace(w.Header().Get(name))
			ok = assert.Equal(t, c.ResponseHeaders[name], got, "%s: заголовок %s", c.Name, name) && ok
		}
		ok = assert.Equal(t, c.RequestId, w.Header().Get("X-Request-Id") != "", "%s: X-Request-Id", c.Name) && ok
		ok = assert.Equal(t, normalizeBody(c.ResponseBody, c.GeneratedCode), normalizeBody(w.Body.String(), c.GeneratedCode), "%s: тело", c.Name) && ok

		if !ok {
			t.Logf("%s: %s %s", c.Name, c.Method, c.Path)
		}
	}
}

func TestRobots(t *testing.T) {
	h := newTestHandler(t)

	want, err := os.ReadFile("testdata/shlink_robots.txt")
	require.NoError(t, err)

	w := do(h, http.MethodGet, "/robots.txt", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
	assert.Equal(t, string(want), w.Body.String(), "пока открытых поисковикам ссылок нет — как у пустого Shlink")

	create := func(body string) {
		w := do(h, http.MethodPost, "/rest/v3/short-urls", map[string]string{"X-Api-Key": testApiKey}, body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	create(`{"longUrl":"https://example.invalid/1","customSlug":"zeta","crawlable":true}`)
	create(`{"longUrl":"https://example.invalid/2","customSlug":"Alpha","crawlable":true}`)
	create(`{"longUrl":"https://example.invalid/3","customSlug":"hidden"}`)

	w = do(h, http.MethodGet, "/robots.txt", nil, "")
	assert.Equal(t, "# For more information about the robots.txt standard, see:\n"+
		"# https://www.robotstxt.org/orig.html\n\n"+
		"User-agent: *\nAllow: /Alpha\nAllow: /zeta\nDisallow: /", w.Body.String())

	w = do(h, http.MethodHead, "/robots.txt", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "0", w.Header().Get("Content-Length"))
	assert.Empty(t, w.Body.String())
}

func TestFavicon(t *testing.T) {
	h := newTestHandler(t)

	w := do(h, http.MethodGet, "/favicon.ico", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/vnd.microsoft.icon", w.Header().Get("Content-Type"))
	assert.Equal(t, "Thu, 22 Oct 2026 09:54:23 GMT", w.Header().Get("Last-Modified"))
	assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
	assert.Equal(t, 16958, w.Body.Len(), "значок Shlink 5.1.8")

	w = do(h, http.MethodHead, "/favicon.ico", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "16958", w.Header().Get("Content-Length"))

	w = do(h, http.MethodGet, "/favicon.ico", map[string]string{"If-Modified-Since": "Thu, 22 Oct 2026 09:54:23 GMT"}, "")
	assert.Equal(t, http.StatusNotModified, w.Code)

	w = do(h, http.MethodPost, "/favicon.ico", nil, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestApiKeys(t *testing.T) {
	h := newTestHandler(t)
	body := `{"longUrl":"https://example.invalid/key"}`

	for _, key := range []string{testApiKey, "other-key"} {
		w := do(h, http.MethodPost, "/rest/v3/short-urls", map[string]string{"X-Api-Key": key}, body)
		assert.Equal(t, http.StatusOK, w.Code, "ключ %q", key)
	}
	for _, key := range []string{"wrong", "links-api-ke", "links-api-keyy", "LINKS-API-KEY", " "} {
		w := do(h, http.MethodPost, "/rest/v3/short-urls", map[string]string{"X-Api-Key": key}, body)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "ключ %q", key)
		assert.Contains(t, w.Body.String(), "invalid-api-key")
	}

	// «/rest» без продолжения: сначала ключ, потом «такого адреса нет» — как у Shlink
	w := do(h, http.MethodGet, "http://l.demo.test/rest", nil, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "missing-authentication")
	w = do(h, http.MethodGet, "http://l.demo.test/rest", map[string]string{"X-Api-Key": testApiKey}, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"detail":"Cannot GET http://l.demo.test!"`)
	w = do(h, http.MethodPost, "http://l.demo.test/rest", nil, "")
	assert.Equal(t, http.StatusNotFound, w.Code, "не GET — ключ не спрашивается")
	w = do(h, http.MethodGet, "http://l.demo.test/rest/", nil, "")
	assert.Equal(t, http.StatusNotFound, w.Code)

	// ключи не заданы — /rest/* закрыт всем, переход и health работают
	closed := New(h.usecase, Config{})
	w = do(closed, http.MethodPost, "/rest/v3/short-urls", map[string]string{"X-Api-Key": testApiKey}, body)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = do(closed, http.MethodGet, "/rest/health", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestObserveAndLimits(t *testing.T) {
	h := newTestHandler(t)

	type seen struct {
		route  string
		status int
	}
	var got []seen
	observed := New(h.usecase, Config{
		ApiKeys: []string{testApiKey},
		Observe: func(route string, status int, _ time.Duration) { got = append(got, seen{route, status}) },
	})

	key := map[string]string{"X-Api-Key": testApiKey}
	do(observed, http.MethodPost, "/rest/v3/short-urls", key, `{"longUrl":"https://example.invalid/o","customSlug":"obs"}`)
	do(observed, http.MethodGet, "/obs", nil, "")
	do(observed, http.MethodGet, "/nope", nil, "")
	do(observed, http.MethodGet, "/rest/v3/short-urls/obs", key, "")
	do(observed, http.MethodGet, "/rest/health", nil, "")
	do(observed, http.MethodGet, "/", nil, "")
	do(observed, http.MethodGet, "/rest/v3/zzz", nil, "")

	assert.Equal(t, []seen{
		{RouteShortUrlCreate, 200}, {RouteRedirect, 302}, {RouteRedirect, 404}, {RouteShortUrlGet, 200},
		{RouteHealth, 200}, {RouteNotFound, 404}, {RouteRestNotFound, 404},
	}, got)

	// тело больше предела не читается целиком
	big := `{"longUrl":"https://example.invalid/` + strings.Repeat("a", maxBodySize) + `"}`
	w := do(h, http.MethodPost, "/rest/v3/short-urls", key, big)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// по умолчанию (как у Shlink, где TIMEZONE не задана) даты в ответах — в UTC, от пояса
// пода не зависят; дата без пояса в запросе понимается как UTC
func TestDatesInUtcByDefault(t *testing.T) {
	prev := time.Local
	time.Local = time.FixedZone("+05", 5*3600) // у пода TZ=Asia/Almaty
	t.Cleanup(func() { time.Local = prev })

	h := New(newTestHandler(t).usecase, Config{ApiKeys: []string{testApiKey}})
	key := map[string]string{"X-Api-Key": testApiKey}

	w := do(h, http.MethodPost, "/rest/v3/short-urls", key,
		`{"longUrl":"https://example.invalid/utc","customSlug":"utc1","validUntil":"2099-12-05T10:01:00+05:00","validSince":"2020-01-01 10:00:00"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"validSince":"2020-01-01T10:00:00+00:00","validUntil":"2099-12-05T05:01:00+00:00"`)
	assert.Regexp(t, `"dateCreated":"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\+00:00"`, w.Body.String())

	w = do(h, http.MethodGet, "/rest/v3/short-urls/utc1", key, "")
	assert.Contains(t, w.Body.String(), `"validUntil":"2099-12-05T05:01:00+00:00"`)
}
