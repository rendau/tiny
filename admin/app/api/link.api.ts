import type { PaginationInfo } from '~/types/common'
import type { LinkCreate, LinkCreateRep, LinkListFilter, LinkMain, LinkUpdate } from '~/types/link'

export const linkApi = {
  // порядок строк — как отдаёт сервер (новые первыми); page — с 0
  list: (filter: LinkListFilter, page: number, pageSize: number, signal?: AbortSignal) =>
    // total_count_capped — ссылок больше, чем total_count: с фильтром сервер считает не дальше 10 000
    apiFetch<{ pagination_info?: PaginationInfo, results?: LinkMain[], total_count_capped?: boolean }>('/link', {
      query: {
        search: filter.search || undefined,
        short_code: filter.short_code || undefined,
        tag: filter.tag || undefined,
        expired: filter.expired,
        list_params: { page, page_size: pageSize, with_total_count: true },
      },
      signal,
    }),
  get: (id: string) => apiFetch<LinkMain>(`/link/${id}`),
  create: (body: LinkCreate) => apiFetch<LinkCreateRep>('/link', { method: 'POST', body }),
  // update заменяет все изменяемые поля целиком — передавать нужно все
  update: (id: string, body: LinkUpdate) => apiFetch(`/link/${id}`, { method: 'PUT', body }),
  remove: (id: string) => apiFetch(`/link/${id}`, { method: 'DELETE' }),
}

export const tagApi = {
  // метки, которые встречаются у ссылок; search — подстрока в названии
  list: (search = '') => apiFetch<{ results?: string[] }>('/tag', { query: { search: search || undefined } }),
}
