package public

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/rendau/tiny/internal/domain/link/model"
)

// Shlink собирает JSON через json_encode из PHP, и клиенты получают его в таком виде. Чтобы ответ
// совпадал байт в байт, строки кодируются так же, а не через encoding/json:
//   - успешные ответы (флаги Diactoros JsonResponse): всё не-ASCII и символы < > & ' " заменяются
//     кодом символа (обратная косая черта, u, четыре шестнадцатеричные цифры); «/» не экранируется;
//   - ответы об ошибке (problem+json): не-ASCII как есть, кавычка — с обратной косой чертой,
//     < > & как есть.

const lowerHex = "0123456789abcdef"

// shlinkDateLayout — формат дат в ответах Shlink (ATOM)
const shlinkDateLayout = "2006-01-02T15:04:05-07:00"

// appendPhpString дописывает строку в кавычках, закодированную как в успешных ответах Shlink
func appendPhpString(dst []byte, v string) []byte {
	dst = append(dst, '"')

	for i := 0; i < len(v); {
		c := v[i]
		if c < utf8.RuneSelf {
			i++
			switch c {
			case '"':
				dst = append(dst, '\\', 'u', '0', '0', '2', '2')
			case '\'':
				dst = append(dst, '\\', 'u', '0', '0', '2', '7')
			case '<':
				dst = append(dst, '\\', 'u', '0', '0', '3', 'C')
			case '>':
				dst = append(dst, '\\', 'u', '0', '0', '3', 'E')
			case '&':
				dst = append(dst, '\\', 'u', '0', '0', '2', '6')
			case '\\':
				dst = append(dst, `\\`...)
			case '\b':
				dst = append(dst, `\b`...)
			case '\f':
				dst = append(dst, `\f`...)
			case '\n':
				dst = append(dst, `\n`...)
			case '\r':
				dst = append(dst, `\r`...)
			case '\t':
				dst = append(dst, `\t`...)
			default:
				if c < 0x20 {
					dst = appendUnicodeEscape(dst, uint16(c))
				} else {
					dst = append(dst, c)
				}
			}
			continue
		}

		r, size := utf8.DecodeRuneInString(v[i:])
		i += size

		if r >= 0x10000 {
			// за пределами основной плоскости — парой суррогатов
			r -= 0x10000
			dst = appendUnicodeEscape(dst, uint16(0xd800+(r>>10)))
			dst = appendUnicodeEscape(dst, uint16(0xdc00+(r&0x3ff)))
		} else {
			dst = appendUnicodeEscape(dst, uint16(r))
		}
	}

	return append(dst, '"')
}

func appendUnicodeEscape(dst []byte, v uint16) []byte {
	return append(dst, '\\', 'u', lowerHex[v>>12], lowerHex[v>>8&0xf], lowerHex[v>>4&0xf], lowerHex[v&0xf])
}

// problemString — строка в кавычках, закодированная как в ответах об ошибке Shlink
func problemString(v string) string {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)

	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// encodeShortUrl — объект ссылки Shlink: поле в поле и в том же порядке
func encodeShortUrl(link *model.Main, zone *time.Location) []byte {
	dst := make([]byte, 0, 512)

	dst = append(dst, `{"shortUrl":`...)
	dst = appendPhpString(dst, link.ShortUrl)
	dst = append(dst, `,"shortCode":`...)
	dst = appendPhpString(dst, link.ShortCode)
	dst = append(dst, `,"longUrl":`...)
	dst = appendPhpString(dst, link.LongUrl)
	dst = append(dst, `,"dateCreated":`...)
	dst = appendPhpString(dst, link.CreatedAt.In(zone).Format(shlinkDateLayout))

	dst = append(dst, `,"tags":[`...)
	for i, tag := range link.Tags {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendPhpString(dst, tag)
	}

	dst = append(dst, `],"meta":{"validSince":`...)
	dst = appendNullableDate(dst, link.ValidSince, zone)
	dst = append(dst, `,"validUntil":`...)
	dst = appendNullableDate(dst, link.ValidUntil, zone)
	dst = append(dst, `,"maxVisits":`...)
	if link.MaxVisits == nil {
		dst = append(dst, `null`...)
	} else {
		dst = strconv.AppendInt(dst, int64(*link.MaxVisits), 10)
	}

	// пустой домен и пустое название Shlink отдаёт как null
	dst = append(dst, `},"domain":`...)
	dst = appendNullableString(dst, link.Domain)
	dst = append(dst, `,"title":`...)
	dst = appendNullableString(dst, link.Title)

	dst = append(dst, `,"crawlable":`...)
	dst = strconv.AppendBool(dst, link.Crawlable)
	dst = append(dst, `,"forwardQuery":`...)
	dst = strconv.AppendBool(dst, link.ForwardQuery)

	// посещения не считаются, правил перенаправления нет — значения постоянные
	dst = append(dst, `,"visitsSummary":{"total":0,"nonBots":0,"bots":0},"hasRedirectRules":false}`...)

	return dst
}

func appendNullableString(dst []byte, v string) []byte {
	if v == "" {
		return append(dst, `null`...)
	}
	return appendPhpString(dst, v)
}

func appendNullableDate(dst []byte, v *time.Time, zone *time.Location) []byte {
	if v == nil {
		return append(dst, `null`...)
	}
	return appendPhpString(dst, v.In(zone).Format(shlinkDateLayout))
}
