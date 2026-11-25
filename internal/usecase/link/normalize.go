package link

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samber/lo"

	"github.com/rendau/tiny/internal/domain/link/model"
	"github.com/rendau/tiny/internal/errs"
)

// Имена полей в errs.ErrFull.Fields при неверном запросе. Публичный сервер переводит их в имена
// полей Shlink (invalidElements), интерфейс админки — в подписи полей. Значение — слово-причина,
// которое понимает интерфейс: required, invalid, exists, too_long.
const (
	FieldLongUrl         = "long_url"
	FieldShortCode       = "short_code"
	FieldShortCodeLength = "short_code_length"
	FieldDomain          = "domain"
	FieldTitle           = "title"
	FieldTags            = "tags"
)

const (
	// MinShortCodeLength — как MIN_SHORT_CODES_LENGTH у Shlink
	MinShortCodeLength = 4
	// maxShortCodeLength — у Shlink предела нет (колонка на 255 символов); берём тот же потолок
	maxShortCodeLength = 255
	// maxTitleLength — Shlink обрезает название до 512 байт
	maxTitleLength = 512

	// phpTrimChars — что срезает trim() в PHP
	phpTrimChars = " \t\n\r\x00\x0B"
)

var (
	// looseUriRe — проверка длинного адреса у Shlink (LOOSE_URI_MATCHER): «что-то : что-то».
	// Сам адрес не разбирается и не обрезается.
	looseUriRe = regexp.MustCompile(`.+:.+`)

	// domainRe — имя хоста (в том числе не латиницей) с необязательным портом
	domainRe = regexp.MustCompile(`^[\p{L}\p{N}]([\p{L}\p{N}-]*[\p{L}\p{N}])?(\.[\p{L}\p{N}]([\p{L}\p{N}-]*[\p{L}\p{N}])?)*(:[0-9]{1,5})?$`)
)

// normalizedCreate — запрос на создание после проверки и приведения к виду хранения
type normalizedCreate struct {
	longUrl    string
	shortCode  string // пусто — сгенерировать
	codeLength int
	// domain — '' основной; domainGiven — в запросе был назван не основной домен
	domain       string
	domainGiven  bool
	title        string
	tags         []string // как в запросе: без повторов, порядок запроса
	validSince   *time.Time
	validUntil   *time.Time
	maxVisits    *int32
	crawlable    bool
	forwardQuery bool
}

// normalizeCreate проверяет запрос по правилам Shlink и собирает сразу все неверные поля
func (u *Usecase) normalizeCreate(req *model.CreateReq) (*normalizedCreate, error) {
	fields := map[string]string{}

	result := &normalizedCreate{
		longUrl:      req.LongUrl,
		codeLength:   u.cfg.DefaultCodeLength,
		tags:         normalizeTags(req.Tags),
		validSince:   truncateToSecond(req.ValidSince),
		validUntil:   truncateToSecond(req.ValidUntil),
		maxVisits:    req.MaxVisits,
		crawlable:    lo.FromPtrOr(req.Crawlable, false),
		forwardQuery: lo.FromPtrOr(req.ForwardQuery, true),
	}

	if msg := validateLongUrl(req.LongUrl); msg != "" {
		fields[FieldLongUrl] = msg
	}

	if req.ShortCode != nil {
		code, msg := normalizeShortCode(*req.ShortCode)
		if msg != "" {
			fields[FieldShortCode] = msg
		}
		result.shortCode = code
	}

	if req.ShortCodeLength != nil {
		if *req.ShortCodeLength < MinShortCodeLength || *req.ShortCodeLength > maxShortCodeLength {
			fields[FieldShortCodeLength] = "invalid"
		}
		result.codeLength = *req.ShortCodeLength
	}

	if req.Domain != nil {
		domain, ok := u.normalizeDomain(*req.Domain)
		if !ok {
			fields[FieldDomain] = "invalid"
		}
		result.domain, result.domainGiven = domain, domain != ""
	}

	if req.Title != nil {
		result.title = normalizeTitle(*req.Title)
	}

	if len(fields) > 0 {
		return nil, errs.ErrFull{Err: errs.InvalidRequest, Desc: "invalid link data", Fields: fields}
	}

	return result, nil
}

