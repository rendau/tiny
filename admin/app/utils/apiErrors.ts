// Тексты ошибок API по коду из ErrorRep.code. Незнакомый код показывается сообщением сервера.
export const API_ERRORS: Record<string, string> = {
  not_authorized: 'Неверный логин или пароль, либо вход истёк или учётная запись отключена',
  no_permission: 'Недостаточно прав',
  object_not_found: 'Не найдено',
  invalid_request: 'Проверьте поля формы',
  service_not_available: 'Сервис временно недоступен',
  not_implemented: 'Это действие пока не поддерживается',
  incorrect_page_size: 'Неверный размер страницы',
  id_required: 'Не указан id',
  usr_username_exists: 'Такой логин уже занят',
  login_rate_limited: 'Слишком много попыток входа — подождите минуту',
  usr_self_change: 'Себя нельзя удалить, отключить или лишить прав администратора',
  short_code_exists: 'Такой код уже занят — выберите другой',
}

// Ошибки по полям (ErrorRep.fields): сервер присылает имя поля и короткое слово-причину
// (required, invalid, …). Здесь они превращаются в понятный текст.

const FIELD_LABELS: Record<string, string> = {
  name: 'Имя',
  username: 'Логин',
  password: 'Пароль',
  current_password: 'Текущий пароль',
  long_url: 'Длинный адрес',
  short_code: 'Код',
  short_code_length: 'Длина кода',
  domain: 'Домен',
  title: 'Название',
  tags: 'Метки',
  valid_since: 'Действует с',
  valid_until: 'Действует до',
  max_visits: 'Предел посещений',
}

const FIELD_PROBLEMS: Record<string, string> = {
  required: 'нужно заполнить',
  invalid: 'неверное значение',
  exists: 'уже занято',
  too_long: 'слишком длинное значение',
  too_short: 'слишком короткое значение',
}

// точные тексты для известных пар «поле.причина»
const FIELD_ERRORS: Record<string, string> = {
  'current_password.invalid': 'Текущий пароль неверный',
  'username.invalid': 'Логин: только a–z, 0–9, точка, дефис и подчёркивание, от 3 до 64 символов',
  'password.required': 'Укажите пароль',
  'password.invalid': 'Пароль должен быть не короче 8 символов',
}

// fieldErrorText — текст ошибки одного поля; withLabel — с названием поля (для общего сообщения)
export function fieldErrorText(field: string, problem: string, withLabel = false): string {
  const exact = FIELD_ERRORS[`${field}.${problem}`]
  if (exact)
    return exact
  const text = FIELD_PROBLEMS[problem] ?? problem
  if (!withLabel)
    return text.charAt(0).toUpperCase() + text.slice(1)
  return `${FIELD_LABELS[field] ?? field}: ${text}`
}
