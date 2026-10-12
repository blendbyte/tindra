<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Event as TindraEvent } from '@/api/types'
import Icon from '@/components/Icon.vue'
import CodeContext from '@/components/CodeContext.vue'
import { useClipboard } from '@/composables/useClipboard'

// The stack trace of the current event: always expanded, as the primary
// section, with a raw text view that can be copied.
const props = defineProps<{ event?: TindraEvent | null }>()

const { copy } = useClipboard()

const showSystem = ref(false)
const showRawStack = ref(false)
const copiedStack = ref(false)

async function copyStack() {
  if (!(await copy(rawStackText.value))) return
  copiedStack.value = true
  setTimeout(() => { copiedStack.value = false }, 2000)
}

const frames = computed(() => {
  const exc = props.event?.payload?.exception as { values?: { stacktrace?: { frames?: unknown[] } }[] } | undefined
  const fs = exc?.values?.[0]?.stacktrace?.frames ?? []
  return [...fs].reverse()
})

const eventPlatform = computed(() => (props.event?.payload as Record<string, unknown> | undefined)?.platform as string | undefined)

const rawStackText = computed(() => {
  const exc = props.event?.payload?.exception as { values?: { type?: string; value?: string; stacktrace?: { frames?: Record<string, unknown>[] } }[] } | undefined
  if (!exc?.values?.length) return ''
  return exc.values.map((ex) => {
    const header = [ex.type, ex.value].filter(Boolean).join(': ')
    const fs = ex.stacktrace?.frames ?? []
    const frameLines = [...fs].reverse().map((f) => {
      const loc = [f['filename'] ?? f['module'], f['lineno'] ? `line ${f['lineno']}` : null, f['function'] ? `in ${f['function']}` : null].filter(Boolean).join(', ')
      const code = f['context_line'] ? `\n    ${String(f['context_line']).trim()}` : ''
      return `  File "${loc}"${code}`
    })
    return [header, 'Traceback (most recent call last):', ...frameLines].join('\n')
  }).join('\n\n')
})
</script>

<template>
  <div v-if="frames.length > 0" class="section section--primary">
    <div class="section__head" style="cursor: default">
      <h2 class="section__title">Stack trace</h2>
      <div class="section__actions">
        <button class="btn btn--ghost" style="font-size: var(--text-xs)" @click="showRawStack = !showRawStack">
          {{ showRawStack ? 'Formatted' : 'Raw' }}
        </button>
        <button v-if="showRawStack" class="btn btn--ghost" style="font-size: var(--text-xs)" @click="copyStack">
          <Icon name="copy" :size="11" />
          {{ copiedStack ? 'Copied' : 'Copy' }}
        </button>
        <button v-if="!showRawStack" class="btn btn--ghost" style="font-size: var(--text-xs)" @click="showSystem = !showSystem">
          {{ showSystem ? 'Hide system frames' : 'Show system frames' }}
        </button>
      </div>
    </div>
    <pre v-if="showRawStack" class="stack__raw">{{ rawStackText }}</pre>
    <div v-else class="stack">
      <div
        v-for="(f, idx) in (frames as Record<string, unknown>[])"
        :key="idx"
        class="stack__frame"
        :class="[
          (f['in_app'] ?? true) ? 'stack__frame--app' : 'stack__frame--system',
          (!showSystem && !(f['in_app'] ?? true)) ? 'stack__frame--collapsed' : '',
        ]"
      >
        <span class="stack__file">
          {{ f['filename'] ?? f['module'] ?? 'unknown' }}
          <span class="stack__line">:{{ f['lineno'] }}</span>
        </span>
        <span class="stack__fn">{{ f['function'] }}</span>
        <CodeContext
          v-if="f['context_line'] != null && (showSystem || (f['in_app'] ?? true))"
          :pre-context="(f['pre_context'] as string[] | undefined) ?? []"
          :context-line="f['context_line'] as string"
          :post-context="(f['post_context'] as string[] | undefined) ?? []"
          :lineno="f['lineno'] as number"
          :platform="eventPlatform"
        />
      </div>
    </div>
  </div>
</template>