func validateLongUrl(v string) string {
	switch {
	case v == "":
		return "required"
	case !utf8.ValidString(v):
		return "invalid"
	case !looseUriRe.MatchString(v):
		return "invalid"
	}
	return ""
}

// normalizeShortCode — свой код как у Shlink: html-разметка убирается, края обрезаются, пробелы и
// «/» становятся «-». Символы, которые в адресе отделяют путь от остального (: ? # [ ] @), и
// управляющие — нельзя.
func normalizeShortCode(v string) (string, string) {
	code := strings.NewReplacer(" ", "-", "/", "-").Replace(strings.Trim(phpStripTags(v), phpTrimChars))

	switch {
	case !utf8.ValidString(code):
		return "", "invalid"
	case utf8.RuneCountInString(code) > maxShortCodeLength:
		return "", "too_long"
	case strings.ContainsAny(code, ":?#[]@"):
		return "", "invalid"
	case strings.ContainsFunc(code, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return "", "invalid"
	}

	return code, ""
}

// normalizeDomain — домен в виде хранения: нижний регистр, основной домен — пустая строка
func (u *Usecase) normalizeDomain(v string) (string, bool) {
	domain := strings.ToLower(strings.Trim(v, phpTrimChars))

	if !domainRe.MatchString(domain) {
		return "", false
	}
	if domain == u.cfg.DefaultDomain {
		return "", true
	}

	return domain, true
}

func normalizeTitle(v string) string {
	title := strings.Trim(strings.ToValidUTF8(v, ""), phpTrimChars)

	// предел — в байтах, как у Shlink; символ, который не влез целиком, отбрасывается
	if len(title) > maxTitleLength {
		cut := maxTitleLength
		for cut > 0 && !utf8.RuneStart(title[cut]) {
			cut--
		}
		title = title[:cut]
	}

	return title
}

// normalizeTags — метки как у Shlink: html-разметка убирается, края обрезаются, нижний регистр,
// пробелы становятся «-»; пустые и повторы убираются, порядок запроса сохраняется
func normalizeTags(tags []string) []string {
	result := lo.FilterMap(tags, func(tag string, _ int) (string, bool) {
		tag = strings.Trim(phpStripTags(strings.ToValidUTF8(tag, "")), phpTrimChars)
		tag = strings.ReplaceAll(strings.ToLower(tag), " ", "-")
		return tag, tag != ""
	})

	return lo.Uniq(result)
}

// phpStripTags — strip_tags из PHP в том объёме, который нужен здесь: всё от «<» до закрывающей
// «>» выбрасывается (кавычки внутри тега учитываются, вложенные «<» — тоже); незакрытый тег
// съедает остаток строки; «<» перед пробелом — обычный знак «меньше».
func phpStripTags(v string) string {
	if !strings.Contains(v, "<") {
		return v
	}

	var sb strings.Builder
	sb.Grow(len(v))

	inTag, depth := false, 0
	var quote byte

	for i := range len(v) {
		c := v[i]

		if !inTag {
			if c == '<' && !(i+1 < len(v) && isPhpSpace(v[i+1])) {
				inTag = true
				continue
			}
			sb.WriteByte(c)
			continue
		}

		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '<':
			depth++
		case c == '>' && depth > 0:
			depth--
		case c == '>':
			inTag = false
		}
	}

	return sb.String()
}

func isPhpSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// storedTags — метки в виде хранения: по алфавиту (так их отдаёт Shlink при чтении)
func storedTags(tags []string) []string {
	result := slices.Clone(tags)
	if result == nil {
		result = []string{}
	}
	slices.Sort(result)
	return result
}

// truncateToSecond — Shlink хранит время с точностью до секунды
func truncateToSecond(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	return new(v.Truncate(time.Second))
}
