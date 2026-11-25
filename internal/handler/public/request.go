package public

import (
	"bytes"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/tiny/internal/domain/link/model"
	usecase "github.com/rendau/tiny/internal/usecase/link"
)

// Имена полей запроса создания у Shlink
const (
	fieldLongUrl         = "longUrl"
	fieldValidSince      = "validSince"
	fieldValidUntil      = "validUntil"
	fieldCustomSlug      = "customSlug"
	fieldMaxVisits       = "maxVisits"
	fieldFindIfExists    = "findIfExists"
	fieldDomain          = "domain"
	fieldShortCodeLength = "shortCodeLength"
	fieldTitle           = "title"
	fieldTags            = "tags"
	fieldCrawlable       = "crawlable"
	fieldForwardQuery    = "forwardQuery"
)

// invalidElementsOrder — порядок, в котором Shlink перечисляет неверные поля в invalidElements
var invalidElementsOrder = []string{
	fieldLongUrl, fieldValidSince, fieldValidUntil, fieldCustomSlug, fieldMaxVisits, fieldFindIfExists,
	fieldDomain, fieldShortCodeLength, fieldTitle, fieldTags, fieldCrawlable, fieldForwardQuery,
}

// usecaseFieldNames — имена полей usecase → имена полей Shlink
var usecaseFieldNames = map[string]string{
	usecase.FieldLongUrl:         fieldLongUrl,
	usecase.FieldShortCode:       fieldCustomSlug,
	usecase.FieldShortCodeLength: fieldShortCodeLength,
	usecase.FieldDomain:          fieldDomain,
	usecase.FieldTitle:           fieldTitle,
	usecase.FieldTags:            fieldTags,
}

// dateLayouts — в каком виде клиенты присылают даты. Shlink разбирает их разборщиком дат PHP,
// который понимает почти всё; здесь — виды, которые встречаются на деле: RFC3339 (в том числе с
// долями секунды, как у Go), то же без часового пояса и «день.месяц.год».
// Время без пояса считается в поясе сервера (Config.TimeZone) — как у Shlink.
var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02",
	"20060102T150405Z0700",
	"20060102T150405",
	"02.01.2006 15:04:05",
	"02.01.2006 15:04",
	"02.01.2006",
}

// rawFields — поля тела запроса в сыром виде: значение разбирается по правилам своего поля
type rawFields map[string]json.RawMessage

// parseBody разбирает тело запроса создания. Как у Shlink, тело читается как JSON при любом
// Content-Type, кроме формы (application/x-www-form-urlencoded). Пустое тело и JSON, который не
// объект, — это запрос без полей; не JSON — ошибка разбора (malformed = true).
func parseBody(contentType string, body []byte) (fields rawFields, malformed bool) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "application/x-www-form-urlencoded") {
		return parseFormBody(body), false
	}

	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return rawFields{}, false
	}

	fields = rawFields{}
	if err := json.Unmarshal(body, &fields); err != nil {
		return rawFields{}, !json.Valid(body)
	}

	return fields, false
}

// parseFormBody — тело в виде формы: значения — строки, «tags[]=a&tags[]=b» — список
func parseFormBody(body []byte) rawFields {
	fields := rawFields{}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return fields
	}

	for key, vals := range values {
		if name, isList := strings.CutSuffix(key, "[]"); isList {
			fields[name], _ = json.Marshal(vals)
		} else if len(vals) > 0 {
			fields[key], _ = json.Marshal(vals[len(vals)-1])
		}
	}

	return fields
}

