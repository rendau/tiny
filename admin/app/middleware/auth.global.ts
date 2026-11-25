// Вход обязателен везде, кроме /login и /setup; пока людей нет — только /setup (первый администратор)
export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuth()

  if (auth.bootstrapRequired.value === null) {
    try {
      await auth.checkBootstrap()
    }
    catch {
      // API недоступен — страница покажет свою ошибку; проверим ещё раз при следующем переходе
    }
  }

  if (auth.bootstrapRequired.value)
    return to.path === '/setup' ? undefined : navigateTo('/setup')
  if (to.path === '/setup')
    return navigateTo('/login')

  await auth.ensureUser()
  if (!auth.user.value)
    return to.path === '/login' ? undefined : navigateTo({ path: '/login', query: { redirect: to.fullPath } })
  if (to.path === '/login')
    return navigateTo('/links')

  if (to.path.startsWith('/users') && !auth.isAdmin.value)
    return navigateTo('/links')
})
