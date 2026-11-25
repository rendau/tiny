// Интерфейс tiny (админка коротких ссылок). SPA без SSR: собранная статика (nuxt generate →
// .output/public) раздаётся Go-сервером на ADMIN_PORT, адрес API подставляется в рантайме через
// /config.js (window.__APP_CONFIG__): один билд на все окружения.
// Дев: `pnpm dev` (порт 3100) + .env (NUXT_PUBLIC_API_BASE_URL).
export default defineNuxtConfig({
  ssr: false,

  modules: ['@nuxt/ui', '@nuxt/eslint'],

  // Иконки и шрифты — только из сборки, без запросов в сеть
  icon: {
    provider: 'none',
    clientBundle: {
      scan: { globInclude: ['**/*.{vue,ts}'] },
    },
  },
  ui: {
    fonts: false,
  },

  // клиенты API (app/api/*.api.ts) — автоимпортом, как utils и composables
  imports: {
    dirs: ['api'],
  },

  css: ['~/assets/css/main.css'],

  app: {
    head: {
      title: 'tiny — короткие ссылки',
      htmlAttrs: { lang: 'ru' },
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
      // рантайм-конфиг: в проде /config.js отдаёт Go-сервер, в деве — заглушка public/config.js
      script: [{ src: '/config.js' }],
    },
  },

  runtimeConfig: {
    public: {
      // адрес API в деве (.env → NUXT_PUBLIC_API_BASE_URL); в проде — /config.js от сервера
      apiBaseUrl: '',
    },
  },

  // Один бандл без динамических подгрузок: страницы и всё остальное — в одном JS, стили — в одном
  // CSS. Проверяет scripts/check-bundle.mjs после сборки.
  vite: {
    build: {
      cssCodeSplit: false,
      chunkSizeWarningLimit: 5000,
      rollupOptions: {
        output: {
          inlineDynamicImports: true,
        },
      },
    },
  },

  // без опроса /_nuxt/builds/latest.json: интерфейс не ходит в сеть сам по себе
  experimental: {
    appManifest: false,
  },

  typescript: {
    strict: true,
    typeCheck: false, // проверка — `pnpm typecheck`
  },

  devtools: { enabled: false },
  compatibilityDate: '2026-01-01',
})
