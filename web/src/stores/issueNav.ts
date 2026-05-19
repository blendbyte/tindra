import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useIssueNavStore = defineStore('issueNav', () => {
  const ids = ref<string[]>([])

  function set(orderedIds: string[]) {
    ids.value = orderedIds
  }

  function prevId(currentId: string): string | null {
    const idx = ids.value.indexOf(currentId)
    return idx > 0 ? ids.value[idx - 1] : null
  }

  function nextId(currentId: string): string | null {
    const idx = ids.value.indexOf(currentId)
    return idx >= 0 && idx < ids.value.length - 1 ? ids.value[idx + 1] : null
  }

  return { ids, set, prevId, nextId }
})
