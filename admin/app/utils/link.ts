import type { LinkCreate, LinkMain, LinkUpdate } from '~/types/link'

// Состояние ссылки по срокам действия: считается в браузере по его часам
export type LinkState = 'active' | 'expired' | 'pending'

export function linkState(link: Pick<LinkMain, 'valid_since' | 'valid_until'>, now = Date.now()): LinkState {
  if (link.valid_until && new Date(link.valid_until).getTime() <= now)
    return 'expired'
  if (link.valid_since && new Date(link.valid_since).getTime() > now)
    return 'pending'
  return 'active'
}

// shortUrlLabel — короткий адрес без https:// — для показа в таблице
export function shortUrlLabel(shortUrl: string): string {
  return shortUrl.replace(/^https?:\/\//i, '')
}

// normalizeShortCode — в поле кода можно вставить и всю короткую ссылку
// (https://l.demo.test/aBc12?x=1): берётся последняя часть пути без параметров
export function normalizeShortCode(value: string): string {
  const v = value.trim()
  if (!v.includes('/'))
    return v
  const withoutTail = v.split(/[?#]/)[0] ?? ''
  const parts = withoutTail.split('/').filter(Boolean)
  return parts[parts.length - 1] ?? ''
}

// normalizeDomain — «https://SL.demo.test/» → «sl.demo.test»
export function normalizeDomain(value: string): string {
  return value.trim().toLowerCase().replace(/^[a-z][a-z0-9+.-]*:\/\//, '').replace(/\/.*$/, '')
}

// Поля формы ссылки (создание и правка). Сроки — значения полей datetime-local (местное время).
export interface LinkFormState {
  long_url: string
  title: string
  tags: string[]
  valid_since: string
  valid_until: string
  max_visits: number | null
  crawlable: boolean
  forward_query: boolean
  // только при создании: код и домен потом не меняются
  code_mode: 'auto' | 'custom'
  short_code: string
  short_code_length: number | null
  domain: string
}

export type LinkFormErrors = Partial<Record<keyof LinkFormState, string>>

// самая короткая длина генерируемого кода, которую принимает сервер
export const MIN_SHORT_CODE_LENGTH = 4

export function emptyLinkForm(): LinkFormState {
  return {
    long_url: '',
    title: '',
    tags: [],
    valid_since: '',
    valid_until: '',
    max_visits: null,
    crawlable: false,
    forward_query: true,
    code_mode: 'auto',
    short_code: '',
    short_code_length: null,
    domain: '',
  }
}

export function linkToForm(link: LinkMain): LinkFormState {
  return {
    ...emptyLinkForm(),
    long_url: link.long_url,
    title: link.title ?? '',
    tags: [...(link.tags ?? [])],
    valid_since: isoToLocalInput(link.valid_since),
    valid_until: isoToLocalInput(link.valid_until),
    max_visits: link.max_visits ?? null,
    crawlable: !!link.crawlable,
    forward_query: !!link.forward_query,
    short_code: link.short_code,
    domain: link.domain ?? '',
  }
}

function cleanTags(tags: string[]): string[] {
  return [...new Set(tags.map(t => t.trim()).filter(Boolean))]
}

export function validateLinkForm(s: LinkFormState, mode: 'create' | 'edit'): LinkFormErrors {
  const errors: LinkFormErrors = {}

  const url = s.long_url.trim()
  if (!url)
    errors.long_url = 'Укажите длинный адрес'
  else if (/\s/.test(url))
    errors.long_url = 'В адресе не должно быть пробелов'
  else if (!/^[a-z][a-z0-9+.-]*:.+/i.test(url))
    errors.long_url = 'Адрес должен начинаться с https:// или http://'

  if (mode === 'create' && s.code_mode === 'custom') {
    const code = s.short_code.trim()
    if (!code)
      errors.short_code = 'Укажите код или выберите «Сгенерировать»'
    else if (/[\s/?#]/.test(code))
      errors.short_code = 'В коде не должно быть пробелов и знаков / ? #'
  }

  const since = localInputToIso(s.valid_since)
  const until = localInputToIso(s.valid_until)
  if (since && until && new Date(until).getTime() <= new Date(since).getTime())
    errors.valid_until = 'Окончание должно быть позже начала'

  return errors
}

export function formToCreate(s: LinkFormState): LinkCreate {
  return {
    long_url: s.long_url.trim(),
    ...(s.code_mode === 'custom'
      ? { short_code: s.short_code.trim() }
      : s.short_code_length ? { short_code_length: s.short_code_length } : {}),
    domain: normalizeDomain(s.domain) || undefined,
    title: s.title.trim() || undefined,
    tags: cleanTags(s.tags),
    valid_since: localInputToIso(s.valid_since),
    valid_until: localInputToIso(s.valid_until),
    max_visits: s.max_visits ?? undefined,
    crawlable: s.crawlable,
    forward_query: s.forward_query,
  }
}

// formToUpdate — все изменяемые поля разом: сервер заменяет их целиком
export function formToUpdate(s: LinkFormState): LinkUpdate {
  return {
    long_url: s.long_url.trim(),
    title: s.title.trim(),
    tags: cleanTags(s.tags),
    valid_since: localInputToIso(s.valid_since),
    valid_until: localInputToIso(s.valid_until),
    max_visits: s.max_visits ?? undefined,
    crawlable: s.crawlable,
    forward_query: s.forward_query,
  }
}
