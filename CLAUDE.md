# CLAUDE.md

Руководство для Claude Code по работе с этим репозиторием. Go-сервис на Clean Architecture + DDD,
с gRPC/grpc-gateway, Postgres (pgx), proto-генерацией через Makefile.

Конвенции этого стека вынесены в глобальные Claude Code скиллы (`crud`, `mobone`,
`golang-service`, `golang-samber-lo`) — они подхватываются автоматически по описанию.
Ставятся из `github.com/rendau/go-service-skill` (`install.sh`); если их нет в списке скиллов —
предложи разработчику установить.

---

## Структура проекта

### Верхний уровень
- `cmd/main.go` — entrypoint, поднимает `internal/app.App`.
- `internal/` — бизнес-логика и инфраструктура (закрытые пакеты).
- `api/proto/` — исходные `.proto` данного сервиса.
- `pkg/proto/` — сгенерированный код protobuf/grpc/gateway (**не редактировать вручную**).
- `migrations/` — SQL миграции Postgres.
- `docs/` — swagger JSON и статические доки, выдаются через `/docs/*`.
- `vendor-proto/` — внешние `.proto` зависимости (`make refresh-vendor-proto` из `PROTOS_REPO`
  в `Makefile`; пустой — обновляются только вместе с шаблоном).
- `Dockerfile`, `Makefile` — сборка, запуск, генерация proto.
- `.env.example`, `.migrate_scripts.example` — примеры окружения и миграций.

