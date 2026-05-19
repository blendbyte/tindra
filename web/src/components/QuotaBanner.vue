<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Project, ServerSettings } from '@/api/types'
import Icon from '@/components/Icon.vue'

const route = useRoute()
const router = useRouter()

const { data: settings } = useQuery({
  queryKey: ['settings'],
  queryFn: () => apiFetch<ServerSettings>('/api/settings'),
})

const { data: projectsData } = useQuery({
  queryKey: ['projects'],
  queryFn: () => apiFetch<Project[]>('/api/projects'),
})

const projects = computed(() => projectsData.value ?? [])

const limit = computed(() => settings.value?.event_limit ?? 0)

const totalEvents = computed(() =>
  projects.value.reduce((sum, p) => sum + p.event_count, 0),
)

const pct = computed(() =>
  limit.value > 0 ? totalEvents.value / limit.value : 0,
)

const isOver = computed(() => limit.value > 0 && pct.value >= 1)
const isNear = computed(() => limit.value > 0 && pct.value >= 0.8 && pct.value < 1)

// Separate dismissed state per severity so crossing from warn → over re-shows the banner.
const dismissedOver = ref(false)
const dismissedNear = ref(false)

const show = computed(() => {
  if (route.name === 'settings') return false
  if (isOver.value) return !dismissedOver.value
  if (isNear.value) return !dismissedNear.value
  return false
})

function dismiss() {
  if (isOver.value) dismissedOver.value = true
  else dismissedNear.value = true
}

function goToSettings() {
  router.push({ name: 'settings', params: { tab: 'projects' } })
}
</script>

<template>
  <div
    v-if="show"
    class="quota-banner"
    :class="isOver ? 'quota-banner--over' : 'quota-banner--warn'"
    role="alert"
  >
    <Icon :name="isOver ? 'alert-circle' : 'alert-triangle'" :size="13" class="quota-banner__icon" />
    <span class="quota-banner__msg">
      <template v-if="isOver">
        Monthly event limit reached. New events are being dropped.
      </template>
      <template v-else>
        {{ Math.round(pct * 100) }}% of your monthly event limit used.
      </template>
    </span>
    <button class="quota-banner__cta" @click="goToSettings">View usage</button>
    <button class="quota-banner__dismiss" :aria-label="'Dismiss'" @click="dismiss">
      <Icon name="x" :size="12" />
    </button>
  </div>
</template>
