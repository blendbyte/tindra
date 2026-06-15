<script setup lang="ts">
import { computed, shallowRef, watchEffect } from 'vue'
import { highlightBlock, langForPlatform } from '@/composables/useShiki'
import type { ThemedToken } from 'shiki'

const props = defineProps<{
  preContext: string[]
  contextLine: string
  postContext: string[]
  lineno: number
  platform?: string
}>()

const tokenLines = shallowRef<ThemedToken[][]>([])
const ready = shallowRef(false)

const allLines = computed(() => [...props.preContext, props.contextLine, ...props.postContext])

watchEffect((onCleanup) => {
  let cancelled = false
  onCleanup(() => { cancelled = true })
  ready.value = false
  const lang = langForPlatform(props.platform)
  highlightBlock(allLines.value.join('\n'), lang).then(tokens => {
    if (!cancelled) { tokenLines.value = tokens; ready.value = true }
  })
})

function lineNo(idx: number): number {
  return props.lineno - props.preContext.length + idx
}
</script>

<template>
  <div class="stack__source">
    <template v-if="ready">
      <div
        v-for="(tokens, idx) in tokenLines"
        :key="idx"
        class="stack__source-line"
        :class="{ 'stack__source-line--hi': idx === preContext.length }"
      >
        <span class="stack__source-ln">{{ lineNo(idx) }}</span>
        <span class="stack__source-code"><span
          v-for="(tok, ti) in tokens"
          :key="ti"
          :style="tok.htmlStyle as Record<string, string>"
        >{{ tok.content }}</span></span>
      </div>
    </template>
    <template v-else>
      <div
        v-for="(line, idx) in allLines"
        :key="idx"
        class="stack__source-line"
        :class="{ 'stack__source-line--hi': idx === preContext.length }"
      >
        <span class="stack__source-ln">{{ lineNo(idx) }}</span>
        <span class="stack__source-code">{{ line || ' ' }}</span>
      </div>
    </template>
  </div>
</template>
