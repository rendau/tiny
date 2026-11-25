package compat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	bodyEmpty   = "empty"   // тела нет
	bodyJSON    = "json"    // JSON: сравниваются значения и порядок полей
	bodyText    = "text"    // текст: сравнивается буква в букву
	bodyBinary  = "binary"  // не текст: сравниваются длина и sha256
	bodyIgnored = "ignored" // skip_body: не сравнивается

	// nowTolerance — на сколько dateCreated может отличаться от часов прогона, чтобы считаться «сейчас».
	nowTolerance = 10 * time.Minute

	userAgent = "tiny-compat/1.0"
)

// ignoredHeaders — заголовки, которые не попадают в эталон вовсе.
var ignoredHeaders = []string{"Date"}

// softHeaders — заголовки, расхождение в которых не считается ошибкой (печатается как заметка):
// клиенты на них не опираются, а точное совпадение потребовало бы повторять Shlink байт в байт.
// При STRICT_HEADERS=1 они сравниваются строго.
var softHeaders = []string{
	"X-Request-Id", "Content-Length", "Transfer-Encoding", "Last-Modified", "Accept-Ranges",
	"Connection", "Keep-Alive", "Server",
}

// meaningfulHeaders — заголовки, которые меняют поведение клиента: если проверяемый сервер
// прислал такой, а в эталоне его нет, это ошибка. Прочие лишние заголовки — только заметка.
var meaningfulHeaders = []string{
	"Location", "Content-Type", "Cache-Control", "Expires", "Pragma", "Set-Cookie",
	"Www-Authenticate", "Allow", "Content-Encoding", "Vary", "Refresh", "Link",
	"Content-Disposition", "X-Robots-Tag",
}

func isSoftHeader(name string) bool {
	return slices.Contains(softHeaders, http.CanonicalHeaderKey(name))
}

