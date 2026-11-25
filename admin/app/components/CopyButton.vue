<script setup lang="ts">
// Копировать текст в буфер обмена. С подписью — обычная кнопка, без подписи — значок.
const props = defineProps<{
  text: string
  label?: string
}>()
const toast = useToast()

async function copy() {
  try {
    await navigator.clipboard.writeText(props.text)
    toast.add({ title: 'Скопировано', description: props.text, color: 'success' })
  }
  catch {
    toast.add({ title: 'Не удалось скопировать', color: 'error' })
  }
}
</script>

<template>
  <UButton
    icon="i-lucide-copy"
    :size="label ? 'md' : 'xs'"
    color="neutral"
    :variant="label ? 'outline' : 'ghost'"
    :label="label"
    :aria-label="label ? undefined : 'Скопировать'"
    :title="label ? undefined : 'Скопировать'"
    @click="copy"
  />
</template>
