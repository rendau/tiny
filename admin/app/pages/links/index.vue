<script setup lang="ts">
import type { LinkListFilter, LinkMain } from '~/types/link'

// Список коротких ссылок — главный экран. Фильтры и страница живут в адресе: ссылку на выборку
// можно переслать.
const route = useRoute()
const { loading: removing, run } = useApiAction()

const PAGE_SIZE = 25
const { state: filters, reset } = useQueryFilters({
  q: '',
  code: '',
  tag: '',
  state: 'all',
  page: '0',
})

// задан ли хоть один фильтр (номер страницы — не фильтр)
const filtered = computed(() => !!filters.q || !!filters.code || !!filters.tag || filters.state !== 'all')

const stateItems = [
  { label: 'Все', value: 'all' },
  { label: 'Действующие', value: 'active' },
  { label: 'Истёкшие', value: 'expired' },
]

// поля поиска пишут в фильтр с задержкой, чтобы не слать запрос на каждую букву
const qInput = ref(filters.q)
const codeInput = ref(filters.code)
let typingTimer: ReturnType<typeof setTimeout> | undefined
function applyTyped() {
  clearTimeout(typingTimer)
  filters.q = qInput.value.trim()
  // в поле кода можно вставить и всю короткую ссылку
  filters.code = normalizeShortCode(codeInput.value)
}
watch([qInput, codeInput], () => {
  clearTimeout(typingTimer)
  typingTimer = setTimeout(applyTyped, 350)
})
// сброс фильтров и щелчок по метке меняют фильтр снаружи — поля должны это показать
watch(() => [filters.q, filters.code], () => {
  if (filters.q !== qInput.value.trim())
    qInput.value = filters.q
  if (filters.code !== normalizeShortCode(codeInput.value))
    codeInput.value = filters.code
})
onBeforeUnmount(() => clearTimeout(typingTimer))

const apiFilter = computed<LinkListFilter>(() => ({
  search: filters.q,
  short_code: filters.code,
  tag: filters.tag,
  expired: filters.state === 'expired' ? true : filters.state === 'active' ? false : undefined,
}))

const items = ref<LinkMain[]>([])
const total = ref(0)
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
// «сейчас» для пометки «истекла»: обновляется при каждой загрузке
const now = ref(Date.now())

let controller: AbortController | null = null
async function load() {
  controller?.abort()
  const own = new AbortController()
  controller = own
  loading.value = true
  error.value = ''
  try {
    const pageIndex = Math.max(0, Number(filters.page) || 0)
    const rep = await linkApi.list(apiFilter.value, pageIndex, PAGE_SIZE, own.signal)
    items.value = rep.results ?? []
    total.value = Number(rep.pagination_info?.total_count ?? 0)
    now.value = Date.now()
    loaded.value = true
    // страница за концом списка (после удаления или по старой ссылке) — на последнюю
    if (!items.value.length && pageIndex > 0)
      filters.page = String(Math.max(0, Math.ceil(total.value / PAGE_SIZE) - 1))
  }
  catch (e) {
    if (own.signal.aborted)
      return
    error.value = apiErrorMessage(e)
  }
  finally {
    if (controller === own)
      loading.value = false
  }
}

watch(() => [filters.q, filters.code, filters.tag, filters.state, filters.page], load, { immediate: true })

// новый фильтр — с первой страницы
watch(() => [filters.q, filters.code, filters.tag, filters.state], () => {
  filters.page = '0'
})

// карточка ссылки возвращается сюда же — с теми же фильтрами и страницей
const backTo = useLinksBack()
watch(() => route.fullPath, (path) => {
  if (route.path === '/links')
    backTo.value = path
}, { immediate: true })

const { tags, load: loadTags } = useTags()
onMounted(loadTags)
// метка из адреса страницы может не встречаться в списке меток — всё равно показываем её выбранной
const tagItems = computed(() => [...new Set([...tags.value, ...(filters.tag ? [filters.tag] : [])])].sort((a, b) => a.localeCompare(b, 'ru')))
const tagModel = computed<string | undefined>({
  get: () => filters.tag || undefined,
  set: (v) => {
    filters.tag = v ?? ''
  },
})

const page = computed({
  get: () => (Number(filters.page) || 0) + 1,
  set: (v: number) => {
    filters.page = String(v - 1)
  },
})

const totalLabel = computed(() => `${total.value.toLocaleString('ru-RU')} ${plural(total.value, ['ссылка', 'ссылки', 'ссылок'])}`)

const confirm = ref<LinkMain | null>(null)
async function remove() {
  const link = confirm.value
  if (link && await run(() => linkApi.remove(link.id), { success: 'Ссылка удалена' }) !== undefined) {
    confirm.value = null
    await load()
    void loadTags()
  }
}
</script>

