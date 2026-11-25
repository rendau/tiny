# Интерфейс админки tiny

Одностраничное приложение на Nuxt 4 + Nuxt UI 4 (устройство — как у `zorko/admin`). Работает
только в браузере: сборка — обычные файлы, которые раздаёт сам сервис на `ADMIN_PORT`.

## Команды

```bash
pnpm install
pnpm dev        # разработка, http://localhost:3100
pnpm lint       # проверка линтером
pnpm typecheck  # проверка типов
pnpm generate   # сборка в .output/public (+ сжатые копии .br и .gz)
```

Для `pnpm dev` адрес API задаётся в `.env` (см. `.env.example`): `NUXT_PUBLIC_API_BASE_URL` —
адрес HTTP-шлюза сервиса (`HTTP_PORT`). Интерфейс и API в разработке живут на разных адресах,
поэтому сервису нужен `HTTP_CORS=true`.

## Как сборка попадает в сервис

`pnpm generate` кладёт всё в `.output/public`. Эту папку целиком копируют в `admin-ui/`
(`ADMIN_DIR` сервиса). Сервис должен:

- отдавать `/config.js` сам, не из файла: `window.__APP_CONFIG__ = {"apiBaseUrl":"…","auth":{"mode":"local"}};`
  (значение — `ADMIN_API_BASE_URL`). В сборке лежит пустая заглушка `config.js` — она нужна только
  для `pnpm dev`;
- на любой неизвестный путь (`/links`, `/links/15`, `/users`…) отдавать `index.html` — страницы
  открывает сам интерфейс;
- по заголовку `Accept-Encoding` отдавать готовые `.br` / `.gz` копии, если они есть.

Сборка — один файл JS и один файл CSS, без подгрузок по сети (проверяет `scripts/check-bundle.mjs`).

## Что где

- `app/api/` — запросы к API; `client.ts` сам продлевает вход (при ответе 401 меняет токен
  продления на новую пару и повторяет запрос).
- `app/types/` — формы ответов API (по `api/proto/tiny_v1/*.proto`).
- `app/pages/links/` — список ссылок, создание, карточка с правкой.
- `app/pages/users/`, `app/components/ProfileModal.vue` — люди и свой профиль.
- `app/utils/apiErrors.ts` — русские тексты для кодов ошибок сервера.
