<script setup lang="ts">
import { computed, ref, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { formatDuration } from '@/utils/formatters'
import { useFormatters } from '@/composables/useFormatters'
import type { Release, ReleaseIssue, ReleaseTxSummary } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useProjectsStore } from '@/stores/projects'

const route = useRoute()
const router = useRouter()
const projects = useProjectsStore()
const { formatRel } = useFormatters()

const releaseId = computed(() => route.params.id as string)

const { data: release, isError, isLoading } = useQuery({
  queryKey: computed(() => ['releases', releaseId.value]),
  queryFn: () => apiFetch<Release>(`/api/releases/${releaseId.value}`),
})

const { data: issues } = useQuery({
  queryKey: computed(() => ['release-issues', releaseId.value]),
  queryFn: () => apiFetch<ReleaseIssue[]>(`/api/releases/${releaseId.value}/issues`),
  enabled: computed(() => !!releaseId.value),
})

const { data: transactions } = useQuery({
  queryKey: computed(() => ['release-transactions', releaseId.value]),
  queryFn: () => apiFetch<ReleaseTxSummary[]>(`/api/releases/${releaseId.value}/transactions`),
  enabled: computed(() => !!releaseId.value),
})

const projectName = computed(() => {
  const map = new Map((projects.projects ?? []).map((p) => [p.id, p.name]))
  return (id: string) => map.get(id) ?? ''
})

type Tab = 'transactions' | 'issues'
const activeTab = ref<Tab>('transactions')

const newIssues = computed(() => issues.value?.filter(i => i.category === 'new') ?? [])
const regressedIssues = computed(() => issues.value?.filter(i => i.category === 'regressed') ?? [])
const ongoingIssues = computed(() => issues.value?.filter(i => i.category === 'ongoing') ?? [])

watchEffect(() => {
  if (release.value?.version) document.title = `${release.value.version} - Tindra`
})

function levelClass(level: string) {
  return `leveldot--${level}`
}
</script>

