package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultHost — заголовок Host, если в шаге не указан другой.
	DefaultHost = "l.demo.test"

	createPath = "/rest/v3/short-urls"

	keyGood  = "good"  // верный ключ (из API_KEY)
	keyBad   = "bad"   // заведомо неверный ключ
	keyEmpty = "empty" // заголовок X-Api-Key есть, но пустой
)

// Scenario — один сценарий: несколько запросов подряд с общими переменными.
type Scenario struct {
	ID    string `yaml:"id"`
	About string `yaml:"about"`
	Steps []Step `yaml:"steps"`

	File string `yaml:"-"`
}

// Step — один запрос. Описание полей — в README, раздел «Как описан сценарий».
type Step struct {
	Name string `yaml:"name"`

	Method string `yaml:"method"` // по умолчанию GET
	Path   string `yaml:"path"`
	Host   string `yaml:"host"` // по умолчанию l.demo.test
	Key    string `yaml:"key"`  // good | bad | empty | (пусто — заголовка нет)

	Headers map[string]string `yaml:"headers"`

	// Create — сокращение: POST /rest/v3/short-urls с верным ключом и этим JSON в теле.
	Create any `yaml:"create"`
	// JSON — тело запроса, записанное данными (уйдёт как JSON).
	JSON any `yaml:"json"`
	// Body — тело запроса как есть (битый JSON, форма и т.п.).
	Body *string `yaml:"body"`
	// ContentType — заголовок Content-Type; для create/json по умолчанию application/json,
	// "none" — не слать заголовок вовсе.
	ContentType string `yaml:"content_type"`

	// Save — взять значение из JSON-ответа в переменную: имя → "путь =~ регулярное выражение".
	Save map[string]string `yaml:"save"`
	// Mask — заменить меняющееся поле JSON-ответа на <имя>: путь → "имя =~ регулярное выражение".
	Mask map[string]string `yaml:"mask"`
	// OnlyThisRunLines — из текстового ответа убрать строки, которые подходят под это выражение,
	// но не содержат метку текущего прогона (следы прошлых прогонов).
	OnlyThisRunLines string `yaml:"only_this_run_lines"`
	// SkipBody — тело ответа не сравнивать (например, картинка).
	SkipBody bool `yaml:"skip_body"`
	// OnlyHeaders — сравнивать только эти заголовки ответа (остальные не записываются).
	OnlyHeaders []string `yaml:"only_headers"`

	saves []fieldRule
	masks []fieldRule
	lines *regexp.Regexp
}

// fieldRule — разобранная запись "… =~ выражение" из save и mask.
type fieldRule struct {
	Name    string // имя переменной (save) или подстановки (mask)
	Path    string // путь поля в JSON
	Pattern *regexp.Regexp
}

// LoadScenarios читает все scenarios/*.yaml; порядок — по имени файла, внутри файла — как записано.
func LoadScenarios(dir string) ([]*Scenario, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	slices.Sort(files)

	var result []*Scenario
	seen := map[string]string{}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}

		var items []*Scenario
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err = dec.Decode(&items); err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}

		for _, sc := range items {
			sc.File = filepath.Base(file)
			if err = sc.prepare(); err != nil {
				return nil, fmt.Errorf("%s: сценарий %q: %w", sc.File, sc.ID, err)
			}
			if prev, ok := seen[sc.ID]; ok {
				return nil, fmt.Errorf("сценарий %q описан дважды: %s и %s", sc.ID, prev, sc.File)
			}
			seen[sc.ID] = sc.File
			result = append(result, sc)
		}
	}

	return result, nil
}

var scenarioIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (sc *Scenario) prepare() error {
	if !scenarioIDRe.MatchString(sc.ID) {
		return fmt.Errorf("id должен состоять из a-z, 0-9 и дефиса")
	}
	if len(sc.Steps) == 0 {
		return fmt.Errorf("нет шагов")
	}

	for i := range sc.Steps {
		if err := sc.Steps[i].prepare(); err != nil {
			return fmt.Errorf("шаг %d: %w", i+1, err)
		}
	}

	return nil
}

func (s *Step) prepare() error {
	if s.Create != nil {
		if s.JSON != nil || s.Body != nil {
			return fmt.Errorf("create нельзя совмещать с json и body")
		}
		s.JSON = s.Create
		s.Create = nil
		if s.Method == "" {
			s.Method = "POST"
		}
		if s.Path == "" {
			s.Path = createPath
		}
		if s.Key == "" {
			s.Key = keyGood
		}
	}

	if s.Method == "" {
		s.Method = "GET"
	}
	if s.Host == "" {
		s.Host = DefaultHost
	}
	if s.Path == "" {
		return fmt.Errorf("не указан path")
	}
	if s.JSON != nil && s.Body != nil {
		return fmt.Errorf("json и body одновременно")
	}
	if s.JSON != nil && s.ContentType == "" {
		s.ContentType = "application/json"
	}
	if s.ContentType == "none" {
		s.ContentType = ""
	}
	if !slices.Contains([]string{"", keyGood, keyBad, keyEmpty}, s.Key) {
		return fmt.Errorf("key: ожидается good, bad или empty, а не %q", s.Key)
	}
	if s.Name == "" {
		s.Name = s.Method + " " + s.Path
	}

	var err error
	if s.saves, err = parseFieldRules(s.Save, false); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	if s.masks, err = parseFieldRules(s.Mask, true); err != nil {
		return fmt.Errorf("mask: %w", err)
	}
	if s.OnlyThisRunLines != "" {
		if s.lines, err = regexp.Compile(s.OnlyThisRunLines); err != nil {
			return fmt.Errorf("only_this_run_lines: %w", err)
		}
	}

	return nil
}

