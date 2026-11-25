<!-- Вход и первый запуск: меню нет -->
<template>
  <div class="flex min-h-screen items-center justify-center bg-default p-4">
    <div class="w-full max-w-sm">
      <div class="mb-6 flex items-center justify-center gap-2 text-lg font-bold text-highlighted">
        <AppLogo class="size-7" />
        tiny
      </div>
      <UCard>
        <slot />
      </UCard>
    </div>
  </div>
</template>
