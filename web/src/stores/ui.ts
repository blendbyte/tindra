import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

type ExplicitTheme = 'dark' | 'light'

function getSystemTheme(): ExplicitTheme {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export const useUiStore = defineStore('ui', () => {
  const stored = (() => {
    try {
      return localStorage.getItem('tindra:theme') as ExplicitTheme | null
    } catch {
      return null
    }
  })()

  // null means "follow the OS"
  const theme = ref<ExplicitTheme | null>(stored)
  const systemTheme = ref<ExplicitTheme>(getSystemTheme())
  const cmdOpen = ref(false)
  const shortcutsOpen = ref(false)

  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
    systemTheme.value = e.matches ? 'dark' : 'light'
  })

  const resolvedTheme = computed<ExplicitTheme>(() => theme.value ?? systemTheme.value)

  function toggleTheme() {
    theme.value = resolvedTheme.value === 'dark' ? 'light' : 'dark'
    try {
      localStorage.setItem('tindra:theme', theme.value)
    } catch {}
  }

  function openCmd() {
    cmdOpen.value = true
  }

  function closeCmd() {
    cmdOpen.value = false
  }

  return { theme, resolvedTheme, cmdOpen, shortcutsOpen, toggleTheme, openCmd, closeCmd }
})