// decodeCreateReq собирает запрос создания из полей тела по правилам Shlink (PHP нестрог к типам:
// число вместо строки и строка вместо числа проходят). Второе значение — поля, которые не удалось
// разобрать: их значения в запросе пропущены. zone — пояс дат, присланных без пояса.
func decodeCreateReq(fields rawFields, zone *time.Location) (*model.CreateReq, []string) {
	var invalid []string
	fail := func(name string) { invalid = append(invalid, name) }

	req := &model.CreateReq{}

	// longUrl: строка или число; пусто или нет — неверно (это скажет usecase)
	if raw := fields.present(fieldLongUrl); raw != nil {
		v, ok := rawString(raw)
		if !ok {
			fail(fieldLongUrl)
		}
		req.LongUrl = v
	}

	req.ValidSince = fields.date(fieldValidSince, zone, fail)
	req.ValidUntil = fields.date(fieldValidUntil, zone, fail)

	if raw := fields.present(fieldCustomSlug); raw != nil {
		if v, ok := rawString(raw); ok {
			req.ShortCode = &v
		} else {
			fail(fieldCustomSlug)
		}
	}

	if raw := fields.present(fieldMaxVisits); raw != nil {
		if v, ok := rawInt(raw); ok && v >= math.MinInt32 && v <= math.MaxInt32 {
			req.MaxVisits = new(int32(v))
		} else {
			fail(fieldMaxVisits)
		}
	}

	if v := fields.boolean(fieldFindIfExists, fail); v != nil {
		req.FindIfExists = *v
	}

	if raw := fields.present(fieldDomain); raw != nil {
		if v, ok := rawString(raw); ok {
			req.Domain = &v
		} else {
			fail(fieldDomain)
		}
	}

	if raw := fields.present(fieldShortCodeLength); raw != nil {
		if v, ok := rawInt(raw); ok && v >= math.MinInt32 && v <= math.MaxInt32 {
			req.ShortCodeLength = new(int(v))
		} else {
			fail(fieldShortCodeLength)
		}
	}

	if raw := fields.present(fieldTitle); raw != nil {
		if v, ok := rawString(raw); ok {
			req.Title = &v
		} else {
			fail(fieldTitle)
		}
	}

	// tags: только список строк или чисел; null — неверно
	if raw, ok := fields[fieldTags]; ok {
		if tags, ok := rawStringList(raw); ok {
			req.Tags = tags
		} else {
			fail(fieldTags)
		}
	}

	req.Crawlable = fields.boolean(fieldCrawlable, fail)
	req.ForwardQuery = fields.boolean(fieldForwardQuery, fail)

	return req, invalid
}

// present — значение поля; nil, если поля нет или оно null
func (f rawFields) present(name string) json.RawMessage {
	raw, ok := f[name]
	if !ok || rawKind(raw) == 'n' {
		return nil
	}
	return raw
}

// date — необязательная дата: нет, null или пустая строка — nil
func (f rawFields) date(name string, zone *time.Location, fail func(string)) *time.Time {
	raw := f.present(name)
	if raw == nil {
		return nil
	}

	switch rawKind(raw) {
	case 's':
		v, _ := rawString(raw)
		if v = strings.TrimSpace(v); v == "" {
			return nil
		}
		for _, layout := range dateLayouts {
			if t, err := time.ParseInLocation(layout, v, zone); err == nil {
				return &t
			}
		}
	case 'd':
		// число — время Unix в секундах
		if v, ok := rawInt(raw); ok {
			return new(time.Unix(v, 0))
		}
	}

	fail(name)
	return nil
}

// boolean — необязательный признак: нет поля — nil; null и всё, что не похоже на да/нет, — неверно
func (f rawFields) boolean(name string, fail func(string)) *bool {
	raw, ok := f[name]
	if !ok {
		return nil
	}

	switch strings.ToLower(strings.Trim(string(raw), `"`)) {
	case "true", "1":
		return new(true)
	case "false", "0":
		return new(false)
	}

	fail(name)
	return nil
}

// rawKind — вид значения JSON: s — строка, d — число, b — да/нет, n — null, a — список, o — объект
func rawKind(raw json.RawMessage) byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 'n'
	}

	switch c := raw[0]; {
	case c == '"':
		return 's'
	case c == 't' || c == 'f':
		return 'b'
	case c == 'n':
		return 'n'
	case c == '[':
		return 'a'
	case c == '{':
		return 'o'
	default:
		return 'd'
	}
}

// rawString — строка; число тоже годится (PHP приводит его к строке)
func rawString(raw json.RawMessage) (string, bool) {
	switch rawKind(raw) {
	case 's':
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", false
		}
		return v, true
	case 'd':
		return string(bytes.TrimSpace(raw)), true
	}
	return "", false
}

// rawInt — целое; строка с целым тоже годится, дробное — нет
func rawInt(raw json.RawMessage) (int64, bool) {
	var text string

	switch rawKind(raw) {
	case 's':
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, false
		}
		text = strings.TrimSpace(text)
	case 'd':
		text = string(bytes.TrimSpace(raw))
	default:
		return 0, false
	}

	v, err := strconv.ParseInt(text, 10, 64)
	return v, err == nil
}

func rawStringList(raw json.RawMessage) ([]string, bool) {
	if rawKind(raw) != 'a' {
		return nil, false
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false
	}

	result := make([]string, 0, len(items))
	for _, item := range items {
		v, ok := rawString(item)
		if !ok {
			return nil, false
		}
		result = append(result, v)
	}

	return result, true
}

// sortInvalidElements — неверные поля без повторов в порядке Shlink
func sortInvalidElements(names []string) []string {
	return lo.Filter(invalidElementsOrder, func(name string, _ int) bool {
		return lo.Contains(names, name)
	})
}
