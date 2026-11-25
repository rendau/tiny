package compat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	bodiesDir = "bodies"
	// inlineTextLimit — текстовые тела длиннее уходят из файла эталона в отдельный файл в bodies/.
	inlineTextLimit = 300
)

// Golden — эталон одного сценария (файл golden/<id>.json).
type Golden struct {
	Scenario string       `json:"scenario"`
	About    string       `json:"about,omitempty"`
	Steps    []GoldenStep `json:"steps"`
}

// GoldenStep — запрос (как он описан в сценарии, с переменными) и ответ настоящего Shlink.
type GoldenStep struct {
	Name    string        `json:"name"`
	Request GoldenRequest `json:"request"`
	// Saved — переменные, взятые из ответа: имя → "поле =~ выражение" (значение обязано подходить).
	Saved    map[string]string `json:"saved,omitempty"`
	Status   int               `json:"status"`
	Headers  map[string]string `json:"headers"`
	BodyKind string            `json:"body_kind"`
	BodyJSON json.RawMessage   `json:"body_json,omitempty"`
	BodyText string            `json:"body_text,omitempty"`
	// BodyFile — длинное текстовое тело лежит в отдельном файле (golden/bodies/…).
	BodyFile string `json:"body_file,omitempty"`
	// BodyDigest — для нетекстового тела: sha256 и длина.
	BodyDigest string `json:"body_digest,omitempty"`
}

