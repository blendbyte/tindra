<script setup lang="ts">
import { ref, computed, watch, watchEffect } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import Icon from '@/components/Icon.vue'
import { vActiveInView } from '@/directives/activeInView'
import { useUiStore } from '@/stores/ui'
import OverviewTab from './settings/OverviewTab.vue'
import TokensTab from './settings/TokensTab.vue'
import ProjectsTab from './settings/ProjectsTab.vue'
import UsersTab from './settings/UsersTab.vue'
import AlertsTab from './settings/AlertsTab.vue'
import AuditTab from './settings/AuditTab.vue'
import ProfileTab from './settings/ProfileTab.vue'
import { usePermissions, type SettingsTab as Tab } from './settings/shared'

// Share the existing rule editor between the dedicated Alerts route and settings.
const props = defineProps<{ alertsPage?: boolean }>()

const ui = useUiStore()
const router = useRouter()
const route = useRoute()

const ALL_TABS: Tab[] = ['overview', 'projects', 'alerts', 'users', 'audit', 'tokens', 'profile']

function resolveTab(param: unknown): Tab {
  const p = param === 'team' ? 'users' : param // backward compat with old URL
  return ALL_TABS.includes(p as Tab) ? (p as Tab) : 'overview'
}

const tab = ref<Tab>(props.alertsPage ? 'alerts' : resolveTab(route.params.tab))

watch(() => [route.params.tab, props.alertsPage] as const, ([t, alertsPage]) => {
  tab.value = alertsPage ? 'alerts' : resolveTab(t)
})

function tabLabel(t: Tab): string {
  return t.charAt(0).toUpperCase() + t.slice(1)
}

function setTab(t: Tab) {
  tab.value = t
  router.replace({ name: 'settings', params: { tab: t } })
}

const { me, canManageProjects, canManageUsers } = usePermissions()

// Redirect away from gated tabs if the user loses / never had the required permission.
watchEffect(() => {
  if (tab.value === 'tokens' && me.value && !me.value.permissions.manage_projects) {
    setTab('projects')
  }
  if (tab.value === 'overview' && me.value && !me.value.permissions.manage_projects) {
    setTab('projects')
  }
  if (tab.value === 'audit' && me.value && !me.value.permissions.manage_users) {
    setTab('projects')
  }
  if (tab.value === 'alerts' && me.value && !me.value.permissions.manage_alerts) {
    setTab('projects')
  }
})

// Alerts live on their own page, so the settings nav never lists them.
const visibleTabs = computed(() => ALL_TABS.filter((t) => {
  if (t === 'tokens' || t === 'overview') return canManageProjects.value
  if (t === 'audit') return canManageUsers.value
  if (t === 'alerts') return false
  return true
}))

// Clicking Profile again resets the profile form to the saved values.
const profileTab = ref<InstanceType<typeof ProfileTab> | null>(null)

function selectTab(t: Tab) {
  setTab(t)
  if (t === 'profile') profileTab.value?.initProfile()
}
</script>

<template>
  <div class="page">
    <div class="settings" :class="{ 'alerts-page': props.alertsPage }">
      <div class="settings__header">
        <h1>{{ props.alertsPage ? 'Alerts' : 'Settings' }}</h1>
        <div v-if="!props.alertsPage" class="settings__about">
          <a href="https://tindra.sh" target="_blank" rel="noopener" class="settings__about-link">
            <Icon name="globe" :size="11" />
            tindra.sh
          </a>
          <a href="https://tindra.sh/docs" target="_blank" rel="noopener" class="settings__about-link">
            <Icon name="book-open" :size="11" />
            Docs
          </a>
          <a href="https://github.com/blendbyte/tindra" target="_blank" rel="noopener" class="settings__about-link">
            <Icon name="github" :size="11" />
            GitHub
          </a>
          <button class="settings__about-link" @click="ui.shortcutsOpen = true">
            <Icon name="key-round" :size="11" />
            Shortcuts <kbd class="nav__kbd" style="font-size: 10px; padding: 1px 4px">?</kbd>
          </button>
        </div>
      </div>

      <div v-if="!props.alertsPage" v-active-in-view="'[aria-current=page]'" class="settings__nav">
        <button
          v-for="t in visibleTabs"
          :key="t"
          :aria-current="tab === t ? 'page' : undefined"
          @click="selectTab(t)"
        >
          {{ tabLabel(t) }}
        </button>
      </div>

      <OverviewTab v-if="tab === 'overview'" />
      <TokensTab v-else-if="tab === 'tokens'" />
      <ProjectsTab v-else-if="tab === 'projects'" />
      <UsersTab v-else-if="tab === 'users'" />
      <AlertsTab v-else-if="tab === 'alerts'" />
      <AuditTab v-else-if="tab === 'audit'" />
      <ProfileTab v-else-if="tab === 'profile'" ref="profileTab" />
    </div>
  </div>
</template>
