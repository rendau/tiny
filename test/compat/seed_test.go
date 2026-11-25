package compat

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	htmlTagRe  = regexp.MustCompile(`<[^>]*>`)
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// shlinkTag — метка в том виде, к которому её приводит Shlink: без html, без пробелов по краям,
// строчными буквами, пробелы внутри заменены дефисами.
func shlinkTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(htmlTagRe.ReplaceAllString(tag, "")))
	return strings.ReplaceAll(tag, " ", "-")
}

func readLines(t *testing.T, file string) []string {
	t.Helper()

	data, err := os.ReadFile(file)
	require.NoError(t, err)

	var result []string
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

// TestSeedCoversScenarios — каждая метка и каждый домен из сценариев заведены на стенде заранее
// (seed-tags.txt, seed-domains.txt). Иначе настоящий Shlink зависнет на втором запросе с ними:
// см. README.md, «Что выяснилось».
func TestSeedCoversScenarios(t *testing.T) {
	scenarios, err := LoadScenarios(scenariosDir)
	require.NoError(t, err)

	seedTags := readLines(t, "seed-tags.txt")
	seedDomains := readLines(t, "seed-domains.txt")

	for _, sc := range scenarios {
		for i := range sc.Steps {
			body, ok := sc.Steps[i].JSON.(map[string]any)
			if !ok {
				continue
			}

			if tags, ok := body["tags"].([]any); ok {
				for _, item := range tags {
					if tag, ok := item.(string); ok {
						assert.Contains(t, seedTags, shlinkTag(tag),
							"сценарий %s, шаг %d: метки %q нет в seed-tags.txt", sc.ID, i+1, tag)
					}
				}
			}

			// неверные значения domain Shlink отвергает до обращения к базе — их заводить не нужно
			if domain, ok := body["domain"].(string); ok && hostnameRe.MatchString(domain) && domain != DefaultHost {
				assert.Contains(t, seedDomains, domain,
					"сценарий %s, шаг %d: домена %q нет в seed-domains.txt", sc.ID, i+1, domain)
			}
		}
	}
}
