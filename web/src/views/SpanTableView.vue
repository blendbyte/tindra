<script setup lang="ts">
import SpanTable, { type SpanTableKind } from '@/components/SpanTable.vue'

// The router reuses this view when moving between the span pages, so the key
// gives each kind a fresh table with its own queries.
defineProps<{ kind: SpanTableKind }>()
</script>

<template>
  <SpanTable :key="kind" :kind="kind" />
</template>
