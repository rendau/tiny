package link

import (
	"strconv"
	"strings"
)

// Сборка адреса перехода повторяет Shlink 5.1.8 (ShortUrlRedirectionBuilder): длинный адрес
// проходит через разбор и сборку адреса из Guzzle (GuzzleHttp\Psr7\Uri), а параметры запроса
// склеиваются через GuzzleHttp\Psr7\Query. Отсюда все «странности» ниже: это не наш вкус, а то,
// что сегодня получает клиент в заголовке Location.

const (
	uriUnreserved = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.~"
	uriSubDelims  = "!$&'()*+,;="
	upperHex      = "0123456789ABCDEF"
)

var (
	uriUserInfoAllowed = newByteSet(uriUnreserved + uriSubDelims)
	uriPathAllowed     = newByteSet(uriUnreserved + uriSubDelims + ":@/")
	uriQueryAllowed    = newByteSet(uriUnreserved + uriSubDelims + ":@/?")
	rawUrlUnreserved   = newByteSet(uriUnreserved)

	// uriDefaultPorts — порт по умолчанию из адреса убирается
	uriDefaultPorts = map[string]int{
		"http": 80, "https": 443, "ftp": 21, "gopher": 70, "nntp": 119, "news": 119,
		"telnet": 23, "tn3270": 23, "imap": 143, "pop": 110, "ldap": 389,
	}
)

type byteSet [256]bool

func newByteSet(chars string) *byteSet {
	set := &byteSet{}
	for i := range len(chars) {
		set[chars[i]] = true
	}
	return set
}

// buildRedirectUrl — адрес для Location: длинный адрес в нормальном виде; при forwardQuery к нему
// дописываются параметры запроса короткой ссылки (одноимённые заменяют параметры длинного адреса)
func buildRedirectUrl(longUrl, requestQuery string, forwardQuery bool) string {
	uri := parseUri(longUrl)

	if forwardQuery {
		merged := parseQuery(uri.query)
		merged.merge(parseQuery(requestQuery))
		uri.query = filterUriComponent(merged.build(), uriQueryAllowed)
	}

	return uri.String()
}

// uri — части адреса после разбора, уже в нормальном виде
type uri struct {
	scheme   string
	userInfo string
	host     string
	port     int // 0 — нет
	path     string
	query    string
	fragment string
}

// parseUri разбирает адрес как parse_url в PHP и приводит части к виду Guzzle: схема и хост —
// в нижний регистр, порт по умолчанию убирается, недопустимые символы кодируются в %XX
func parseUri(v string) *uri {
	result := &uri{}
	rest := v
	hasAuthority := false

	if colon := strings.IndexByte(v, ':'); colon > 0 && isUriScheme(v[:colon]) {
		after := v[colon+1:]
		switch {
		case isPortLike(after):
			// «host:8080/путь» — это хост с портом, а не схема
			hasAuthority = true
		case strings.HasPrefix(after, "//"):
			result.scheme, rest, hasAuthority = v[:colon], after[2:], true
		default:
			// mailto:, tel: и подобные — после схемы сразу путь
			result.scheme, rest = v[:colon], after
		}
	} else if strings.HasPrefix(v, "//") {
		rest, hasAuthority = v[2:], true
	}

	if hasAuthority {
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		result.parseAuthority(rest[:end])
		rest = rest[end:]
	}

	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest, result.fragment = rest[:i], rest[i+1:]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest, result.query = rest[:i], rest[i+1:]
	}
	result.path = rest

	result.scheme = asciiLower(result.scheme)
	result.host = asciiLower(result.host)
	if result.port != 0 && uriDefaultPorts[result.scheme] == result.port {
		result.port = 0
	}
	result.path = filterUriComponent(result.path, uriPathAllowed)
	result.query = filterUriComponent(result.query, uriQueryAllowed)
	result.fragment = filterUriComponent(result.fragment, uriQueryAllowed)

	return result
}

func (u *uri) parseAuthority(v string) {
	if at := strings.LastIndexByte(v, '@'); at >= 0 {
		user, pass, hasPass := strings.Cut(v[:at], ":")
		u.userInfo = filterUriComponent(user, uriUserInfoAllowed)
		if hasPass {
			u.userInfo += ":" + filterUriComponent(pass, uriUserInfoAllowed)
		}
		v = v[at+1:]
	}

	u.host = v

	// порт — цифры после последнего двоеточия; у IPv6 в скобках двоеточия внутри скобок не в счёт
	colon := strings.LastIndexByte(v, ':')
	if colon < 0 || colon < strings.LastIndexByte(v, ']') {
		return
	}
	if port, err := strconv.Atoi(v[colon+1:]); err == nil && port > 0 && port <= 65535 {
		u.host, u.port = v[:colon], port
	} else if v[colon+1:] == "" {
		u.host = v[:colon]
	}
}

func (u *uri) String() string {
	var sb strings.Builder

	if u.scheme != "" {
		sb.WriteString(u.scheme)
		sb.WriteByte(':')
	}

	authority := u.host
	if u.userInfo != "" {
		authority = u.userInfo + "@" + authority
	}
	if u.port != 0 {
		authority += ":" + strconv.Itoa(u.port)
	}

	if authority != "" || u.scheme == "file" {
		sb.WriteString("//")
		sb.WriteString(authority)
	}
	if authority != "" && u.path != "" && u.path[0] != '/' {
		sb.WriteByte('/')
	}
	sb.WriteString(u.path)

	if u.query != "" {
		sb.WriteByte('?')
		sb.WriteString(u.query)
	}
	if u.fragment != "" {
		sb.WriteByte('#')
		sb.WriteString(u.fragment)
	}

	return sb.String()
}

