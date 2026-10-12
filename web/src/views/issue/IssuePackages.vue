<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Event as TindraEvent } from '@/api/types'
import IssueSection from './IssueSection.vue'

const props = defineProps<{ event?: TindraEvent | null }>()

const modules = computed<[string, string][]>(() => {
  const raw = props.event?.payload?.modules as Record<string, unknown> | undefined
  if (!raw || typeof raw !== 'object') return []
  return Object.entries(raw)
    .filter(([, v]) => v !== null && v !== undefined)
    .map(([k, v]): [string, string] => [k, String(v)])
    .sort(([a], [b]) => a.localeCompare(b))
})

const showAllModules = ref(false)
const MODULES_PREVIEW = 10
</script>

<template>
  <IssueSection v-if="modules.length > 0" section-key="packages" title="Packages">
    <template #head>
      <span class="section__count">{{ modules.length }}</span>
    </template>
    <table class="pkg-table">
      <tbody>
        <tr v-for="[name, version] in (showAllModules ? modules : modules.slice(0, MODULES_PREVIEW))" :key="name">
          <td class="pkg-table__name mono">{{ name }}</td>
          <td class="pkg-table__ver mono">{{ version }}</td>
        </tr>
      </tbody>
    </table>
    <button v-if="modules.length > MODULES_PREVIEW" class="btn btn--ghost pkg-table__more" @click="showAllModules = !showAllModules">
      {{ showAllModules ? 'Show less' : `Show ${modules.length - MODULES_PREVIEW} more` }}
    </button>
  </IssueSection>
</template>
