<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type { Event as TindraEvent } from '@/api/types'
import IssueSection from './IssueSection.vue'
import { eventHttpRequest, formatRequestBody, unwrapHeaderVal } from './issueEvent'

const props = defineProps<{ event?: TindraEvent | null }>()

const BODY_PREVIEW_LINES = 15
const reqBodyExpanded = ref(false)
watch(() => props.event, () => { reqBodyExpanded.value = false })

const httpRequest = computed(() => eventHttpRequest(props.event))

const reqBodyFull = computed(() => formatRequestBody(httpRequest.value?.data))
const reqBodyLines = computed(() => reqBodyFull.value.split('\n'))
const reqBodyTruncated = computed(() => reqBodyLines.value.length > BODY_PREVIEW_LINES)
const reqBodyDisplay = computed(() =>
  reqBodyExpanded.value || !reqBodyTruncated.value
    ? reqBodyFull.value
    : reqBodyLines.value.slice(0, BODY_PREVIEW_LINES).join('\n')
)
</script>

<template>
  <IssueSection v-if="httpRequest" section-key="request" title="HTTP request">
    <template #head>
      <span v-if="httpRequest.method" class="section__badge section__badge--method">{{ httpRequest.method }}</span>
    </template>
    <div class="req-section">
      <div v-if="httpRequest.url" class="req-url">{{ httpRequest.url }}</div>

      <template v-if="httpRequest.headers && Object.keys(httpRequest.headers).length > 0">
        <div class="req-subtitle">Headers</div>
        <table class="ctx-table req-table">
          <tbody>
            <tr v-for="[k, v] in Object.entries(httpRequest.headers)" :key="k">
              <td class="ctx-table__key">{{ k }}</td>
              <td class="ctx-table__val mono">{{ unwrapHeaderVal(v) }}</td>
            </tr>
          </tbody>
        </table>
      </template>

      <template v-if="httpRequest.query_string">
        <div class="req-subtitle">Query String</div>
        <pre class="req-body">{{ typeof httpRequest.query_string === 'string' ? httpRequest.query_string : JSON.stringify(httpRequest.query_string, null, 2) }}</pre>
      </template>

      <template v-if="reqBodyFull">
        <div class="req-subtitle">
          Body
          <span class="req-subtitle__meta">{{ reqBodyLines.length }} lines</span>
        </div>
        <pre class="req-body">{{ reqBodyDisplay }}</pre>
        <button v-if="reqBodyTruncated" class="req-body__toggle" @click="reqBodyExpanded = !reqBodyExpanded">
          {{ reqBodyExpanded ? 'Show less' : `Show all ${reqBodyLines.length} lines` }}
        </button>
      </template>

      <template v-if="httpRequest.cookies && Object.keys(httpRequest.cookies).length > 0">
        <div class="req-subtitle">Cookies</div>
        <table class="ctx-table req-table">
          <tbody>
            <tr v-for="[k, v] in Object.entries(httpRequest.cookies)" :key="k">
              <td class="ctx-table__key">{{ k }}</td>
              <td class="ctx-table__val mono">{{ unwrapHeaderVal(v) }}</td>
            </tr>
          </tbody>
        </table>
      </template>
    </div>
  </IssueSection>
</template>
