import { beforeEach, afterEach } from 'vitest'
import { enableAutoUnmount } from '@vue/test-utils'
import { createPinia, disposePinia, setActivePinia } from 'pinia'

enableAutoUnmount(afterEach)

let pinia: ReturnType<typeof createPinia>
beforeEach(() => {
  // Views may mock their metadata store while still using investigation state.
  // Give each test an isolated session and active Pinia, just as main.ts does.
  try { sessionStorage.clear() } catch {}
  pinia = createPinia()
  setActivePinia(pinia)
})
afterEach(() => disposePinia(pinia))
