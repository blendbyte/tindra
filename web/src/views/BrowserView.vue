<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { RouterLink, useRoute } from 'vue-router'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'
import { useAppUserStore, routeUserIdentity } from '@/stores/appUser'
import { apiFetch } from '@/api/client'
import type { WebVitalsSummary, WebVitalsPage, Transaction, TransactionListPage } from '@/api/types'
import FilterChip from '@/components/FilterChip.vue'
import UserFilter from '@/components/UserFilter.vue'
import PerformanceSubnav from '@/components/PerformanceSubnav.vue'
import Icon from '@/components/Icon.vue'
import { WINDOW_MAP } from '@/utils/time'
import { formatDuration } from '@/utils/formatters'
import { useFormatters } from '@/composables/useFormatters'

const projects = useProjectsStore()
const perf = usePerformanceStore()
const appUser = useAppUserStore()
const route = useRoute()
const { formatRel } = useFormatters()
const lensIdentity = computed(() => routeUserIdentity(route.query) || appUser.identity)
const userMode = computed(() => !!lensIdentity.value)

const hours = computed(() => WINDOW_MAP[perf.windowHrs] ?? 24)

const params = computed(() => {
  const p = new URLSearchParams()
  p.set('hours', String(hours.value))
  if (perf.envFilter !== 'All') p.set('env', perf.envFilter)
  if (lensIdentity.value) p.set('user', lensIdentity.value)
  for (const id of projects.selectedIds) p.append('project_id', id)
  return p.toString()
})

const pageloadParams = computed(() => {
  const p = new URLSearchParams()
  p.set('hours', String(hours.value))
  p.set('user', lensIdentity.value)
  p.set('limit', '50')
  p.append('op', 'pageload')
  p.append('op', 'navigation')
  if (perf.envFilter !== 'All') p.set('environment', perf.envFilter)
  for (const id of projects.selectedIds) p.append('project_id', id)
  return p.toString()
})

const { data: summary, isLoading: summaryLoading, isError: summaryError, refetch: refetchSummary } = useQuery({
  queryKey: computed(() => ['web-vitals-summary', params.value]),
  queryFn: () => apiFetch<WebVitalsSummary>(`/api/vitals?${params.value}`),
  enabled: computed(() => !userMode.value),
})

const { data: pages, isLoading: pagesLoading, isError: pagesError, refetch: refetchPages } = useQuery({
  queryKey: computed(() => ['web-vitals-pages', params.value]),
  queryFn: () => apiFetch<WebVitalsPage[]>(`/api/vitals/pages?${params.value}`),
  enabled: computed(() => !userMode.value),
})

const { data: pageloadPage, isLoading: pageloadsLoading, isError: pageloadsError, refetch: refetchPageloads } = useQuery({
  queryKey: computed(() => ['user-pageloads', pageloadParams.value]),
  queryFn: () => apiFetch<TransactionListPage>(`/api/transactions?${pageloadParams.value}`),
  enabled: computed(() => userMode.value),
})

const extraPageloads = ref<Transaction[]>([])
const pageloadsMoreCursor = ref<{ cursor_time: string; cursor_id: string } | null>(null)
const loadingMorePageloads = ref(false)
let paginationVersion = 0
function resetPagination() {
  paginationVersion++
  extraPageloads.value = []
  pageloadsMoreCursor.value = null
  loadingMorePageloads.value = false
}
watch(pageloadParams, resetPagination, { flush: 'sync' })
onUnmounted(resetPagination)
const pageloads = computed(() => [...(pageloadPage.value?.transactions ?? []), ...extraPageloads.value])
const pageloadsHasMore = computed(() => extraPageloads.value.length === 0
  ? !!(pageloadPage.value?.next_cursor_id && pageloadPage.value?.next_cursor_time)
  : pageloadsMoreCursor.value != null)
