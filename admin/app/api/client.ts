import { FetchError } from 'ofetch'
import type { ErrorRep } from '~/types/common'
import type { TokenPair, UsrMain } from '~/types/usr'

// HTTP-клиент API tiny (grpc-gateway):
// - query: вложенные объекты — через точку (list_params.page_size=50), массивы — повтором ключа,
//   undefined не отправляется; page — с 0;
// - ошибки — телом ErrorRep { code, message, fields }; 401 — нужен вход, 403 — нет прав;
// - вход — свой access-токен (15 минут): при 401 один раз меняем refresh на новую пару и
//   повторяем запрос. Не вышло — на /login.

export type QueryValue = string | number | boolean | undefined | null
export type QueryParams = Record<string, QueryValue | QueryValue[] | Record<string, QueryValue | QueryValue[]>>

export interface ApiRequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  query?: QueryParams
  body?: unknown
  signal?: AbortSignal
  // authRedirect: false — при негодном входе не уводить на /login (проверка «кто вошёл»)
  authRedirect?: boolean
}

const AUTH_PATHS = ['/usr/login', '/usr/token/refresh']

export function flattenQuery(params?: QueryParams): Record<string, string | string[]> {
  const out: Record<string, string | string[]> = {}
  if (!params)
    return out

  const assign = (key: string, value: QueryValue | QueryValue[]) => {
    if (value === undefined || value === null)
      return
    if (Array.isArray(value)) {
      const items = value.filter(v => v !== undefined && v !== null && v !== '').map(String)
      if (items.length)
        out[key] = items
    }
    else {
      out[key] = String(value)
    }
  }

  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null)
      continue
    if (typeof value === 'object' && !Array.isArray(value)) {
      for (const [subKey, subValue] of Object.entries(value))
        assign(`${key}.${subKey}`, subValue)
    }
    else {
      assign(key, value)
    }
  }
  return out
}

// apiErrorMessage — сообщение из ErrorRep: код переводится словарём utils/apiErrors.ts,
// незнакомый — сообщением сервера или как есть
export function apiErrorMessage(error: unknown): string {
  if (error instanceof FetchError) {
    const rep = error.data as ErrorRep | undefined
    if (rep?.code) {
      // «проверьте поля» — сразу говорим, какие именно
      const fields = Object.entries(rep.fields ?? {})
      if (rep.code === 'invalid_request' && fields.length)
        return fields.map(([field, problem]) => fieldErrorText(field, problem, true)).join('; ')
      const known = API_ERRORS[rep.code]
      if (known)
        return known
      return rep.message && rep.message !== rep.code ? rep.message : rep.code
    }
    // ответа нет вовсе — сервер недоступен или запрос не пустил браузер
    if (error.statusCode === undefined)
      return 'Нет связи с сервером'
    return error.message
  }
  return String(error)
}

export function apiErrorCode(error: unknown): string {
  return error instanceof FetchError ? ((error.data as ErrorRep | undefined)?.code ?? '') : ''
}

// apiErrorFields — ошибки по полям, уже понятным текстом: показывать под полем формы
export function apiErrorFields(error: unknown): Record<string, string> {
  const fields = error instanceof FetchError ? ((error.data as ErrorRep | undefined)?.fields ?? {}) : {}
  return Object.fromEntries(Object.entries(fields).map(([field, problem]) => [field, fieldErrorText(field, problem)]))
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof FetchError && error.statusCode === 401
}

// один обмен refresh на все запросы, упавшие с 401 одновременно
let refreshing: Promise<boolean> | null = null

async function refreshTokens(): Promise<boolean> {
  const refresh = getStoredRefresh()
  if (!refresh)
    return false
  refreshing ??= $fetch<TokenPair>('/usr/token/refresh', {
    baseURL: useApiBaseUrl(),
    method: 'POST',
    body: { refresh_token: refresh },
  }).then((pair) => {
    storeTokens(pair.jwt, pair.refresh_token)
    return true
  }).catch(() => false).finally(() => {
    refreshing = null
  })
  return refreshing
}

function authHeaders(): Record<string, string> {
  const token = getStoredJwt()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function request<T>(path: string, options: ApiRequestOptions): Promise<T> {
  return $fetch<T>(path, {
    baseURL: useApiBaseUrl(),
    method: options.method ?? 'GET',
    query: flattenQuery(options.query),
    body: options.body as Record<string, unknown> | undefined,
    signal: options.signal,
    responseType: 'json',
    headers: { Accept: 'application/json', ...authHeaders() },
  })
}

export async function apiFetch<T>(path: string, options: ApiRequestOptions = {}): Promise<T> {
  try {
    return await request<T>(path, options)
  }
  catch (error) {
    if (!isUnauthorized(error) || AUTH_PATHS.includes(path))
      throw error

    if (await refreshTokens())
      return await request<T>(path, options)

    forgetLogin()
    if (options.authRedirect !== false)
      await navigateTo({ path: '/login', query: { redirect: useRoute().fullPath } })
    throw error
  }
}

// forgetLogin — вход больше не годен: забываются и токены, и «кто вошёл» (иначе страница входа
// сочла бы человека вошедшим и вернула его обратно)
export function forgetLogin() {
  clearTokens()
  useState<UsrMain | null>('auth:user').value = null
}
