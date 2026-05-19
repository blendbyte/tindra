<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import { formatDuration, formatRel } from '@/utils/formatters'
import type { Release, ReleaseListPage } from '@/api/types'
import Icon from '@/components/Icon.vue'

const router = useRouter()
const projects = useProjectsStore()

// ── Pagination ────────────────────────────────────────────────────────────────

type Cursor = { cursor_time: string; cursor_id: string } | null

const nextCursor = ref<Cursor>(null)
const isFetchingMore = ref(false)
const extraReleases = ref<Release[]>([])

function buildParams(cursor: Cursor) {
  const p = new URLSearchParams()
  for (const id of projects.selectedIds) p.append('project_id', id)
  if (cursor) {
    p.set('cursor_time', cursor.cursor_time)
    p.set('cursor_id', cursor.cursor_id)
  }
  return p.toString()
}

const queryKey = computed(() => ['releases', [...projects.selectedIds].sort().join(',')])

const { data: firstPage, isFetching, isError, refetch } = useQuery({
  queryKey,
  queryFn: () => apiFetch<ReleaseListPage>(`/api/releases?${buildParams(null)}`),
  refetchInterval: 60_000,
})

watch(firstPage, (data) => {
  extraReleases.value = []
  nextCursor.value = data?.has_more && data.next_cursor_time && data.next_cursor_id
    ? { cursor_time: data.next_cursor_time, cursor_id: data.next_cursor_id }
    : null
})

watch(() => projects.selectedIds, () => {
  extraReleases.value = []
  nextCursor.value = null
  refetch()
})

const allReleases = computed<Release[]>(() => [
  ...(firstPage.value?.releases ?? []),
  ...extraReleases.value,
])

const total = computed(() => firstPage.value?.total ?? 0)
const hasMore = computed(() => nextCursor.value !== null)

async function loadMore() {
  if (!nextCursor.value || isFetchingMore.value) return
  isFetchingMore.value = true
  try {
    const data = await apiFetch<ReleaseListPage>(`/api/releases?${buildParams(nextCursor.value)}`)
    extraReleases.value = [...extraReleases.value, ...(data.releases ?? [])]
    nextCursor.value = data.has_more && data.next_cursor_time && data.next_cursor_id
      ? { cursor_time: data.next_cursor_time, cursor_id: data.next_cursor_id }
      : null
  } finally {
    isFetchingMore.value = false
  }
}

// ── Sorting ───────────────────────────────────────────────────────────────────

type SortCol = 'deployed_at' | 'version' | 'tx_count' | 'tx_p50' | 'tx_error_rate' | 'new_issues'
const sortCol = ref<SortCol>('deployed_at')
const sortDir = ref<'asc' | 'desc'>('desc')

function toggleSort(col: SortCol) {
  if (sortCol.value === col) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortCol.value = col
    sortDir.value = col === 'version' ? 'asc' : 'desc'
  }
}

function sortIcon(col: SortCol) {
  if (sortCol.value !== col) return ''
  return sortDir.value === 'desc' ? '↓' : '↑'
}

const sorted = computed(() => {
  const list = [...allReleases.value]
  list.sort((a, b) => {
    let cmp = 0
    switch (sortCol.value) {
      case 'version':
        cmp = a.version.localeCompare(b.version)
        break
      case 'deployed_at':
        cmp = new Date(a.deployed_at).getTime() - new Date(b.deployed_at).getTime()
        break
      case 'tx_count':     cmp = a.tx_count - b.tx_count; break
      case 'tx_p50':       cmp = a.tx_p50 - b.tx_p50; break
      case 'tx_error_rate':cmp = a.tx_error_rate - b.tx_error_rate; break
      case 'new_issues':   cmp = a.new_issues - b.new_issues; break
    }
    return sortDir.value === 'asc' ? cmp : -cmp
  })
  return list
})

// ── Helpers ───────────────────────────────────────────────────────────────────

const projectName = computed(() => {
  const map = new Map((projects.projects ?? []).map((p) => [p.id, p.name]))
  return (id: string) => map.get(id) ?? ''
})

const showProject = computed(() =>
  new Set(allReleases.value.map((r) => r.project_id)).size > 1
)


function formatCount(n: number) {
  if (n === 0) return '–'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}
</script>

