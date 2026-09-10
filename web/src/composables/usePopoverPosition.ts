import { ref, watch, onMounted, onUnmounted, nextTick, type Ref } from 'vue'

// Fixed positioning keeps menus out of the horizontally scrolling filter bars.
export function usePopoverPosition(open: Ref<boolean>, anchor: Ref<HTMLElement | null>, preferredWidth: number) {
  const style = ref<Record<string, string>>({})
  function position() {
    if (!open.value || !anchor.value) return
    const rect = anchor.value.getBoundingClientRect()
    const width = Math.min(preferredWidth, window.innerWidth - 16)
    const below = Math.max(0, window.innerHeight - rect.bottom - 14)
    const above = Math.max(0, rect.top - 14)
    const upwards = below < 160 && above > below
    style.value = {
      position: 'fixed',
      left: `${Math.max(8, Math.min(rect.left, window.innerWidth - width - 8))}px`,
      right: 'auto',
      top: upwards ? 'auto' : `${rect.bottom + 6}px`,
      bottom: upwards ? `${window.innerHeight - rect.top + 6}px` : 'auto',
      width: `${width}px`,
      minWidth: '0',
      maxHeight: `${upwards ? above : below}px`,
      overflowY: 'auto',
    }
  }
  watch(open, async () => { await nextTick(); position() })
  function onScroll(event: Event) {
    // Keep scrolling within the menu; dismiss when the page or toolbar moves.
    if (event.target instanceof Node && anchor.value?.contains(event.target)) return
    open.value = false
  }
  onMounted(() => {
    window.addEventListener('resize', position)
    document.addEventListener('scroll', onScroll, true)
  })
  onUnmounted(() => {
    window.removeEventListener('resize', position)
    document.removeEventListener('scroll', onScroll, true)
  })
  return style
}
