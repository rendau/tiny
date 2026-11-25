package compat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Проверки самого прогона: сравнение обязано замечать расхождения и объяснять их.

func observedJSON(t *testing.T, status int, headers map[string]string, body string) *Observed {
	t.Helper()

	parsed, err := parseJSON([]byte(body))
	require.NoError(t, err)

	return &Observed{Status: status, Headers: headers, BodyKind: bodyJSON, Body: parsed.Pretty()}
}

func TestParseJSONKeepsFieldOrder(t *testing.T) {
	v, err := parseJSON([]byte(`{"b":1,"a":{"z":null,"y":[true,"x"]},"c":1.50}`))
	require.NoError(t, err)

	assert.Equal(t, []string{"b", "a", "c"}, memberKeys(v))
	assert.Equal(t, []string{"z", "y"}, memberKeys(v.Get("a")))
	assert.Equal(t, "1.50", v.Get("c").Lit, "число хранится как записано")
	assert.Nil(t, v.Get("a.nope"))

	again, err := parseJSON([]byte(v.Pretty()))
	require.NoError(t, err)
	assert.Equal(t, v.Pretty(), again.Pretty())

	_, err = parseJSON([]byte(`{"a":1} лишнее`))
	assert.Error(t, err)
}

func TestCompareSame(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/json", "X-Request-Id": "<uuid>"}
	want := observedJSON(t, 200, headers, `{"a":1,"b":[1,2]}`)
	got := observedJSON(t, 200, headers, `{ "a": 1, "b": [1, 2] }`)

	diffs, notes := Compare(want, got, false)
	assert.Empty(t, diffs)
	assert.Empty(t, notes)
}

func TestCompareFindsDifferences(t *testing.T) {
	want := observedJSON(t, 200,
		map[string]string{"Content-Type": "application/json", "Content-Length": "10", "X-Request-Id": "<uuid>"},
		`{"shortCode":"${code}","domain":null,"tags":[]}`)

	t.Run("код ответа", func(t *testing.T) {
		got := *want
		got.Status = 201
		diffs, _ := Compare(want, &got, false)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], "ждали 200, пришло 201")
	})

	t.Run("значимый заголовок", func(t *testing.T) {
		got := *want
		got.Headers = map[string]string{"Content-Type": "application/json; charset=utf-8", "Content-Length": "10", "X-Request-Id": "<uuid>"}
		diffs, notes := Compare(want, &got, false)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], "заголовок Content-Type")
		assert.Empty(t, notes)
	})

	t.Run("мягкие заголовки — заметка, при строгом режиме — ошибка", func(t *testing.T) {
		got := *want
		got.Headers = map[string]string{"Content-Type": "application/json", "Content-Length": "12"}

		diffs, notes := Compare(want, &got, false)
		assert.Empty(t, diffs)
		assert.Len(t, notes, 2, "Content-Length и X-Request-Id")

		diffs, notes = Compare(want, &got, true)
		assert.Len(t, diffs, 2)
		assert.Empty(t, notes)
	})

	t.Run("лишний заголовок", func(t *testing.T) {
		got := *want
		got.Headers = map[string]string{
			"Content-Type": "application/json", "Content-Length": "10", "X-Request-Id": "<uuid>",
			"Cache-Control": "no-store", "X-Powered-By": "tiny",
		}
		diffs, notes := Compare(want, &got, false)
		require.Len(t, diffs, 1, "Cache-Control меняет поведение клиента")
		assert.Contains(t, diffs[0], "Cache-Control")
		require.Len(t, notes, 1)
		assert.Contains(t, notes[0], "X-Powered-By")
	})

	t.Run("порядок полей", func(t *testing.T) {
		got := observedJSON(t, 200, want.Headers, `{"domain":null,"shortCode":"${code}","tags":[]}`)
		diffs, _ := Compare(want, got, false)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], "другой порядок полей: ждали shortCode, domain, tags, пришло domain, shortCode, tags")
	})

	t.Run("нет поля и лишнее поле", func(t *testing.T) {
		got := observedJSON(t, 200, want.Headers, `{"shortCode":"${code}","tags":[],"extra":1}`)
		diffs, _ := Compare(want, got, false)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], `нет поля "domain"`)
		assert.Contains(t, diffs[0], `лишнее поле "extra"`)
	})

	t.Run("значение поля", func(t *testing.T) {
		got := observedJSON(t, 200, want.Headers, `{"shortCode":"${code}","domain":"","tags":[]}`)
		diffs, _ := Compare(want, got, false)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], `-   "domain": null,`)
		assert.Contains(t, diffs[0], `+   "domain": "",`)
	})

	t.Run("вид тела", func(t *testing.T) {
		got := &Observed{Status: 200, Headers: want.Headers, BodyKind: bodyText, Body: "<a href=\"x\">Found</a>"}
		diffs, _ := Compare(want, got, false)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], "ждали JSON, пришло текст")
	})

	t.Run("пустое тело против непустого", func(t *testing.T) {
		empty := &Observed{Status: 302, Headers: map[string]string{"Location": "https://x"}, BodyKind: bodyEmpty}
		got := &Observed{Status: 302, Headers: map[string]string{"Location": "https://x"}, BodyKind: bodyText, Body: "<a href=\"https://x\">Found</a>.\n"}
		diffs, _ := Compare(empty, got, false)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], "ждали пустое тело, пришло текст")
	})

	t.Run("тело не сравнивается", func(t *testing.T) {
		ignored := &Observed{Status: 200, Headers: map[string]string{}, BodyKind: bodyIgnored}
		got := &Observed{Status: 200, Headers: map[string]string{}, BodyKind: bodyIgnored}
		diffs, _ := Compare(ignored, got, false)
		assert.Empty(t, diffs)
	})
}

