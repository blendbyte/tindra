<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { InstanceHealth } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { formatBytes, useMonthlyEvents, usePermissions, useProjects, useServerSettings, useUsers } from './shared'

const { canManageProjects } = usePermissions()
const { projects } = useProjects()
const { users } = useUsers()
const { settings } = useServerSettings()
const { totalMonthlyEvents, globalEventPct } = useMonthlyEvents(projects, settings)

const { data: healthData } = useQuery({
  queryKey: ['instance-health'],
  queryFn: ({ signal }) => apiFetch<InstanceHealth>('/api/instance/health', { signal }),
  enabled: canManageProjects,
  staleTime: 60_000,
})

function formatNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return n.toLocaleString()
}

function dataAgeDays(iso: string | null | undefined): string {
  if (!iso) return '–'
  const days = Math.floor((Date.now() - new Date(iso).getTime()) / 86_400_000)
  return days === 0 ? 'today' : `${days}d ago`
}
</script>

<template>
  <div class="pane-head">
    <div>
      <div class="pane-head__title">Instance</div>
      <div class="pane-head__sub mono" style="letter-spacing: 0">
        {{ settings?.version ?? 'dev' }}<template v-if="settings?.commit && settings.commit !== 'unknown'"> &middot; {{ settings.commit }}</template>
        <a v-if="settings?.update_available && settings.release_url" :href="settings.release_url" target="_blank" rel="noopener noreferrer" class="update-avail">{{ settings.latest_version }} available</a>
      </div>
    </div>
    <div class="overview-chips">
      <div class="overview-chip">
        <span class="overview-chip__label">Database</span>
        <span class="overview-chip__value">{{ healthData ? formatBytes(healthData.db_size_bytes) : '–' }}</span>
      </div>
      <div class="overview-chip">
        <span class="overview-chip__label">Retention</span>
        <span class="overview-chip__value">{{ healthData ? (healthData.retention_days > 0 ? `${healthData.retention_days} days` : 'Forever') : '–' }}</span>
      </div>
    </div>
  </div>

  <div class="pane-head">
    <div class="pane-head__title">Data volumes</div>
  </div>
  <div class="overview-vol-wrap" style="margin-bottom: 24px">
    <div class="overview-vol-row overview-vol-row--head">
      <span></span>
      <span>Total</span>
      <span>Last 24h</span>
      <span>Oldest</span>
      <span>Storage</span>
    </div>
    <div class="overview-vol-row">
      <span class="overview-vol-row__name">Errors</span>
      <span class="mono">{{ healthData ? formatNum(healthData.events_total) : '–' }}</span>
      <span class="mono overview-vol-row__rate">+{{ healthData ? formatNum(healthData.events_24h) : '–' }}</span>
      <span class="overview-vol-row__age">{{ dataAgeDays(healthData?.oldest_event_at) }}</span>
      <span class="mono overview-vol-row__size">{{ healthData ? formatBytes(healthData.events_size_bytes) : '–' }}</span>
    </div>
    <div class="overview-vol-row">
      <span class="overview-vol-row__name">Traces</span>
      <span class="mono">{{ healthData ? formatNum(healthData.tx_total) : '–' }}</span>
      <span class="mono overview-vol-row__rate">+{{ healthData ? formatNum(healthData.tx_24h) : '–' }}</span>
      <span class="overview-vol-row__age">{{ dataAgeDays(healthData?.oldest_tx_at) }}</span>
      <span class="mono overview-vol-row__size">{{ healthData ? formatBytes(healthData.tx_size_bytes) : '–' }}</span>
    </div>
    <div class="overview-vol-row">
      <span class="overview-vol-row__name">Logs</span>
      <span class="mono">{{ healthData ? formatNum(healthData.logs_total) : '–' }}</span>
      <span class="mono overview-vol-row__rate">+{{ healthData ? formatNum(healthData.logs_24h) : '–' }}</span>
      <span class="overview-vol-row__age">{{ dataAgeDays(healthData?.oldest_log_at) }}</span>
      <span class="mono overview-vol-row__size">{{ healthData ? formatBytes(healthData.logs_size_bytes) : '–' }}</span>
    </div>
  </div>

  <template v-if="settings">
    <div class="pane-head">
      <div class="pane-head__title">Usage</div>
      <a v-if="settings.billing_url" :href="settings.billing_url" target="_blank" rel="noopener" class="overview-billing-link">
        <Icon name="credit-card" :size="11" />
        Billing
      </a>
    </div>
    <div class="proj-grid">
      <div class="overview-usage-row">
        <div class="overview-usage-row__meta">
          <span class="overview-usage-row__name">Events / month</span>
          <span class="overview-usage-row__count mono">
            {{ formatNum(totalMonthlyEvents) }}
            <span class="overview-limit__sep">/</span>
            <span v-if="settings.event_limit > 0">{{ formatNum(settings.event_limit) }}</span>
            <span v-else class="overview-limit__unlimited">∞</span>
          </span>
        </div>
        <div class="overview-bar" :class="settings.event_limit === 0 ? 'overview-bar--unlimited' : ''">
          <div
            v-if="settings.event_limit > 0"
            class="overview-bar__fill"
            :class="globalEventPct >= 100 ? 'overview-bar__fill--danger' : globalEventPct >= 80 ? 'overview-bar__fill--warn' : ''"
            :style="{ width: `${globalEventPct}%` }"
          />
        </div>
      </div>
      <div class="overview-usage-row">
        <div class="overview-usage-row__meta">
          <span class="overview-usage-row__name">Projects</span>
          <span class="overview-usage-row__count mono">
            {{ projects.length }}
            <span class="overview-limit__sep">/</span>
            <span v-if="settings.project_limit > 0">{{ settings.project_limit }}</span>
            <span v-else class="overview-limit__unlimited">∞</span>
          </span>
        </div>
        <div class="overview-bar" :class="settings.project_limit === 0 ? 'overview-bar--unlimited' : ''">
          <div
            v-if="settings.project_limit > 0"
            class="overview-bar__fill"
            :style="{ width: `${Math.min(100, Math.round(projects.length / settings.project_limit * 100))}%` }"
          />
        </div>
      </div>
      <div class="overview-usage-row">
        <div class="overview-usage-row__meta">
          <span class="overview-usage-row__name">Users</span>
          <span class="overview-usage-row__count mono">
            {{ users.length }}
            <span class="overview-limit__sep">/</span>
            <span v-if="settings.user_limit > 0">{{ settings.user_limit }}</span>
            <span v-else class="overview-limit__unlimited">∞</span>
          </span>
        </div>
        <div class="overview-bar" :class="settings.user_limit === 0 ? 'overview-bar--unlimited' : ''">
          <div
            v-if="settings.user_limit > 0"
            class="overview-bar__fill"
            :style="{ width: `${Math.min(100, Math.round(users.length / settings.user_limit * 100))}%` }"
          />
        </div>
      </div>
      <div class="overview-usage-row">
        <div class="overview-usage-row__meta">
          <span class="overview-usage-row__name">Logs stored</span>
          <span class="overview-usage-row__count mono">
            {{ healthData ? formatNum(healthData.logs_total) : '–' }}
            <span class="overview-limit__sep">/</span>
            <span v-if="settings.log_row_limit > 0">{{ formatNum(settings.log_row_limit) }}</span>
            <span v-else class="overview-limit__unlimited">∞</span>
          </span>
        </div>
        <div class="overview-bar" :class="settings.log_row_limit === 0 ? 'overview-bar--unlimited' : ''">
          <div
            v-if="settings.log_row_limit > 0 && healthData"
            class="overview-bar__fill"
            :class="healthData.logs_total / settings.log_row_limit >= 1 ? 'overview-bar__fill--danger' : healthData.logs_total / settings.log_row_limit >= 0.8 ? 'overview-bar__fill--warn' : ''"
            :style="{ width: `${Math.min(100, Math.round(healthData.logs_total / settings.log_row_limit * 100))}%` }"
          />
        </div>
      </div>
      <div class="overview-usage-row">
        <div class="overview-usage-row__meta">
          <span class="overview-usage-row__name">Transactions stored</span>
          <span class="overview-usage-row__count mono">
            {{ healthData ? formatNum(healthData.tx_total) : '–' }}
            <span class="overview-limit__sep">/</span>
            <span v-if="settings.tx_row_limit > 0">{{ formatNum(settings.tx_row_limit) }}</span>
            <span v-else class="overview-limit__unlimited">∞</span>
          </span>
        </div>
        <div class="overview-bar" :class="settings.tx_row_limit === 0 ? 'overview-bar--unlimited' : ''">
          <div
            v-if="settings.tx_row_limit > 0 && healthData"
            class="overview-bar__fill"
            :class="healthData.tx_total / settings.tx_row_limit >= 1 ? 'overview-bar__fill--danger' : healthData.tx_total / settings.tx_row_limit >= 0.8 ? 'overview-bar__fill--warn' : ''"
            :style="{ width: `${Math.min(100, Math.round(healthData.tx_total / settings.tx_row_limit * 100))}%` }"
          />
        </div>
      </div>
    </div>
  </template>
</template>
