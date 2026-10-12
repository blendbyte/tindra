import { useToast } from '@/composables/useToast'

// copyText copies text and reports whether it worked. navigator.clipboard only
// exists in secure contexts (HTTPS or localhost), and self-hosted instances are
// often reached over plain HTTP on a LAN, so fall back to a hidden textarea.
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Permission denied or not focused; try the fallback below.
    }
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    area.remove()
  }
}

export function useClipboard() {
  const { show } = useToast()

  // copy copies text and toasts the outcome. Pass successMessage for a toast on
  // success; callers that flash an inline "Copied" state can omit it. A failure
  // is always reported, so the user knows to copy by hand.
  async function copy(text: string, successMessage?: string): Promise<boolean> {
    const ok = await copyText(text)
    if (!ok) show('Could not copy to the clipboard. Select the text and copy it manually.', 'error')
    else if (successMessage) show(successMessage)
    return ok
  }

  return { copy }
}
