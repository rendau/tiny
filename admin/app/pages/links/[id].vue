<script setup lang="ts">
import type { LinkMain } from '~/types/link'
import type { LinkFormErrors } from '~/utils/link'

// Карточка ссылки: правка всего, кроме кода и домена, и удаление
const route = useRoute()
const toast = useToast()
const backTo = useLinksBack()
const { loading: removing, run } = useApiAction()
const { tags, load: loadTags } = useTags()

const id = computed(() => String(route.params.id))

const link = ref<LinkMain | null>(null)
const form = ref(emptyLinkForm())
// что лежит на сервере — чтобы понять, есть ли несохранённые правки
const savedSnapshot = ref('')
const loadError = ref('')
const notFound = ref(false)

const submitted = ref(false)
const saving = ref(false)
const error = ref('')
// ошибки по полям, которые вернул сервер; пропадают, как только форму начали править
const serverErrors = ref<LinkFormErrors>({})

function applyLink(l: LinkMain) {
  link.value = l
  form.value = linkToForm(l)
  savedSnapshot.value = JSON.stringify(formToUpdate(form.value))
  submitted.value = false
}

async function load() {
  loadError.value = ''
  notFound.value = false
  try {
    applyLink(await linkApi.get(id.value))
  }
  catch (e) {
    if (apiErrorCode(e) === 'object_not_found')
      notFound.value = true
    else
      loadError.value = apiErrorMessage(e)
  }
}
onMounted(() => {
  void load()
  void loadTags()
})

const localErrors = computed(() => validateLinkForm(form.value, 'edit'))
const errors = computed<LinkFormErrors>(() => ({ ...serverErrors.value, ...(submitted.value ? localErrors.value : {}) }))
const changed = computed(() => !!link.value && JSON.stringify(formToUpdate(form.value)) !== savedSnapshot.value)

watch(form, () => {
  serverErrors.value = {}
  error.value = ''
}, { deep: true })

async function save() {
  submitted.value = true
  if (Object.keys(localErrors.value).length)
    return
  saving.value = true
  error.value = ''
  try {
    await linkApi.update(id.value, formToUpdate(form.value))
    toast.add({ title: 'Сохранено', color: 'success' })
    // показать то, что сервер сохранил на самом деле
    applyLink(await linkApi.get(id.value))
    void loadTags()
  }
  catch (e) {
    serverErrors.value = apiErrorFields(e)
    error.value = apiErrorMessage(e)
  }
  finally {
    saving.value = false
  }
}

const confirmOpen = ref(false)
async function remove() {
  if (await run(() => linkApi.remove(id.value), { success: 'Ссылка удалена' }) !== undefined) {
    confirmOpen.value = false
    await navigateTo(backTo.value)
  }
}

const state = computed(() => link.value ? linkState(link.value) : 'active')
</script>

<template>
  <UDashboardPanel id="link-edit">
    <template #header>
      <UDashboardNavbar :title="link ? shortUrlLabel(link.short_url) : 'Ссылка'">
        <template #leading>
          <UDashboardSidebarCollapse />
          <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" aria-label="Назад к списку" title="Назад к списку" :to="backTo" />
        </template>
        <template v-if="link" #right>
          <UButton color="error" variant="ghost" icon="i-lucide-trash-2" label="Удалить" @click="confirmOpen = true" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div class="mx-auto w-full max-w-2xl space-y-5">
        <LoadAlert title="Не удалось загрузить ссылку" :error="loadError" @retry="load" />

        <EmptyState
          v-if="notFound"
          icon="i-lucide-link-2-off"
          title="Такой ссылки нет"
          description="Возможно, её удалили."
        >
          <UButton color="neutral" variant="soft" label="К списку" :to="backTo" />
        </EmptyState>

        <template v-if="link">
          <div class="space-y-3 rounded-lg border border-default bg-elevated/50 px-4 py-3">
            <div class="flex flex-wrap items-center gap-2">
              <a
                :href="link.short_url"
                target="_blank"
                rel="noopener noreferrer"
                class="min-w-0 flex-1 font-mono text-base font-medium break-all text-highlighted hover:text-primary hover:underline"
              >{{ link.short_url }}</a>
              <UBadge v-if="state === 'expired'" color="error" variant="subtle">истекла</UBadge>
              <UBadge v-else-if="state === 'pending'" color="warning" variant="subtle">ещё не началась</UBadge>
              <CopyButton :text="link.short_url" label="Скопировать" />
            </div>
            <dl class="grid gap-x-6 gap-y-1 text-xs text-muted sm:grid-cols-2">
              <div class="flex gap-1.5">
                <dt>Код:</dt>
                <dd class="font-mono text-default">{{ link.short_code }}</dd>
              </div>
              <div class="flex gap-1.5">
                <dt>Домен:</dt>
                <dd class="text-default">{{ link.domain || 'основной (любой наш домен)' }}</dd>
              </div>
              <div class="flex gap-1.5">
                <dt>Создана:</dt>
                <dd class="text-default">{{ formatDateTime(link.created_at) }}</dd>
              </div>
              <div class="flex gap-1.5">
                <dt>Изменена:</dt>
                <dd class="text-default">{{ formatDateTime(link.modified_at) }}</dd>
              </div>
            </dl>
            <p class="text-xs text-dimmed">Код и домен изменить нельзя. Нужен другой — создайте новую ссылку.</p>
          </div>

          <form class="space-y-5" novalidate @submit.prevent="save">
            <LinkForm v-model="form" mode="edit" :errors="errors" :tag-suggestions="tags" />
            <UAlert v-if="error" color="error" variant="subtle" title="Не сохранено" :description="error" />
            <div class="flex items-center justify-end gap-2">
              <span v-if="changed" class="me-auto text-xs text-muted">Есть несохранённые изменения</span>
              <UButton color="neutral" variant="ghost" label="К списку" :to="backTo" />
              <UButton type="submit" icon="i-lucide-check" label="Сохранить" :loading="saving" :disabled="!changed" />
            </div>
          </form>
        </template>
      </div>

      <ConfirmModal
        v-model:open="confirmOpen"
        title="Удалить ссылку?"
        :description="link ? `${shortUrlLabel(link.short_url)} перестанет открываться. Вернуть её будет нельзя.` : undefined"
        :loading="removing"
        @confirm="remove"
      />
    </template>
  </UDashboardPanel>
</template>
