<script setup lang="ts">
definePageMeta({ layout: 'auth' })

// Вход по логину и паролю
const auth = useAuth()
const route = useRoute()

const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

// после входа — туда, откуда пришли (только на свои страницы)
const redirect = computed(() => typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') && !route.query.redirect.startsWith('//') ? route.query.redirect : '/links')

async function submit() {
  loading.value = true
  error.value = ''
  try {
    await auth.login(username.value, password.value)
    await navigateTo(redirect.value)
  }
  catch (e) {
    error.value = apiErrorMessage(e)
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="submit">
    <h1 class="text-base font-semibold text-highlighted">Вход</h1>
    <UFormField label="Логин">
      <UInput v-model="username" autocomplete="username" autofocus class="w-full" />
    </UFormField>
    <UFormField label="Пароль">
      <UInput v-model="password" type="password" autocomplete="current-password" class="w-full" />
    </UFormField>
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UButton type="submit" block label="Войти" :loading="loading" :disabled="!username || !password" />
  </form>
</template>
