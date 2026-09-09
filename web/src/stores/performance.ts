import { computed } from 'vue'
import { defineStore } from 'pinia'
import { useInvestigationStore } from './investigation'

// Compatibility facade for performance views; the investigation owns state.
export const usePerformanceStore = defineStore('performance', () => {
  const investigation = useInvestigationStore()
  const windowHrs = computed({ get: () => investigation.range, set: (value: string) => investigation.setRange(value) })
  const envFilter = computed({ get: () => investigation.environment, set: (value: string) => { investigation.environment = value } })
  return { windowHrs, envFilter }
})
