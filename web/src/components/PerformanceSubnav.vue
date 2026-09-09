<script setup lang="ts">
import { useInvestigationStore } from '@/stores/investigation'
import { investigationQuery } from '@/router/investigation'
const investigation = useInvestigationStore()
import { useRoute } from 'vue-router'
const route = useRoute()
</script>

<template>
  <div class="perf-subnav">
    <RouterLink
      :to="{ path: '/performance/transactions', query: investigationQuery(investigation) }"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/transactions' || route.path === '/transactions' }"
    >Transactions</RouterLink>
    <RouterLink
      :to="{ path: '/performance/queries', query: investigationQuery(investigation) }"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/queries' }"
    >Queries</RouterLink>
    <RouterLink
      :to="{ path: '/performance/caches', query: investigationQuery(investigation) }"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/caches' }"
    >Caches</RouterLink>
    <RouterLink
      :to="{ path: '/performance/jobs', query: investigationQuery(investigation) }"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/jobs' }"
    >Jobs</RouterLink>
    <RouterLink
      :to="{ path: '/performance/browser', query: investigationQuery(investigation) }"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/browser' }"
    >Browser</RouterLink>
  </div>
</template>
