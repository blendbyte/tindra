import { watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { User } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

// useMe is the single source for the signed-in user. It starts from the user
// the auth store loaded at startup and mirrors every refetch back into the
// store, so a permission or 2FA change made in settings reaches the router
// guard and every view that reads auth.user, not only this query's readers.
export function useMe() {
  const auth = useAuthStore()
  const query = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => apiFetch<User>('/api/me', { signal }),
    initialData: () => auth.user ?? undefined,
  })
  watch(query.data, (user) => {
    if (user && user !== auth.user) auth.setUser(user)
  })
  return query
}
