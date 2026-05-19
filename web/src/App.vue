<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import Navbar from '@/components/Navbar.vue'
import QuotaBanner from '@/components/QuotaBanner.vue'
import CommandPalette from '@/components/CommandPalette.vue'
import ShortcutsModal from '@/components/ShortcutsModal.vue'
import ToastStack from '@/components/ToastStack.vue'

const route = useRoute()
const ui = useUiStore()
const auth = useAuthStore()

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
    <RouterView />
    <CommandPalette v-if="!isLogin" />
    <ShortcutsModal v-if="!isLogin" />
    <ToastStack />
  </div>
</template>
