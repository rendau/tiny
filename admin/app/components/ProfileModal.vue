<script setup lang="ts">
// Свой профиль: имя и пароль (смена пароля требует текущий)
const open = defineModel<boolean>('open', { required: true })
const auth = useAuth()
const { loading, run } = useApiAction()

const name = ref('')
const currentPassword = ref('')
const password = ref('')

watch(open, (v) => {
  if (v) {
    name.value = auth.user.value?.name ?? ''
    currentPassword.value = ''
    password.value = ''
  }
})

// новый пароль без текущего сервер не примет — не даём отправить
const canSave = computed(() => !password.value || !!currentPassword.value)

async function save() {
  const ok = await run(() => usrApi.updateProfile({
    name: name.value.trim(),
    ...(password.value ? { password: password.value, current_password: currentPassword.value } : {}),
  }), { success: 'Профиль сохранён' })
  if (ok !== undefined) {
    auth.user.value = await usrApi.profile()
    open.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" title="Профиль">
    <template #body>
      <form id="profile-form" class="space-y-4" @submit.prevent="save">
        <UFormField label="Логин">
          <UInput :model-value="auth.user.value?.username" disabled class="w-full font-mono" />
        </UFormField>
        <UFormField label="Имя">
          <UInput v-model="name" class="w-full" />
        </UFormField>
        <USeparator label="Смена пароля" />
        <UFormField label="Текущий пароль" description="Нужен, только если меняете пароль">
          <UInput v-model="currentPassword" type="password" autocomplete="current-password" class="w-full" />
        </UFormField>
        <UFormField label="Новый пароль" hint="Не короче 8 символов">
          <UInput v-model="password" type="password" autocomplete="new-password" class="w-full" />
        </UFormField>
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" label="Отмена" @click="open = false" />
        <UButton type="submit" form="profile-form" label="Сохранить" :loading="loading" :disabled="!canSave" />
      </div>
    </template>
  </UModal>
</template>
