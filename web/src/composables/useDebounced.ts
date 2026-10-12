import { onScopeDispose, readonly, ref, watch, type Ref } from 'vue'

// useDebounced follows source after it has stopped changing for delay ms, so
// typing in a search box sends one request instead of one per keystroke.
export function useDebounced<T>(source: Ref<T>, delay = 300): Readonly<Ref<T>> {
  const value = ref(source.value) as Ref<T>
  let timer: ReturnType<typeof setTimeout> | undefined
  watch(source, (next) => {
    clearTimeout(timer)
    timer = setTimeout(() => { value.value = next }, delay)
  })
  onScopeDispose(() => clearTimeout(timer))
  return readonly(value) as Readonly<Ref<T>>
}
