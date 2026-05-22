import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'

export function useTimezone() {
  const auth = useAuthStore()
  return computed(() => auth.user?.timezone ?? 'UTC')
}
