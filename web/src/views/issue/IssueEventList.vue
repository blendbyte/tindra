<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { ref, computed, watch, onUnmounted } from 'vue'
import { apiFetch } from '@/api/client'
import type { EventSummary, EventListPage } from '@/api/types'
import { useToast } from '@/composables/useToast'
import { useFormatters } from '@/composables/useFormatters'

// The full, sortable list of the issue's events, fetched page by page the
// first time it is opened. Picking a row emits select; the view opens it.
const props = defineProps<{ issueId: string; open: boolean; currentEventId?: string }>()
const emit = defineEmits<{ select: [event: EventSummary] }>()

const { show: showToast } = useToast()
const { formatRel } = useFormatters()

const loadedEvents = ref<EventSummary[]>([])
const eventListHasMore = ref(false)
const eventListLoading = ref(false)
const eventListCursor = ref<{ time: string; id: string } | null>(null)
const evtSortCol = ref<'received_at' | 'level' | 'environment' | 'release'>('received_at')
const evtSortDir = ref<'asc' | 'desc'>('desc')

// Responses that arrive after the section is gone are dropped too.
let unmounted = false
onUnmounted(() => { unmounted = true })

// The view is reused when moving to the next or previous issue, so the list
// of the old issue must not carry over.
watch(() => props.issueId, () => {
  loadedEvents.value = []
  eventListCursor.value = null
  eventListHasMore.value = false
  eventListLoading.value = false
})

watch(() => props.open, (open) => {
  if (open && loadedEvents.value.length === 0) loadEventPage(true)
}, { immediate: true })

const sortedEvents = computed(() => {
  const col = evtSortCol.value
  const dir = evtSortDir.value === 'asc' ? 1 : -1
  return [...loadedEvents.value].sort((a, b) => {
    const av: string = (a[col] ?? '') as string
    const bv: string = (b[col] ?? '') as string
    return av < bv ? -dir : av > bv ? dir : 0
  })
})

function toggleEvtSort(col: typeof evtSortCol.value) {
  if (evtSortCol.value === col) {
    evtSortDir.value = evtSortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    evtSortCol.value = col
    evtSortDir.value = col === 'received_at' ? 'desc' : 'asc'
  }
}

const TAG_PRIORITY = ['browser', 'browser.name', 'os', 'os.name', 'runtime', 'server_name', 'url', 'transaction']

function displayTags(tags: Record<string, string>, max = 3): [string, string][] {
  const result: [string, string][] = []
  for (const key of TAG_PRIORITY) {
    if (tags[key] && result.length < max) result.push([key, tags[key]])
  }
  for (const [k, v] of Object.entries(tags)) {
    if (!TAG_PRIORITY.includes(k) && result.length < max) result.push([k, v])
  }
  return result
}

async function loadEventPage(reset = false) {
  if (reset) {
    loadedEvents.value = []
    eventListCursor.value = null
    eventListHasMore.value = false
  }
  // A page that arrives after the user moved to another issue is dropped,
  // instead of being appended to the new issue's list.
  const forIssue = props.issueId
  const current = () => !unmounted && props.issueId === forIssue
  eventListLoading.value = true
  try {
    const params = new URLSearchParams()
    if (eventListCursor.value) {
      params.set('cursor_time', eventListCursor.value.time)
      params.set('cursor_id', eventListCursor.value.id)
    }
    const page = await apiFetch<EventListPage>(`/api/issues/${forIssue}/events?${params}`)
    if (!current()) return
    loadedEvents.value = [...loadedEvents.value, ...page.events]
    eventListHasMore.value = page.has_more
    eventListCursor.value = page.next_cursor_time && page.next_cursor_id
      ? { time: page.next_cursor_time, id: page.next_cursor_id }
      : null
  } catch {
    if (current()) showToast('Could not load events. Try again.', 'error')
  } finally {
    if (current()) eventListLoading.value = false
  }
}
</script>

<template>
  <div v-if="open" class="eventlist">
    <table class="evttable">
      <thead>
        <tr>
          <th>
            <button
              class="col-sort"
              :class="{ 'col-sort--active': evtSortCol === 'received_at' }"
              @click="toggleEvtSort('received_at')"
            >
              Time
              <em class="col-sort__icon">{{ evtSortCol === 'received_at' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
            </button>
          </th>
          <th>
            <button
              class="col-sort"
              :class="{ 'col-sort--active': evtSortCol === 'level' }"
              @click="toggleEvtSort('level')"
            >
              Level
              <em class="col-sort__icon">{{ evtSortCol === 'level' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
            </button>
          </th>
          <th>
            <button
              class="col-sort"
              :class="{ 'col-sort--active': evtSortCol === 'environment' }"
              @click="toggleEvtSort('environment')"
            >
              Environment
              <em class="col-sort__icon">{{ evtSortCol === 'environment' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
            </button>
          </th>
          <th>
            <button
              class="col-sort"
              :class="{ 'col-sort--active': evtSortCol === 'release' }"
              @click="toggleEvtSort('release')"
            >
              Release
              <em class="col-sort__icon">{{ evtSortCol === 'release' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
            </button>
          </th>
          <th><span class="col-sort" style="cursor: default">Tags</span></th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="ev in sortedEvents"
          :key="ev.id"
          class="evttable__row"
          :class="{ 'evttable__row--active': currentEventId === ev.id }"
          v-clickable
          :aria-current="currentEventId === ev.id ? 'true' : undefined"
          @click="emit('select', ev)"
        >
          <td class="evttable__time mono" :title="new Date(ev.received_at).toUTCString()">
            {{ formatRel(ev.received_at) }}
          </td>
          <td>
            <span v-if="ev.level" class="leveldot" :class="`leveldot--${ev.level}`" style="margin-right:5px" />
            <span style="font-size:var(--text-xs);color:var(--text-2)">{{ ev.level ?? '–' }}</span>
          </td>
          <td>
            <span
              v-if="ev.environment"
              class="envbadge"
              :title="ev.environment"
              :class="ev.environment === 'production' ? 'envbadge--prod' : ''"
            >{{ ev.environment }}</span>
            <span v-else style="color:var(--text-3)">–</span>
          </td>
          <td class="mono evttable__release">{{ ev.release ?? '–' }}</td>
          <td class="evttable__tags">
            <span v-for="[k, v] in displayTags(ev.tags)" :key="k" class="eventlist__tag">
              <span class="eventlist__tag-k">{{ k }}</span>
              <span class="eventlist__tag-v">{{ v }}</span>
            </span>
            <span v-if="Object.keys(ev.tags).length === 0" style="color:var(--text-3)">–</span>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-if="eventListLoading && loadedEvents.length === 0" class="eventlist__empty">Loading…</div>
    <div v-else-if="loadedEvents.length === 0" class="eventlist__empty">No events found.</div>
    <div v-if="eventListHasMore || eventListLoading" class="eventlist__footer">
      <button
        class="btn btn--ghost"
        style="font-size: var(--text-xs)"
        :disabled="eventListLoading"
        @click="loadEventPage()"
      >
        {{ eventListLoading ? 'Loading…' : 'Load more' }}
      </button>
    </div>
  </div>
</template>
