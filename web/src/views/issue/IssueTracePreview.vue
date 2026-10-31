<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Event as TindraEvent, Transaction, Span } from '@/api/types'
import Icon from '@/components/Icon.vue'
import IssueSection from './IssueSection.vue'

// The transaction the current event happened in, with its first spans as a
// small waterfall. eventIndex and pinnedEvent identify the event in the query
// key; eventParams is the same selection as a query string.
const props = defineProps<{
  issueId: string
  event?: TindraEvent | null
  eventIndex: number
  pinnedEvent: string
  eventParams: string
}>()

const router = useRouter()

const traceId = computed(() => {
  if (props.event?.trace_id) return props.event.trace_id
  const ctx = props.event?.payload?.contexts as Record<string, unknown> | undefined
  return (ctx?.trace as Record<string, unknown> | undefined)?.trace_id as string | undefined
})

const { data: linkedTransaction } = useQuery({
  queryKey: computed(() => ['issues', props.issueId, 'trace', props.eventIndex, props.pinnedEvent]),
  queryFn: ({ signal }) => apiFetch<Transaction | null>(`/api/issues/${props.issueId}/trace?${props.eventParams}`, { signal }),
  enabled: computed(() => !!props.issueId && !!traceId.value),
})

const { data: traceSpans } = useQuery({
  queryKey: computed(() => ['transactions', linkedTransaction.value?.id, 'spans']),
  queryFn: ({ signal }) => apiFetch<Span[]>(`/api/transactions/${linkedTransaction.value!.id}/spans`, { signal }),
  enabled: computed(() => !!linkedTransaction.value?.id),
})

const TRACE_PREVIEW_MAX = 10

const tracePreviewRows = computed(() => {
  const tx = linkedTransaction.value
  const spans = traceSpans.value
  if (!tx) return []
  const total = tx.duration_ms || 1
  const rows: { label: string; op: string; left: number; width: number; isTx: boolean }[] = []
  rows.push({ label: tx.transaction, op: tx.op, left: 0, width: 100, isTx: true })
  if (spans) {
    for (const s of spans.slice(0, TRACE_PREVIEW_MAX - 1)) {
      const left = Math.max(0, (s.start_offset_ms / total) * 100)
      const width = Math.max(0.5, (s.duration_ms / total) * 100)
      rows.push({ label: s.description || s.op, op: s.op, left, width, isTx: false })
    }
  }
  return rows
})
</script>

<template>
  <IssueSection v-if="linkedTransaction" section-key="trace" title="Trace preview">
    <template #head>
      <a
        class="section__link"
        :href="`/transactions/${linkedTransaction.id}`"
        @click.stop="router.push(`/transactions/${linkedTransaction.id}`)"
      >View full trace <Icon name="external" :size="10" /></a>
    </template>
    <div class="trace-preview">
      <div
        v-for="(row, i) in tracePreviewRows"
        :key="i"
        class="trace-preview__row"
        :class="{ 'trace-preview__row--tx': row.isTx }"
      >
        <span class="trace-preview__label">
          <span class="trace-preview__op">{{ row.op }}</span>
          {{ row.label }}
        </span>
        <div class="trace-preview__track">
          <div
            class="trace-preview__bar"
            :class="{ 'trace-preview__bar--tx': row.isTx }"
            :style="{ left: row.left + '%', width: row.width + '%' }"
          />
        </div>
      </div>
      <div v-if="(traceSpans?.length ?? 0) > TRACE_PREVIEW_MAX - 1" class="trace-preview__more">
        +{{ traceSpans!.length - (TRACE_PREVIEW_MAX - 1) }} more spans
      </div>
    </div>
  </IssueSection>
</template>