<template>
  <div class="page">
    <div v-if="isError" class="txerror" style="margin: 24px">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      <span>Failed to load releases.</span>
      <button class="btn" @click="refetch()">Try again</button>
    </div>

    <div v-else-if="!isFetching && allReleases.length === 0" class="rel-empty">
      <Icon name="package" :size="28" style="color: var(--text-3)" />
      <div>No releases yet.</div>
      <div class="rel-empty__sub">Releases are created automatically when SDKs report a <span class="mono">release</span> field.</div>
    </div>

    <template v-else>
      <!-- Header row with sortable columns -->
      <div class="relrow relrow--header">
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'version' }" @click="toggleSort('version')">
          Version <em class="col-sort__icon">{{ sortIcon('version') }}</em>
        </button>
        <button class="col-sort relrow__num" :class="{ 'col-sort--active': sortCol === 'tx_count' }" @click="toggleSort('tx_count')">
          Txns <em class="col-sort__icon">{{ sortIcon('tx_count') }}</em>
        </button>
        <button class="col-sort relrow__num" :class="{ 'col-sort--active': sortCol === 'tx_p50' }" @click="toggleSort('tx_p50')">
          P50 <em class="col-sort__icon">{{ sortIcon('tx_p50') }}</em>
        </button>
        <button class="col-sort relrow__num" :class="{ 'col-sort--active': sortCol === 'tx_error_rate' }" @click="toggleSort('tx_error_rate')">
          Errors <em class="col-sort__icon">{{ sortIcon('tx_error_rate') }}</em>
        </button>
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'new_issues' }" @click="toggleSort('new_issues')">
          Issues <em class="col-sort__icon">{{ sortIcon('new_issues') }}</em>
        </button>
        <button class="col-sort relrow__num" :class="{ 'col-sort--active': sortCol === 'deployed_at' }" @click="toggleSort('deployed_at')">
          Deployed <em class="col-sort__icon">{{ sortIcon('deployed_at') }}</em>
        </button>
      </div>

      <!-- Skeleton rows while loading -->
      <template v-if="isFetching && allReleases.length === 0">
        <div v-for="i in 8" :key="i" class="relrow">
          <div class="rel-version">
            <div class="rel-version__icon skel" style="width:28px;height:28px;border-radius:6px" />
            <div class="rel-version__text">
              <span class="skel" style="width:120px;height:10px;display:block" />
            </div>
          </div>
          <span class="skel relrow__num" style="width:36px;height:10px;display:block;margin-left:auto" />
          <span class="skel relrow__num" style="width:44px;height:10px;display:block;margin-left:auto" />
          <span class="skel relrow__num" style="width:28px;height:10px;display:block;margin-left:auto" />
          <span class="skel" style="width:56px;height:20px;display:block;border-radius:10px" />
          <span class="skel relrow__num" style="width:52px;height:10px;display:block;margin-left:auto" />
        </div>
      </template>

      <!-- Data rows -->
      <div
        v-for="r in sorted"
        :key="r.id"
        class="relrow"
        @click="router.push(`/releases/${r.id}`)"
      >
        <div class="rel-version">
          <div class="rel-version__icon">
            <Icon name="package" :size="12" />
          </div>
          <div class="rel-version__text">
            <span class="rel-version__label mono">{{ r.version }}</span>
            <span v-if="projectName(r.project_id)" class="rel-version__project">{{ projectName(r.project_id) }}</span>
          </div>
        </div>

        <span class="relrow__num mono">{{ formatCount(r.tx_count) }}</span>

        <span class="relrow__num mono" :class="{ muted: r.tx_count === 0 }">
          {{ r.tx_p50 === 0 ? '–' : formatDuration(r.tx_p50) }}
        </span>

        <span class="relrow__num mono" :class="r.tx_error_rate > 0 ? 'tx-failure' : 'muted'">
          {{ r.tx_count > 0 ? (r.tx_error_rate > 0 ? r.tx_error_rate.toFixed(1) + '%' : '–') : '–' }}
        </span>

        <div class="rel-issues-cell">
          <span v-if="r.new_issues > 0" class="rel-issues-pill rel-issues-pill--active">
            {{ r.new_issues }} new
          </span>
          <span v-else class="rel-issues-pill rel-issues-pill--clean">
            Clean
          </span>
        </div>

        <div class="rel-deployed relrow__num">
          <Icon name="clock" :size="11" style="color: var(--text-3); flex: 0 0 auto" />
          <span>{{ formatRel(r.deployed_at) }}</span>
        </div>
      </div>

      <!-- Load more -->
      <div v-if="hasMore || isFetchingMore" class="list-footer">
        <button
          class="btn btn--ghost"
          :disabled="isFetchingMore"
          @click="loadMore"
        >
          {{ isFetchingMore ? 'Loading…' : `Load ${Math.min(50, total - allReleases.length).toLocaleString()} more` }}
        </button>
        <span class="list-footer__count">{{ allReleases.length.toLocaleString() }} of {{ total.toLocaleString() }}</span>
      </div>
    </template>
  </div>
</template>
