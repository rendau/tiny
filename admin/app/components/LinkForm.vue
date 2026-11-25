<script setup lang="ts">
import type { LinkFormErrors, LinkFormState } from '~/utils/link'

// Поля ссылки — общие для создания и правки. При правке кода и домена нет: они не меняются.
const props = defineProps<{
  mode: 'create' | 'edit'
  // ошибки по полям: показываются под полем
  errors: LinkFormErrors
  // подсказки меток — те, что уже есть у других ссылок
  tagSuggestions: string[]
}>()

const state = defineModel<LinkFormState>({ required: true })

const codeModes = [
  { label: 'Сгенерировать', value: 'auto' },
  { label: 'Свой код', value: 'custom' },
]

// в списке выбора — подсказки и уже выбранные метки (в том числе только что придуманные)
const tagItems = computed(() => [...new Set([...props.tagSuggestions, ...state.value.tags])].sort((a, b) => a.localeCompare(b, 'ru')))

// что набрано в поиске списка меток; после добавления новой метки очищается
const tagSearch = ref('')

function addTag(tag: string) {
  const t = tag.trim()
  if (t && !state.value.tags.includes(t))
    state.value.tags = [...state.value.tags, t]
  tagSearch.value = ''
}

// «дополнительно» раскрыто сразу, если там уже что-то задано
const advancedOpen = ref(
  !!state.value.domain || state.value.max_visits !== null || state.value.crawlable || !state.value.forward_query,
)

const untilInPast = computed(() => {
  const iso = localInputToIso(state.value.valid_until)
  return !!iso && new Date(iso).getTime() <= Date.now()
})
</script>

<template>
  <div class="space-y-5">
    <UFormField label="Длинный адрес" required :error="errors.long_url" description="Куда ведёт короткая ссылка">
      <UTextarea
        v-model="state.long_url"
        :rows="2"
        autoresize
        :autofocus="mode === 'create'"
        placeholder="https://www.demo.test/…"
        class="w-full"
        :ui="{ base: 'font-mono text-sm break-all' }"
      />
    </UFormField>

    <UFormField v-if="mode === 'create'" label="Код короткой ссылки" :error="errors.short_code">
      <div class="space-y-2">
        <UTabs v-model="state.code_mode" :items="codeModes" :content="false" size="sm" class="w-full sm:w-80" />
        <div v-if="state.code_mode === 'auto'" class="flex flex-wrap items-center gap-2">
          <span class="text-sm text-muted">Длина кода</span>
          <UInputNumber
            v-model="state.short_code_length"
            :min="MIN_SHORT_CODE_LENGTH"
            placeholder="обычная"
            class="w-32"
          />
          <span class="text-xs text-dimmed">не меньше {{ MIN_SHORT_CODE_LENGTH }}; пусто — как настроено на сервере</span>
        </div>
        <div v-else>
          <UInput v-model="state.short_code" placeholder="например, akciya2026" class="w-full font-mono sm:w-80" />
          <p class="mt-1 text-xs text-dimmed">Большие и маленькие буквы различаются: aBc и abc — разные ссылки. Потом код не изменить.</p>
        </div>
      </div>
    </UFormField>

    <UFormField label="Название" description="Для себя — чтобы потом легче найти ссылку">
      <UInput v-model="state.title" class="w-full" />
    </UFormField>

    <UFormField label="Метки" description="Выберите из уже существующих или впишите новую">
      <USelectMenu
        v-model="state.tags"
        v-model:search-term="tagSearch"
        :items="tagItems"
        multiple
        create-item
        icon="i-lucide-tags"
        placeholder="Без меток"
        class="w-full"
        @create="addTag"
      />
    </UFormField>

    <div class="grid gap-5 sm:grid-cols-2">
      <UFormField label="Действует с" :error="errors.valid_since" description="Пусто — работает сразу">
        <UFieldGroup class="w-full">
          <UInput v-model="state.valid_since" type="datetime-local" class="w-full" />
          <UButton
            v-if="state.valid_since"
            color="neutral"
            variant="outline"
            icon="i-lucide-x"
            aria-label="Убрать начало срока"
            title="Убрать"
            @click="state.valid_since = ''"
          />
        </UFieldGroup>
      </UFormField>
      <UFormField label="Действует до" :error="errors.valid_until" description="Пусто — без срока">
        <UFieldGroup class="w-full">
          <UInput v-model="state.valid_until" type="datetime-local" class="w-full" />
          <UButton
            v-if="state.valid_until"
            color="neutral"
            variant="outline"
            icon="i-lucide-x"
            aria-label="Убрать конец срока"
            title="Убрать"
            @click="state.valid_until = ''"
          />
        </UFieldGroup>
      </UFormField>
    </div>
    <p class="-mt-2 text-xs text-dimmed">Время — по часам этого компьютера. До начала и после окончания срока ссылка не открывается.</p>
    <UAlert
      v-if="untilInPast && !errors.valid_until"
      color="warning"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      description="Срок «до» уже прошёл — ссылка открываться не будет."
    />

    <UCollapsible v-model:open="advancedOpen" class="rounded-lg border border-default">
      <UButton
        color="neutral"
        variant="ghost"
        block
        class="justify-between"
        label="Дополнительно"
        :trailing-icon="advancedOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
      />
      <template #content>
        <div class="space-y-5 border-t border-default p-4">
          <UFormField
            v-if="mode === 'create'"
            label="Домен"
            :error="errors.domain"
            description="Пусто — основной домен: ссылка открывается на любом нашем домене. С доменом — только на нём. Потом не изменить."
          >
            <UInput v-model="state.domain" placeholder="например, sl.demo.test" class="w-full font-mono sm:w-80" />
          </UFormField>
          <UFormField
            label="Предел посещений"
            :error="errors.max_visits"
            description="Значение сохраняется, но сейчас не действует: переходы по ссылкам не считаются."
          >
            <UInputNumber v-model="state.max_visits" :min="1" placeholder="без предела" class="w-40" />
          </UFormField>
          <UFormField label="Разрешить поисковикам" description="Поисковым роботам можно обходить эту ссылку">
            <USwitch v-model="state.crawlable" />
          </UFormField>
          <UFormField
            label="Дописывать параметры запроса"
            description="Всё, что стоит после «?» в короткой ссылке, переносится в длинный адрес"
          >
            <USwitch v-model="state.forward_query" />
          </UFormField>
        </div>
      </template>
    </UCollapsible>
  </div>
</template>
