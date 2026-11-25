<script setup lang="ts">
// Первый запуск: людей ещё нет — создаём администратора и сразу входим
definePageMeta({ layout: 'auth' })

const auth = useAuth()

const name = ref('')
const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  loading.value = true
  error.value = ''
  try {
    await usrApi.create({ name: name.value.trim(), username: username.value.trim(), password: password.value })
    auth.bootstrapRequired.value = false
    await auth.login(username.value.trim(), password.value)
    await navigateTo('/links')
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
    <div>
      <h1 class="text-base font-semibold text-highlighted">Первый запуск</h1>
      <p class="mt-1 text-sm text-muted">Здесь ещё никого нет. Создайте администратора — он сможет управлять ссылками и добавлять людей.</p>
    </div>
    <UFormField label="Имя">
      <UInput v-model="name" autofocus class="w-full" />
    </UFormField>
    <UFormField label="Логин" hint="a–z, 0–9, точка, дефис, подчёркивание">
      <UInput v-model="username" autocomplete="username" class="w-full" />
    </UFormField>
    <UFormField label="Пароль" hint="Не короче 8 символов">
      <UInput v-model="password" type="password" autocomplete="new-password" class="w-full" />
    </UFormField>
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UButton type="submit" block label="Создать и войти" :loading="loading" :disabled="!username || !password" />
  </form>
</template>
