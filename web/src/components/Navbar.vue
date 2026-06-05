<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUiStore } from '@/stores/ui'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import Icon from './Icon.vue'
import logoLight from '@/assets/logo.png'
import logoDark from '@/assets/logo-dark.png'

const route = useRoute()
const router = useRouter()
const ui = useUiStore()
const projects = useProjectsStore()

// Mobile menu
const menuOpen = ref(false)
watch(() => route.path, () => { menuOpen.value = false })

// Project filter popover
const filterOpen = ref(false)
const filterEl = ref<HTMLElement | null>(null)
const filterSearch = ref('')

const filteredProjects = computed(() => {
  const q = filterSearch.value.trim().toLowerCase()
  if (!q) return projects.projects
  return projects.projects.filter(
    (p) => p.name.toLowerCase().includes(q) || p.slug.toLowerCase().includes(q),
  )
})

const allSelected = computed(
  () =>
    projects.selectedIds.length === 0 ||
    projects.selectedIds.length === projects.projects.length,
)

const noProjects = computed(() => projects.projects.length === 0)

const filterLabel = computed(() => {
  if (noProjects.value) return 'No projects'
  if (allSelected.value) return 'All projects'
  if (projects.selectedIds.length === 1) {
    return projects.projects.find((p) => p.id === projects.selectedIds[0])?.name ?? '1 project'
  }
  return `${projects.selectedIds.length} projects`
})

function onMouseDown(e: MouseEvent) {
  if (filterEl.value && !filterEl.value.contains(e.target as Node)) {
    filterOpen.value = false
  }
}
onMounted(() => document.addEventListener('mousedown', onMouseDown))
onUnmounted(() => document.removeEventListener('mousedown', onMouseDown))

function toggleProject(id: string) {
  projects.toggleProject(id)
}

async function logout() {
  await apiFetch('/api/auth/logout', { method: 'POST' }).catch(() => {})
  window.location.href = '/login'
}
</script>

