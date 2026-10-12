<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type { Event as TindraEvent } from '@/api/types'
import { useFormatters } from '@/composables/useFormatters'
import IssueSection from './IssueSection.vue'

const props = defineProps<{ event?: TindraEvent | null }>()

const { formatTs } = useFormatters()

const breadcrumbs = computed(() => {
  const bc = props.event?.payload?.breadcrumbs as { values?: unknown[] } | undefined
  return bc?.values ?? []
})

const expandedCrumbs = ref(new Set<number>())
watch(() => props.event, () => { expandedCrumbs.value = new Set() })

function toggleCrumb(i: number) {
  const s = new Set(expandedCrumbs.value)
  if (s.has(i)) s.delete(i); else s.add(i)
  expandedCrumbs.value = s
}

// JS SDK sends breadcrumb timestamps as Unix float seconds (e.g. 1716739678.943),
// not ISO strings. Handle both so formatTs always receives a valid ISO string.
function formatCrumbTime(ts: unknown): string {
  if (typeof ts === 'number') return formatTs(new Date(ts * 1000).toISOString())
  if (typeof ts === 'string') return formatTs(ts)
  return ''
}
</script>

<template>
  <IssueSection v-if="breadcrumbs.length > 0" section-key="breadcrumbs" title="Breadcrumbs">
    <template #head>
      <span class="section__count">{{ breadcrumbs.length }} events leading up to crash</span>
    </template>
    <div class="crumbs">
      <div
        v-for="(c, i) in (breadcrumbs as Record<string, unknown>[])"
        :key="i"
        class="crumb"
        :class="{ 'crumb--error': c['level'] === 'error' || c['type'] === 'error' }"
      >
        <div class="crumb__row">
          <span class="crumb__time">{{ formatCrumbTime(c['timestamp']) }}</span>
          <span class="crumb__type">{{ c['category'] ?? c['type'] }}</span>
          <span class="crumb__msg">{{ c['message'] ?? (typeof c['data'] !== 'object' ? c['data'] : '') }}</span>
          <button
            v-if="c['data'] !== null && c['data'] !== undefined && typeof c['data'] === 'object'"
            class="crumb__toggle"
            :class="{ 'crumb__toggle--open': expandedCrumbs.has(i) }"
            :aria-expanded="expandedCrumbs.has(i)"
            aria-label="Show breadcrumb data"
            @click="toggleCrumb(i)"
          >{…}</button>
        </div>
        <div v-if="expandedCrumbs.has(i)" class="crumb__detail">
          <div
            v-for="[k, v] in Object.entries(c['data'] as Record<string, unknown>)"
            :key="k"
            class="crumb__detail-row"
          >
            <span class="crumb__detail-key">{{ k }}</span>
            <span class="crumb__detail-val">{{ typeof v === 'object' ? JSON.stringify(v) : v }}</span>
          </div>
        </div>
      </div>
    </div>
  </IssueSection>
</template>