<template>
  <UDashboardPanel id="links">
    <template #header>
      <UDashboardNavbar title="Ссылки">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
        <template #right>
          <UButton icon="i-lucide-refresh-cw" color="neutral" variant="ghost" :loading="loading" aria-label="Обновить" title="Обновить" @click="load" />
          <UButton icon="i-lucide-plus" label="Создать ссылку" to="/links/new" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div class="space-y-3">
        <div class="flex flex-wrap items-center gap-2">
          <UInput
            v-model="qInput"
            icon="i-lucide-search"
            placeholder="Часть длинного адреса или названия"
            class="w-full sm:w-80"
            aria-label="Поиск по длинному адресу или названию"
            @keydown.enter="applyTyped"
          />
          <UInput
            v-model="codeInput"
            icon="i-lucide-hash"
            placeholder="Точный код"
            class="w-full font-mono sm:w-44"
            aria-label="Точный код короткой ссылки"
            @keydown.enter="applyTyped"
          />
          <USelectMenu
            v-model="tagModel"
            :items="tagItems"
            clear
            icon="i-lucide-tag"
            placeholder="Все метки"
            class="w-full sm:w-52"
            aria-label="Метка"
          />
          <UTabs v-model="filters.state" :items="stateItems" :content="false" size="sm" class="w-full sm:w-auto" />
          <UButton v-if="filtered" color="neutral" variant="link" label="Сбросить" @click="reset" />
        </div>

        <LoadAlert title="Не удалось загрузить ссылки" :error="error" @retry="load" />

        <EmptyState
          v-if="loaded && !items.length && !loading && !error && !filtered"
          icon="i-lucide-link"
          title="Ссылок пока нет"
          description="Создайте первую короткую ссылку — она появится в этом списке."
        >
          <UButton to="/links/new" icon="i-lucide-plus" label="Создать ссылку" variant="soft" />
        </EmptyState>
        <EmptyState
          v-else-if="loaded && !items.length && !loading && !error"
          icon="i-lucide-search-x"
          title="Ничего не найдено"
          description="Попробуйте изменить поиск или сбросить фильтры."
        >
          <UButton color="neutral" variant="soft" label="Сбросить фильтры" @click="reset" />
        </EmptyState>

        <template v-if="items.length">
          <p class="text-xs text-muted">Найдено: {{ totalLabel }}</p>
          <div class="overflow-x-auto rounded-lg border border-default" :class="loading ? 'opacity-60' : ''">
            <table class="w-full min-w-[960px] table-fixed text-sm">
              <thead class="bg-elevated/50 text-left text-xs text-muted">
                <tr>
                  <th class="w-56 px-4 py-2 font-medium">Короткая ссылка</th>
                  <th class="px-4 py-2 font-medium">Название и длинный адрес</th>
                  <th class="w-44 px-4 py-2 font-medium">Метки</th>
                  <th class="w-40 px-4 py-2 font-medium">Создана</th>
                  <th class="w-44 px-4 py-2 font-medium">Действует до</th>
                  <th class="w-24 px-4 py-2" />
                </tr>
              </thead>
              <tbody class="divide-y divide-default">
                <tr v-for="link in items" :key="link.id" class="align-top hover:bg-elevated/40">
                  <td class="px-4 py-2.5">
                    <div class="flex items-center gap-1">
                      <NuxtLink
                        :to="`/links/${link.id}`"
                        class="truncate font-mono text-sm font-medium text-highlighted hover:text-primary hover:underline"
                        :title="link.short_url"
                      >
                        {{ shortUrlLabel(link.short_url) }}
                      </NuxtLink>
                      <CopyButton :text="link.short_url" class="shrink-0" />
                    </div>
                  </td>
                  <td class="px-4 py-2.5">
                    <div v-if="link.title" class="truncate text-highlighted" :title="link.title">{{ link.title }}</div>
                    <a
                      :href="link.long_url"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="block truncate font-mono text-xs text-muted hover:text-primary hover:underline"
                      :title="link.long_url"
                    >{{ link.long_url }}</a>
                  </td>
                  <td class="px-4 py-2.5">
                    <div v-if="link.tags?.length" class="flex flex-wrap gap-1">
                      <UBadge
                        v-for="tag in link.tags"
                        :key="tag"
                        as="button"
                        type="button"
                        :color="tag === filters.tag ? 'primary' : 'neutral'"
                        variant="subtle"
                        size="sm"
                        class="max-w-full cursor-pointer truncate"
                        :title="`Показать ссылки с меткой «${tag}»`"
                        @click="filters.tag = tag"
                      >
                        {{ tag }}
                      </UBadge>
                    </div>
                    <span v-else class="text-dimmed">—</span>
                  </td>
                  <td class="px-4 py-2.5 whitespace-nowrap text-muted" :title="formatDateTimeFull(link.created_at)">{{ formatDateTime(link.created_at) }}</td>
                  <td class="px-4 py-2.5">
                    <div v-if="link.valid_until" class="whitespace-nowrap" :title="formatDateTimeFull(link.valid_until)">{{ formatDateTime(link.valid_until) }}</div>
                    <div v-else class="text-dimmed">без срока</div>
                    <UBadge v-if="linkState(link, now) === 'expired'" color="error" variant="subtle" size="sm" class="mt-1">истекла</UBadge>
                    <UBadge
                      v-else-if="linkState(link, now) === 'pending'"
                      color="warning"
                      variant="subtle"
                      size="sm"
                      class="mt-1"
                      :title="`Начнёт работать ${formatDateTime(link.valid_since)}`"
                    >
                      ещё не началась
                    </UBadge>
                  </td>
                  <td class="px-4 py-2.5 text-right whitespace-nowrap">
                    <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-pencil" aria-label="Изменить" title="Изменить" :to="`/links/${link.id}`" />
                    <UButton size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" aria-label="Удалить" title="Удалить" @click="confirm = link" />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>

        <div v-if="total > PAGE_SIZE" class="flex justify-center">
          <UPagination v-model:page="page" :total="total" :items-per-page="PAGE_SIZE" />
        </div>
      </div>

      <ConfirmModal
        :open="!!confirm"
        title="Удалить ссылку?"
        :description="confirm ? `${shortUrlLabel(confirm.short_url)} перестанет открываться. Вернуть её будет нельзя.` : undefined"
        :loading="removing"
        @update:open="v => { if (!v) confirm = null }"
        @confirm="remove"
      />
    </template>
  </UDashboardPanel>
</template>
