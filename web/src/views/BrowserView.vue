<script setup lang="ts">
import { ref, computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { RouterLink } from 'vue-router'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'
import { apiFetch } from '@/api/client'
import type { WebVitalsSummary, WebVitalsPage } from '@/api/types'
import FilterChip from '@/components/FilterChip.vue'
import PerformanceSubnav from '@/components/PerformanceSubnav.vue'
import Icon from '@/components/Icon.vue'

const projects = useProjectsStore()
const perf = usePerformanceStore()

const WINDOW_MAP: Record<string, number> = { '1h': 1, '24h': 24, '7d': 168, '30d': 720 }
const hours = computed(() => WINDOW_MAP[perf.windowHrs] ?? 24)

const params = computed(() => {
  const p = new URLSearchParams()
  p.set('hours', String(hours.value))
  if (perf.envFilter !== 'All') p.set('env', perf.envFilter)
  for (const id of projects.selectedIds) p.append('project_id', id)
  return p.toString()
})

const { data: summary, isLoading: summaryLoading, isError: summaryError, refetch: refetchSummary } = useQuery({
  queryKey: computed(() => ['web-vitals-summary', params.value]),
  queryFn: () => apiFetch<WebVitalsSummary>(`/api/vitals?${params.value}`),
})

const { data: pages, isLoading: pagesLoading, isError: pagesError, refetch: refetchPages } = useQuery({
  queryKey: computed(() => ['web-vitals-pages', params.value]),
  queryFn: () => apiFetch<WebVitalsPage[]>(`/api/vitals/pages?${params.value}`),
})

const isError = computed(() => summaryError.value || pagesError.value)
function refetch() { refetchSummary(); refetchPages() }

const noData = computed(() =>
  !summaryLoading.value && summary.value &&
  summary.value.lcp.count === 0 && summary.value.fcp.count === 0 && summary.value.inp.count === 0,
)

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
        <h2 class="empty-state__title">No browser data in this window</h2>
        <p class="empty-state__body">Point your Sentry browser SDK at this instance to start capturing Web Vitals. Only <code>pageload</code> and <code>navigation</code> transactions are included.</p>
      </div>
    </div>

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
