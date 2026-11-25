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