// GoldenRequest — описание запроса; при сравнении сверяется со сценарием, чтобы поймать
// устаревший эталон.
type GoldenRequest struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Host        string            `json:"host"`
	Key         string            `json:"key,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	BodyJSON    json.RawMessage   `json:"body_json,omitempty"`
	Body        string            `json:"body,omitempty"`
}

func goldenRequest(step *Step) (GoldenRequest, error) {
	req := GoldenRequest{
		Method:      step.Method,
		Path:        step.Path,
		Host:        step.Host,
		Key:         step.Key,
		Headers:     step.Headers,
		ContentType: step.ContentType,
	}

	tmpl, isJSON, err := step.bodyTemplate()
	if err != nil {
		return req, err
	}
	if isJSON {
		req.BodyJSON = json.RawMessage(tmpl)
	} else {
		req.Body = tmpl
	}

	return req, nil
}

func goldenSaved(step *Step) map[string]string {
	if len(step.saves) == 0 {
		return nil
	}
	result := make(map[string]string, len(step.saves))
	for _, rule := range step.saves {
		result[rule.Name] = rule.Path + " =~ " + rule.Pattern.String()
	}
	return result
}

// same — одинаково ли описан запрос (сравнение через запись в JSON, чтобы не зависеть от отступов).
func (r GoldenRequest) same(other GoldenRequest) bool {
	return compactJSON(r) == compactJSON(other)
}

func compactJSON(v any) string {
	data, _ := json.Marshal(v)
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}

// GoldenStore читает и пишет эталоны в каталоге.
type GoldenStore struct {
	Dir string
}

func (s GoldenStore) path(id string) string {
	return filepath.Join(s.Dir, id+".json")
}

// Load читает эталон сценария и возвращает ожидаемые ответы шагов.
func (s GoldenStore) Load(id string) (*Golden, []*Observed, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, nil, fmt.Errorf("read golden: %w", err)
	}

	var g Golden
	if err = json.Unmarshal(data, &g); err != nil {
		return nil, nil, fmt.Errorf("parse golden %s: %w", id, err)
	}

	expected := make([]*Observed, len(g.Steps))
	for i, gs := range g.Steps {
		obs := &Observed{Status: gs.Status, Headers: gs.Headers, BodyKind: gs.BodyKind}
		if obs.Headers == nil {
			obs.Headers = map[string]string{}
		}

		switch gs.BodyKind {
		case bodyJSON:
			parsed, err := parseJSON(gs.BodyJSON)
			if err != nil {
				return nil, nil, fmt.Errorf("golden %s, шаг %d: %w", id, i+1, err)
			}
			obs.Body = parsed.Pretty()
		case bodyText:
			obs.Body = gs.BodyText
			if gs.BodyFile != "" {
				body, err := os.ReadFile(filepath.Join(s.Dir, filepath.FromSlash(gs.BodyFile)))
				if err != nil {
					return nil, nil, fmt.Errorf("golden %s, шаг %d: %w", id, i+1, err)
				}
				obs.Body = string(body)
			}
		case bodyBinary:
			obs.Body = gs.BodyDigest
		}

		expected[i] = obs
	}

	return &g, expected, nil
}

// Save записывает эталон сценария по наблюдённым ответам.
func (s GoldenStore) Save(sc *Scenario, observed []*Observed) error {
	g := Golden{Scenario: sc.ID, About: sc.About, Steps: make([]GoldenStep, len(sc.Steps))}

	for i := range sc.Steps {
		step, obs := &sc.Steps[i], observed[i]

		req, err := goldenRequest(step)
		if err != nil {
			return err
		}

		gs := GoldenStep{
			Name:     step.Name,
			Request:  req,
			Saved:    goldenSaved(step),
			Status:   obs.Status,
			Headers:  obs.Headers,
			BodyKind: obs.BodyKind,
		}

		switch obs.BodyKind {
		case bodyJSON:
			gs.BodyJSON = json.RawMessage(obs.Body)
		case bodyText:
			if len(obs.Body) <= inlineTextLimit {
				gs.BodyText = obs.Body
				break
			}
			if gs.BodyFile, err = s.saveBody(obs.Body, obs.Headers["Content-Type"]); err != nil {
				return err
			}
		case bodyBinary:
			gs.BodyDigest = obs.Body
		}

		g.Steps[i] = gs
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(g); err != nil {
		return fmt.Errorf("encode golden: %w", err)
	}

	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(s.path(sc.ID), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write golden: %w", err)
	}

	return nil
}

// saveBody кладёт длинное текстовое тело в bodies/<начало sha256>.<расширение>; одинаковые тела
// (страница 404 повторяется в десятках шагов) попадают в один файл.
func (s GoldenStore) saveBody(body, contentType string) (string, error) {
	ext := ".txt"
	if strings.Contains(contentType, "html") {
		ext = ".html"
	}

	sum := sha256.Sum256([]byte(body))
	rel := bodiesDir + "/" + hex.EncodeToString(sum[:])[:12] + ext

	full := filepath.Join(s.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("write body: %w", err)
	}

	return rel, nil
}

// Prune удаляет эталоны сценариев, которых больше нет, и тела, на которые никто не ссылается.
func (s GoldenStore) Prune(scenarios []*Scenario) error {
	known := map[string]struct{}{}
	usedBodies := map[string]struct{}{}

	for _, sc := range scenarios {
		known[sc.ID] = struct{}{}
		g, _, err := s.Load(sc.ID)
		if err != nil {
			return err
		}
		for _, gs := range g.Steps {
			if gs.BodyFile != "" {
				usedBodies[filepath.Base(gs.BodyFile)] = struct{}{}
			}
		}
	}

	files, _ := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	for _, file := range files {
		if _, ok := known[strings.TrimSuffix(filepath.Base(file), ".json")]; !ok {
			if err := os.Remove(file); err != nil {
				return fmt.Errorf("remove: %w", err)
			}
		}
	}

	bodies, _ := filepath.Glob(filepath.Join(s.Dir, bodiesDir, "*"))
	for _, file := range bodies {
		if _, ok := usedBodies[filepath.Base(file)]; !ok {
			if err := os.Remove(file); err != nil {
				return fmt.Errorf("remove: %w", err)
			}
		}
	}

	return nil
}

// Compare сравнивает ответ с эталоном. diffs — расхождения (ошибка), notes — расхождения в
// «мягких» заголовках (заметка). При strictHeaders заметки тоже становятся расхождениями.
func Compare(expected, got *Observed, strictHeaders bool) (diffs, notes []string) {
	if expected.Status != got.Status {
		diffs = append(diffs, fmt.Sprintf("код ответа: ждали %d, пришло %d", expected.Status, got.Status))
	}

	names := slices.Sorted(maps.Keys(expected.Headers))
	for _, name := range names {
		want := expected.Headers[name]
		have, ok := got.Headers[name]

		var msg string
		switch {
		case !ok:
			msg = fmt.Sprintf("заголовок %s: ждали %q, заголовка нет", name, want)
		case want != have:
			msg = fmt.Sprintf("заголовок %s: ждали %q, пришло %q", name, want, have)
		default:
			continue
		}

		if isSoftHeader(name) && !strictHeaders {
			notes = append(notes, msg)
		} else {
			diffs = append(diffs, msg)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(got.Headers)) {
		if _, ok := expected.Headers[name]; ok {
			continue
		}
		msg := fmt.Sprintf("заголовок %s: в эталоне его нет, пришло %q", name, got.Headers[name])
		if isMeaningfulHeader(name) || strictHeaders {
			diffs = append(diffs, msg)
		} else {
			notes = append(notes, msg)
		}
	}

	diffs = append(diffs, compareBody(expected, got)...)

	return diffs, notes
}

func compareBody(expected, got *Observed) []string {
	if expected.BodyKind == bodyIgnored {
		return nil
	}

	if expected.BodyKind != got.BodyKind {
		return []string{fmt.Sprintf(
			"тело: ждали %s, пришло %s\n%s",
			describeBody(expected), describeBody(got), indentBlock(lineDiff(expected.Body, got.Body)),
		)}
	}

	if expected.Body == got.Body {
		return nil
	}

	switch expected.BodyKind {
	case bodyJSON:
		var hints []string
		want, errWant := parseJSON([]byte(expected.Body))
		have, errHave := parseJSON([]byte(got.Body))
		if errWant == nil && errHave == nil {
			hints = structureHints("", want, have)
		}

		msg := "тело (JSON) отличается (- ждали, + пришло):\n" + indentBlock(lineDiff(expected.Body, got.Body))
		if len(hints) > 0 {
			msg += "\n    коротко:\n      " + strings.Join(hints, "\n      ")
		}
		return []string{msg}
	case bodyBinary:
		return []string{fmt.Sprintf("тело: ждали %s, пришло %s", expected.Body, got.Body)}
	default:
		return []string{"тело (текст) отличается (- ждали, + пришло):\n" + indentBlock(lineDiff(expected.Body, got.Body))}
	}
}

func describeBody(o *Observed) string {
	switch o.BodyKind {
	case bodyEmpty:
		return "пустое тело"
	case bodyJSON:
		return "JSON"
	case bodyText:
		return fmt.Sprintf("текст (%d байт)", len(o.Body))
	case bodyBinary:
		return "не текст, " + o.Body
	default:
		return o.BodyKind
	}
}

// structureHints — короткое объяснение расхождений в составе и порядке полей JSON.
func structureHints(path string, want, have *jsonValue) []string {
	if want.Kind != have.Kind {
		return nil
	}

	var hints []string

	switch want.Kind {
	case jsonObject:
		wantKeys := memberKeys(want)
		haveKeys := memberKeys(have)
		where := "в корне"
		if path != "" {
			where = "в " + path
		}

		for _, key := range wantKeys {
			if !slices.Contains(haveKeys, key) {
				hints = append(hints, fmt.Sprintf("%s нет поля %q", where, key))
			}
		}
		for _, key := range haveKeys {
			if !slices.Contains(wantKeys, key) {
				hints = append(hints, fmt.Sprintf("%s лишнее поле %q", where, key))
			}
		}
		if len(hints) == 0 && !slices.Equal(wantKeys, haveKeys) {
			hints = append(hints, fmt.Sprintf(
				"%s другой порядок полей: ждали %s, пришло %s",
				where, strings.Join(wantKeys, ", "), strings.Join(haveKeys, ", "),
			))
		}

		for _, m := range want.Members {
			if other := have.Get(m.Key); other != nil && !strings.Contains(m.Key, ".") {
				sub := m.Key
				if path != "" {
					sub = path + "." + m.Key
				}
				hints = append(hints, structureHints(sub, m.Value, other)...)
			}
		}
	case jsonArray:
		if len(want.Items) != len(have.Items) {
			hints = append(hints, fmt.Sprintf("в %s ждали элементов: %d, пришло: %d", path, len(want.Items), len(have.Items)))
			break
		}
		for i := range want.Items {
			hints = append(hints, structureHints(fmt.Sprintf("%s[%d]", path, i), want.Items[i], have.Items[i])...)
		}
	default:
	}

	return hints
}

func memberKeys(v *jsonValue) []string {
	keys := make([]string, len(v.Members))
	for i, m := range v.Members {
		keys[i] = m.Key
	}
	return keys
}

func indentBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "      " + lines[i]
	}
	return strings.Join(lines, "\n")
}
