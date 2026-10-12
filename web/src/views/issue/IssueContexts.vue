<script setup lang="ts">
import { computed } from 'vue'
import type { Event as TindraEvent } from '@/api/types'
import Icon from '@/components/Icon.vue'
import IssueSection from './IssueSection.vue'
import { eventContexts } from './issueEvent'

const props = defineProps<{ event?: TindraEvent | null }>()

const displayContexts = computed(() => eventContexts(props.event))
</script>

<template>
  <IssueSection v-if="displayContexts.length > 0" section-key="context" title="Context">
    <div class="ctx-grid">
      <div v-for="ctx in displayContexts" :key="ctx.key" class="ctx-card">
        <div class="ctx-card__title">
          <Icon :name="ctx.icon" :size="12" class="ctx-card__icon" />
          {{ ctx.label }}
        </div>
        <table class="ctx-table">
          <tbody>
            <tr v-for="[k, v] in ctx.rows" :key="k">
              <td class="ctx-table__key">{{ k }}</td>
              <td class="ctx-table__val mono">{{ v }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </IssueSection>
</template>
