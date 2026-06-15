<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { useUiStore } from '@/stores/ui'
import { useProjectsStore } from '@/stores/projects'
import { useIssueNavStore } from '@/stores/issueNav'
import { apiFetch } from '@/api/client'
import { useFormatters } from '@/composables/useFormatters'
import type { Issue, IssueListPage } from '@/api/types'
import Icon from './Icon.vue'

const router = useRouter()
const ui = useUiStore()
const projects = useProjectsStore()
const navStore = useIssueNavStore()
const { formatRel } = useFormatters()

const q = ref('')
const active = ref(0)
const inputRef = ref<HTMLInputElement | null>(null)

interface CmdItem {
  id: string
  group: string
  label: string
  hint: string
  level?: string | null
  action: () => void
}

// ── Issue search ──────────────────────────────────────────────────────────────
const issueResults = ref<Issue[]>([])
const isSearching = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null

watch(q, (val) => {
  if (searchTimer) clearTimeout(searchTimer)
  const trimmed = val.trim()
  if (trimmed.length < 2) {
    issueResults.value = []
    isSearching.value = false
    return
  }
  isSearching.value = true
  searchTimer = setTimeout(async () => {
    try {
      const params = new URLSearchParams({ q: trimmed, limit: '8' })
      for (const id of projects.selectedIds) params.append('project_id', id)
      const data = await apiFetch<IssueListPage>(`/api/issues?${params}`)
      issueResults.value = Array.isArray(data) ? data as Issue[] : (data.issues ?? [])
    } catch {
      issueResults.value = []
    } finally {
      isSearching.value = false
    }
  }, 200)
})


// ── Items ─────────────────────────────────────────────────────────────────────
const navItems = computed<CmdItem[]>(() => {
  const nav: CmdItem[] = [
    { id: 'nav:issues',      group: 'Go to', label: 'Issues',      hint: '⌘1', action: () => router.push('/issues') },
    { id: 'nav:performance', group: 'Go to', label: 'Performance', hint: '⌘2', action: () => router.push('/performance') },
    { id: 'nav:releases',    group: 'Go to', label: 'Releases',    hint: '⌘3', action: () => router.push('/releases') },
    { id: 'nav:settings',    group: 'Go to', label: 'Settings',    hint: '⌘,', action: () => router.push('/settings') },
    { id: 'filter:all', group: 'Projects', label: 'Show all projects', hint: '', action: () => projects.setSelected([]) },
    ...projects.projects.map((p) => ({
      id: 'filter:' + p.id,
      group: 'Projects',
      label: p.name,
      hint: p.slug,
      action: () => projects.setSelected([p.id]),
    })),
  ]
  const qq = q.value.trim().toLowerCase()
  if (!qq) return nav
  return nav.filter(it => it.label.toLowerCase().includes(qq) || it.group.toLowerCase().includes(qq))
})

const issueItems = computed<CmdItem[]>(() =>
  issueResults.value.map((iss) => ({
    id: 'issue:' + iss.id,
    group: 'Issues',
    label: iss.title,
    hint: formatRel(iss.last_seen),
    level: iss.level,
    action: () => {
      navStore.set(issueResults.value.map((i) => i.id))
      router.push(`/issues/${iss.id}`)
    },
  }))
)

const items = computed<CmdItem[]>(() => [...navItems.value, ...issueItems.value])

const grouped = computed(() => {
  const map = new Map<string, (CmdItem & { idx: number })[]>()
  items.value.forEach((item, idx) => {
    if (!map.has(item.group)) map.set(item.group, [])
    map.get(item.group)!.push({ ...item, idx })
  })
  return [...map.entries()]
})

const isEmpty = computed(() =>
  items.value.length === 0 && !isSearching.value
)

// ── Palette lifecycle ─────────────────────────────────────────────────────────
watch(
  () => ui.cmdOpen,
  async (open) => {
    if (open) {
      q.value = ''
      active.value = 0
      issueResults.value = []
      await nextTick()
      inputRef.value?.focus()
    }
  },
)

