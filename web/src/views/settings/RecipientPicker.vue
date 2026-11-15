<script setup lang="ts">
import { computed } from 'vue'
import type { User } from '@/api/types'

// Picks the people a push rule notifies. Selected IDs the user list does not
// contain (still loading, or the list failed) get a checkbox too, so they
// are never sent back on save without a way to see or clear them.
const props = defineProps<{ users: User[] }>()
const selected = defineModel<string[]>({ required: true })

const options = computed(() => {
  const known = props.users.map(u => ({ id: u.id, name: u.name || u.email }))
  const missing = selected.value
    .filter(id => !props.users.some(u => u.id === id))
    .map(id => ({ id, name: 'Unknown user' }))
  return [...known, ...missing]
})
</script>

<template>
  <div class="field" style="grid-column: 1 / -1">
    <label class="field__label">Notify <span style="font-weight: normal; color: var(--text-2)">(on every device they turned push on for, in Profile)</span></label>
    <div class="rule__project-checks">
      <label v-for="o in options" :key="o.id" class="rule__project-check">
        <input type="checkbox" :value="o.id" v-model="selected" />
        {{ o.name }}
      </label>
    </div>
  </div>
</template>