async function loadMorePageloads() {
  const cur = extraPageloads.value.length === 0
    ? { cursor_time: pageloadPage.value?.next_cursor_time, cursor_id: pageloadPage.value?.next_cursor_id }
    : pageloadsMoreCursor.value
  if (!cur?.cursor_time || !cur?.cursor_id || loadingMorePageloads.value) return
  const version = paginationVersion
  loadingMorePageloads.value = true
  try {
    const p = new URLSearchParams(pageloadParams.value)
    p.set('cursor_time', cur.cursor_time)
    p.set('cursor_id', cur.cursor_id)
    const page = await apiFetch<TransactionListPage>(`/api/transactions?${p}`)
    if (version !== paginationVersion) return
    extraPageloads.value = [...extraPageloads.value, ...(page.transactions ?? [])]
    pageloadsMoreCursor.value = page.next_cursor_id && page.next_cursor_time
      ? { cursor_time: page.next_cursor_time, cursor_id: page.next_cursor_id }
      : null
  } finally {
    if (version === paginationVersion) loadingMorePageloads.value = false
  }
}
const isError = computed(() => userMode.value ? pageloadsError.value : (summaryError.value || pagesError.value))
function refetch() {
  resetPagination()
  if (userMode.value) refetchPageloads()
  else { refetchSummary(); refetchPages() }
}

function measurementValue(tx: { measurements?: Record<string, { value?: number } | number> | null }, key: string): number {
  const m = tx.measurements?.[key]
  if (m == null) return 0
  if (typeof m === 'number') return m
  return typeof m.value === 'number' ? m.value : 0
}

const noData = computed(() => {
  if (userMode.value) return !pageloadsLoading.value && pageloads.value.length === 0
  return !summaryLoading.value && summary.value &&
    summary.value.lcp.count === 0 && summary.value.fcp.count === 0 && summary.value.inp.count === 0
})

const THRESHOLDS = {
  lcp:  { good: 2500, poor: 4000, unit: 'ms', label: 'LCP',  name: 'Largest Contentful Paint' },
  fcp:  { good: 1800, poor: 3000, unit: 'ms', label: 'FCP',  name: 'First Contentful Paint' },
  cls:  { good: 0.1,  poor: 0.25, unit: '',   label: 'CLS',  name: 'Cumulative Layout Shift' },
  inp:  { good: 200,  poor: 500,  unit: 'ms', label: 'INP',  name: 'Interaction to Next Paint' },
  ttfb: { good: 800,  poor: 1800, unit: 'ms', label: 'TTFB', name: 'Time to First Byte' },
} as const

type VitalKey = keyof typeof THRESHOLDS
const allVitals: VitalKey[] = ['lcp', 'inp', 'cls', 'fcp', 'ttfb']

function vitalStatus(key: VitalKey, p75: number): 'good' | 'needs-improvement' | 'poor' {
  const t = THRESHOLDS[key]
  if (p75 <= t.good) return 'good'
  if (p75 <= t.poor) return 'needs-improvement'
  return 'poor'
}

