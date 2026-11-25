// Метки, которые уже встречаются у ссылок (GET /tag): для фильтра списка и подсказок в форме.
// Загружаются один раз на страницу; не загрузились — подсказок просто нет.
export function useTags() {
  const tags = ref<string[]>([])

  async function load() {
    try {
      tags.value = (await tagApi.list()).results ?? []
    }
    catch {
      tags.value = []
    }
  }

  return { tags, load }
}