func TestLineDiff(t *testing.T) {
	diff := lineDiff("a\nb\nc\nd\ne\nf\ng", "a\nb\nc\nX\ne\nf\ng")
	assert.Equal(t, "  …\n  b\n  c\n- d\n+ X\n  e\n  f\n  …\n", diff)
}

func TestVarsExpandAndNormalize(t *testing.T) {
	v := newVars("r1u2n3")
	v.set("codeOld", "aB3dE")
	v.set("code", "aB3dE") // значения одинаковые — в ответе окажется имя той, что сохранена раньше

	got, err := v.expand("/${code}?x=${run}&p=${pad:40}")
	require.NoError(t, err)
	assert.Equal(t, "/aB3dE?x=r1u2n3&p="+padString(40), got)

	_, err = v.expand("/${nope}")
	assert.Error(t, err, "неизвестная переменная")

	assert.Equal(t, "https://l.demo.test/${codeOld} ${run} ${pad:40}",
		v.normalize("https://l.demo.test/aB3dE r1u2n3 "+padString(40)))
}

func TestNormalizeJSONRules(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	step := &Step{Path: "/x", Mask: map[string]string{"version": `version =~ ^\d+\.\d+\.\d+$`}}
	require.NoError(t, step.prepare())

	v := newVars("r1u2n3")
	v.set("code", "aB3dE")

	body, err := parseJSON([]byte(`{
		"shortUrl": "https://l.demo.test/aB3dE",
		"dateCreated": "2026-11-25T10:00:07+00:00",
		"old": {"dateCreated": "2020-01-01T00:00:00+00:00"},
		"almaty": {"dateCreated": "2026-11-25T15:00:07+05:00"},
		"validUntil": "2026-11-25T10:00:07+00:00",
		"version": "5.1.8",
		"longUrl": "https://shop.example.invalid/r1u2n3/a",
		"count": 3
	}`))
	require.NoError(t, err)

	normalizeJSON(body, step, v, now)

	assert.Equal(t, "https://l.demo.test/${code}", body.Get("shortUrl").Str)
	assert.Equal(t, "<now+00:00>", body.Get("dateCreated").Str)
	assert.Equal(t, "2020-01-01T00:00:00+00:00", body.Get("old.dateCreated").Str, "не «сейчас» — остаётся как есть")
	assert.Equal(t, "<now+05:00>", body.Get("almaty.dateCreated").Str, "смещение сохраняется")
	assert.Equal(t, "2026-11-25T10:00:07+00:00", body.Get("validUntil").Str, "правило только для dateCreated")
	assert.Equal(t, "<version>", body.Get("version").Str)
	assert.Equal(t, "https://shop.example.invalid/${run}/a", body.Get("longUrl").Str)
	assert.Equal(t, "3", body.Get("count").Lit)
}

