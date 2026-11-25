package public

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/tiny/internal/domain/link/model"
)

// bs — обратная косая черта: в ожидаемых строках коды символов собираются из частей, чтобы в
// исходнике не было самих экранирующих последовательностей
const bs = `\`

func uni(hex string) string { return bs + "u" + hex }

func TestAppendPhpString(t *testing.T) {
	tests := map[string]string{
		"":                      `""`,
		"abc/def":               `"abc/def"`,
		"a&b":                   `"a` + uni("0026") + `b"`,
		"<b>":                   `"` + uni("003C") + `b` + uni("003E") + `"`,
		"it's":                  `"it` + uni("0027") + `s"`,
		`say "hi"`:              `"say ` + uni("0022") + `hi` + uni("0022") + `"`,
		`back\slash`:            `"back` + bs + bs + `slash"`,
		"tab\tnl\ncr\rbs\bff\f": `"tab` + bs + `tnl` + bs + `ncr` + bs + `rbs` + bs + `bff` + bs + `f"`,
		"\x01\x1f":              `"` + uni("0001") + uni("001f") + `"`,
		"\x7f":                  "\"\x7f\"",
		"пр":                    `"` + uni("043f") + uni("0440") + `"`,
		"é":                     `"` + uni("00e9") + `"`,
		"😀":                     `"` + uni("d83d") + uni("de00") + `"`,
	}

	for in, want := range tests {
		assert.Equal(t, want, string(appendPhpString(nil, in)), "строка %q", in)
	}
}

func TestProblemString(t *testing.T) {
	assert.Equal(t, `"пр<&'"`, problemString("пр<&'"), "в ошибках не-ASCII и < & ' идут как есть")
	assert.Equal(t, `"say `+bs+`"hi`+bs+`""`, problemString(`say "hi"`))
	assert.Equal(t, `"https://shlink.io/api/error/x"`, problemString("https://shlink.io/api/error/x"))
}

func TestEncodeShortUrl(t *testing.T) {
	almaty := time.FixedZone("+05", 5*3600)
	created := time.Date(2026, 11, 25, 5, 1, 5, 999, time.UTC)

	got := string(encodeShortUrl(&model.Main{
		ShortUrl: "https://l.demo.test/56JzUe", ShortCode: "56JzUe", LongUrl: "https://example.com/a",
		CreatedAt: created.In(almaty), Tags: []string{}, ForwardQuery: true,
	}, time.UTC))
	assert.Contains(t, got, `"dateCreated":"2026-11-25T05:01:05+00:00"`, "по умолчанию даты в UTC — как у Shlink без TIMEZONE")

	got = string(encodeShortUrl(&model.Main{
		ShortUrl: "https://l.demo.test/56JzUe", ShortCode: "56JzUe", LongUrl: "https://example.com/a",
		CreatedAt: created, Tags: []string{}, ForwardQuery: true,
	}, almaty))
	assert.Equal(t, `{"shortUrl":"https://l.demo.test/56JzUe","shortCode":"56JzUe","longUrl":"https://example.com/a",`+
		`"dateCreated":"2026-11-25T10:01:05+05:00","tags":[],"meta":{"validSince":null,"validUntil":null,"maxVisits":null},`+
		`"domain":null,"title":null,"crawlable":false,"forwardQuery":true,`+
		`"visitsSummary":{"total":0,"nonBots":0,"bots":0},"hasRedirectRules":false}`, got)

	until := time.Date(2026, 12, 5, 5, 1, 0, 0, time.UTC)
	got = string(encodeShortUrl(&model.Main{
		ShortUrl: "https://sl.demo.test/x", ShortCode: "x", LongUrl: "https://example.com/a", CreatedAt: created,
		Tags: []string{"mail_sms", "b"}, ValidSince: &created, ValidUntil: &until, MaxVisits: new(int32(0)),
		Domain: "sl.demo.test", Title: "T", Crawlable: true,
	}, almaty))
	assert.Contains(t, got, `"tags":["mail_sms","b"],"meta":{"validSince":"2026-11-25T10:01:05+05:00","validUntil":"2026-12-05T10:01:00+05:00","maxVisits":0}`)
	assert.Contains(t, got, `"domain":"sl.demo.test","title":"T","crawlable":true,"forwardQuery":false,`)
	assert.True(t, json.Valid([]byte(got)))
}

func TestParseBody(t *testing.T) {
	fields, malformed := parseBody("application/json", []byte(`{"longUrl":"https://e.com","tags":["a"]}`))
	assert.False(t, malformed)
	assert.Len(t, fields, 2)

	// Content-Type не важен, пока это не форма
	for _, ct := range []string{"", "text/plain", "application/json; charset=utf-8"} {
		fields, malformed = parseBody(ct, []byte(` {"longUrl":"x:y"} `))
		assert.False(t, malformed, ct)
		assert.Contains(t, fields, "longUrl", ct)
	}

	_, malformed = parseBody("application/json", []byte(`{"longUrl":`))
	assert.True(t, malformed, "не JSON")

	for _, body := range []string{"", "  ", "[1,2]", `"str"`, "123", "null"} {
		fields, malformed = parseBody("application/json", []byte(body))
		assert.False(t, malformed, "тело %q — запрос без полей", body)
		assert.Empty(t, fields)
	}

	fields, _ = parseBody("application/x-www-form-urlencoded", []byte("longUrl=https%3A%2F%2Fe.com%2Fa%3Fb&tags[]=a&tags[]=b&maxVisits=5"))
	req, invalid := decodeCreateReq(fields, time.UTC)
	assert.Empty(t, invalid)
	assert.Equal(t, "https://e.com/a?b", req.LongUrl)
	assert.Equal(t, []string{"a", "b"}, req.Tags)
	assert.EqualValues(t, 5, *req.MaxVisits)

	fields, _ = parseBody("Application/X-WWW-Form-Urlencoded; charset=utf-8", []byte(`{"longUrl":"https://e.com"}`))
	assert.NotContains(t, fields, "longUrl", "JSON в теле формы Shlink не понимает")
}

func decode(t *testing.T, body string) (*model.CreateReq, []string) {
	t.Helper()
	return decodeIn(t, body, time.UTC)
}

func decodeIn(t *testing.T, body string, zone *time.Location) (*model.CreateReq, []string) {
	t.Helper()
	fields, malformed := parseBody("application/json", []byte(body))
	require.False(t, malformed, body)
	return decodeCreateReq(fields, zone)
}

func TestDecodeCreateReq(t *testing.T) {
	req, invalid := decode(t, `{"longUrl":"https://e.com","findIfExists":true,"tags":["a",5],"validUntil":"2026-12-05T10:01:00+05:00",
		"shortCodeLength":6,"maxVisits":"3","customSlug":12345,"domain":"sl.demo.test","title":77,"crawlable":"1","forwardQuery":0}`)
	assert.Empty(t, invalid)
	assert.Equal(t, "https://e.com", req.LongUrl)
	assert.True(t, req.FindIfExists)
	assert.Equal(t, []string{"a", "5"}, req.Tags)
	assert.Equal(t, 6, *req.ShortCodeLength)
	assert.EqualValues(t, 3, *req.MaxVisits, "число строкой")
	assert.Equal(t, "12345", *req.ShortCode, "строка числом")
	assert.Equal(t, "sl.demo.test", *req.Domain)
	assert.Equal(t, "77", *req.Title)
	assert.True(t, *req.Crawlable)
	assert.False(t, *req.ForwardQuery)

	// нет поля и null — «не задано»
	req, invalid = decode(t, `{"longUrl":"https://e.com","title":null,"validUntil":null,"validSince":null,"maxVisits":null,
		"customSlug":null,"domain":null,"shortCodeLength":null}`)
	assert.Empty(t, invalid)
	assert.Nil(t, req.Title)
	assert.Nil(t, req.ValidUntil)
	assert.Nil(t, req.MaxVisits)
	assert.Nil(t, req.ShortCode)
	assert.Nil(t, req.Domain)
	assert.Nil(t, req.ShortCodeLength)
	assert.Nil(t, req.Crawlable)
	assert.Nil(t, req.ForwardQuery)
	assert.False(t, req.FindIfExists)
	assert.Nil(t, req.Tags)

	// что Shlink считает неверным
	bad := map[string]string{
		`{"longUrl":true}`:            fieldLongUrl,
		`{"longUrl":["x"]}`:           fieldLongUrl,
		`{"tags":null}`:               fieldTags,
		`{"tags":"x"}`:                fieldTags,
		`{"tags":{"a":"x"}}`:          fieldTags,
		`{"tags":[null]}`:             fieldTags,
		`{"crawlable":null}`:          fieldCrawlable,
		`{"crawlable":"yes"}`:         fieldCrawlable,
		`{"forwardQuery":5}`:          fieldForwardQuery,
		`{"findIfExists":""}`:         fieldFindIfExists,
		`{"findIfExists":null}`:       fieldFindIfExists,
		`{"maxVisits":1.5}`:           fieldMaxVisits,
		`{"maxVisits":""}`:            fieldMaxVisits,
		`{"maxVisits":"abc"}`:         fieldMaxVisits,
		`{"maxVisits":99999999999}`:   fieldMaxVisits,
		`{"shortCodeLength":5.5}`:     fieldShortCodeLength,
		`{"shortCodeLength":""}`:      fieldShortCodeLength,
		`{"validUntil":"qqq"}`:        fieldValidUntil,
		`{"validSince":true}`:         fieldValidSince,
		`{"validSince":"2026-13-45"}`: fieldValidSince,
		`{"domain":["a"]}`:            fieldDomain,
		`{"customSlug":{"a":1}}`:      fieldCustomSlug,
		`{"title":["a"]}`:             fieldTitle,
	}
	for body, field := range bad {
		_, invalid = decode(t, body)
		assert.Equal(t, []string{field}, invalid, "тело %s", body)
	}

	_, invalid = decode(t, `{"forwardQuery":5,"tags":"x","maxVisits":1.5,"validUntil":"qqq"}`)
	assert.Equal(t, []string{fieldValidUntil, fieldMaxVisits, fieldTags, fieldForwardQuery}, sortInvalidElements(invalid),
		"порядок — как в invalidElements у Shlink")
}

func TestDecodeDates(t *testing.T) {
	almaty := time.FixedZone("+05", 5*3600)

	want := time.Date(2026, 12, 5, 10, 1, 0, 0, almaty)

	for _, v := range []string{
		`"2026-12-05T10:01:00+05:00"`,           // delivery
		`"2026-12-05T10:01:00.123456789+05:00"`, // RFC3339 от Go
		`"2026-12-05T05:01:00Z"`,
		`"2026-12-05T05:01:00.000Z"`,
		`"2026-12-05T10:01:00+0500"`,
		`"2026-12-05T10:01:00"`, // без пояса — в поясе сервера
		`"2026-12-05 10:01:00"`,
		`"2026-12-05 10:01"`,
		`"2026-12-05T10:01"`,
		`"05.12.2026 10:01:00"`,
		`"05.12.2026 10:01"`,
		`"20261205T100100"`,
		`" 2026-12-05T10:01:00+05:00 "`,
		`1796446860`, // время Unix
	} {
		req, invalid := decodeIn(t, `{"validUntil":`+v+`}`, almaty)
		require.Empty(t, invalid, "дата %s", v)
		require.NotNil(t, req.ValidUntil, "дата %s", v)
		assert.True(t, want.Equal(req.ValidUntil.Truncate(time.Second)), "дата %s разобрана как %s", v, req.ValidUntil)
	}

	req, invalid := decodeIn(t, `{"validUntil":"2026-12-05","validSince":"05.12.2026"}`, almaty)
	require.Empty(t, invalid)
	assert.True(t, time.Date(2026, 12, 5, 0, 0, 0, 0, almaty).Equal(*req.ValidUntil), "только дата — полночь в поясе сервера")
	assert.True(t, req.ValidUntil.Equal(*req.ValidSince))

	// пояс сервера по умолчанию — UTC: дата без пояса понимается как UTC
	req, invalid = decode(t, `{"validUntil":"2026-12-05T10:01:00"}`)
	require.Empty(t, invalid)
	assert.True(t, time.Date(2026, 12, 5, 10, 1, 0, 0, time.UTC).Equal(*req.ValidUntil))

	req, invalid = decode(t, `{"validUntil":""}`)
	assert.Empty(t, invalid)
	assert.Nil(t, req.ValidUntil, "пустая строка — не задано")
}

func TestCollapseSlashesAndRequestId(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/": "/", "//abc": "/abc", "///a//b/": "/a/b/", "/rest//health": "/rest/health"} {
		assert.Equal(t, want, collapseSlashes(in), in)
	}

	id := newRequestId()
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, id)
	assert.NotEqual(t, id, newRequestId())

	assert.True(t, isShortUrlPath("/short-urls/abc"))
	for _, path := range []string{"/short-urls", "/short-urls/", "/short-urls/a/b", "/short-urlsx/a"} {
		assert.False(t, isShortUrlPath(path), path)
	}
	assert.True(t, strings.HasPrefix(healthBodyPass, `{"status":"pass"`))
}

// Ключ, заданный хешем (как в api_keys.key у Shlink), принимается наравне с ключом открытым текстом
func TestApiKeyHashes(t *testing.T) {
	sum := sha256.Sum256([]byte("key-by-hash"))
	hash := hex.EncodeToString(sum[:])

	require.NoError(t, CheckApiKeyHashes([]string{hash, "", " " + strings.ToUpper(hash) + " "}))
	require.Error(t, CheckApiKeyHashes([]string{hash, "not-a-hash"}))
	require.Error(t, CheckApiKeyHashes([]string{hash[:62]}), "короткая строка — не хеш")
	err := CheckApiKeyHashes([]string{"key-by-hash"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "key-by-hash", "значение не попадает в текст ошибки")

	// GET /rest: сначала проверяется ключ (401), потом «такого адреса нет» (404) — до хранилища не доходит
	status := func(h *Handler, key string) int {
		r := httptest.NewRequest(http.MethodGet, "http://l.demo.test/rest", nil)
		if key != "" {
			r.Header.Set("X-Api-Key", key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	h := New(nil, Config{ApiKeys: []string{"plain-key"}, ApiKeyHashes: []string{strings.ToUpper(hash), "not-a-hash"}})
	assert.Equal(t, http.StatusNotFound, status(h, "key-by-hash"), "ключ из хеша")
	assert.Equal(t, http.StatusNotFound, status(h, "plain-key"), "ключ открытым текстом")
	assert.Equal(t, http.StatusUnauthorized, status(h, hash), "сам хеш ключом не является")
	assert.Equal(t, http.StatusUnauthorized, status(h, "not-a-hash"), "негодная строка ключом не стала")
	assert.Equal(t, http.StatusUnauthorized, status(h, "other"))
	assert.Equal(t, http.StatusUnauthorized, status(h, ""))

	onlyHashes := New(nil, Config{ApiKeyHashes: []string{hash}})
	assert.Equal(t, http.StatusNotFound, status(onlyHashes, "key-by-hash"))
	assert.Equal(t, http.StatusUnauthorized, status(onlyHashes, "plain-key"))
}

// captureLog подменяет журнал на время теста и возвращает разбор записанных строк
func captureLog(t *testing.T) func() []map[string]any {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []map[string]any {
		var lines []map[string]any
		for raw := range strings.Lines(buf.String()) {
			line := map[string]any{}
			require.NoError(t, json.Unmarshal([]byte(raw), &line))
			lines = append(lines, line)
		}
		buf.Reset()
		return lines
	}
}

// Журнал /rest/*: отказы пишутся всегда (без тела запроса), тело — только по LogCreateRequests;
// сам ключ в журнал не попадает, только отпечаток
func TestRestLog(t *testing.T) {
	const key, wrongKey = "log-test-key", "someone-elses-key"
	sum := sha256.Sum256([]byte(key))
	wrongSum := sha256.Sum256([]byte(wrongKey))
	logged := captureLog(t)

	post := func(h *Handler, apiKey, body string) int {
		r := httptest.NewRequest(http.MethodPost, "http://l.demo.test/rest/v3/short-urls", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("User-Agent", "ExampleClient/1.0")
		r.Header.Set("Cf-Connecting-Ip", "203.0.113.7")
		if apiKey != "" {
			r.Header.Set("X-Api-Key", apiKey)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	// битое тело: отказ 400 до обращения к хранилищу
	const badBody = `{"longUrl":"https://example.invalid/?phone=1","validUntil":`

	// тела выключены
	quiet := New(nil, Config{ApiKeys: []string{key}})
	require.Equal(t, http.StatusBadRequest, post(quiet, key, badBody))
	lines := logged()
	require.Len(t, lines, 1)
	line := lines[0]
	assert.Equal(t, "public: rest request rejected", line["msg"])
	assert.Equal(t, RouteShortUrlCreate, line["route"])
	assert.EqualValues(t, http.StatusBadRequest, line["status"])
	assert.Equal(t, hex.EncodeToString(sum[:4]), line["key"])
	assert.Equal(t, true, line["key_valid"])
	assert.Equal(t, "203.0.113.7", line["client"])
	assert.Equal(t, "ExampleClient/1.0", line["user_agent"])
	assert.Equal(t, "application/json", line["content_type"])
	assert.EqualValues(t, len(badBody), line["body_bytes"])
	assert.NotContains(t, line, "body", "без LogCreateRequests тело запроса не пишется")
	assert.Contains(t, line["response"], "shlink.io/api/error", "в журнале — ответ с причиной отказа")

	// тела включены
	verbose := New(nil, Config{ApiKeys: []string{key}, LogCreateRequests: true})
	require.Equal(t, http.StatusBadRequest, post(verbose, key, badBody))
	lines = logged()
	require.Len(t, lines, 1)
	assert.Equal(t, badBody, lines[0]["body"])

	// очень длинное тело в журнале обрезается
	long := `{"longUrl":"` + strings.Repeat("я", logBodyLimit) + `"`
	require.Equal(t, http.StatusBadRequest, post(verbose, key, long))
	lines = logged()
	require.Len(t, lines, 1)
	body, _ := lines[0]["body"].(string)
	assert.LessOrEqual(t, len(body), logBodyLimit+len("…"))
	assert.True(t, strings.HasSuffix(body, "…"))
	assert.EqualValues(t, len(long), lines[0]["body_bytes"])

	// чужой ключ: виден его отпечаток, но не сам ключ; тело не читалось
	require.Equal(t, http.StatusUnauthorized, post(verbose, wrongKey, badBody))
	lines = logged()
	require.Len(t, lines, 1)
	line = lines[0]
	assert.Equal(t, hex.EncodeToString(wrongSum[:4]), line["key"])
	assert.Equal(t, false, line["key_valid"])
	assert.NotContains(t, line, "body")
	for _, v := range line {
		assert.NotEqual(t, wrongKey, v, "сам ключ в журнал не попадает")
	}

	// без ключа
	require.Equal(t, http.StatusUnauthorized, post(verbose, "", badBody))
	lines = logged()
	require.Len(t, lines, 1)
	assert.Equal(t, "", lines[0]["key"])

	// сканеры по неизвестным адресам /rest/* журнал не засоряют
	r := httptest.NewRequest(http.MethodGet, "http://l.demo.test/rest/settings", nil)
	verbose.ServeHTTP(httptest.NewRecorder(), r)
	assert.Empty(t, logged())
}
