// Which issue detail sections are collapsed. The user's choices are kept in
// localStorage so a section folded away stays folded on every issue.

const STORAGE_KEY = 'tindra:issue:collapsed'

export const SECTION_DEFAULTS: Record<string, boolean> = {
  breadcrumbs: false,
  context: false,
  packages: true,
  events: false,
  tags: false,
  activity: false,
  request: false,
  trace: false,
}

export function loadCollapsed(): Record<string, boolean> {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) return { ...SECTION_DEFAULTS, ...JSON.parse(stored) }
  } catch { /* ignore */ }
  return { ...SECTION_DEFAULTS }
}

// saveCollapsed stores one section's state alongside the others. It rereads
// the stored map first, so sections toggled elsewhere on the page are kept.
export function saveCollapsed(key: string, collapsed: boolean) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...loadCollapsed(), [key]: collapsed })) } catch { /* ignore */ }
}
