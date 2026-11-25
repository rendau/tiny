<script setup lang="ts">
import type { UsrMain } from '~/types/usr'

// Люди (только администратор): добавить со случайным паролем, роль, отключить, сбросить пароль,
// удалить. «Сохранить и скопировать» кладёт в буфер адрес админки, логин и пароль.
const auth = useAuth()
const toast = useToast()
const { loading, run } = useApiAction()

const items = ref<UsrMain[]>([])
const error = ref('')
const loaded = ref(false)

async function load() {
  error.value = ''
  try {
    items.value = (await usrApi.list({ page: 0, page_size: 100 })).results ?? []
    loaded.value = true
  }
  catch (e) {
    error.value = apiErrorMessage(e)
  }
}
onMounted(load)

const modal = ref(false)
const editing = ref<UsrMain | null>(null)
const name = ref('')
const username = ref('')
const password = ref('')
// сгенерированный пароль показан открыто: администратор должен видеть, что передаёт
const passwordRevealed = ref(false)
const isAdmin = ref(false)
const active = ref(true)
const confirm = ref<UsrMain | null>(null)

function openCreate() {
  editing.value = null
  name.value = ''
  username.value = ''
  password.value = ''
  passwordRevealed.value = false
  isAdmin.value = false
  active.value = true
  modal.value = true
}

function openEdit(u: UsrMain) {
  editing.value = u
  name.value = u.name
  username.value = u.username
  password.value = ''
  passwordRevealed.value = false
  isAdmin.value = u.is_admin
  active.value = u.active
  modal.value = true
}

function fillGeneratedPassword() {
  password.value = generatePassword()
  passwordRevealed.value = true
}

watch(password, (v) => {
  // стёрли пароль — поле снова скрытое
  if (!v)
    passwordRevealed.value = false
})

const isSelf = computed(() => !!editing.value && editing.value.id === auth.user.value?.id)
const canSave = computed(() => !!username.value.trim() && (!!editing.value || !!password.value))

async function save(): Promise<boolean> {
  const body = {
    name: name.value.trim(),
    username: username.value.trim(),
    active: active.value,
    is_admin: isAdmin.value,
    ...(password.value ? { password: password.value } : {}),
  }
  const ok = await run(async () => {
    if (editing.value)
      await usrApi.update(editing.value.id, body)
    else
      await usrApi.create(body)
    return true
  }, { success: 'Сохранено' })
  if (ok) {
    // поменяли себя — обновить имя в меню
    if (isSelf.value)
      auth.user.value = await usrApi.profile().catch(() => auth.user.value)
    modal.value = false
    await load()
  }
  return !!ok
}

// адрес админки, логин и пароль одним сообщением — чтобы переслать в мессенджер
function credentialsMessage(): string {
  const siteUrl = new URL(useRuntimeConfig().app.baseURL || '/', window.location.origin).href
  return [
    `Админка коротких ссылок: ${siteUrl}`,
    `Логин: ${username.value.trim().toLowerCase()}`,
    `Пароль: ${password.value}`,
  ].join('\n')
}

// Буфер пишется до запроса: после закрытия окна пароля уже не увидеть, поэтому человек не
// сохраняется с паролем, который не удалось скопировать.
async function saveAndCopy() {
  try {
    await navigator.clipboard.writeText(credentialsMessage())
  }
  catch {
    toast.add({ title: 'Буфер обмена недоступен, ничего не сохранено', description: 'Нажмите «Сохранить» и скопируйте пароль вручную.', color: 'error' })
    return
  }
  if (await save())
    toast.add({ title: 'Данные для входа скопированы', color: 'success' })
}

async function remove() {
  const u = confirm.value
  if (u && await run(() => usrApi.remove(u.id), { success: 'Удалено' }) !== undefined) {
    confirm.value = null
    await load()
  }
}
</script>

