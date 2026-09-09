<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import { useTimezone } from '@/composables/useTimezone'

const props = defineProps<{
  resource: string
  failed: boolean
  hasData: boolean
  paused?: boolean
  refreshing?: boolean
  updatedAt?: number
}>()
defineEmits<{ retry: [] }>()
const timezone = useTimezone()
const lastUpdated = computed(() => props.updatedAt ? new Date(props.updatedAt).toLocaleString(undefined, {
  timeZone: timezone.value, dateStyle: 'medium', timeStyle: 'short',
}) : null)
</script>

<template>
  <div v-if="failed || paused" class="txerror query-feedback" role="alert">
    <Icon name="alert-triangle" :size="14" class="txerror__icon" />
    <span v-if="paused">
      {{ hasData ? `Updates to ${resource} are paused.` : `Loading ${resource} is paused.` }}
      Check your connection; requests will resume automatically when possible.
      <template v-if="hasData">
        {{ lastUpdated ? `Showing data last updated at ${lastUpdated}.` : 'Showing the last successful result.' }}
      </template>
    </span>
    <span v-else-if="hasData">
      Couldn't refresh {{ resource }}.
      {{ lastUpdated ? `Showing data last updated at ${lastUpdated}.` : 'Showing the last successful result.' }}
    </span>
    <span v-else>We couldn't load {{ resource }}.</span>
    <button v-if="!paused" class="btn" :disabled="refreshing" @click="$emit('retry')">{{ refreshing ? 'Retrying…' : 'Try again' }}</button>
  </div>
</template>

<style scoped>
.query-feedback { flex-wrap: wrap; }
.query-feedback > span { flex: 1; min-width: min(240px, 100%); }
</style>
