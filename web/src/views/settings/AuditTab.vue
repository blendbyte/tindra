<script setup lang="ts">
import { ref, computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { AuditRow } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useFormatters } from '@/composables/useFormatters'
import { useDebounced } from '@/composables/useDebounced'

const { formatRel } = useFormatters()

const auditKindFilter = ref('All')
const auditSearch = ref('')

const auditKinds = ['All', 'auth', 'alert', 'issue', 'release', 'token', 'project']

// Search is debounced and URL-encoded: "&", "#", "+" and "%" in a search used
// to corrupt the query string.
const auditSearchTerm = useDebounced(auditSearch, 300)
const auditQuery = computed(() => new URLSearchParams({
  kind: auditKindFilter.value === 'All' ? '' : auditKindFilter.value,
  q: auditSearchTerm.value,
}).toString())
const { data: auditLogData } = useQuery({
  queryKey: computed(() => ['audit', auditQuery.value]),
  queryFn: ({ signal }) => apiFetch<AuditRow[]>(`/api/audit?${auditQuery.value}`, { signal }),
})
const auditLog = computed(() => auditLogData.value ?? [])

function actionKindOf(action: string) {
  if (action.startsWith('auth.')) return 'auth'
  if (action.startsWith('alert.')) return 'alert'
  if (action.startsWith('issue.')) return 'issue'
  if (action.startsWith('release.')) return 'release'
  if (action.startsWith('token.')) return 'token'
  if (action.startsWith('project.')) return 'project'
  return 'other'
}
</script>

<template>
  <div class="pane-head">
    <div>
      <div class="pane-head__title">Audit log</div>
      <div class="pane-head__sub">{{ auditLog.length }} events</div>
    </div>
  </div>

  <div class="audit-toolbar">
    <button
      v-for="k in auditKinds"
      :key="k"
      class="audit-chip"
      :class="{ 'audit-chip--active': k === auditKindFilter }"
      @click="auditKindFilter = k"
    >
      {{ k }}
    </button>
    <div class="filterbar__search" style="margin-left: auto; min-width: 240px">
      <Icon name="search" :size="12" style="color: var(--text-3)" />
      <input
        v-model="auditSearch"
        placeholder="Search audit log..."
      />
    </div>
  </div>

  <div class="audit-list">
    <div
      v-for="row in auditLog"
      :key="row.id"
      class="audit-row"
    >
      <span class="audit-row__when mono">{{ formatRel(row.created_at) }}</span>
      <span class="audit-row__kind" :class="`audit-row__kind--${actionKindOf(row.event_type)}`">
        {{ actionKindOf(row.event_type) }}
      </span>
      <span class="audit-row__actor">{{ row.actor_email ?? 'system' }}</span>
      <span class="audit-row__action mono">{{ row.event_type }}</span>
      <span class="audit-row__ip mono">{{ row.ip }}</span>
    </div>
    <div v-if="auditLog.length === 0" class="settings-empty">
      <div class="settings-empty__icon">
        <Icon name="shield" :size="28" />
      </div>
      <div class="settings-empty__title">No audit events yet</div>
      <div class="settings-empty__body">Logins, project changes, and token management actions will appear here.</div>
    </div>
  </div>
</template>
