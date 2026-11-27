import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'

// Whether the signed-in user only sees the projects of their teams. Such a user
// with no project cannot create one: the empty states tell them to ask for a team
// instead of offering to create a project. It reads the auth store, which useMe
// keeps current, so views get this without running another query.
export function useProjectAccess() {
  const auth = useAuthStore()
  const restricted = computed(() => auth.user?.project_access === 'teams')
  return { restricted }
}

export const NO_ACCESS_TITLE = "You don't have access to any projects yet"
export const NO_ACCESS_BODY = 'Ask an admin to add you to a team.'