func TestFilterLines(t *testing.T) {
	step := &Step{Path: "/robots.txt", OnlyThisRunLines: `^Allow: /`}
	require.NoError(t, step.prepare())

	text := "User-agent: *\nAllow: /old-abc\nAllow: /all-r1u2n3\nDisallow: /"
	assert.Equal(t, "User-agent: *\nAllow: /all-r1u2n3\nDisallow: /", filterLines(text, step.lines, "r1u2n3"))
}

// TestRunnerAgainstFakeServer — сквозная проверка: сценарий выполняется против подставного сервера,
// переменные берутся из ответа, перенаправление не выполняется, отличия от «эталона» находятся.
func TestRunnerAgainstFakeServer(t *testing.T) {
	var hosts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)

		switch {
		case r.Method == http.MethodPost && r.URL.Path == createPath:
			if r.Header.Get("X-Api-Key") != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"shortUrl":"https://l.demo.test/Ab3De","shortCode":"Ab3De","dateCreated":"` +
				time.Now().UTC().Format("2006-01-02T15:04:05-07:00") + `"}`))
		case r.URL.Path == "/Ab3De":
			w.Header().Set("Location", "https://shop.example.invalid/target?"+r.URL.RawQuery)
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	sc := &Scenario{ID: "fake", Steps: []Step{
		{Create: map[string]any{"longUrl": "https://shop.example.invalid/${run}/x"}, Save: map[string]string{"code": `shortCode =~ ^[0-9a-zA-Z]{5}$`}},
		{Path: "/${code}?a=${run}", Host: "sl.demo.test"},
		{Method: "POST", Path: createPath, JSON: map[string]any{"longUrl": "x"}},
	}}
	require.NoError(t, sc.prepare())

	runner := &Runner{Target: srv.URL, APIKey: "secret", RunID: "r1u2n3", Client: NewClient(ClientConfig{})}
	results := runner.RunScenario(t.Context(), sc)
	require.Len(t, results, 3)
	for _, res := range results {
		require.NoError(t, res.Err)
	}

	assert.Equal(t, []string{DefaultHost, "sl.demo.test", DefaultHost}, hosts, "заголовок Host берётся из шага")

	created := results[0].Observed
	assert.Equal(t, 200, created.Status)
	assert.Contains(t, created.Body, `"shortCode": "${code}"`)
	assert.Contains(t, created.Body, `"shortUrl": "https://l.demo.test/${code}"`)
	assert.Contains(t, created.Body, `"dateCreated": "<now+00:00>"`)

	redirect := results[1].Observed
	assert.Equal(t, 302, redirect.Status, "перенаправление видно как есть")
	assert.Equal(t, "https://shop.example.invalid/target?a=${run}", redirect.Headers["Location"])
	assert.NotContains(t, redirect.Headers, "Date")

	assert.Equal(t, 401, results[2].Observed.Status, "без key ключ не отправляется")

	want := &Observed{Status: 302, Headers: map[string]string{"Location": "https://shop.example.invalid/other"}, BodyKind: bodyEmpty}
	diffs, _ := Compare(want, redirect, false)
	require.Len(t, diffs, 1)
	assert.True(t, strings.HasPrefix(diffs[0], "заголовок Location: ждали"))
}