<template>
  <UDashboardPanel id="users">
    <template #header>
      <UDashboardNavbar title="Люди">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
        <template #right>
          <UButton icon="i-lucide-user-plus" label="Добавить человека" @click="openCreate" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <LoadAlert title="Не удалось загрузить людей" :error="error" @retry="load" />
      <div v-if="loaded" class="overflow-x-auto rounded-lg border border-default">
        <table class="w-full min-w-[560px] text-sm">
          <thead class="bg-elevated/50 text-left text-xs text-muted">
            <tr>
              <th class="px-4 py-2 font-medium">Имя</th>
              <th class="px-4 py-2 font-medium">Логин</th>
              <th class="px-4 py-2 font-medium">Роль</th>
              <th class="px-4 py-2 font-medium">Статус</th>
              <th class="px-4 py-2" />
            </tr>
          </thead>
          <tbody class="divide-y divide-default">
            <tr v-for="u in items" :key="u.id" :class="u.active ? '' : 'opacity-60'">
              <td class="px-4 py-2 text-highlighted">{{ u.name }}</td>
              <td class="px-4 py-2 font-mono text-xs">{{ u.username }}</td>
              <td class="px-4 py-2">
                <UBadge :color="u.is_admin ? 'primary' : 'neutral'" variant="subtle" size="sm">{{ u.is_admin ? 'Администратор' : 'Участник' }}</UBadge>
              </td>
              <td class="px-4 py-2">{{ u.active ? 'Активен' : 'Отключён' }}</td>
              <td class="px-4 py-2 text-right whitespace-nowrap">
                <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-pencil" aria-label="Изменить" title="Изменить" @click="openEdit(u)" />
                <UButton v-if="u.id !== auth.user.value?.id" size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" aria-label="Удалить" title="Удалить" @click="confirm = u" />
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <UModal v-model:open="modal" :title="editing ? 'Человек' : 'Добавить человека'">
        <template #body>
          <form id="usr-form" class="space-y-4" @submit.prevent="save">
            <UFormField label="Имя">
              <UInput v-model="name" class="w-full" autofocus />
            </UFormField>
            <UFormField label="Логин" hint="a–z, 0–9, точка, дефис, подчёркивание">
              <UInput v-model="username" autocomplete="off" class="w-full font-mono" />
            </UFormField>
            <UFormField
              :label="editing ? 'Новый пароль (пусто — не менять)' : 'Временный пароль'"
              hint="Не короче 8 символов"
              :description="password ? '«Сохранить и скопировать» положит в буфер адрес админки, логин и пароль одним сообщением' : undefined"
            >
              <UFieldGroup class="w-full">
                <UInput
                  v-model="password"
                  :type="passwordRevealed ? 'text' : 'password'"
                  autocomplete="new-password"
                  class="w-full"
                  :class="passwordRevealed ? 'font-mono' : ''"
                />
                <UButton color="neutral" variant="outline" icon="i-lucide-dices" label="Сгенерировать" @click="fillGeneratedPassword" />
              </UFieldGroup>
            </UFormField>
            <UFormField
              label="Администратор"
              :description="isSelf ? 'Себя нельзя лишить прав администратора или отключить' : 'Администратор ещё и управляет людьми; участник работает только со ссылками'"
            >
              <USwitch v-model="isAdmin" :disabled="isSelf" />
            </UFormField>
            <UFormField label="Активен" description="Отключённый человек не может войти">
              <USwitch v-model="active" :disabled="isSelf" />
            </UFormField>
          </form>
        </template>
        <template #footer>
          <div class="flex w-full justify-end gap-2">
            <UButton color="neutral" variant="ghost" label="Отмена" @click="modal = false" />
            <UButton
              type="submit"
              form="usr-form"
              :color="password ? 'neutral' : 'primary'"
              :variant="password ? 'outline' : 'solid'"
              label="Сохранить"
              :loading="loading"
              :disabled="!canSave"
            />
            <UButton
              v-if="password"
              icon="i-lucide-copy"
              label="Сохранить и скопировать"
              :loading="loading"
              :disabled="!canSave"
              @click="saveAndCopy"
            />
          </div>
        </template>
      </UModal>

      <ConfirmModal
        :open="!!confirm"
        title="Удалить человека?"
        :description="confirm ? `${confirm.name || confirm.username} больше не сможет войти.` : undefined"
        :loading="loading"
        @update:open="v => { if (!v) confirm = null }"
        @confirm="remove"
      />
    </template>
  </UDashboardPanel>
</template>