<template>
  <!-- Error state -->
  <div v-if="isError" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/releases')">
        <Icon name="arrow-left" :size="12" />
        Releases
      </a>
    </div>
    <div class="rel-not-found">
      <Icon name="package" :size="24" style="color: var(--text-3)" />
      <div class="rel-not-found__title">Release not found</div>
      <button class="btn" @click="router.push('/releases')">Back to releases</button>
    </div>
  </div>

  <!-- Shape-matched loading skeleton -->
  <div v-else-if="isLoading" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/releases')">
        <Icon name="arrow-left" :size="12" />
        Releases
      </a>
    </div>
    <div class="detail-body">
      <div class="detail-hero">
        <div class="detail-hero__tags">
          <span class="skel" style="width: 88px; height: 20px; display: inline-block; border-radius: 2px" />
          <span class="skel" style="width: 64px; height: 20px; display: inline-block; border-radius: 2px" />
        </div>
        <div class="stat-row" style="margin-top: 20px">
          <div v-for="i in 4" :key="i" class="stat">
            <span class="skel" style="width: 56px; height: 10px; display: block" />
            <span class="skel" style="width: 72px; height: 22px; display: block; margin-top: 6px" />
          </div>
        </div>
      </div>
      <div style="margin-top: 28px">
        <span class="skel skel--section-head" style="display: block" />
        <div class="rel-tx-table" style="margin-top: 12px">
          <div v-for="i in 4" :key="i" class="rel-tx-row" style="cursor: default">
            <div class="rel-tx-name">
              <span class="skel" style="width: 44px; height: 20px; border-radius: 10px; display: block; flex: 0 0 44px" />
              <span class="skel" :style="{ width: ['58%','72%','48%','65%'][i-1] }" style="height: 10px; display: block" />
            </div>
            <span class="skel rel-tx-num" style="width: 28px; height: 10px; display: inline-block" />
            <span class="skel rel-tx-num" style="width: 36px; height: 10px; display: inline-block" />
            <span class="skel rel-tx-num" style="width: 36px; height: 10px; display: inline-block" />
            <span class="skel rel-tx-num" style="width: 20px; height: 10px; display: inline-block" />
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- Data view -->
  <div v-else-if="release" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/releases')">
        <Icon name="arrow-left" :size="12" />
        Releases
      </a>
      <div class="detail-breadcrumb__title"><span>{{ release.version }}</span></div>
    </div>

    <div class="detail-body">
      <div class="detail-hero">
        <div class="detail-hero__tags">
          <span class="tag"><Icon name="package" :size="11" /> {{ release.version }}</span>
          <span v-if="projectName(release.project_id)" class="tag">{{ projectName(release.project_id) }}</span>
          <span class="tag"><Icon name="clock" :size="11" /> {{ formatRel(release.deployed_at) }}</span>
        </div>
        <div class="stat-row" style="margin-top: 20px">
          <div class="stat">
            <div class="stat__label">Transactions</div>
            <div class="stat__value">
              {{ release.tx_count > 0 ? release.tx_count.toLocaleString() : '–' }}
            </div>
          </div>
          <div class="stat">
            <div class="stat__label">P50</div>
            <div class="stat__value mono">
              {{ release.tx_count > 0 ? formatDuration(release.tx_p50) : '–' }}
            </div>
          </div>
          <div class="stat">
            <div class="stat__label">Error rate</div>
            <div class="stat__value" :style="{ color: release.tx_error_rate > 0 ? 'var(--danger)' : undefined }">
              {{ release.tx_count > 0 ? (release.tx_error_rate > 0 ? release.tx_error_rate.toFixed(1) + '%' : '–') : '–' }}
            </div>
          </div>
          <div class="stat">
            <div class="stat__label">New issues</div>
            <div class="stat__value" :style="{ color: release.new_issues > 0 ? 'var(--danger)' : undefined }">
              {{ release.new_issues > 0 ? release.new_issues : '–' }}
            </div>
          </div>
          <div class="stat">
            <div class="stat__label">Regressions</div>
            <div class="stat__value" :style="{ color: release.regressed_issues > 0 ? 'var(--warning, oklch(0.78 0.15 75))' : undefined }">
              {{ release.regressed_issues > 0 ? release.regressed_issues : '–' }}
            </div>
          </div>
        </div>
      </div>

      <!-- Tab strip + content - full width -->
      <div style="grid-column: 1 / -1">
        <div class="optabs" style="padding: 0; margin-top: 8px">
          <button
            class="optab"
            :class="{ 'optab--active': activeTab === 'transactions' }"
            @click="activeTab = 'transactions'"
          >
            Transactions
            <span v-if="transactions?.length" class="optab__count">{{ transactions.length }}</span>
          </button>
          <button
            class="optab"
            :class="{ 'optab--active': activeTab === 'issues' }"
            @click="activeTab = 'issues'"
          >
            Issues
            <span v-if="issues?.length" class="optab__count">{{ issues.length }}</span>
          </button>
        </div>

        <!-- Transactions tab -->
        <div v-if="activeTab === 'transactions'" class="section" style="margin-top: 16px">
          <div v-if="transactions && transactions.length === 0" class="rel-section-empty">
            No transactions recorded for this release.
          </div>
          <div v-else-if="transactions" class="rel-tx-table">
            <div class="rel-tx-row rel-tx-row--header">
              <span>Transaction</span>
              <span class="rel-tx-num">Count</span>
              <span class="rel-tx-num">P50</span>
              <span class="rel-tx-num">P95</span>
              <span class="rel-tx-num">Errors</span>
            </div>
            <div
              v-for="tx in transactions"
              :key="`${tx.transaction}|${tx.op}`"
              class="rel-tx-row"
              @click="router.push({ name: 'transaction-profile', query: { name: tx.transaction, op: tx.op } })"
            >
              <div class="rel-tx-name">
                <span class="optag" :class="`optag--${tx.op.split('.')[0]}`">{{ tx.op }}</span>
                <span class="mono rel-tx-label">{{ tx.transaction }}</span>
              </div>
              <span class="rel-tx-num">{{ tx.sample_count.toLocaleString() }}</span>
              <span class="rel-tx-num mono">{{ formatDuration(tx.p50) }}</span>
              <span class="rel-tx-num mono">{{ formatDuration(tx.p95) }}</span>
              <span class="rel-tx-num" :class="tx.error_rate > 0 ? 'tx-failure' : 'muted'">
                {{ tx.error_rate > 0 ? tx.error_rate.toFixed(1) + '%' : '–' }}
              </span>
            </div>
          </div>
        </div>

        <!-- Issues tab -->
        <div v-else-if="activeTab === 'issues'" class="section" style="margin-top: 16px">
          <div v-if="issues && issues.length === 0" class="rel-section-empty">
            No issues recorded for this release.
          </div>
          <template v-else-if="issues">
            <!-- New issues -->
            <div v-if="newIssues.length > 0" class="rel-issue-group">
              <div class="rel-issue-group__head">
                <span class="rel-category-badge rel-category-badge--new">New</span>
                <span class="rel-issue-group__count">{{ newIssues.length }}</span>
              </div>
              <div class="rel-issue-list">
                <div
                  v-for="issue in newIssues"
                  :key="issue.id"
                  class="rel-issue-row"
                  @click="router.push(`/issues/${issue.id}`)"
                >
                  <span class="leveldot" :class="levelClass(issue.level)" />
                  <div class="rel-issue-title">{{ issue.title }}</div>
                  <span class="rel-issue-count mono">{{ issue.event_count.toLocaleString() }}</span>
                  <span class="rel-issue-time">{{ formatRel(issue.last_seen) }}</span>
                </div>
              </div>
            </div>

            <!-- Regressions -->
            <div v-if="regressedIssues.length > 0" class="rel-issue-group">
              <div class="rel-issue-group__head">
                <span class="rel-category-badge rel-category-badge--regressed">Regressed</span>
                <span class="rel-issue-group__count">{{ regressedIssues.length }}</span>
              </div>
              <div class="rel-issue-list">
                <div
                  v-for="issue in regressedIssues"
                  :key="issue.id"
                  class="rel-issue-row"
                  @click="router.push(`/issues/${issue.id}`)"
                >
                  <span class="leveldot" :class="levelClass(issue.level)" />
                  <div class="rel-issue-title">{{ issue.title }}</div>
                  <span class="rel-issue-count mono">{{ issue.event_count.toLocaleString() }}</span>
                  <span class="rel-issue-time">{{ formatRel(issue.last_seen) }}</span>
                </div>
              </div>
            </div>

            <!-- Ongoing -->
            <div v-if="ongoingIssues.length > 0" class="rel-issue-group">
              <div class="rel-issue-group__head">
                <span class="rel-category-badge rel-category-badge--ongoing">Ongoing</span>
                <span class="rel-issue-group__count">{{ ongoingIssues.length }}</span>
              </div>
              <div class="rel-issue-list">
                <div
                  v-for="issue in ongoingIssues"
                  :key="issue.id"
                  class="rel-issue-row"
                  @click="router.push(`/issues/${issue.id}`)"
                >
                  <span class="leveldot" :class="levelClass(issue.level)" />
                  <div class="rel-issue-title">{{ issue.title }}</div>
                  <span class="rel-issue-count mono">{{ issue.event_count.toLocaleString() }}</span>
                  <span class="rel-issue-time">{{ formatRel(issue.last_seen) }}</span>
                </div>
              </div>
            </div>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>
