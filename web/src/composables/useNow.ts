import { onScopeDispose, readonly, ref } from 'vue'

// One shared clock for elapsed times ("down for 12m"), so they keep counting
// between data refreshes. It ticks only while a component uses it.
const now = ref(Date.now())
let users = 0
let timer: ReturnType<typeof setInterval> | undefined

export function useNow(intervalMs = 15_000) {
  if (users++ === 0) {
    now.value = Date.now()
    timer = setInterval(() => { now.value = Date.now() }, intervalMs)
  }
  onScopeDispose(() => {
    if (--users === 0) clearInterval(timer)
  })
  return readonly(now)
}
