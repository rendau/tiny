// Токены входа — в localStorage: интерфейс и API на разных адресах, cookie не подходят.
// Чтение обёрнуто в try — приватный режим может бросать.
const JWT_KEY = 'tiny:jwt'
const REFRESH_KEY = 'tiny:refresh'

function read(key: string): string {
  try {
    return localStorage.getItem(key) ?? ''
  }
  catch {
    return ''
  }
}

function write(key: string, value: string) {
  try {
    if (value)
      localStorage.setItem(key, value)
    else
      localStorage.removeItem(key)
  }
  catch {
    // хранилище недоступно — вход живёт до перезагрузки страницы
  }
}

export function getStoredJwt(): string {
  return read(JWT_KEY)
}

export function getStoredRefresh(): string {
  return read(REFRESH_KEY)
}

export function storeTokens(jwt: string, refresh: string) {
  write(JWT_KEY, jwt)
  write(REFRESH_KEY, refresh)
}

export function clearTokens() {
  storeTokens('', '')
}
