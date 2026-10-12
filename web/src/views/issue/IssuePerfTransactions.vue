<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Issue, PerfEvent } from '@/api/types'
import { useFormatters } from '@/composables/useFormatters'

// The transactions an N+1 performance issue was detected in.
const props = defineProps<{ issueId: string; issue: Issue }>()

const { formatRel } = useFormatters()

const { data: perfEvents, isLoading: perfEventsLoading } = useQuery({
  queryKey: computed(() => ['issues', props.issueId, 'perf-events']),
  queryFn: ({ signal }) => apiFetch<PerfEvent[]>(`/api/issues/${props.issueId}/perf-events`, { signal }),
  enabled: computed(() => !!props.issueId && props.issue.kind === 'n1_query'),
})
</script>

<template>
  <div class="section section--primary">
    <div class="section__head" style="cursor: default">
      <h2 class="section__title">Affected transactions</h2>
      <span class="section__count">{{ issue.event_count.toLocaleString() }} detections</span>
    </div>
    <div v-if="perfEventsLoading" class="section-empty">Loading...</div>
    <div v-else-if="!perfEvents?.length" class="section-empty">No transactions recorded yet.</div>
    <div v-else class="data-list-scroll">
      <table class="perf-table">
        <thead>
          <tr>
            <th>Transaction</th>
            <th>Repeated queries</th>
            <th>Time wasted</th>
            <th>Detected</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="pe in perfEvents" :key="pe.id">
            <td><RouterLink :to="`/transactions/${pe.transaction_id}`" class="link mono">{{ pe.transaction }}</RouterLink></td>
            <td>{{ pe.span_count }}×</td>
            <td>{{ pe.total_ms }}ms</td>
            <td :title="new Date(pe.created_at).toUTCString()">{{ formatRel(pe.created_at) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
