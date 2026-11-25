// Короткая ссылка (api/proto/tiny_v1/link.proto). Время — RFC 3339; незаданное приходит как null
// или не приходит вовсе.

export interface LinkMain {
  id: string
  created_at: string
  modified_at: string
  // пусто — ссылка основного домена
  domain: string
  short_code: string
  // полный короткий адрес, собирает сервер
  short_url: string
  long_url: string
  title: string
  tags: string[]
  valid_since?: string | null
  valid_until?: string | null
  max_visits?: number | null
  crawlable: boolean
  forward_query: boolean
}

// LinkListFilter — фильтры списка; пустое значение — фильтр не задан
export interface LinkListFilter {
  search?: string
  short_code?: string
  tag?: string
  // true — только истёкшие, false — только действующие
  expired?: boolean
}

export interface LinkCreate {
  long_url: string
  short_code?: string
  short_code_length?: number
  domain?: string
  title?: string
  tags: string[]
  valid_since?: string
  valid_until?: string
  max_visits?: number
  crawlable?: boolean
  forward_query?: boolean
}

export interface LinkCreateRep {
  id: string
  short_code: string
  short_url: string
}

// LinkUpdate — полная замена изменяемых полей: не переданный срок или предел снимается
export interface LinkUpdate {
  long_url: string
  title: string
  tags: string[]
  valid_since?: string
  valid_until?: string
  max_visits?: number
  crawlable: boolean
  forward_query: boolean
}
