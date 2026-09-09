<script setup lang="ts">
import { useInvestigationStore } from '@/stores/investigation'
import { investigationQuery } from '@/router/investigation'
const investigation = useInvestigationStore()
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
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
const menuButton = ref<HTMLButtonElement | null>(null)
const mobileDrawer = ref<HTMLElement | null>(null)

function closeMenu(restoreFocus = false) {
  menuOpen.value = false
  if (restoreFocus) menuButton.value?.focus()
}

function onKeyDown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    if (menuOpen.value) closeMenu(true)
    filterOpen.value = false
  }
}

function onResize() {
  if (window.innerWidth >= 1200) closeMenu()
}
watch(() => route.path, () => { menuOpen.value = false })
watch(menuOpen, async (open) => {
  if (open) {
    await nextTick()
    mobileDrawer.value?.querySelector<HTMLElement>('a')?.focus()
  }
})

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

const allSelected = computed(() => projects.selectedIds.length === 0)
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
  if (menuOpen.value && !mobileDrawer.value?.contains(e.target as Node) && !menuButton.value?.contains(e.target as Node)) closeMenu()
  if (filterEl.value && !filterEl.value.contains(e.target as Node)) {
    filterOpen.value = false
  }
}
onMounted(() => {
  document.addEventListener('mousedown', onMouseDown)
  document.addEventListener('keydown', onKeyDown)
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onMouseDown)
  document.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('resize', onResize)
})

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
        :to="{ path: '/dashboard', query: investigationQuery(investigation) }"
        class="nav__link"
        :aria-current="route.path === '/dashboard' ? 'page' : undefined"
      >
        <Icon name="squares" :size="14" />
        <span class="nav__link-text">Dashboard</span>
      </RouterLink>
      <RouterLink
        :to="{ path: '/issues', query: investigationQuery(investigation) }"
        class="nav__link"
        :aria-current="route.path.startsWith('/issues') ? 'page' : undefined"
      >
        <Icon name="alert-circle" :size="15" />
        <span class="nav__link-text">Issues</span>
      </RouterLink>
      <div class="nav__dropdown-wrap">
        <RouterLink
          :to="{ path: '/performance', query: investigationQuery(investigation) }"
          class="nav__link"
          :aria-current="route.path.startsWith('/performance') || route.path.startsWith('/transactions') ? 'page' : undefined"
        >
          <Icon name="activity" :size="15" />
          <span class="nav__link-text">Performance</span>
          <Icon name="chevron-down" :size="10" class="nav__link-caret" />
        </RouterLink>
        <div class="nav__dropdown">
          <RouterLink
            :to="{ path: '/performance/transactions', query: investigationQuery(investigation) }"
            class="nav__dropdown-item"
            :class="{ 'nav__dropdown-item--active': route.path.startsWith('/performance/transactions') || route.path.startsWith('/transactions') }"
          >Transactions</RouterLink>
          <RouterLink
            :to="{ path: '/performance/queries', query: investigationQuery(investigation) }"
            class="nav__dropdown-item"
            :class="{ 'nav__dropdown-item--active': route.path.startsWith('/performance/queries') }"
          >Queries</RouterLink>
          <RouterLink
            :to="{ path: '/performance/caches', query: investigationQuery(investigation) }"
            class="nav__dropdown-item"
            :class="{ 'nav__dropdown-item--active': route.path.startsWith('/performance/caches') }"
          >Caches</RouterLink>
          <RouterLink
            :to="{ path: '/performance/jobs', query: investigationQuery(investigation) }"
            class="nav__dropdown-item"
            :class="{ 'nav__dropdown-item--active': route.path.startsWith('/performance/jobs') }"
          >Jobs</RouterLink>
          <RouterLink
            :to="{ path: '/performance/browser', query: investigationQuery(investigation) }"
            class="nav__dropdown-item"
            :class="{ 'nav__dropdown-item--active': route.path.startsWith('/performance/browser') }"
          >Browser</RouterLink>
        </div>
      </div>
      <RouterLink
        :to="{ path: '/logs', query: investigationQuery(investigation) }"
        class="nav__link"
        :aria-current="route.path.startsWith('/logs') ? 'page' : undefined"
      >
        <Icon name="file-text" :size="13" />
        Logs
      </RouterLink>
      <RouterLink
        to="/alerts"
        class="nav__link"
        :aria-current="route.path.startsWith('/alerts') ? 'page' : undefined"
      >
        <Icon name="bell" :size="14" />
        <span class="nav__link-text">Alerts</span>
      </RouterLink>
      <div class="nav__dropdown-wrap">
        <RouterLink
          :to="{ path: '/monitors', query: investigationQuery(investigation) }"
          class="nav__link"
          :aria-current="route.path.startsWith('/monitors') ? 'page' : undefined"
        >
          <Icon name="clock" :size="15" />
          <span class="nav__link-text">Monitors</span>
          <Icon name="chevron-down" :size="10" class="nav__link-caret" />
        </RouterLink>
        <div class="nav__dropdown">
          <RouterLink
            :to="{ path: '/monitors/cron', query: investigationQuery(investigation) }"
            class="nav__dropdown-item"
            :class="{ 'nav__dropdown-item--active': route.path.startsWith('/monitors/cron') }"
          >Cron</RouterLink>
          <RouterLink
            :to="{ path: '/monitors/uptime', query: investigationQuery(investigation) }"
            class="nav__dropdown-item"
            :class="{ 'nav__dropdown-item--active': route.path.startsWith('/monitors/uptime') }"
          >Uptime</RouterLink>
        </div>
      </div>
      <RouterLink
        :to="{ path: '/releases', query: investigationQuery(investigation) }"
        class="nav__link"
        :aria-current="route.path.startsWith('/releases') ? 'page' : undefined"
      >
        <Icon name="package" :size="15" />
        <span class="nav__link-text">Releases</span>
      </RouterLink>
    </div>

    <div class="nav__spacer" />

    <div class="nav__right">
      <button ref="menuButton" class="nav__hamburger" :aria-expanded="menuOpen" aria-controls="mobile-navigation" aria-label="Toggle navigation" @click="menuOpen = !menuOpen; filterOpen = false">
        <Icon :name="menuOpen ? 'x' : 'menu'" :size="18" />
      </button>
      <!-- Project filter -->
      <div ref="filterEl" class="nav__projects">
        <button
          class="nav__projects-trigger"
          :aria-label="`Filter projects: ${filterLabel}`"
          :title="filterLabel"
          :aria-haspopup="true"
          :aria-expanded="filterOpen"
          @click="filterOpen = !filterOpen; menuOpen = false"
        >
          <span class="nav__projects-label">{{ filterLabel }}</span>
          <span v-if="!allSelected" class="nav__projects-count">
            {{ projects.selectedIds.length }}
          </span>
          <Icon name="chevron-down" :size="12" />
        </button>

        <div v-if="filterOpen" class="popover">
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
        class="nav__icon-btn nav__secondary-action"
        :title="ui.resolvedTheme === 'light' ? 'Switch to dark' : 'Switch to light'"
        aria-label="Toggle theme"
        @click="ui.toggleTheme()"
      >
        <Icon :name="ui.resolvedTheme === 'light' ? 'moon' : 'sun'" :size="14" />
      </button>

      <!-- Settings -->
      <div class="nav__dropdown-wrap">
        <button
          class="nav__icon-btn"
          :aria-current="route.path.startsWith('/settings') ? 'page' : undefined"
          title="Settings"
          @click="router.push('/settings')"
        >
          <Icon name="cog" :size="14" />
        </button>
        <div class="nav__dropdown nav__dropdown--right">
          <RouterLink to="/settings/overview" class="nav__dropdown-item" :class="{ 'nav__dropdown-item--active': route.path === '/settings/overview' || route.path === '/settings' }">Overview</RouterLink>
          <RouterLink to="/settings/projects" class="nav__dropdown-item" :class="{ 'nav__dropdown-item--active': route.path === '/settings/projects' }">Projects</RouterLink>
          <RouterLink to="/settings/users" class="nav__dropdown-item" :class="{ 'nav__dropdown-item--active': route.path === '/settings/users' }">Users</RouterLink>
          <RouterLink to="/settings/audit" class="nav__dropdown-item" :class="{ 'nav__dropdown-item--active': route.path === '/settings/audit' }">Audit</RouterLink>
          <RouterLink to="/settings/tokens" class="nav__dropdown-item" :class="{ 'nav__dropdown-item--active': route.path === '/settings/tokens' }">Tokens</RouterLink>
          <div class="nav__dropdown-divider"></div>
          <RouterLink to="/settings/profile" class="nav__dropdown-item" :class="{ 'nav__dropdown-item--active': route.path === '/settings/profile' }">Profile</RouterLink>
        </div>
      </div>

      <!-- Logout -->
      <button
        class="nav__icon-btn nav__secondary-action"
        title="Log out"
        @click="logout"
      >
        <Icon name="log-out" :size="14" />
      </button>
    </div>
  </nav>

  <!-- Mobile nav drawer — teleported to body to escape nav's stacking context -->
  <Teleport to="body">
    <div v-if="menuOpen" id="mobile-navigation" ref="mobileDrawer" class="nav__mobile-drawer" role="navigation" aria-label="Main navigation" @click.self="menuOpen = false">
      <RouterLink :to="{ path: '/dashboard', query: investigationQuery(investigation) }" class="nav__mobile-link" :aria-current="route.path === '/dashboard' ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="squares" :size="15" />Dashboard
      </RouterLink>
      <RouterLink :to="{ path: '/issues', query: investigationQuery(investigation) }" class="nav__mobile-link" :aria-current="route.path.startsWith('/issues') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="alert-circle" :size="15" />Issues
      </RouterLink>
      <RouterLink :to="{ path: '/performance', query: investigationQuery(investigation) }" class="nav__mobile-link" :aria-current="route.path.startsWith('/performance') || route.path.startsWith('/transactions') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="activity" :size="15" />Performance
      </RouterLink>
      <RouterLink :to="{ path: '/logs', query: investigationQuery(investigation) }" class="nav__mobile-link" :aria-current="route.path.startsWith('/logs') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="file-text" :size="14" />Logs
      </RouterLink>
      <RouterLink to="/alerts" class="nav__mobile-link" :aria-current="route.path.startsWith('/alerts') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="bell" :size="15" />Alerts
      </RouterLink>
      <RouterLink :to="{ path: '/monitors', query: investigationQuery(investigation) }" class="nav__mobile-link" :aria-current="route.path.startsWith('/monitors') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="clock" :size="15" />Monitors
      </RouterLink>
      <RouterLink :to="{ path: '/releases', query: investigationQuery(investigation) }" class="nav__mobile-link" :aria-current="route.path.startsWith('/releases') ? 'page' : undefined" @click="menuOpen = false">
        <Icon name="package" :size="15" />Releases
      </RouterLink>
      <button class="nav__mobile-link nav__mobile-utility" @click="ui.toggleTheme()">
        <Icon :name="ui.resolvedTheme === 'light' ? 'moon' : 'sun'" :size="15" />
        {{ ui.resolvedTheme === 'light' ? 'Switch to dark theme' : 'Switch to light theme' }}
      </button>
      <button class="nav__mobile-link nav__mobile-utility" @click="logout">
        <Icon name="log-out" :size="15" />Log out
      </button>
    </div>
  </Teleport>
</template>