### Внутренние пакеты (`internal/`)
- `internal/app/` — сборка приложения: серверы, миграции, метрики, трассировка, HTTP-gateway, DI.
  - `app.go` — граф зависимостей и запуск компонентов.
  - `grpc.go` — gRPC сервер + интерсепторы ошибок/метрик/трейсинга.
  - `grpc_gateway.go` — HTTP-gateway, CORS, error handler.
  - `system_http_server.go` — системный HTTP-сервер (порт 3003): /healthcheck, /docs/*, /metrics.
  - `migration.go` — запуск миграций из `migrations/`.
- `internal/config/` — конфигурация через env (см. `config.go`).
- `internal/handler/` — транспортный слой.
  - `grpc/` — gRPC handlers.
  - `grpc/dto/` — преобразование protobuf ↔ domain models; общие хелперы — `dto/common.go`
    (`DecodeListParams`, `JsonObjToGrpcStruct`, `GrpcStructToJsonObj`).
  - могут быть и другие транспортные каналы.
- `internal/usecase/` — usecase-слой (валидация, оркестрация сервисов и доменных сервисов).
- `internal/domain/` — доменная модель, сервисы и репозитории.
  - `*/model/` — доменные структуры (entity).
  - `*/service/` — доменные сервисы (инварианты/логика).
  - `*/repo/` — репозитории.
  - `common/` — общие модели/утилиты/PG базовый репозиторий.
- `internal/service/` — сервисы (фоновые/инфраструктурные), для переиспользования или выделения логики.
- `internal/errs/` и `internal/constant/` — общие коды ошибок и константы (`Version`, `Commit`,
  `BuiltAt` — из ldflags при сборке).

---

## Архитектура: слои и зависимости

- **Transport** (`internal/handler/grpc/*`):
  - Работает только с protobuf DTO и usecase-слоем: handler подключает `*usecase.Usecase` напрямую,
    без интерфейса (handler тонкий, отдельно не тестируется).
  - Не обращается напрямую к репозиториям и сервисам.
- **Usecase** (`internal/usecase/*`):
  - Входной слой от транспортного слоя (запросы от внешних систем).
  - Валидация входных параметров.
  - Оркестрация доменных сервисов и сервисов `internal/service/*`.
  - Вход/выход — доменные модели (не protobuf).
  - Зависимости (доменные сервисы, сервисы) принимает через интерфейсы, объявленные в своём
    пакете (`interfaces.go`).
  - Желательно не обращается в соседние usecases.
- **Domain** (`internal/domain/*`):
  - `model/` — структуры данных, сущности (entity).
    - **Запрещены теги сериализации** (`json:"..."`, `yaml:"..."` и т.п.) на полях доменной
      модели — она не знает про транспорт и про формат хранения.
  - `service/` — доменные операции и инварианты. Может использовать только репозиторий.
  - `repo/` — доступ к хранилищам. Может содержать подпапки для разных типов хранилищ или моков.
- **Service** (Infrastructure/background/external integrations, `internal/service/*`):
  - Фоновые процессы, интеграции с внешними системами.
  - Выделенные или переиспользуемые логики.
  - Может использовать другие сервисы `internal/service/*` и доменные сервисы `internal/domain/*/service`.
  - Не обращается в usecase слой.
- **Composition** (`internal/app/`):
  - Сборка зависимостей, запуск серверов, миграций, фоновых сервисов.

### Правило зависимостей
```
handler        → usecase
usecase        → domain service
usecase        → service
service        → service
service        → domain service
domain service → repo
```
- Обратные зависимости **запрещены**.
- К `repo` слою доступ только из `domain service`.

### Хранилища
- Postgres: все доменные entities (см. `migrations/*`).
- **Имена таблиц всегда в единственном числе**, без plural: `usr`, `app`, `secret`, `item`
  (не `usrs`, `apps`, `secrets`, `items`). То же значение указывается в `TableName` репозитория.

### Миграции
- Файлы в `migrations/` в формате `NNNNNN_<name>.up.sql` / `.down.sql` (golang-migrate).
- В `down`-миграциях во всех командах `DROP` обязательно указывать `CASCADE`
  (напр. `drop table if exists <table> cascade;`).
- В `down` объекты удаляются в порядке, обратном `up` (с учётом внешних ключей).

### API
- gRPC сервисы: `api/proto/tiny_v1/*`.
- HTTP-gateway: через grpc-gateway + swagger (`docs/api.swagger.json`).
- DTO маппинг: `internal/handler/grpc/dto`.
- **REST-пути (route paths) всегда в единственном числе**, без plural:
  `/secret`, `/secret/{id}`, `/app`, `/item` (не `/secrets`, `/apps`, `/items`).
- **Пагинация: во всех `list` запросах `page` начинается с `0`** (нумерация страниц с нуля).
- **Для update использовать HTTP-метод `PUT`** (не `PATCH`): `put: "/<entity>/{id}"`.

### Ошибки и валидация
- Семантические ошибки — через `internal/errs` (см. gRPC interceptor в `internal/app/grpc.go`).
  Отказ по делу (не найдено, неверный запрос, неверный код) — всегда `errs.Err` (свой код сервиса
  или общий): такой ответ пишется в лог уровнем info и в метрике `request_total` получает свой
  `code` — это отказ, а не сбой. Любая другая ошибка — `service_not_available` (`errs.IsFailure`):
  лог уровнем error, сбой в метриках.
- Валидация параметров — в usecase.
- Нельзя пробрасывать ошибки наружу без wrapping (оборачивать в `fmt.Errorf("...: %w")`).
- Для работы с ошибками всегда используй `errors.Is` и `errors.AsType`. Избегай прямого
  сравнения ошибок (`==`) и type assertion (`err.(*MyError)`), чтобы корректно обрабатывать
  обёрнутые ошибки.

### Правила изменения кода
- gRPC DTO не должны протекать в доменные сервисы.
- `pkg/proto` и `docs/api.swagger.json` — генерируемые файлы (обновляются через Makefile).
- Генерация идёт только локальным тулчейном из `bin/` (в `.gitignore`): `make proto-tools` ставит
  protoc и плагины закреплённых версий (protoc — `PROTOC_VERSION` в Makefile, protoc-gen-go и
  grpc-gateway — версий из `go.mod`), `generate-proto` вызывает его сам. Глобальные protoc/плагины
  не используются; версии поднимаем правкой Makefile/`go.mod`, всей командой сразу.
- В тестах всегда предпочитай `testify`: `require` для проверок, прерывающих тест,
  и `assert` для остальных утверждений.
- При реализации worker pool / параллельной обработки используй `errgroup`
  (golang.org/x/sync/errgroup), а не ручное управление горутинами через `sync.WaitGroup` + каналы.

---

## Композиционный корень (`internal/app/app.go`)

`app.go` — единственная точка композиции приложения: здесь собирается граф зависимостей
(`repo → service → usecase → handler`) и описывается жизненный цикл. Бизнес-логики тут нет —
только связывание компонентов и управление их запуском/остановкой.

### Тип `App`
- В поля выносится **только то, чем нужно управлять после `Init`**: то, что надо явно
  останавливать (серверы), ждать (фоновые сервисы/обработчики), закрывать (трассировщик,
  pgx pool), а также корневой `ctx` с его `ctxCancel` и `exitCode`.
- Локальные звенья графа (repo, service, usecase, handler), которые нужны только для сборки и
  сразу передаются дальше, **не** выносятся в поля — это локальные переменные внутри `Init`.

### Импорты Композиционного корня
- Группируются блоками с пустой строкой между группами: стандартная библиотека → внешние
  зависимости → внутренние пакеты проекта.
- Внутренние пакеты-конструкторы импортируются с суффиксом-алиасом `P`, и в алиасе прописывается весь путь в camel-case
  (например, `internal/service/mdm` -> `serviceMdmP`, `internal/handler/grpc` -> `handlerGrpcP`, `internal/service/2gis/service` -> `service2gisServiceP`).

### Методы-фазы жизненного цикла
Фиксированный набор методов, каждый делает ровно одно:
- `Init` — создание и связывание всех зависимостей.
- `PreStartHook` — действия перед стартом.
- `Start` — запуск серверов и фоновых сервисов.
- `Listen` — блокировка до сигнала ОС (`SIGINT`/`SIGTERM`).
- `Stop` — отмена контекста и graceful-остановка серверов.
- `WaitJobs` — ожидание завершения фоновых задач.
- `Exit` — закрытие ресурсов и выход с `exitCode`.

### Стиль `Init`
- Сборка идёт **сверху вниз в порядке зависимостей**: инфраструктура (логгер, трассировка,
  pgx pool, кэш, миграции) → доменные блоки → сервисы → серверы.
- Каждый логический блок предваряется коротким комментарием-меткой в нижнем регистре
  (`// ord`, `// checkout`, `// grpc server`).
- Внутри блока цепочка строится единообразно:
  `repo := ...New(...)` → `service := ...New(repo)` → `usecase := ...New(service)` →
  `handler := ...New(usecase)`.
- Блоки, которым не нужны внешние переменные, оборачиваются в анонимный блок `{ ... }` —
  для ограничения области видимости и визуального разделения.

### Обработка ошибок при инициализации
- Используется хелпер `errCheck(err, msg)`: на этапе сборки любая ошибка фатальна
  (лог + `os.Exit(1)`). Ошибки из `Init` наверх не пробрасываются.
- Один заранее объявленный `var err error` переиспользуется по ходу `Init`.

### Парность Start / WaitJobs / Stop
- Для каждого фонового компонента, запускаемого в `Start()`, есть симметричный вызов в
  `WaitJobs()` (`.Wait()`) и/или в `Stop()`. **Порядок перечисления компонентов одинаков во
  всех трёх методах** — это упрощает чтение и сверку.

### Конфигурация и опциональные компоненты
- Все параметры берутся из единого глобального конфига (`config.Conf.*`) прямо в месте
  использования.
- Опциональные компоненты включаются по условию на конфиг (`if config.Conf.X != "" { ... }`),
  с фолбэком на in-memory/no-op реализацию через общий интерфейс.

---

## Runtime и конфигурация

### Запуск
- Entry: `cmd/main.go` → `internal/app.App`.
- На старте выполняются:
  - загрузка env (autoload `.env`),
  - настройка логгера/метрик/трейсинга,
  - pgx pool,
  - миграции (`internal/app/migration.go`),
  - запуск gRPC + HTTP-gateway.

### Переменные окружения
- Описаны в `internal/config/config.go`.
- Примеры: `.env.example`, `.migrate_scripts.example`.

### Системный HTTP-сервер
- Отдельный сервер на порту `3003` (`const systemHttpPort` в `internal/app/system_http_server.go`).
- Обслуживает служебные ручки: `/healthcheck`, `/docs/*`, `/metrics`.

### Интеграция с pulse
- **Необязательна и делается отдельно — только когда об этом просит разработчик.** Без такой
  просьбы pulse не подключать и под него не адаптировать: ни пакета `pulsekit`, ни манифеста, ни
  `/.well-known/pulse`, ни вопросов о команде и критичности.
- Когда разработчик попросил: стандарт, готовый пакет `internal/infra/pulsekit` и шаги подключения
  к сервису на этом шаблоне — в `github.com/rendau/pulse`, `docs/service-manifest.md` (раздел
  «Go: пакет pulsekit»); **сначала прочитай там раздел «Для агента»**.

### Метрики и трассировка
- Prometheus метрики на `/metrics` (системный сервер, порт 3003) при `WITH_METRICS=true`.
  Используется `metrics.Registry`, а не дефолтный `promhttp.Handler()`.
- `request_total{protocol, method, status, code}`: `status` — ok|error, `code` — ok или код
  ошибки ответа (`errs.Code`, набор конечен). Лейблы не убирать: по `code` сбои отличаются
  от отказов по делу.
- Трейсинг Jaeger включается при `WITH_TRACING=true` и `JAEGER_ADDRESS`.

### Документация и healthcheck
- `/healthcheck` — HTTP healthcheck (200 OK), порт 3003.
- `/docs/*` — статические docs + swagger (`docs/api.swagger.json`), порт 3003.

### Сборка
- `make build` создаёт бинарник `cmd/build/svc` (версия, полный коммит и время сборки — в ldflags,
  `internal/constant`; пишутся в лог при старте).
- Dockerfile копирует бинарник, `docs/` и `migrations/` в `/app`.

### Flow проверки изменений
```
make generate-proto  →  gofmt  →  go test ./...  →  go run ./cmd/.
```

---

## Что делает сервис

`tiny` — сервис коротких ссылок, **совместимый по внешнему контракту с Shlink 5.1.8**: им можно
заменить работающий Shlink так, что клиенты не меняют ни адресов, ни запросов, ни ключей.
Повторяется только то, чем реально пользуются (раздел ниже); внутреннее устройство и схема базы — свои.

Четыре части:
- **публичный сервер** (`PUBLIC_PORT`, по умолчанию `8080`) — обычный `net/http`, без gRPC: переход
  по короткой ссылке и `/rest/v3/*`, совместимые с Shlink (`internal/handler/public`);
- **API админки** — gRPC (`GRPC_PORT`) и HTTP-шлюз (`HTTP_PORT`): `UsrService`, `LinkService`,
  `TagService` из `api/proto/tiny_v1/`; вход — свои учётки, JWT (как в `github.com/rendau/zorko`);
- **интерфейс админки** — `admin/` (Nuxt 4 + Nuxt UI 4, как `zorko/admin`); сборка кладётся в
  `admin-ui/` (`make build-admin`), сервис раздаёт её на `ADMIN_PORT` (`8001`), адрес API — в `/config.js`;
- **перенос данных** из базы Shlink — `cmd/shlink-import` (разовый инструмент, не часть сервиса).

Сведения о конкретном боевом контуре (кто ходит, объёмы, порядок замены) в репозиторий не
кладутся — они в локальном `CLAUDE.local.md`.

## Контракт совместимости с Shlink

Образец — `shlinkio/shlink:5.1.8-roadrunner` с настройками `IS_HTTPS_ENABLED=true`,
`DEFAULT_SHORT_CODES_LENGTH=5`, `DISABLE_TRACKING=true`, без `TIMEZONE`. В тестах и эталонах
домены условные: `l.demo.test` (основной) и `sl.demo.test`.

**Правило: при любом расхождении прав настоящий Shlink.** Эталонные ответы сняты с настоящего
Shlink 5.1.8 в docker и лежат в `test/compat/` (сверка — там же, подробности поведения — в
`test/compat/README.md`, раздел «Что выяснилось»). Повторяется не весь Shlink: статистики
переходов, QR-кодов, правил перенаправления, управления ключами и доменами через API нет.

| Запрос | Ответ |
|---|---|
| `GET`, `HEAD /{код}` | `302` + `Location`; нет ссылки, не началась или истекла — `404` со страницей Shlink |
| `POST /rest/v3/short-urls` | создание ссылки, `200` и полный объект ссылки Shlink |
| `GET /rest/v3/short-urls/{код}` | объект ссылки или `404` |
| `GET /rest/health` | для проб kubernetes |
| `GET /robots.txt`, `/favicon.ico` | `200`; `GET /` — `404` |

Переход по ссылке:
- код чувствителен к регистру (`aBc` и `abc` — разные ссылки);
- ссылка основного домена (`domain = ''`) открывается на любом домене сервиса; ссылка с доменом —
  только на нём;
- параметры запроса короткой ссылки дописываются к длинному адресу (`forward_query`);
- ответ — `302` с пустым телом и одним `Location`, без `Content-Type` и `Cache-Control`
  (`http.Redirect` из Go не годится); неподдерживаемый метод — `404`, а не `405`; страниц 404 две;
- посещения не считаются (как при `DISABLE_TRACKING=true`), поэтому `max_visits` хранится, но
  не применяется.

Создание (`POST /rest/v3/short-urls`, заголовок `X-Api-Key`, тело JSON) принимает все поля
создания Shlink: `longUrl`, `findIfExists`, `tags`, `validSince`, `validUntil`, `maxVisits`,
`customSlug`, `shortCodeLength`, `domain`, `title`, `crawlable`, `forwardQuery`. Ответ — поле в
поле и в том же порядке, что у Shlink:

```json
{"shortUrl":"https://l.demo.test/56JzUe","shortCode":"56JzUe","longUrl":"https://…","dateCreated":"2026-11-25T05:01:05+00:00",
 "tags":["news"],"meta":{"validSince":null,"validUntil":"2026-12-05T05:01:00+00:00","maxVisits":null},
 "domain":null,"title":null,"crawlable":false,"forwardQuery":true,
 "visitsSummary":{"total":0,"nonBots":0,"bots":0},"hasRedirectRules":false}
```

- **время в ответах — в UTC (`+00:00`)**, как у Shlink без настройки `TIMEZONE`; от `TZ` пода не
  зависит. Дата без пояса в запросе понимается в том же поясе;
- `findIfExists=true` — вернуть самую старую ссылку с тем же длинным адресом, у которой совпадают
  присланные в запросе поля (метки — точный набор, сроки, `maxVisits`, свой код, домен); `title`,
  `crawlable`, `forwardQuery`, `shortCodeLength` не сравниваются;
- `shortCodeLength` — не меньше 4, верхней границы нет; 0 и меньше 4 → `400`;
- код генерируется из `0-9a-zA-Z`;
- ошибки — `application/problem+json` (нет ключа или он неверный — `401`, неверные данные — `400`,
  занятый `customSlug` — `400`, ссылка не найдена — `404`);
- любой ключ видит все ссылки (как ключи Shlink без ролей).

Сознательно не повторяется: Shlink сам ходит по длинному адресу за заголовком страницы, если
`title` не прислан, — `tiny` наружу не ходит, `title` остаётся `null`; ответы `500` и зависания
Shlink на кривых данных.

Ключи API задаются двумя списками, которые складываются: `API_KEYS` — открытым текстом,
`API_KEY_HASHES` — SHA-256 в hex через запятую, ровно то, что Shlink хранит в `api_keys.key`.
Вторым способом ключи переезжают из Shlink без переноса самих значений (запрос — в
`cmd/shlink-import/README.md`; колонку `name` у Shlink не выводить — у старых ключей в ней сам
ключ). Негодная строка в `API_KEY_HASHES` — сервис не запускается.

### Журнал запросов к `/rest/*`

Нужен после замены Shlink: увидеть, что именно присылают клиенты и на чём они получают отказ.
Код — `internal/handler/public/reqlog.go`.

- Отказ (код ответа от 400) на создании или чтении ссылки пишется **всегда**: строка
  `public: rest request rejected` (5xx — `public: rest request failed`) с кодом, адресом клиента,
  `user_agent`, отпечатком ключа и ответом, в котором названа причина и неверные поля. Тела запроса
  в ней нет.
- Создание ссылки, которое шло дольше 5 секунд, пишется **всегда**, даже успешное: строка
  `public: rest request slow` (уровень warn). Клиент столько не ждёт, и без неё зависание видно
  только по жалобам.
- `LOG_CREATE_REQUESTS=true` — дополнительно пишется **каждый** запрос создания
  (`public: short url created`) и во все строки создания добавляется тело запроса (до 4 КБ).
  В длинных адресах могут быть личные данные — включать на время разбора, потом убрать.
- `key` — отпечаток ключа: первые 8 знаков его SHA-256, то есть начало `api_keys.key` у Shlink и
  строки в `API_KEY_HASHES`; сам ключ в журнал не попадает. `key_valid=false` с отпечатком —
  клиент ходит ключом, которого нет в настройках.
- Неизвестные адреса `/rest/*`, пробы и переходы по ссылкам в этот журнал не идут.

## Тестовый стенд

Только docker, контейнеры с `--rm`. У каждой части свои контейнеры и порты.

| Часть | Что | Контейнер | Порт на хосте | Логин / пароль / БД |
|---|---|---|---|---|
| Сервис (основной стенд) | Postgres 17 | `tiny-pg` | `54340` | `tiny` / `tiny` / `tiny` |
| Тесты входа и людей | Postgres 17 | `tiny-pg-usr` | `54341` | `tiny` / `tiny` / `tiny` |
| Сверка | настоящий Shlink 5.1.8 | `tiny-compat-shlink` | `18081` | ключ API — `compat-api-key` |
| Сверка | Postgres 17 для Shlink | `tiny-compat-shlink-pg` | `54342` | `shlink` / `shlink` / `shlink` |
| Перенос | настоящий Shlink 5.1.8 (наполнить источник) | `tiny-imp-shlink` | `18082` | ключ API — `imp-api-key` |
| Перенос | Postgres 17 — источник (база Shlink) | `tiny-imp-shlink-pg` | `54344` | `shlink` / `shlink` / `shlink` |
| Перенос | Postgres 17 — приёмник (база tiny) | `tiny-imp-pg` | `54345` | `tiny` / `tiny` / `tiny` |

DSN основного стенда: `PG_DSN=postgres://tiny:tiny@localhost:54340/tiny?sslmode=disable`.
Тесты на живой базе помечены тегом `integration` (`go test -tags integration ./...`).

Порты сервиса на стенде: публичный `8080`, gRPC `5050`, HTTP-шлюз `9090`, интерфейс `8001`,
системный `3003` (зашит).

Сервис целиком на стенде: `make build && make build-admin`, затем запуск с настройками из
`.env.example` (для сверки — `API_KEYS=compat-api-key DEFAULT_DOMAIN=l.demo.test IS_HTTPS_ENABLED=true`
и `ADMIN_API_BASE_URL=http://localhost:9090 HTTP_CORS=true`). Интерфейс — `http://localhost:8001`;
тестовый человек стенда — `admin` / `admin-pass-1` (админ), создаётся вручную на странице первого
запуска, в базу не зашит. Сверка: `test/compat/stand.sh up`, затем из `test/compat` —
`TARGET_URL=http://localhost:8080 API_KEY=compat-api-key go test -count=1 ./...`.

Состояние сверки: 53 сценария из 54. Не совпадает один шаг (`create-title`, шаг 2): на
`"title": ""` Shlink возвращает `""`, `tiny` — `null` (колонка `title` не отличает «пусто» от
«нет названия»). Оставлено сознательно.

## Сборка образа

`.github/workflows/deploy.yml`: на push в `master` раннер GitHub прогоняет `go vet` и тесты,
собирает сервер (`make build`) и интерфейс (`make build-admin`), затем образ
`ghcr.io/rendau/tiny:latest` (Dockerfile только копирует бинарник, `admin-ui`, миграции и доки).