func isMeaningfulHeader(name string) bool {
	name = http.CanonicalHeaderKey(name)
	return slices.Contains(meaningfulHeaders, name) || strings.HasPrefix(name, "Access-Control-")
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// дата в том виде, в каком её отдаёт Shlink: без долей секунды, со смещением
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}([+-]\d{2}:\d{2})$`)
)

// Observed — ответ сервера после приведения к стабильному виду.
type Observed struct {
	Status   int
	Headers  map[string]string
	BodyKind string
	Body     string // json — запись с отступами; text — текст; binary — "sha256:… (N байт)"
}

// Runner выполняет сценарии против одного сервера.
type Runner struct {
	Target string // адрес сервера, например http://localhost:18081
	APIKey string
	RunID  string // метка прогона: делает адреса и свои коды уникальными
	Client *http.Client
}

// StepResult — итог одного шага.
type StepResult struct {
	Observed *Observed
	// Err — запрос не выполнен или из ответа не удалось взять переменную.
	Err error
}

// RunScenario выполняет шаги по порядку. Если запрос не удался, остальные шаги не выполняются.
func (r *Runner) RunScenario(ctx context.Context, sc *Scenario) []StepResult {
	v := newVars(r.RunID)
	results := make([]StepResult, len(sc.Steps))

	for i := range sc.Steps {
		obs, fatal, err := r.runStep(ctx, &sc.Steps[i], v)
		results[i] = StepResult{Observed: obs, Err: err}
		if fatal {
			for j := i + 1; j < len(sc.Steps); j++ {
				results[j].Err = fmt.Errorf("шаг не выполнялся: упал шаг %d", i+1)
			}
			break
		}
	}

	return results
}

// runStep возвращает ответ; fatal — запрос не состоялся, продолжать сценарий нет смысла.
func (r *Runner) runStep(ctx context.Context, step *Step, v *vars) (obs *Observed, fatal bool, err error) {
	req, err := r.buildRequest(ctx, step, v)
	if err != nil {
		return nil, true, err
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("запрос не выполнен: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("чтение ответа: %w", err)
	}

	obs = &Observed{Status: resp.StatusCode, Headers: map[string]string{}}

	var parsed *jsonValue
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json") && len(raw) > 0 {
		parsed, _ = parseJSON(raw)
	}

	// переменные берутся из «сырого» ответа, до подстановок
	var saveErr error
	for _, rule := range step.saves {
		value, err := extract(parsed, rule)
		if err != nil {
			if saveErr == nil {
				saveErr = fmt.Errorf("переменная ${%s}: %w", rule.Name, err)
			}
			continue
		}
		v.set(rule.Name, value)
	}

	for name, values := range resp.Header {
		if slices.Contains(ignoredHeaders, name) {
			continue
		}
		if len(step.OnlyHeaders) > 0 && !slices.ContainsFunc(step.OnlyHeaders, func(h string) bool {
			return http.CanonicalHeaderKey(h) == name
		}) {
			continue
		}
		value := strings.Join(values, ", ")
		if name == "X-Request-Id" && uuidRe.MatchString(value) {
			value = "<uuid>"
		}
		obs.Headers[name] = v.normalize(value)
	}
	if len(resp.TransferEncoding) > 0 && len(step.OnlyHeaders) == 0 {
		obs.Headers["Transfer-Encoding"] = strings.Join(resp.TransferEncoding, ", ")
	}

	switch {
	case step.SkipBody:
		obs.BodyKind = bodyIgnored
	case len(raw) == 0:
		obs.BodyKind = bodyEmpty
	case parsed != nil:
		obs.BodyKind = bodyJSON
		normalizeJSON(parsed, step, v, time.Now())
		obs.Body = parsed.Pretty()
	case utf8.Valid(raw) && !strings.ContainsRune(string(raw), 0):
		obs.BodyKind = bodyText
		obs.Body = v.normalize(filterLines(string(raw), step.lines, v.runID))
	default:
		sum := sha256.Sum256(raw)
		obs.BodyKind = bodyBinary
		obs.Body = fmt.Sprintf("sha256:%s (%d байт)", hex.EncodeToString(sum[:]), len(raw))
	}

	return obs, false, saveErr
}

func (r *Runner) buildRequest(ctx context.Context, step *Step, v *vars) (*http.Request, error) {
	path, err := v.expand(step.Path)
	if err != nil {
		return nil, fmt.Errorf("path: %w", err)
	}

	tmpl, _, err := step.bodyTemplate()
	if err != nil {
		return nil, err
	}
	body, err := v.expand(tmpl)
	if err != nil {
		return nil, fmt.Errorf("тело запроса: %w", err)
	}

	var reader io.Reader
	if step.JSON != nil || step.Body != nil {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, step.Method, strings.TrimRight(r.Target, "/")+path, reader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	req.Host = step.Host
	req.Header.Set("User-Agent", userAgent)
	if step.ContentType != "" {
		req.Header.Set("Content-Type", step.ContentType)
	}

	switch step.Key {
	case keyGood:
		req.Header.Set("X-Api-Key", r.APIKey)
	case keyBad:
		req.Header.Set("X-Api-Key", "wrong-"+r.RunID)
	case keyEmpty:
		req.Header.Set("X-Api-Key", "")
	}

	for name, value := range step.Headers {
		value, err = v.expand(value)
		if err != nil {
			return nil, fmt.Errorf("заголовок %s: %w", name, err)
		}
		req.Header.Set(name, value)
	}

	return req, nil
}

func extract(body *jsonValue, rule fieldRule) (string, error) {
	if body == nil {
		return "", fmt.Errorf("ответ — не JSON, поле %q взять неоткуда", rule.Path)
	}
	field := body.Get(rule.Path)
	if field == nil {
		return "", fmt.Errorf("в ответе нет поля %q", rule.Path)
	}
	value, ok := field.Scalar()
	if !ok {
		return "", fmt.Errorf("поле %q — не строка и не число", rule.Path)
	}
	if !rule.Pattern.MatchString(value) {
		return "", fmt.Errorf("поле %q = %q не подходит под %s", rule.Path, value, rule.Pattern)
	}
	return value, nil
}

// normalizeJSON заменяет в ответе всё меняющееся от прогона к прогону. Правила (все — здесь):
//  1. поля из mask шага, подошедшие под своё выражение → <имя>;
//  2. dateCreated в пределах nowTolerance от текущего времени → <now±ЧЧ:ММ> (смещение сохраняется:
//     часовой пояс ответа — часть контракта);
//  3. значения сохранённых переменных → ${имя}, набивка → ${pad:N}, метка прогона → ${run}.
//
// Значение, не подошедшее под правило, остаётся как есть — и расхождение видно в сравнении.
func normalizeJSON(root *jsonValue, step *Step, v *vars, now time.Time) {
	root.Walk("", func(path string, val *jsonValue) {
		if val.Kind == jsonObject {
			for _, m := range val.Members {
				m.Key = v.normalize(m.Key)
			}
			return
		}

		scalar, ok := val.Scalar()
		if !ok {
			return
		}

		for _, rule := range step.masks {
			if rule.Path == path && rule.Pattern.MatchString(scalar) {
				val.Kind, val.Str = jsonString, "<"+rule.Name+">"
				return
			}
		}

		if val.Kind != jsonString {
			return
		}

		if path == "dateCreated" || strings.HasSuffix(path, ".dateCreated") {
			if m := dateRe.FindStringSubmatch(scalar); m != nil {
				if t, err := time.Parse(time.RFC3339, scalar); err == nil && now.Sub(t).Abs() <= nowTolerance {
					val.Str = "<now" + m[1] + ">"
					return
				}
			}
		}

		val.Str = v.normalize(scalar)
	})
}

// filterLines убирает строки, подходящие под re, в которых нет метки текущего прогона.
func filterLines(text string, re *regexp.Regexp, runID string) string {
	if re == nil {
		return text
	}

	lines := strings.SplitAfter(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if re.MatchString(line) && !strings.Contains(line, runID) {
			continue
		}
		kept = append(kept, line)
	}

	return strings.Join(kept, "")
}
