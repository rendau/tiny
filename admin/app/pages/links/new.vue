<script setup lang="ts">
import type { LinkCreateRep } from '~/types/link'
import type { LinkFormErrors } from '~/utils/link'

// Создание короткой ссылки. После создания — короткий адрес с кнопкой «скопировать».
const backTo = useLinksBack()
const { tags, load: loadTags } = useTags()
onMounted(loadTags)

const form = ref(emptyLinkForm())
const submitted = ref(false)
const saving = ref(false)
const error = ref('')
// ошибки по полям, которые вернул сервер; пропадают, как только форму начали править
const serverErrors = ref<LinkFormErrors>({})
const created = ref<LinkCreateRep | null>(null)

const localErrors = computed(() => validateLinkForm(form.value, 'create'))
const errors = computed<LinkFormErrors>(() => ({ ...serverErrors.value, ...(submitted.value ? localErrors.value : {}) }))

watch(form, () => {
  serverErrors.value = {}
  error.value = ''
}, { deep: true })

async function submit() {
  submitted.value = true
  if (Object.keys(localErrors.value).length)
    return
  saving.value = true
  error.value = ''
  try {
    created.value = await linkApi.create(formToCreate(form.value))
  }
  catch (e) {
    serverErrors.value = apiErrorFields(e)
    error.value = apiErrorMessage(e)
  }
  finally {
    saving.value = false
  }
}

function createAnother() {
  form.value = emptyLinkForm()
  submitted.value = false
  created.value = null
  void loadTags()
}
</script>

<template>
  <UDashboardPanel id="link-new">
    <template #header>
      <UDashboardNavbar title="Новая ссылка">
        <template #leading>
          <UDashboardSidebarCollapse />
          <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" aria-label="Назад к списку" title="Назад к списку" :to="backTo" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div class="mx-auto w-full max-w-2xl">
        <UCard v-if="created">
          <div class="space-y-4">
            <div class="flex items-center gap-2 text-highlighted">
              <UIcon name="i-lucide-circle-check" class="size-5 text-success" />
              <h2 class="text-base font-semibold">Ссылка создана</h2>
            </div>
            <div class="flex flex-wrap items-center gap-2 rounded-lg border border-default bg-elevated/50 px-4 py-3">
              <a
                :href="created.short_url"
                target="_blank"
                rel="noopener noreferrer"
                class="min-w-0 flex-1 font-mono text-base font-medium break-all text-highlighted hover:text-primary hover:underline"
              >{{ created.short_url }}</a>
              <CopyButton :text="created.short_url" label="Скопировать" />
            </div>
            <p class="text-sm break-all text-muted">Ведёт на {{ form.long_url.trim() }}</p>
            <div class="flex flex-wrap gap-2">
              <UButton icon="i-lucide-plus" label="Создать ещё одну" @click="createAnother" />
              <UButton color="neutral" variant="outline" label="Изменить эту ссылку" :to="`/links/${created.id}`" />
              <UButton color="neutral" variant="ghost" label="К списку" :to="backTo" />
            </div>
          </div>
        </UCard>

        <form v-else class="space-y-5" novalidate @submit.prevent="submit">
          <LinkForm v-model="form" mode="create" :errors="errors" :tag-suggestions="tags" />
          <UAlert v-if="error" color="error" variant="subtle" title="Ссылка не создана" :description="error" />
          <div class="flex justify-end gap-2">
            <UButton color="neutral" variant="ghost" label="Отмена" :to="backTo" />
            <UButton type="submit" icon="i-lucide-check" label="Создать ссылку" :loading="saving" />
          </div>
        </form>
      </div>
    </template>
  </UDashboardPanel>
</template>
