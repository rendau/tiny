package compat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Разбор JSON с сохранением порядка полей: порядок полей — часть эталона, а обычный
// разбор в map его теряет.

type jsonKind int

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

type jsonMember struct {
	Key   string
	Value *jsonValue
}

type jsonValue struct {
	Kind    jsonKind
	Str     string        // значение строки
	Lit     string        // запись числа или true/false как в исходном тексте
	Items   []*jsonValue  // элементы массива
	Members []*jsonMember // поля объекта в исходном порядке
}

func parseJSON(data []byte) (*jsonValue, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	v, err := parseJSONValue(dec)
	if err != nil {
		return nil, err
	}

	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("после JSON-значения идёт лишний текст")
	}

	return v, nil
}

func parseJSONValue(dec *json.Decoder) (*jsonValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("json token: %w", err)
	}

	switch t := tok.(type) {
	case nil:
		return &jsonValue{Kind: jsonNull}, nil
	case bool:
		return &jsonValue{Kind: jsonBool, Lit: fmt.Sprint(t)}, nil
	case json.Number:
		return &jsonValue{Kind: jsonNumber, Lit: t.String()}, nil
	case string:
		return &jsonValue{Kind: jsonString, Str: t}, nil
	case json.Delim:
		switch t {
		case '[':
			v := &jsonValue{Kind: jsonArray}
			for dec.More() {
				item, err := parseJSONValue(dec)
				if err != nil {
					return nil, err
				}
				v.Items = append(v.Items, item)
			}
			if _, err = dec.Token(); err != nil {
				return nil, fmt.Errorf("json array end: %w", err)
			}
			return v, nil
		case '{':
			v := &jsonValue{Kind: jsonObject}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("json key: %w", err)
				}
				key, _ := keyTok.(string)
				val, err := parseJSONValue(dec)
				if err != nil {
					return nil, err
				}
				v.Members = append(v.Members, &jsonMember{Key: key, Value: val})
			}
			if _, err = dec.Token(); err != nil {
				return nil, fmt.Errorf("json object end: %w", err)
			}
			return v, nil
		}
	}

	return nil, fmt.Errorf("неожиданный элемент JSON: %v", tok)
}

// Pretty — запись значения с отступами; поля идут в исходном порядке. Одинаковые значения
// дают одинаковый текст, поэтому сравнивать ответы можно как строки.
func (v *jsonValue) Pretty() string {
	var sb strings.Builder
	v.write(&sb, "")
	return sb.String()
}

func (v *jsonValue) write(sb *strings.Builder, indent string) {
	switch v.Kind {
	case jsonNull:
		sb.WriteString("null")
	case jsonBool, jsonNumber:
		sb.WriteString(v.Lit)
	case jsonString:
		sb.WriteString(quoteJSON(v.Str))
	case jsonArray:
		if len(v.Items) == 0 {
			sb.WriteString("[]")
			return
		}
		sb.WriteString("[\n")
		for i, item := range v.Items {
			sb.WriteString(indent + "  ")
			item.write(sb, indent+"  ")
			if i < len(v.Items)-1 {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(indent + "]")
	case jsonObject:
		if len(v.Members) == 0 {
			sb.WriteString("{}")
			return
		}
		sb.WriteString("{\n")
		for i, m := range v.Members {
			sb.WriteString(indent + "  " + quoteJSON(m.Key) + ": ")
			m.Value.write(sb, indent+"  ")
			if i < len(v.Members)-1 {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(indent + "}")
	}
}

// quoteJSON — строка в кавычках без замены <, >, & на \u-коды: эталон должен читаться глазами.
func quoteJSON(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

// Get — значение по пути вида "meta.validUntil"; nil, если такого поля нет.
func (v *jsonValue) Get(path string) *jsonValue {
	cur := v
	for part := range strings.SplitSeq(path, ".") {
		if cur == nil || cur.Kind != jsonObject {
			return nil
		}
		var next *jsonValue
		for _, m := range cur.Members {
			if m.Key == part {
				next = m.Value
				break
			}
		}
		cur = next
	}
	return cur
}

// Walk обходит все значения; path — путь поля ("meta.validUntil", для элементов массива "tags[]").
func (v *jsonValue) Walk(path string, fn func(path string, v *jsonValue)) {
	fn(path, v)
	switch v.Kind {
	case jsonArray:
		for _, item := range v.Items {
			item.Walk(path+"[]", fn)
		}
	case jsonObject:
		for _, m := range v.Members {
			p := m.Key
			if path != "" {
				p = path + "." + m.Key
			}
			m.Value.Walk(p, fn)
		}
	default:
	}
}

// Scalar — значение как строка (для строк — содержимое, для чисел и bool — запись).
func (v *jsonValue) Scalar() (string, bool) {
	switch v.Kind {
	case jsonString:
		return v.Str, true
	case jsonNumber, jsonBool:
		return v.Lit, true
	default:
		return "", false
	}
}