function formatVital(key: VitalKey, value: number): string {
  if (value === 0) return '–'
  const { unit } = THRESHOLDS[key]
  if (unit === 'ms') {
    return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${Math.round(value)}ms`
  }
  return value.toFixed(3)
}

function formatPassRate(rate: number): string {
  return `${Math.round(rate * 100)}%`
}

function pageStatus(p: WebVitalsPage): 'good' | 'needs-improvement' | 'poor' {
  if (p.pass_rate >= 0.9) return 'good'
  if (p.pass_rate >= 0.5) return 'needs-improvement'
  return 'poor'
}

type PageSortCol = 'transaction' | 'sessions' | 'lcp_p75' | 'inp_p75' | 'cls_p75' | 'pass_rate'
const sortCol = ref<PageSortCol>('pass_rate')
const sortDir = ref<'asc' | 'desc'>('desc')

function toggleSort(col: PageSortCol) {
  if (sortCol.value === col) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortCol.value = col
    sortDir.value = col === 'transaction' ? 'asc' : 'desc'
  }
}

function sortIcon(col: PageSortCol) {
  if (sortCol.value !== col) return ''
  return sortDir.value === 'desc' ? '↓' : '↑'
}

const sortedPages = computed(() => {
  const list = [...(pages.value ?? [])]
  list.sort((a, b) => {
    const col = sortCol.value
    if (col === 'transaction') {
      const cmp = a.transaction.localeCompare(b.transaction)
      return sortDir.value === 'asc' ? cmp : -cmp
    }
    const diff = a[col] - b[col]
    return sortDir.value === 'desc' ? -diff : diff
  })
  return list
})
</script>

<template>
  <div class="page">
    <PerformanceSubnav />

    <div class="filterbar">
      <FilterChip
        label="Window"
        :value="perf.windowHrs"
        :options="['1h', '24h', '7d', '30d']"
        @change="perf.windowHrs = $event"
      />
      <FilterChip
        label="Env"
        :value="perf.envFilter"
        :options="['All', 'production', 'staging', 'development']"
        @change="perf.envFilter = $event"
      />
      <UserFilter />
    </div>

    <!-- Error -->
    <div v-if="isError" class="txerror">
      <Icon name="alert-circle" :size="16" class="txerror__icon" />
      <span>Couldn't load Web Vitals. Check your connection and try again.</span>
      <button class="btn" @click="refetch()">Retry</button>
    </div>

    <!-- Empty state -->
    <div v-else-if="noData" class="empty-state">
      <div class="empty-state__card">
        <div class="empty-state__icon empty-state__icon--ok">
          <Icon name="globe" :size="28" />
        </div>
        <h2 class="empty-state__title">{{ userMode ? `No page loads for ${appUser.label || lensIdentity} in this window` : 'No browser data in this window' }}</h2>
        <p class="empty-state__body">{{ userMode ? 'Try a wider window. Browser traces only appear when set_user() runs in the browser SDK.' : 'Point your Sentry browser SDK at this instance to start capturing Web Vitals. Only pageload and navigation transactions are included.' }}</p>
      </div>
    </div>

    <template v-else-if="userMode">
      <div v-if="pageloadsLoading" class="txrow txrow--header txrow--pageload">
        <span>Time</span>
        <span>Page</span>
        <span>LCP</span>
        <span>INP</span>
        <span>CLS</span>
        <span>Duration</span>
      </div>
      <div v-if="pageloadsLoading">
        <div v-for="i in 6" :key="i" class="txrow txrow--pageload" aria-hidden="true">
          <span class="ghost ghost--bar" style="width:70px" />
          <span class="ghost ghost--bar" style="width:60%" />
          <span class="ghost ghost--bar" style="width:40px" />
          <span class="ghost ghost--bar" style="width:40px" />
          <span class="ghost ghost--bar" style="width:40px" />
          <span class="ghost ghost--bar" style="width:40px" />
        </div>
      </div>
      <template v-else>
        <div class="txrow txrow--header txrow--pageload">
          <span>Time</span>
          <span>Page</span>
          <span>LCP</span>
          <span>INP</span>
          <span>CLS</span>
          <span>Duration</span>
        </div>
        <RouterLink
          v-for="t in pageloads"
          :key="t.id"
          class="txrow txrow--pageload"
          :to="{ name: 'transaction-detail', params: { id: t.id } }"
        >
          <span class="mono" style="font-size: 11.5px; color: var(--text-3); white-space: nowrap">{{ formatRel(t.start_timestamp) }}</span>
          <span class="mono" style="color: var(--text-1); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">{{ t.transaction }}</span>
          <span class="tx-num-cell">
            <span class="vital-pill" :class="`vital-pill--${vitalStatus('lcp', measurementValue(t, 'lcp'))}`">{{ formatVital('lcp', measurementValue(t, 'lcp')) }}</span>
          </span>
          <span class="tx-num-cell">
            <span class="vital-pill" :class="`vital-pill--${vitalStatus('inp', measurementValue(t, 'inp'))}`">{{ formatVital('inp', measurementValue(t, 'inp')) }}</span>
          </span>
          <span class="tx-num-cell">
            <span class="vital-pill" :class="`vital-pill--${vitalStatus('cls', measurementValue(t, 'cls'))}`">{{ formatVital('cls', measurementValue(t, 'cls')) }}</span>
          </span>
          <span class="tx-num-cell">{{ formatDuration(t.duration_ms) }}</span>
        </RouterLink>
        <div v-if="pageloads.length > 0" class="list-footer">
          <span class="list-footer__count">{{ pageloads.length.toLocaleString() }} page load{{ pageloads.length === 1 ? '' : 's' }}</span>
          <button v-if="pageloadsHasMore" class="btn" :disabled="loadingMorePageloads" @click="loadMorePageloads">
            {{ loadingMorePageloads ? 'Loading…' : 'Load more' }}
          </button>
        </div>
      </template>
    </template>

    <template v-else>
      <!-- Vitals summary strip -->
      <div class="txstats">
        <div v-for="key in allVitals" :key="key" class="txstat">
          <span class="txstat__label">{{ THRESHOLDS[key].label }}</span>
          <span v-if="summaryLoading" class="skel skel--inline" style="width:56px;height:20px"></span>
          <span
            v-else-if="summary && summary[key].count > 0"
            class="txstat__value"
            :class="`txval--${vitalStatus(key, summary[key].p75)}`"
          >{{ formatVital(key, summary[key].p75) }}</span>
          <span v-else class="txstat__value">–</span>
          <span v-if="summary && summary[key].count > 0 && !summaryLoading" class="txstat__sub">
            {{ formatPassRate(summary[key].pass_rate) }} pass &middot; {{ summary[key].count.toLocaleString() }} sessions
          </span>
        </div>
      </div>

      <!-- Per-page breakdown -->
      <div class="perf-table-wrap">
        <table class="perf-table">
          <thead>
            <tr>
              <th>
                <button class="col-sort" @click="toggleSort('transaction')">
                  Page {{ sortIcon('transaction') }}
                </button>
              </th>
              <th class="perf-table__num">
                <button class="col-sort" @click="toggleSort('sessions')">
                  Sessions {{ sortIcon('sessions') }}
                </button>
              </th>
              <th class="perf-table__num">
                <button class="col-sort" @click="toggleSort('lcp_p75')">
                  LCP p75 {{ sortIcon('lcp_p75') }}
                </button>
              </th>
              <th class="perf-table__num">
                <button class="col-sort" @click="toggleSort('inp_p75')">
                  INP p75 {{ sortIcon('inp_p75') }}
                </button>
              </th>
              <th class="perf-table__num">
                <button class="col-sort" @click="toggleSort('cls_p75')">
                  CLS p75 {{ sortIcon('cls_p75') }}
                </button>
              </th>
              <th class="perf-table__num">
                <button class="col-sort" @click="toggleSort('pass_rate')">
                  CWV pass {{ sortIcon('pass_rate') }}
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            <template v-if="pagesLoading">
              <tr v-for="i in 6" :key="i" class="perf-table__skel-row">
                <td><span class="skel" style="width:60%"></span></td>
                <td><span class="skel" style="width:40px"></span></td>
                <td><span class="skel" style="width:48px"></span></td>
                <td><span class="skel" style="width:48px"></span></td>
                <td><span class="skel" style="width:48px"></span></td>
                <td><span class="skel" style="width:40px"></span></td>
              </tr>
            </template>
            <template v-else-if="!sortedPages.length">
              <tr>
                <td colspan="6" class="perf-table__empty">No pages with Web Vitals data in this window.</td>
              </tr>
            </template>
            <template v-else>
              <tr
                v-for="page in sortedPages"
                :key="page.transaction"
                class="perf-table__row perf-table__row--link"
              >
                <td class="perf-table__desc">
                  <RouterLink
                    :to="{ name: 'transaction-profile', query: { name: page.transaction } }"
                    class="perf-table__page-link mono"
                  >{{ page.transaction }}</RouterLink>
                </td>
                <td class="perf-table__num">{{ page.sessions.toLocaleString() }}</td>
                <td class="perf-table__num">
                  <span class="vital-pill" :class="`vital-pill--${vitalStatus('lcp', page.lcp_p75)}`">
                    {{ formatVital('lcp', page.lcp_p75) }}
                  </span>
                </td>
                <td class="perf-table__num">
                  <span class="vital-pill" :class="`vital-pill--${vitalStatus('inp', page.inp_p75)}`">
                    {{ formatVital('inp', page.inp_p75) }}
                  </span>
                </td>
                <td class="perf-table__num">
                  <span class="vital-pill" :class="`vital-pill--${vitalStatus('cls', page.cls_p75)}`">
                    {{ formatVital('cls', page.cls_p75) }}
                  </span>
                </td>
                <td class="perf-table__num">
                  <span
                    class="pass-bar-wrap"
                    :title="`${formatPassRate(page.pass_rate)} of sessions meet all three Core Web Vitals thresholds`"
                  >
                    <span class="pass-bar">
                      <span
                        class="pass-bar__fill"
                        :class="`pass-bar__fill--${pageStatus(page)}`"
                        :style="{ width: `${Math.round(page.pass_rate * 100)}%` }"
                      ></span>
                    </span>
                    <span
                      class="pass-bar__label"
                      :class="`vital-pill--${pageStatus(page)}`"
                    >{{ formatPassRate(page.pass_rate) }}</span>
                  </span>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>