<template>
  <nav class="nav" role="navigation">
    <a class="nav__brand" href="#" @click.prevent="router.push('/dashboard')" title="Tindra">
      <img :src="logoLight" alt="Tindra" class="nav__logo nav__logo--light" />
      <img :src="logoDark" alt="Tindra" class="nav__logo nav__logo--dark" />
    </a>

    <div class="nav__links">
      <RouterLink
        to="/dashboard"
        class="nav__link"
        :aria-current="route.path === '/dashboard' ? 'page' : undefined"
      >
        <Icon name="squares" :size="14" />
        <span class="nav__link-text">Dashboard</span>
      </RouterLink>
      <RouterLink
        to="/issues"
        class="nav__link"
        :aria-current="route.path.startsWith('/issues') ? 'page' : undefined"
      >
        <Icon name="alert-circle" :size="15" />
        <span class="nav__link-text">Issues</span>
      </RouterLink>
      <RouterLink
        to="/performance"
        class="nav__link"
        :aria-current="route.path.startsWith('/performance') || route.path.startsWith('/transactions') ? 'page' : undefined"
      >
        <Icon name="activity" :size="15" />
        <span class="nav__link-text">Performance</span>
      </RouterLink>
      <RouterLink
        to="/logs"
        class="nav__link"
        :aria-current="route.path.startsWith('/logs') ? 'page' : undefined"
      >
        <Icon name="file-text" :size="13" />
        Logs
      </RouterLink>
      <RouterLink
        to="/monitors"
        class="nav__link"
        :aria-current="route.path.startsWith('/monitors') ? 'page' : undefined"
      >
        <Icon name="clock" :size="15" />
        <span class="nav__link-text">Monitors</span>
      </RouterLink>
      <RouterLink
        to="/releases"
        class="nav__link"
        :aria-current="route.path.startsWith('/releases') ? 'page' : undefined"
      >
        <Icon name="package" :size="15" />
        <span class="nav__link-text">Releases</span>
      </RouterLink>
    </div>

    <div class="nav__spacer" />

    <div class="nav__right">
      <button class="nav__hamburger" :aria-expanded="menuOpen" aria-label="Toggle navigation" @click="menuOpen = !menuOpen">
        <Icon :name="menuOpen ? 'x' : 'menu'" :size="18" />
      </button>
      <!-- Project filter -->
      <div ref="filterEl" class="nav__projects">
        <button
          class="nav__projects-trigger"
          :aria-haspopup="true"
          :aria-expanded="filterOpen"
          @click="filterOpen = !filterOpen"
        >
          <span class="nav__projects-label">{{ filterLabel }}</span>
          <span v-if="!allSelected" class="nav__projects-count">
            {{ projects.selectedIds.length }}
          </span>
          <Icon name="chevron-down" :size="12" />
        </button>

        <div v-if="filterOpen" class="popover" style="min-width: 300px">
          <!-- Empty state -->
          <template v-if="noProjects">
            <div class="popover-empty">
              <Icon name="package" :size="20" style="color: var(--text-3); margin-bottom: 8px" />
              <div class="popover-empty__title">No projects yet</div>
              <div class="popover-empty__sub">Create a project to get your DSN and start capturing errors.</div>
              <button
                class="btn btn--primary"
                style="margin-top: 12px; width: 100%"
                @click="filterOpen = false; router.push('/settings/projects?new=1')"
              >
                <Icon name="plus" :size="12" />
                Create project
              </button>
            </div>
          </template>

          <!-- Normal project list -->
          <template v-else>
            <div class="popover__search">
              <input
                v-model="filterSearch"
                placeholder="Search projects…"
                aria-label="Search projects"
              />
            </div>
            <div class="popover__list">
              <div
                v-for="p in filteredProjects"
                :key="p.id"
                class="popover__item"
                @click="toggleProject(p.id)"
              >
                <span
                  class="popover__check"
                  :class="{ 'popover__check--on': projects.selectedIds.includes(p.id) }"
                >
                  <Icon v-if="projects.selectedIds.includes(p.id)" name="check" :size="10" />
                </span>
                <span>{{ p.name }}</span>
                <span class="popover__meta">{{ p.slug }}</span>
              </div>
            </div>
            <div class="popover__footer">
              <button @click="projects.setSelected([])">All projects</button>
              <button @click="filterOpen = false">Done</button>
            </div>
          </template>
        </div>
      </div>

      <!-- Command palette trigger -->
      <button class="nav__btn" title="Open command palette" @click="ui.openCmd()">
        <Icon name="search" :size="12" />
        <span class="nav__btn-text">Search</span>
        <span class="nav__kbd">⌘K</span>
      </button>

      <!-- Theme toggle -->
      <button
        class="nav__icon-btn"
        :title="ui.resolvedTheme === 'light' ? 'Switch to dark' : 'Switch to light'"
        aria-label="Toggle theme"
        @click="ui.toggleTheme()"
      >
        <Icon :name="ui.resolvedTheme === 'light' ? 'moon' : 'sun'" :size="14" />
      </button>

      <!-- Settings -->
      <button
        class="nav__icon-btn"
        :aria-current="route.path.startsWith('/settings') ? 'page' : undefined"
        title="Settings"
        @click="router.push('/settings')"
      >
        <Icon name="cog" :size="14" />
      </button>

      <!-- Logout -->
      <button
        class="nav__icon-btn"
        title="Log out"
        @click="logout"
      >
        <Icon name="log-out" :size="14" />
      </button>
    </div>
  </nav>

  <!-- Mobile nav drawer — teleported to body to escape nav's stacking context -->
  <Teleport to="body">
    <div v-if="menuOpen" class="nav__mobile-drawer" @click.self="menuOpen = false">
      <RouterLink to="/dashboard" class="nav__mobile-link" :aria-current="route.path === '/dashboard' ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="squares" :size="15" />Dashboard
      </RouterLink>
      <RouterLink to="/issues" class="nav__mobile-link" :aria-current="route.path.startsWith('/issues') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="alert-circle" :size="15" />Issues
      </RouterLink>
      <RouterLink to="/performance" class="nav__mobile-link" :aria-current="route.path.startsWith('/performance') || route.path.startsWith('/transactions') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="activity" :size="15" />Performance
      </RouterLink>
      <RouterLink to="/logs" class="nav__mobile-link" :aria-current="route.path.startsWith('/logs') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="file-text" :size="14" />Logs
      </RouterLink>
      <RouterLink to="/monitors" class="nav__mobile-link" :aria-current="route.path.startsWith('/monitors') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="clock" :size="15" />Monitors
      </RouterLink>
      <RouterLink to="/releases" class="nav__mobile-link" :aria-current="route.path.startsWith('/releases') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="package" :size="15" />Releases
      </RouterLink>
    </div>
  </Teleport>
</template>
