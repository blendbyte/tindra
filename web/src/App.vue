<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import Navbar from '@/components/Navbar.vue'
import InvestigationBar from '@/components/InvestigationBar.vue'
import { hasInvestigation, isTelemetry } from '@/router/investigation'
import { useInvestigationStore } from '@/stores/investigation'
import QuotaBanner from '@/components/QuotaBanner.vue'
import CommandPalette from '@/components/CommandPalette.vue'
import ShortcutsModal from '@/components/ShortcutsModal.vue'
import ToastStack from '@/components/ToastStack.vue'

const route = useRoute()
const ui = useUiStore()
const auth = useAuthStore()
const investigation = useInvestigationStore()
const unsupported = computed(() => isTelemetry(route.path) && route.path !== '/issues' && (investigation.absolute ? Date.parse(investigation.absolute.to) - Date.parse(investigation.absolute.from) > 30 * 86400000 : investigation.range === 'All' || investigation.range === '90d'))

const isLogin = computed(() =>
  route.name === 'login' || route.name === 'accept-invite' || route.name === 'reset-password'
)

onMounted(() => {
  document.documentElement.setAttribute('data-theme', ui.resolvedTheme)
})

watch(
  () => ui.resolvedTheme,
  (t) => document.documentElement.setAttribute('data-theme', t),
)
</script>

<template>
  <div v-if="auth.ready && (isLogin || !!auth.user)" class="app">
    <Navbar v-if="!isLogin" />
    <QuotaBanner v-if="!isLogin" />
    <InvestigationBar v-if="!isLogin && hasInvestigation(route.path)" />
    <p v-if="hasInvestigation(route.path) && investigation.routeError" class="page" role="alert">{{ investigation.routeError }}</p>
    <p v-else-if="unsupported" class="page" role="status">This view supports time ranges up to 30 days. Choose a supported time range above to load its data.</p>
    <RouterView v-else />
    <CommandPalette v-if="!isLogin" />
    <ShortcutsModal v-if="!isLogin" />
    <ToastStack />
  </div>
</template>