watch(items, () => {
  active.value = Math.min(active.value, Math.max(0, items.value.length - 1))
})

function select(item: CmdItem) {
  item.action()
  ui.closeCmd()
}

function onKey(e: KeyboardEvent) {
  if (!ui.cmdOpen) return
  if (e.key === 'Escape') { e.preventDefault(); ui.closeCmd() }
  else if (e.key === 'ArrowDown') { e.preventDefault(); active.value = Math.min(active.value + 1, items.value.length - 1) }
  else if (e.key === 'ArrowUp') { e.preventDefault(); active.value = Math.max(active.value - 1, 0) }
  else if (e.key === 'Enter') { e.preventDefault(); const it = items.value[active.value]; if (it) select(it) }
}

function onGlobalKey(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    ui.cmdOpen ? ui.closeCmd() : ui.openCmd()
  }
  if ((e.metaKey || e.ctrlKey) && e.key === '1') { e.preventDefault(); router.push('/issues') }
  if ((e.metaKey || e.ctrlKey) && e.key === '2') { e.preventDefault(); router.push('/performance') }
  if ((e.metaKey || e.ctrlKey) && e.key === '3') { e.preventDefault(); router.push('/releases') }
  if ((e.metaKey || e.ctrlKey) && e.key === ',') { e.preventDefault(); router.push('/settings') }
  if (e.key === '?' && !e.metaKey && !e.ctrlKey && (e.target as HTMLElement).tagName !== 'INPUT' && (e.target as HTMLElement).tagName !== 'TEXTAREA') {
    e.preventDefault()
    ui.shortcutsOpen = !ui.shortcutsOpen
  }
}

onMounted(() => {
  document.addEventListener('keydown', onKey)
  document.addEventListener('keydown', onGlobalKey)
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKey)
  document.removeEventListener('keydown', onGlobalKey)
  if (searchTimer) clearTimeout(searchTimer)
})
</script>

<template>
  <Teleport to="body">
    <div
      v-if="ui.cmdOpen"
      class="cmdk-overlay"
      @mousedown.self="ui.closeCmd()"
    >
      <div class="cmdk" role="dialog" aria-label="Command palette">
        <div class="cmdk__search">
          <Icon v-if="!isSearching" name="search" :size="14" />
          <Icon v-else name="loader" :size="14" class="cmdk__spinner" />
          <input
            ref="inputRef"
            v-model="q"
            placeholder="Search issues or jump to a page…"
            aria-label="Search"
            @input="active = 0"
          />
          <span class="nav__kbd" style="margin-left: auto">esc</span>
        </div>

        <div class="cmdk__list">
          <div
            v-if="isEmpty"
            class="cmdk__empty"
          >
            No results for "{{ q }}"
          </div>
          <template v-for="[group, groupItems] in grouped" :key="group">
            <div class="cmdk__group-label">{{ group }}</div>
            <div
              v-for="it in groupItems"
              :key="it.id"
              class="cmdk__item"
              :class="{ 'cmdk__item--active': it.idx === active }"
              @mouseenter="active = it.idx"
              @click="select(it)"
            >
              <span v-if="it.level" class="leveldot" :class="`leveldot--${it.level}`" style="flex-shrink:0" />
              <span class="cmdk__item-text">{{ it.label }}</span>
              <span v-if="it.hint" class="cmdk__item-meta">{{ it.hint }}</span>
            </div>
          </template>
        </div>

        <div class="cmdk__footer">
          <span><kbd class="nav__kbd">↑↓</kbd> navigate</span>
          <span><kbd class="nav__kbd">↵</kbd> select</span>
          <span><kbd class="nav__kbd">esc</kbd> close</span>
        </div>
      </div>
    </div>
  </Teleport>
</template>
