<script setup lang="ts">
import { useRoute } from 'vue-router'
const route = useRoute()
</script>

<template>
  <div class="perf-subnav">
    <RouterLink
      to="/performance/transactions"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/transactions' || route.path === '/transactions' }"
    >Transactions</RouterLink>
    <RouterLink
      to="/performance/queries"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/queries' }"
    >Queries</RouterLink>
    <RouterLink
      to="/performance/caches"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/caches' }"
    >Caches</RouterLink>
    <RouterLink
      to="/performance/jobs"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/jobs' }"
    >Jobs</RouterLink>
    <RouterLink
      to="/performance/browser"
      class="perf-subnav__link"
      :class="{ 'perf-subnav__link--active': route.path === '/performance/browser' }"
    >Browser</RouterLink>
  </div>
</template>
