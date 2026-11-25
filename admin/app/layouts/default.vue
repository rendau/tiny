<script setup lang="ts">
import type { DropdownMenuItem, NavigationMenuItem } from '@nuxt/ui'

const route = useRoute()
const auth = useAuth()
const profileOpen = ref(false)

const userMenu = computed<DropdownMenuItem[][]>(() => [
  [{
    label: auth.user.value?.name || auth.user.value?.username || '',
    description: auth.isAdmin.value ? 'Администратор' : 'Участник',
    type: 'label',
  }],
  [
    { label: 'Профиль и пароль', icon: 'i-lucide-key-round', onSelect: () => { profileOpen.value = true } },
    { label: 'Выйти', icon: 'i-lucide-log-out', onSelect: () => auth.logout() },
  ],
])

function inSection(to: string): boolean {
  return route.path === to || route.path.startsWith(`${to}/`)
}

const navItems = computed<NavigationMenuItem[][]>(() => [
  [
    { label: 'Ссылки', icon: 'i-lucide-link', to: '/links', active: inSection('/links') },
  ],
  auth.isAdmin.value
    ? [{ label: 'Люди', icon: 'i-lucide-users', to: '/users', active: inSection('/users') }]
    : [],
])
</script>

<template>
  <UDashboardGroup unit="rem">
    <UDashboardSidebar
      collapsible
      :min-size="11"
      :default-size="14"
      :max-size="18"
      class="group"
      :ui="{
        header: 'group-data-[collapsed=true]:px-2',
        body: 'group-data-[collapsed=true]:px-2',
      }"
    >
      <template #header="{ collapsed }">
        <NuxtLink v-if="!collapsed" to="/links" class="flex items-center gap-2 font-bold text-highlighted">
          <AppLogo class="size-6" />
          tiny
        </NuxtLink>
        <UDashboardSidebarCollapse :class="collapsed ? 'mx-auto' : 'ms-auto'" />
      </template>

      <template #default="{ collapsed }">
        <UNavigationMenu
          :collapsed="collapsed"
          :items="navItems"
          orientation="vertical"
          :ui="{ linkLabel: 'whitespace-normal' }"
        />
      </template>

      <template #footer="{ collapsed }">
        <UDropdownMenu :items="userMenu" :content="{ side: 'top', align: 'start' }" class="w-full">
          <UButton
            color="neutral"
            variant="ghost"
            class="w-full"
            :class="collapsed ? 'justify-center px-0' : 'justify-start'"
            :label="collapsed ? undefined : (auth.user.value?.name || auth.user.value?.username || '')"
            icon="i-lucide-circle-user"
            :trailing-icon="collapsed ? undefined : 'i-lucide-chevrons-up-down'"
          />
        </UDropdownMenu>
      </template>
    </UDashboardSidebar>

    <slot />

    <ProfileModal v-model:open="profileOpen" />
  </UDashboardGroup>
</template>
