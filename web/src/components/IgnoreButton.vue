<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import Icon from './Icon.vue'

export interface IgnorePayload {
  status: 'ignored'
  ignore_until?: string
  ignore_count_limit?: number
}

const props = defineProps<{
  disabled?: boolean
  direction?: 'up' | 'down'
}>()

const emit = defineEmits<{
  ignore: [payload: IgnorePayload]
}>()

const open = ref(false)
const showCustom = ref(false)
const customDate = ref('')
const el = ref<HTMLElement | null>(null)

const timeOptions = [
  { label: 'For 30 minutes', ms: 30 * 60 * 1000 },
  { label: 'For 2 hours', ms: 2 * 60 * 60 * 1000 },
  { label: 'For 6 hours', ms: 6 * 60 * 60 * 1000 },
  { label: 'For 1 day', ms: 24 * 60 * 60 * 1000 },
  { label: 'For 1 week', ms: 7 * 24 * 60 * 60 * 1000 },
]

const countOptions = [1, 10, 100, 1_000, 10_000]

function ignoreForever() {
  emit('ignore', { status: 'ignored' })
}

function ignoreUntil(ms: number) {
  const until = new Date(Date.now() + ms).toISOString()
  emit('ignore', { status: 'ignored', ignore_until: until })
  open.value = false
}

function ignoreCustomDate() {
  if (!customDate.value) return
  emit('ignore', { status: 'ignored', ignore_until: new Date(customDate.value).toISOString() })
  open.value = false
  showCustom.value = false
}

function ignoreCount(n: number) {
  emit('ignore', { status: 'ignored', ignore_count_limit: n })
  open.value = false
}

function onMouseDown(e: MouseEvent) {
  if (el.value && !el.value.contains(e.target as Node)) {
    open.value = false
    showCustom.value = false
  }
}

onMounted(() => document.addEventListener('mousedown', onMouseDown))
onUnmounted(() => document.removeEventListener('mousedown', onMouseDown))
</script>

<template>
  <div ref="el" class="ignore-btn" style="position: relative; display: inline-flex">
    <button
      class="btn ignore-btn__main"
      :disabled="disabled"
      @click="ignoreForever"
    >
      Ignore
    </button>
    <button
      class="btn ignore-btn__chevron"
      :disabled="disabled"
      :class="{ 'btn--active': open }"
      @click.stop="open = !open"
      title="Ignore with limit"
    >
      <Icon name="chevron-down" :size="11" />
    </button>

    <div v-if="open" class="popover ignore-btn__popover" :class="{ 'ignore-btn__popover--down': props.direction === 'down' }">
      <div class="popover__list">
        <div class="popover__group-label">Time limit</div>
        <div
          v-for="opt in timeOptions"
          :key="opt.ms"
          class="popover__item"
          @click="ignoreUntil(opt.ms)"
        >{{ opt.label }}</div>
        <div class="popover__item" @click="showCustom = !showCustom">
          Custom date&hellip;
        </div>
        <div v-if="showCustom" class="ignore-btn__custom">
          <input
            v-model="customDate"
            type="datetime-local"
            class="ignore-btn__date-input"
            @keydown.enter="ignoreCustomDate"
          />
          <button class="btn btn--primary ignore-btn__date-ok" @click="ignoreCustomDate">OK</button>
        </div>
        <div class="popover__separator" />
        <div class="popover__group-label">Until N more occurrences</div>
        <div
          v-for="n in countOptions"
          :key="n"
          class="popover__item"
          @click="ignoreCount(n)"
        >{{ n.toLocaleString() }} time{{ n === 1 ? '' : 's' }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ignore-btn {
  gap: 1px;
}

.ignore-btn__main {
  border-radius: 6px 0 0 6px;
}

.ignore-btn__chevron {
  border-radius: 0 6px 6px 0;
  padding: 0 6px;
}

.ignore-btn__popover {
  right: 0;
  left: auto;
  min-width: 220px;
  bottom: calc(100% + 4px);
  top: auto;
}

.ignore-btn__popover--down {
  bottom: auto;
  top: calc(100% + 4px);
}

.popover__group-label {
  padding: 6px 12px 2px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-3);
}

.popover__separator {
  height: 1px;
  background: var(--border);
  margin: 4px 0;
}

.ignore-btn__custom {
  display: flex;
  gap: 6px;
  padding: 4px 12px 8px;
}

.ignore-btn__date-input {
  flex: 1;
  font-size: 12px;
  padding: 4px 6px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface);
  color: var(--text-1);
  outline: none;
}

.ignore-btn__date-input:focus {
  border-color: var(--accent);
}

.ignore-btn__date-ok {
  padding: 4px 10px;
  font-size: 12px;
}
</style>
