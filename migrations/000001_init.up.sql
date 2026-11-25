-- поиск по части адреса и названия в админке (индексы link_*_trgm_idx ниже). Расширение — в
-- схеме public, и в индексах оно названо со схемой: так миграция не зависит от search_path.
-- Блокировка — чтобы два одновременных запуска на одной базе (тесты в своих схемах) не
-- столкнулись на создании расширения: «if not exists» от этого не защищает.
select pg_advisory_xact_lock(7411001);
create extension if not exists pg_trgm with schema public;

-- люди админки: вход по логину и паролю (как в zorko/kusec); password — хеш bcrypt
create table usr (
    id          bigserial primary key,
    created_at  timestamptz not null default now(),
    modified_at timestamptz not null default now(),
    active      boolean     not null default true,
    is_admin    boolean     not null default false,
    name        text        not null default '',
    username    text        not null unique,
    password    text        not null default ''
);

-- короткие ссылки. domain: '' — ссылка основного домена (открывается на любом нашем домене),
-- иначе — только на указанном. short_code чувствителен к регистру (aBc и abc — разные ссылки).
create table link (
    id            bigserial primary key,
    created_at    timestamptz not null default now(),
    modified_at   timestamptz not null default now(),
    domain        text        not null default '',
    short_code    text        not null,
    long_url      text        not null,
    title         text        not null default '',
    tags          text[]      not null default '{}',
    valid_since   timestamptz,
    valid_until   timestamptz,
    max_visits    integer,
    crawlable     boolean     not null default false,
    forward_query boolean     not null default true,
    constraint link_short_code_domain_key unique (short_code, domain)
);

-- поиск уже существующей ссылки по длинному адресу (findIfExists); md5 — чтобы индекс не упирался
-- в предел длины строки индекса на длинных адресах
create index link_long_url_md5_idx on link (md5(long_url));
-- фильтр по метке
create index link_tags_idx on link using gin (tags);
-- список в админке: новые первыми
create index link_created_at_idx on link (created_at desc, id desc);
-- истёкшие ссылки
create index link_valid_until_idx on link (valid_until) where valid_until is not null;
-- поиск в админке по части длинного адреса или названия (ilike '%…%'): без этих индексов на
-- 2 млн ссылок — 5–12 с перебором всей таблицы, с ними — от десятков мс до 1,5 с.
-- Цена: +0,4 ГБ на 2 млн ссылок и вставка в несколько раз медленнее (создание ссылки — те же ~5 мс)
create index link_long_url_trgm_idx on link using gin (long_url public.gin_trgm_ops);
create index link_title_trgm_idx on link using gin (title public.gin_trgm_ops);
