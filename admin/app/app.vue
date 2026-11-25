<script setup lang="ts">
import { ru } from '@nuxt/ui/locale'

// сообщения — снизу справа: сверху справа они закрывали бы кнопки в шапке страницы
</script>

<template>
  <UApp :locale="ru" :toaster="{ position: 'bottom-right' }">
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </UApp>
</template>
