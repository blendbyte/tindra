<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import type { SpanSummary, SpanSample } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { formatDuration } from '@/utils/formatters'
import { useTimezone } from '@/composables/useTimezone'

const props = defineProps<{
  row: SpanSummary
  hours: number
  env: string
}>()

const emit = defineEmits<{ close: [] }>()

const projects = useProjectsStore()
const tz = useTimezone()

const queryParams = computed(() => {
  const p = new URLSearchParams()
  p.set('op', props.row.op)
  p.set('description', props.row.description)
  p.set('hours', String(props.hours))
  if (props.env && props.env !== 'All') p.set('env', props.env)
  for (const id of projects.selectedIds) p.append('project_id', id)
  return p.toString()
})

const { data: samples, isLoading } = useQuery({
  queryKey: computed(() => ['span-samples', queryParams.value]),
  queryFn: () => apiFetch<SpanSample[]>(`/api/spans/samples?${queryParams.value}`),
})


function formatTime(iso: string) {
  const d = new Date(iso)
  const now = Date.now()
  const diff = now - d.getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: tz.value })
}

function durClass(ms: number) {
  if (ms > props.row.p95) return 'sample-item__dur--slow'
  if (ms > props.row.p50) return 'sample-item__dur--medium'
  return ''
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onUnmounted(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="samples-backdrop" @click="emit('close')" />

  <div class="samples-panel">
    <div class="samples-panel__header">
      <div class="samples-panel__header-left">
        <span class="optag" :class="`optag--${row.op.split('.')[0]}`">{{ row.op }}</span>
        <span class="samples-panel__title">{{ row.description || '(no description)' }}</span>
      </div>
      <button class="icon-btn" @click="emit('close')" title="Close">
        <Icon name="x" :size="16" />
      </button>
    </div>

    <div class="samples-panel__stats">
      <div class="samples-panel__stat">
        <span class="samples-panel__stat-label">Samples</span>
        <span class="samples-panel__stat-value">{{ row.sample_count.toLocaleString() }}</span>
      </div>
      <div class="samples-panel__stat">
        <span class="samples-panel__stat-label">P50</span>
        <span class="samples-panel__stat-value">{{ formatDuration(row.p50) }}</span>
      </div>
      <div class="samples-panel__stat">
        <span class="samples-panel__stat-label">P95</span>
        <span class="samples-panel__stat-value">{{ formatDuration(row.p95) }}</span>
      </div>
      <div class="samples-panel__stat">
        <span class="samples-panel__stat-label">Error %</span>
        <span class="samples-panel__stat-value" :class="row.error_rate > 0 ? 'tx-failure' : ''">
          {{ row.error_rate.toFixed(1) }}%
        </span>
      </div>
    </div>

    <div class="samples-panel__body">
      <!-- skeleton -->
      <template v-if="isLoading">
        <div v-for="i in 8" :key="i" class="sample-item sample-item--skel">
          <span class="skel" style="width: 50%; height: 10px" />
          <span class="skel" style="width: 48px; height: 10px" />
          <span class="skel" style="width: 36px; height: 10px" />
        </div>
      </template>

      <!-- empty -->
      <div v-else-if="!samples || samples.length === 0" class="samples-empty">
        No recent samples found.
      </div>

      <!-- list -->
      <template v-else>
        <RouterLink
          v-for="s in samples"
          :key="s.span_id"
          :to="`/transactions/${s.transaction_id}`"
          class="sample-item"
          @click="emit('close')"
        >
          <span class="sample-item__name">{{ s.transaction_name }}</span>
          <span class="sample-item__time">{{ formatTime(s.start_timestamp) }}</span>
          <span class="sample-item__dur" :class="durClass(s.duration_ms)">
            {{ formatDuration(s.duration_ms) }}
          </span>
          <span v-if="s.status !== 'ok'" class="sample-item__status">error</span>
          <span v-else style="width: 36px" />
        </RouterLink>
      </template>
    </div>
  </div>
</template>
