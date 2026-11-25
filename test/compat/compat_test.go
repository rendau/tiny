package compat

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	scenariosDir = "scenarios"
	goldenDir    = "golden"

	envTarget        = "TARGET_URL"     // адрес проверяемого сервера, например http://localhost:18081
	envAPIKey        = "API_KEY"        // ключ для X-Api-Key
	envRecord        = "COMPAT_RECORD"  // 1 — не сравнивать, а снять эталон с этого сервера
	envStrictHeaders = "STRICT_HEADERS" // 1 — «мягкие» заголовки тоже сравнивать строго
)

// TestCompat выполняет все сценарии против сервера из TARGET_URL и сравнивает ответы с эталоном
// (или, при COMPAT_RECORD=1, записывает ответы как новый эталон).
func TestCompat(t *testing.T) {
	target := os.Getenv(envTarget)
	if target == "" {
		t.Skipf("%s не задан — сверка пропущена (см. README.md)", envTarget)
	}
	apiKey := os.Getenv(envAPIKey)
	require.NotEmpty(t, apiKey, "%s не задан", envAPIKey)

	scenarios, err := LoadScenarios(scenariosDir)
	require.NoError(t, err)
	require.NotEmpty(t, scenarios)

	record := os.Getenv(envRecord) == "1"
	strictHeaders := os.Getenv(envStrictHeaders) == "1"

	runner := &Runner{Target: target, APIKey: apiKey, RunID: newRunID(t), Client: NewClient(ClientConfig{})}
	store := GoldenStore{Dir: goldenDir}

	t.Logf("сервер %s, метка прогона %s, сценариев %d", target, runner.RunID, len(scenarios))

	var (
		stepCount int
		noteCount int
		failed    []string
	)

	for _, sc := range scenarios {
		stepCount += len(sc.Steps)

		ok := t.Run(sc.ID, func(t *testing.T) {
			results := runner.RunScenario(t.Context(), sc)

			if record {
				recordScenario(t, store, sc, results)
				return
			}

			noteCount += compareScenario(t, store, sc, results, strictHeaders)
		})
		if !ok {
			failed = append(failed, sc.ID)
		}
	}

	if record {
		if len(failed) == 0 && flag.Lookup("test.run").Value.String() == "" {
			require.NoError(t, store.Prune(scenarios))
		}
		t.Logf("эталон записан: сценариев %d, шагов %d; не записано: %d %v",
			len(scenarios)-len(failed), stepCount, len(failed), failed)
		return
	}

	t.Logf("итог: сценариев %d, шагов %d; с расхождениями — %d %v; заметок о «мягких» заголовках — %d",
		len(scenarios), stepCount, len(failed), failed, noteCount)
}

func recordScenario(t *testing.T, store GoldenStore, sc *Scenario, results []StepResult) {
	t.Helper()

	observed := make([]*Observed, len(results))
	for i, res := range results {
		require.NoError(t, res.Err, "шаг %d «%s»: эталон не записан", i+1, sc.Steps[i].Name)
		// ответ 5xx — это сбой Shlink, а не поведение, которое стоит повторять
		require.Less(t, res.Observed.Status, 500,
			"шаг %d «%s»: Shlink ответил %d — такой шаг в эталон не берём", i+1, sc.Steps[i].Name, res.Observed.Status)
		observed[i] = res.Observed
	}

	require.NoError(t, store.Save(sc, observed))
}

// compareScenario сравнивает ответы с эталоном, возвращает число заметок.
func compareScenario(t *testing.T, store GoldenStore, sc *Scenario, results []StepResult, strictHeaders bool) int {
	t.Helper()

	golden, expected, err := store.Load(sc.ID)
	require.NoError(t, err, "нет эталона — сними его заново (README.md, «Снять эталон»)")
	require.Len(t, expected, len(sc.Steps), "в эталоне другое число шагов — сними эталон заново")

	notes := 0

	for i, res := range results {
		step := &sc.Steps[i]
		title := fmt.Sprintf("сценарий %s, шаг %d «%s»: %s %s [Host: %s]", sc.ID, i+1, step.Name, step.Method, step.Path, step.Host)

		req, err := goldenRequest(step)
		require.NoError(t, err)
		if !golden.Steps[i].Request.same(req) {
			t.Errorf("%s\n    запрос в сценарии изменился, а эталон старый — сними эталон заново", title)
			continue
		}

		if res.Observed == nil {
			t.Errorf("%s\n    %v", title, res.Err)
			continue
		}

		diffs, stepNotes := Compare(expected[i], res.Observed, strictHeaders)
		if res.Err != nil {
			diffs = append(diffs, res.Err.Error())
		}

		if len(diffs) > 0 {
			t.Errorf("%s\n    %s", title, strings.Join(diffs, "\n    "))
		}
		if len(stepNotes) > 0 {
			notes += len(stepNotes)
			t.Logf("%s\n    заметка (не ошибка): %s", title, strings.Join(stepNotes, "\n    заметка (не ошибка): "))
		}
	}

	return notes
}

// newRunID — метка прогона: 6 знаков из a-z и 0-9. Подставляется в адреса и свои коды, чтобы
// повторный прогон на том же стенде не натыкался на ссылки прошлого.
func newRunID(t *testing.T) string {
	t.Helper()

	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

	buf := make([]byte, 6)
	_, err := rand.Read(buf)
	require.NoError(t, err)
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}

	return string(buf)
}

// TestGoldenUpToDate — проверка без сервера: у каждого сценария есть эталон, снятый с тех же запросов.
func TestGoldenUpToDate(t *testing.T) {
	if os.Getenv(envRecord) == "1" {
		t.Skip("идёт запись эталона")
	}

	scenarios, err := LoadScenarios(scenariosDir)
	require.NoError(t, err)

	store := GoldenStore{Dir: goldenDir}

	for _, sc := range scenarios {
		golden, _, err := store.Load(sc.ID)
		if !assert.NoError(t, err, "сценарий %s: нет эталона", sc.ID) {
			continue
		}
		if !assert.Len(t, golden.Steps, len(sc.Steps), "сценарий %s: другое число шагов", sc.ID) {
			continue
		}
		for i := range sc.Steps {
			req, err := goldenRequest(&sc.Steps[i])
			require.NoError(t, err)
			assert.True(t, golden.Steps[i].Request.same(req), "сценарий %s, шаг %d: запрос изменился, эталон старый", sc.ID, i+1)
		}
	}
}
