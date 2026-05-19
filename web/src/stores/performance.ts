import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const VALID_WINDOWS = ['1h', '24h', '7d', '30d']
const VALID_ENVS = ['All', 'production', 'staging', 'development']

function lsGet(key: string): string | null {
  try { return localStorage.getItem(`tindra:perf:${key}`) } catch { return null }
}
function lsSet(key: string, val: string | null) {
  try {
    if (val === null) localStorage.removeItem(`tindra:perf:${key}`)
    else localStorage.setItem(`tindra:perf:${key}`, val)
  } catch {}
}

export const usePerformanceStore = defineStore('performance', () => {
  const saved = lsGet('window')
  const windowHrs = ref(saved && VALID_WINDOWS.includes(saved) ? saved : '24h')

  const savedEnv = lsGet('env')
  const envFilter = ref(savedEnv && VALID_ENVS.includes(savedEnv) ? savedEnv : 'All')

  watch(windowHrs, (v) => lsSet('window', v !== '24h' ? v : null))
  watch(envFilter, (v) => lsSet('env', v !== 'All' ? v : null))

  return { windowHrs, envFilter }
})
