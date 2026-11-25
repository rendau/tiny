<script setup lang="ts">
// Ошибка загрузки страницы с кнопкой повтора.
defineProps<{
  title: string
  error: string
}>()

const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <UAlert
    v-if="error"
    color="error"
    variant="subtle"
    :title="title"
    :description="error"
    :actions="[{ label: 'Повторить', color: 'error', variant: 'soft', onClick: () => emit('retry') }]"
  />
</template>
