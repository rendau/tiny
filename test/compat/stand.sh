#!/usr/bin/env bash
# Стенд сверки: настоящий Shlink 5.1.8 со своим Postgres 17 (и отдельный Postgres для tiny).
# Контейнеры и порты — зона «Сверка» из раздела «Тестовый стенд» в CLAUDE.md проекта.
#
#   ./stand.sh up        поднять Shlink и его базу (чистые: контейнеры с --rm, томов нет)
#                        и завести в Shlink метки и домены из seed-tags.txt и seed-domains.txt
#   ./stand.sh up-tiny   поднять базу для tiny (сам tiny запускается отдельно)
#   ./stand.sh down      остановить всё (данные пропадают)
#   ./stand.sh reset     down + up — чистый Shlink
#   ./stand.sh status    что запущено и отвечает ли Shlink
#
# SHLINK_TIMEZONE=Asia/Almaty ./stand.sh reset — поднять Shlink с его собственной настройкой
# часового пояса TIMEZONE (по умолчанию её нет, как у Shlink по умолчанию; от неё зависит
# вид всех дат в ответах — README.md, «Что выяснилось»).
set -euo pipefail

cd "$(dirname "$0")"

NET=tiny-compat

SHLINK_IMAGE=shlinkio/shlink:5.1.8-roadrunner
SHLINK_NAME=tiny-compat-shlink
SHLINK_PORT=18081
SHLINK_API_KEY=compat-api-key

SHLINK_PG_NAME=tiny-compat-shlink-pg
SHLINK_PG_PORT=54342

TINY_PG_NAME=tiny-compat-pg
TINY_PG_PORT=54343

PG_IMAGE=postgres:17

ensure_docker() {
  if docker info >/dev/null 2>&1; then
    return
  fi
  echo "docker не запущен — запускаю"
  open -a Docker 2>/dev/null || true
  for _ in $(seq 1 60); do
    if docker info >/dev/null 2>&1; then
      return
    fi
    sleep 2
  done
  echo "docker так и не поднялся" >&2
  exit 1
}

running() {
  [ -n "$(docker ps -q -f "name=^$1\$")" ]
}

wait_pg() {
  local name=$1 user=$2
  for _ in $(seq 1 60); do
    if docker exec "$name" pg_isready -U "$user" -h 127.0.0.1 >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  echo "Postgres в контейнере $name не поднялся" >&2
  exit 1
}

up_pg() {
  local name=$1 port=$2 user=$3
  if running "$name"; then
    echo "$name уже запущен"
    return
  fi
  docker run --rm -d --name "$name" --network "$NET" \
    -p "127.0.0.1:$port:5432" \
    -e POSTGRES_USER="$user" -e POSTGRES_PASSWORD="$user" -e POSTGRES_DB="$user" \
    -e TZ=Asia/Almaty \
    "$PG_IMAGE" >/dev/null
  wait_pg "$name" "$user"
  echo "$name: localhost:$port ($user / $user / $user)"
}

up_shlink() {
  if running "$SHLINK_NAME"; then
    echo "$SHLINK_NAME уже запущен"
    return
  fi
  local extra_env=()
  if [ -n "${SHLINK_TIMEZONE:-}" ]; then
    extra_env+=(-e "TIMEZONE=$SHLINK_TIMEZONE")
  fi

  # настройки образца (см. «Контракт совместимости с Shlink» в CLAUDE.md)
  docker run --rm -d --name "$SHLINK_NAME" --network "$NET" \
    -p "127.0.0.1:$SHLINK_PORT:8080" \
    -e DEFAULT_DOMAIN=l.demo.test \
    -e IS_HTTPS_ENABLED=true \
    -e DEFAULT_SHORT_CODES_LENGTH=5 \
    -e DISABLE_TRACKING=true \
    -e TZ=Asia/Almaty \
    -e INITIAL_API_KEY="$SHLINK_API_KEY" \
    -e DB_DRIVER=postgres \
    -e DB_HOST="$SHLINK_PG_NAME" \
    -e DB_PORT=5432 \
    -e DB_NAME=shlink \
    -e DB_USER=shlink \
    -e DB_PASSWORD=shlink \
    ${extra_env[@]+"${extra_env[@]}"} \
    "$SHLINK_IMAGE" >/dev/null
  for _ in $(seq 1 90); do
    if curl -fsS -m 2 -o /dev/null "http://localhost:$SHLINK_PORT/rest/health" 2>/dev/null; then
      seed_shlink
      echo "$SHLINK_NAME: http://localhost:$SHLINK_PORT (ключ API — $SHLINK_API_KEY)"
      return
    fi
    if ! running "$SHLINK_NAME"; then
      echo "$SHLINK_NAME упал при старте" >&2
      exit 1
    fi
    sleep 1
  done
  echo "Shlink не ответил на /rest/health за 90 с; журнал: docker logs $SHLINK_NAME" >&2
  exit 1
}

# Метки и домены заводятся заранее, собственной командой Shlink (отдельный процесс).
#
# Зачем: у Shlink 5.1.8 ошибка — новую метку или новый домен, впервые пришедшие в запросе
# POST /rest/v3/short-urls, он оставляет заблокированными, и следующий запрос с той же меткой
# или доменом зависает навсегда (подробности — README.md, «Что выяснилось»). Если метка или
# домен уже есть в базе, блокировка снимается сразу. Поэтому всё, что встречается в сценариях,
# перечислено в seed-tags.txt и seed-domains.txt (за полнотой следит тест TestSeedCoversScenarios).
seed_shlink() {
  local tag_args=() line
  while IFS= read -r line; do
    [ -n "$line" ] && tag_args+=(--tag "$line")
  done < seed-tags.txt

  docker exec "$SHLINK_NAME" shlink short-url:create "${tag_args[@]}" \
    -- "https://seed.example.invalid/tags" >/dev/null

  while IFS= read -r line; do
    [ -n "$line" ] || continue
    docker exec "$SHLINK_NAME" shlink short-url:create --domain "$line" \
      -- "https://seed.example.invalid/domain" >/dev/null
  done < seed-domains.txt

  echo "$SHLINK_NAME: метки и домены заведены"
}

cmd_up() {
  ensure_docker
  docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
  up_pg "$SHLINK_PG_NAME" "$SHLINK_PG_PORT" shlink
  up_shlink
}

cmd_up_tiny() {
  ensure_docker
  docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
  up_pg "$TINY_PG_NAME" "$TINY_PG_PORT" tiny
  echo "PG_DSN=postgres://tiny:tiny@localhost:$TINY_PG_PORT/tiny?sslmode=disable"
}

cmd_down() {
  ensure_docker
  for name in "$SHLINK_NAME" "$SHLINK_PG_NAME" "$TINY_PG_NAME"; do
    if running "$name"; then
      docker stop "$name" >/dev/null
      echo "$name остановлен"
    fi
  done
  docker network rm "$NET" >/dev/null 2>&1 || true
}

cmd_status() {
  ensure_docker
  docker ps -a --filter "name=^tiny-compat-" --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
  if curl -fsS -m 2 "http://localhost:$SHLINK_PORT/rest/health" 2>/dev/null; then
    echo
  else
    echo "Shlink на localhost:$SHLINK_PORT не отвечает"
  fi
}

case "${1:-}" in
  up) cmd_up ;;
  up-tiny) cmd_up_tiny ;;
  down) cmd_down ;;
  reset) cmd_down; cmd_up ;;
  status) cmd_status ;;
  *)
    echo "использование: $0 up|up-tiny|down|reset|status" >&2
    exit 2
    ;;
esac