// isUriScheme — годится ли начало адреса как схема. Guzzle перед разбором кодирует адрес
// (пробел становится «+»), поэтому пробел в схеме проходит, а «+» и «_» — нет.
func isUriScheme(v string) bool {
	for i := range len(v) {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == ' ':
		default:
			return false
		}
	}
	return true
}

// isPortLike — после двоеточия идут только цифры (до конца или до «/»): «host:8080/путь»
func isPortLike(v string) bool {
	digits := 0
	for digits < len(v) && v[digits] >= '0' && v[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < 6 && (digits == len(v) || v[digits] == '/')
}

func asciiLower(v string) string {
	for i := range len(v) {
		if c := v[i]; c >= 'A' && c <= 'Z' {
			return strings.Map(func(r rune) rune {
				if r >= 'A' && r <= 'Z' {
					return r + ('a' - 'A')
				}
				return r
			}, v)
		}
	}
	return v
}

// filterUriComponent кодирует в %XX всё, чего нет в allowed. Готовые %XX не трогаются; «%»,
// за которым не идут две шестнадцатеричные цифры, кодируется как %25.
func filterUriComponent(v string, allowed *byteSet) string {
	clean := true
	for i := range len(v) {
		if !allowed[v[i]] && !(v[i] == '%' && isHexPair(v, i+1)) {
			clean = false
			break
		}
	}
	if clean {
		return v
	}

	var sb strings.Builder
	sb.Grow(len(v) + 16)
	for i := range len(v) {
		c := v[i]
		if allowed[c] || (c == '%' && isHexPair(v, i+1)) {
			sb.WriteByte(c)
		} else {
			writePercent(&sb, c)
		}
	}
	return sb.String()
}

func isHexPair(v string, at int) bool {
	return at+1 < len(v) && isHex(v[at]) && isHex(v[at+1])
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func writePercent(sb *strings.Builder, c byte) {
	sb.WriteByte('%')
	sb.WriteByte(upperHex[c>>4])
	sb.WriteByte(upperHex[c&0x0f])
}

// queryParam — параметр запроса: одно имя и все его значения по порядку. Значение nil — параметр
// без знака «=» («?debug»).
type queryParam struct {
	key    string
	values []*string
}

// queryParams — параметры в порядке появления (как массив PHP)
type queryParams struct {
	items []*queryParam
	index map[string]*queryParam
}

// parseQuery разбирает строку запроса как GuzzleHttp\Psr7\Query::parse: «+» и %XX раскодируются,
// повторяющееся имя копит значения
func parseQuery(v string) *queryParams {
	result := &queryParams{index: map[string]*queryParam{}}
	if v == "" {
		return result
	}

	for pair := range strings.SplitSeq(v, "&") {
		rawKey, rawValue, hasValue := strings.Cut(pair, "=")
		key := phpUrlDecode(rawKey)

		var value *string
		if hasValue {
			value = new(phpUrlDecode(rawValue))
		}

		if param, ok := result.index[key]; ok {
			param.values = append(param.values, value)
			continue
		}
		param := &queryParam{key: key, values: []*string{value}}
		result.items = append(result.items, param)
		result.index[key] = param
	}

	return result
}

// merge — параметры other заменяют одноимённые (на прежнем месте), новые дописываются в конец
func (q *queryParams) merge(other *queryParams) {
	for _, param := range other.items {
		if existing, ok := q.index[param.key]; ok {
			existing.values = param.values
			continue
		}
		q.items = append(q.items, param)
		q.index[param.key] = param
	}
}

// build собирает строку запроса как GuzzleHttp\Psr7\Query::build (кодирование по RFC 3986)
func (q *queryParams) build() string {
	var sb strings.Builder

	first := true
	for _, param := range q.items {
		key := phpRawUrlEncode(param.key)
		for _, value := range param.values {
			if !first {
				sb.WriteByte('&')
			}
			first = false

			sb.WriteString(key)
			if value != nil {
				sb.WriteByte('=')
				sb.WriteString(phpRawUrlEncode(*value))
			}
		}
	}

	return sb.String()
}

// phpUrlDecode — urldecode из PHP: «+» — пробел, %XX — байт, неверный «%» остаётся как есть
func phpUrlDecode(v string) string {
	if !strings.ContainsAny(v, "+%") {
		return v
	}

	var sb strings.Builder
	sb.Grow(len(v))
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c == '+':
			sb.WriteByte(' ')
		case c == '%' && isHexPair(v, i+1):
			sb.WriteByte(unhex(v[i+1])<<4 | unhex(v[i+2]))
			i += 2
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// phpRawUrlEncode — rawurlencode из PHP: всё, кроме букв, цифр и «-_.~», кодируется в %XX
func phpRawUrlEncode(v string) string {
	var sb strings.Builder
	sb.Grow(len(v) + 8)
	for i := range len(v) {
		if c := v[i]; rawUrlUnreserved[c] {
			sb.WriteByte(c)
		} else {
			writePercent(&sb, c)
		}
	}
	return sb.String()
}
