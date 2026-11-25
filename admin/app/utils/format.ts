// Даты для показа и для полей формы. Время от API — RFC 3339 (UTC); показываем и вводим в часовом
// поясе браузера.

const dateTimeFmt = new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short' })
const dateTimeFullFmt = new Intl.DateTimeFormat('ru-RU', { dateStyle: 'long', timeStyle: 'medium' })

function parse(iso?: string | null): Date | null {
  if (!iso)
    return null
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? null : d
}

// formatDateTime — «25 нояб. 2026 г., 10:01»; пусто — прочерк
export function formatDateTime(iso?: string | null): string {
  const d = parse(iso)
  return d ? dateTimeFmt.format(d) : '—'
}

// formatDateTimeFull — с секундами, для подсказки при наведении
export function formatDateTimeFull(iso?: string | null): string {
  const d = parse(iso)
  return d ? dateTimeFullFmt.format(d) : ''
}

const pad = (n: number) => String(n).padStart(2, '0')

// isoToLocalInput — время API → значение поля <input type="datetime-local"> (местное время)
export function isoToLocalInput(iso?: string | null): string {
  const d = parse(iso)
  if (!d)
    return ''
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// localInputToIso — значение поля datetime-local (местное время) → RFC 3339 в UTC; пусто — undefined
export function localInputToIso(value: string): string | undefined {
  if (!value)
    return undefined
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

// plural — форма слова по числу: plural(3, ['ссылка', 'ссылки', 'ссылок']) → «ссылки»
const pluralRules = new Intl.PluralRules('ru-RU')
export function plural(n: number, forms: [string, string, string]): string {
  const category = pluralRules.select(n)
  return category === 'one' ? forms[0] : category === 'few' ? forms[1] : forms[2]
}