// parseFieldRules разбирает записи save ("переменная: путь =~ выражение") и
// mask ("путь: имя =~ выражение").
func parseFieldRules(src map[string]string, keyIsPath bool) ([]fieldRule, error) {
	result := make([]fieldRule, 0, len(src))

	for key, spec := range src {
		left, pattern, ok := strings.Cut(spec, " =~ ")
		if !ok {
			return nil, fmt.Errorf("%s: ожидается запись вида \"… =~ выражение\"", key)
		}
		re, err := regexp.Compile(strings.TrimSpace(pattern))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}

		rule := fieldRule{Name: key, Path: strings.TrimSpace(left), Pattern: re}
		if keyIsPath {
			rule.Name, rule.Path = rule.Path, key
		}
		result = append(result, rule)
	}

	slices.SortFunc(result, func(a, b fieldRule) int { return strings.Compare(a.Name, b.Name) })

	return result, nil
}

// bodyTemplate — тело запроса до подстановки переменных и признак, что оно описано данными.
func (s *Step) bodyTemplate() (string, bool, error) {
	if s.Body != nil {
		return *s.Body, false, nil
	}
	if s.JSON == nil {
		return "", false, nil
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s.JSON); err != nil {
		return "", false, fmt.Errorf("json body: %w", err)
	}

	return strings.TrimSuffix(buf.String(), "\n"), true, nil
}

const padAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// padString — предсказуемая «набивка» нужной длины для очень длинных адресов и заголовков.
func padString(n int) string {
	return strings.Repeat(padAlphabet, n/len(padAlphabet)+1)[:n]
}

var placeholderRe = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)(?::(\d+))?\}`)

// vars — переменные сценария: метка прогона, значения из save и использованные набивки.
type vars struct {
	runID  string
	values map[string]string // имя → значение
	order  []string          // имена в порядке первого сохранения
	pads   map[int]struct{}  // длины использованных ${pad:N}
}

// set запоминает значение переменной, взятое из ответа.
func (v *vars) set(name, value string) {
	if _, ok := v.values[name]; !ok {
		v.order = append(v.order, name)
	}
	v.values[name] = value
}

func newVars(runID string) *vars {
	return &vars{runID: runID, values: map[string]string{}, pads: map[int]struct{}{}}
}

// expand подставляет ${run}, ${pad:N} и сохранённые переменные; неизвестная переменная — ошибка.
func (v *vars) expand(s string) (string, error) {
	var firstErr error

	result := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := placeholderRe.FindStringSubmatch(m)
		name, arg := parts[1], parts[2]

		switch {
		case name == "run" && arg == "":
			return v.runID
		case name == "pad" && arg != "":
			n, _ := strconv.Atoi(arg)
			v.pads[n] = struct{}{}
			return padString(n)
		}

		val, ok := v.values[name]
		if !ok || arg != "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("нет значения для %s", m)
			}
			return m
		}
		return val
	})

	return result, firstErr
}

type replacement struct {
	value       string
	placeholder string
}

// replacements — обратные подстановки для ответа: длинные значения заменяются первыми, чтобы
// короткое значение не «откусило» кусок длинного.
//
// Если у двух переменных значение одинаковое, в ответе окажется имя той, что сохранена раньше.
// На этом держится проверка «должна была создаться новая ссылка»: когда сервер вернул старый
// код, в ответе стоит ${code}, а эталон ждёт, например, ${codeFewer}.
func (v *vars) replacements() []replacement {
	result := make([]replacement, 0, len(v.values)+len(v.pads)+1)

	for _, n := range slices.Sorted(maps.Keys(v.pads)) {
		result = append(result, replacement{padString(n), fmt.Sprintf("${pad:%d}", n)})
	}
	for _, name := range v.order {
		if val := v.values[name]; val != "" {
			result = append(result, replacement{val, "${" + name + "}"})
		}
	}
	result = append(result, replacement{v.runID, "${run}"})

	// сортировка устойчивая: при равной длине порядок выше сохраняется
	slices.SortStableFunc(result, func(a, b replacement) int {
		return len(b.value) - len(a.value)
	})

	return result
}

func (v *vars) normalize(s string) string {
	for _, r := range v.replacements() {
		s = strings.ReplaceAll(s, r.value, r.placeholder)
	}
	return s
}
