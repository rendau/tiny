package link

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Эталон снят с настоящего Shlink 5.1.8: для каждого длинного адреса и строки параметров запроса —
// что Shlink отдал в заголовке Location. Наша сборка адреса должна давать то же самое.
func TestBuildRedirectUrlMatchesShlink(t *testing.T) {
	raw, err := os.ReadFile("testdata/shlink_redirects.json")
	require.NoError(t, err)

	var cases []struct {
		LongUrl      string `json:"longUrl"`
		ForwardQuery bool   `json:"forwardQuery"`
		Query        string `json:"query"`
		Location     string `json:"location"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.Greater(t, len(cases), 50)

	for _, c := range cases {
		got := buildRedirectUrl(c.LongUrl, c.Query, c.ForwardQuery)
		// пробелы по краям значения заголовка при передаче по HTTP отбрасываются
		assert.Equal(t, c.Location, strings.TrimSpace(got), "longUrl=%q query=%q forward=%v", c.LongUrl, c.Query, c.ForwardQuery)
	}
}

func TestBuildRedirectUrl(t *testing.T) {
	tests := []struct {
		name, longUrl, query string
		forward              bool
		want                 string
	}{
		{"без параметров", "https://demo.test/product/1", "", true, "https://demo.test/product/1"},
		{"параметры дописываются", "https://demo.test/p", "utm_source=sms&id=5", true, "https://demo.test/p?utm_source=sms&id=5"},
		{"одноимённый заменяется на месте", "https://demo.test/p?a=1&b=2", "b=9&c=3", true, "https://demo.test/p?a=1&b=9&c=3"},
		{"якорь остаётся в конце", "https://demo.test/p?a=1#top", "b=2", true, "https://demo.test/p?a=1&b=2#top"},
		{"forward_query выключен", "https://demo.test/p?a=1", "b=2", false, "https://demo.test/p?a=1"},
		{"плюс становится %20", "https://demo.test/p", "q=a+b", true, "https://demo.test/p?q=a%20b"},
		{"параметр без значения", "https://demo.test/p", "debug", true, "https://demo.test/p?debug"},
		{"схема и хост в нижний регистр, порт по умолчанию убран", "HTTPS://Demo.teST:443/Path", "", true, "https://demo.test/Path"},
		{"свой порт остаётся", "https://demo.test:8443/p", "", true, "https://demo.test:8443/p"},
		{"кириллица в пути кодируется", "https://demo.test/акция", "", true, "https://demo.test/%D0%B0%D0%BA%D1%86%D0%B8%D1%8F"},
		{"адрес приложения", "sample://product/1", "a=1", true, "sample://product/1?a=1"},
		{"адрес без хоста остаётся как есть", "https:/p", "a=1", true, "https:/p?a=1"},
		{"адрес без схемы — тоже", "/just/path?x=a:b", "a=9", true, "/just/path?x=a%3Ab&a=9"},
		{"незакрытый %", "https://demo.test/a%2?b=%2", "", true, "https://demo.test/a%252?b=%252"},
		{"порт по умолчанию у ftp", "ftp://h.kz:21/x", "", true, "ftp://h.kz/x"},
		{"имя параметра чувствительно к регистру", "https://h.kz/x?a=1&A=2", "a=9", true, "https://h.kz/x?a=9&A=2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildRedirectUrl(tt.longUrl, tt.query, tt.forward))
		})
	}
}

func TestQueryParseBuild(t *testing.T) {
	// разбор и сборка без слияния оставляют порядок и повторы
	for in, want := range map[string]string{
		"":                  "",
		"a=1&b=2":           "a=1&b=2",
		"a=1&a=2":           "a=1&a=2",
		"a":                 "a",
		"a=":                "a=",
		"=1":                "=1",
		"&&":                "&&",
		"a=%zz&b=%":         "a=%25zz&b=%25",
		"a%5B%5D=1":         "a%5B%5D=1",
		"a=1;b=2":           "a=1%3Bb%3D2",
		"x=%d0%bf":          "x=%D0%BF",
		"k=~-._":            "k=~-._",
		"k=!$'()*,":         "k=%21%24%27%28%29%2A%2C",
		"a=1&&b=2&=3&c==4":  "a=1&&=3&b=2&c=%3D4",
		"a=1&b=2&a=3":       "a=1&a=3&b=2",
		"пр=зн":             "%D0%BF%D1%80=%D0%B7%D0%BD",
		"a=b+c%2Bd%20e":     "a=b%20c%2Bd%20e",
		"a=%00%0a&b=%E2%82": "a=%00%0A&b=%E2%82",
	} {
		assert.Equal(t, want, parseQuery(in).build(), "query %q", in)
	}
}
