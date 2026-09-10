<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { ApiError, apiFetch } from '@/api/client'
import type { ProjectMetadata } from '@/api/types'
import type { SetupCheck, SetupKind, SetupStatus, SourceMapVerification } from '@/api/setup'
import { useConfig } from '@/composables/useConfig'
import { useToast } from '@/composables/useToast'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/Icon.vue'

const props = defineProps<{ project: ProjectMetadata; focus?: string }>()
const emit = defineEmits<{ continue: [] }>()
const { dsnFor } = useConfig()
const toast = useToast()
const auth = useAuthStore()
const id = useId()
const sdk = ref('node')
const checkId = ref('')
const starting = ref(false)
const actionError = ref('')
const now = ref(Date.now())
const sourceEvent = ref('')
const verification = ref<SourceMapVerification | null>(null)
const verifying = ref(false)
const verifiedEvent = ref('')
const sourceError = ref('')
const uploadRelease = ref('')
const uploadURL = ref('')
const uploadFile = ref<File | null>(null)
const uploading = ref(false)
const endpoint = computed(() => `/api/projects/${encodeURIComponent(props.project.slug)}`)
const dsn = computed(() => dsnFor(props.project.public_key, props.project.id))
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { timer = setInterval(() => { now.value = Date.now() }, 1000) })
onUnmounted(() => clearInterval(timer))

watch(() => props.project.id, () => {
  try { checkId.value = sessionStorage.getItem(`tindra:setup:${props.project.id}`) ?? '' } catch { checkId.value = '' }
  sourceEvent.value = ''
  verification.value = null
  sourceError.value = ''
  actionError.value = ''
}, { immediate: true })

const { data, error, isPending, isFetching, refetch } = useQuery({
  queryKey: computed(() => ['project-setup', props.project.id, checkId.value]),
  queryFn: ({ signal }) => apiFetch<SetupStatus>(`${endpoint.value}/setup-status${checkId.value ? `?check_id=${checkId.value}` : ''}`, { signal }),
  retry: false,
  refetchInterval: (query) => {
    if (query.state.error) return false
    const check = query.state.data?.check
    if (check && !check.received_at && Date.now() < Date.parse(check.expires_at)) {
      return Date.now() - Date.parse(check.created_at) < 60_000 ? 2500 : 15_000
    }
    return 30_000
  },
  refetchIntervalInBackground: false,
})
const receipt = (kind: SetupKind) => data.value?.receipts.find(r => r.kind === kind)
const connected = computed(() => !!receipt('events') || !!receipt('transactions'))
const confirmed = computed(() => !!data.value?.check?.received_at)
const expired = computed(() => !!data.value?.check && !confirmed.value && now.value >= Date.parse(data.value.check.expires_at))
const waitingLong = computed(() => !!data.value?.check && !confirmed.value && now.value - Date.parse(data.value.check.created_at) >= 60_000)
const headline = computed(() => error.value ? 'Setup status unavailable' : expired.value ? 'Setup check expired' : confirmed.value ? 'Test event received' : checkId.value ? 'Waiting for your test event' : connected.value ? 'Your app is connected' : 'Waiting for your first event')
const testExample = computed(() => data.value?.examples.test)
const eventExample = computed(() => testExample.value ?? data.value?.examples.events)
const eventLink = computed(() => eventExample.value?.issue_id ? `/issues/${eventExample.value.issue_id}?event_id=${eventExample.value.id}` : '')
const time = (value: string) => new Date(value).toLocaleString()

const snippet = computed(() => {
  const pkg = sdk.value === 'browser' ? '@sentry/browser' : '@sentry/node'
  const marker = checkId.value || 'START_A_SETUP_CHECK'
  return `import * as Sentry from ${JSON.stringify(pkg)}\n\nSentry.init({\n  dsn: ${JSON.stringify(dsn.value)}\n})\n\nSentry.withScope((scope) => {\n  scope.setTag("tindra_setup", ${JSON.stringify(marker)})\n  Sentry.captureException(new Error("Hello, Tindra!"))\n})${sdk.value === 'node' ? '\n\nawait Sentry.flush(5000)' : ''}`
})

async function copy(value: string, label: string) {
  try { await navigator.clipboard.writeText(value); toast.show(`${label} copied`) }
  catch { toast.show('Could not copy. Select the text and copy it manually.', 'error') }
}
function finish() {
  try { sessionStorage.setItem(`tindra:setup-dismissed:${props.project.id}`, '1') } catch { /* Optional tab preference. */ }
  emit('continue')
}

async function start() {
  starting.value = true
  actionError.value = ''
  const projectID = props.project.id
  try {
    const check = await apiFetch<SetupCheck>(`${endpoint.value}/setup-checks`, { method: 'POST' })
    if (projectID !== props.project.id) return
    checkId.value = check.id
    try { sessionStorage.setItem(`tindra:setup:${projectID}`, check.id) } catch { /* Optional tab persistence. */ }
  } catch { actionError.value = 'Could not start a setup check. Please try again.' }
  finally { starting.value = false }
}

const reasonText: Record<string, string> = {
  storage_failed: 'This data could not be stored. Check the database connection and server writer logs, then retry.',
  event_limit: 'The instance event limit was reached. Review usage and limits in settings.',
  bad_gzip: 'Tindra could not decompress the request. Check SDK and proxy compression settings.',
  envelope_too_large: 'The request exceeded the ingestion size limit. Reduce the payload size and retry.',
  bad_envelope: 'The request was not a valid Sentry envelope. Check the SDK transport configuration.',
  buffer_full: 'The ingestion buffer was full. Wait briefly and retry; check server load if this continues.',
  unavailable: 'This data type is unavailable on the server. Check the server configuration.',
  invalid_transaction: 'A transaction payload could not be parsed. Check the SDK version and transport.',
  profiling_disabled: 'A profile arrived, but profiling is disabled for this project. Enable it in project settings.',
  invalid_profile: 'A profile was discarded because its format or duration was invalid. Check your SDK profiling configuration.',
  profile_encoding_failed: 'A profile could not be prepared for storage. Check the server logs and retry.',
}
const kinds: { kind: SetupKind; title: string; help: string }[] = [
  { kind: 'events', title: 'Errors', help: 'Run the test exception in your app, check its DSN, and allow pending events to flush before the process exits.' },
  { kind: 'transactions', title: 'Transactions', help: 'Enable tracing in your SDK, temporarily sample all traces in development, then complete a request or page load. Check the environment and time filters if stored transactions are missing from a list.' },
  { kind: 'profile_chunks', title: 'Profiles', help: 'Enable profiling for this project and install the profiling integration supported by your SDK. Run a sampled transaction, or wait for a continuous profile chunk to finish.' },
]
function state(kind: SetupKind) {
  if (kind === 'profile_chunks' && data.value && !data.value.profiling_enabled) return 'Disabled'
  const stored = receipt(kind)
  const rejected = data.value?.observations.find(o => o.kind === kind && o.outcome === 'rejected')
  if (rejected && (!stored || Date.parse(rejected.observed_at) > Date.parse(stored.last_received_at))) return 'Needs attention'
  if (stored) return 'Received'
  if (data.value?.observations.some(o => o.kind === kind && o.outcome === 'queued')) return 'Queued'
  return 'Not yet received'
}
function observations(kind: SetupKind) {
  return data.value?.observations.filter(o => o.kind === kind && o.outcome === 'rejected') ?? []
}

watch(sourceEvent, () => { verification.value = null; verifiedEvent.value = ''; sourceError.value = '' })
async function verify(eventID?: string) {
  verifying.value = true
  sourceError.value = ''
  verification.value = null
  const selected = eventID || sourceEvent.value.trim() || data.value?.examples.events?.id
  const projectID = props.project.id
  const input = sourceEvent.value
  try {
    if (!selected) { sourceError.value = 'Choose an event first, or send an error from your built application.'; return }
    const result = await apiFetch<SourceMapVerification>(`${endpoint.value}/setup-sourcemaps?event_id=${encodeURIComponent(selected)}`)
    if (projectID !== props.project.id) return
    if (input !== sourceEvent.value) return
    verification.value = result
    verifiedEvent.value = selected
    uploadRelease.value = result.release
    uploadURL.value = result.frames.find(f => f.status !== 'verified')?.normalized_url ?? ''
  } catch (err) {
    if (projectID !== props.project.id || input !== sourceEvent.value) return
    sourceError.value = err instanceof ApiError && err.status === 404 ? 'This event was not found in this project. It may have expired.' : 'Could not verify source maps. Check the event ID and try again.' }
  finally { verifying.value = false }
}
const sourceLabels: Record<string, string> = {
  verified: 'Verified', partially_verified: 'Some frames resolved', needs_attention: 'Needs attention',
  no_frames: 'No stack frames to verify. Send an exception from your built application.',
  not_applicable: 'Source maps do not apply to this event’s platform.',
  missing_release: 'No release on this event. Configure the SDK release and send a new error.',
  no_matching_map: 'No map matches this release and file URL. Upload a map with these exact values.',
  map_unreadable: 'The uploaded map could not be read or parsed. Check the file and upload it again.',
  no_mapping: 'The map has no mapping at this position. Upload the map from the same build as the event.',
}
async function upload() {
  if (!uploadFile.value) return
  uploading.value = true
  sourceError.value = ''
  const projectID = props.project.id
  const input = sourceEvent.value
  const eventID = sourceEvent.value.trim() || verifiedEvent.value || data.value?.examples.events?.id
  try {
    const form = new FormData()
    form.set('release', uploadRelease.value)
    form.set('url', uploadURL.value)
    form.set('file', uploadFile.value)
    const res = await fetch(`${endpoint.value}/sourcemaps`, { method: 'POST', credentials: 'include', body: form })
    if (!res.ok) throw new Error('upload failed')
    if (projectID !== props.project.id) return
    toast.show('Source map uploaded')
    await refetch()
    if (eventID && projectID === props.project.id && input === sourceEvent.value) await verify(eventID)
  } catch {
    if (projectID !== props.project.id || input !== sourceEvent.value) return
    sourceError.value = 'Could not upload the source map. Check the file, release, URL, and your project permissions.' }
  finally { uploading.value = false }
}
</script>

<template>
  <section class="setup" :aria-labelledby="`${id}-title`">
    <header class="setup__header">
      <div>
        <p class="setup__eyebrow"><Icon name="braces" :size="14" /> Project setup <span>/</span> {{ project.name }}</p>
        <h2 :id="`${id}-title`">{{ headline }}</h2>
        <p>Connect your app, confirm an event, then check any optional data you need.</p>
      </div>
      <button v-if="confirmed && !error" class="btn btn--primary" @click="finish">Continue to dashboard</button>
      <button v-else class="btn" :disabled="isFetching" @click="refetch()"><Icon name="refresh-cw" :size="12" />Check again</button>
    </header>

    <p v-if="isPending" role="status">Checking this project’s setup…</p>
    <div v-if="error" role="alert" class="setup__notice">
      <p>Could not refresh setup status. This does not mean your app is disconnected.</p>
      <button v-if="error instanceof ApiError && error.status === 404 && checkId" class="btn" :disabled="starting" @click="start">Start a new check</button>
      <button v-else class="btn" @click="refetch()">Try again</button>
    </div>

    <div class="setup__layout">
    <div class="setup__main">
    <div class="setup__connection">
      <div class="setup__panel-head"><Icon name="braces" :size="14" /><h3>Connect your application</h3></div>
      <div class="field">
        <label class="field__label" :for="`${id}-dsn`">Project DSN</label>
        <div class="setup__inline">
          <input :id="`${id}-dsn`" class="field__input mono" readonly :value="dsn" />
          <button class="btn" @click="copy(dsn, 'DSN')"><Icon name="copy" :size="12" />Copy DSN</button>
        </div>
      </div>
      <div class="setup__inline">
        <label :for="`${id}-sdk`">SDK</label>
        <select :id="`${id}-sdk`" v-model="sdk" class="field__input">
          <option value="node">Node.js</option><option value="browser">Browser JavaScript</option><option value="other">Other Sentry SDK</option>
        </select>
        <button class="btn" :class="{ 'btn--primary': !checkId }" :disabled="starting" @click="start">{{ starting ? 'Starting…' : checkId ? 'Start a new test' : 'Start setup check' }}</button>
      </div>
      <p v-if="actionError" role="alert">{{ actionError }}</p>
      <template v-if="checkId && !expired">
        <template v-if="sdk !== 'other'">
          <p>Install <code>{{ sdk === 'node' ? '@sentry/node' : '@sentry/browser' }}</code> in your app. For an existing integration, keep its initialization and run the test block below.</p>
          <div class="setup__code">
            <pre class="codeblock"><code>{{ snippet }}</code></pre>
            <button class="btn" :class="{ 'btn--primary': !confirmed }" @click="copy(snippet, 'Snippet')">Copy snippet</button>
          </div>
        </template>
        <p v-else>Configure your SDK with this DSN, then capture an exception with the event tag <code>tindra_setup</code> set to <code>{{ checkId }}</code>. Flush pending events before your app exits.</p>
        <p class="setup__muted">Run this in the application you want to connect. This test sends a real error and may trigger your project’s alerts.</p>
      </template>
      <p v-else-if="!checkId">Start a check to generate a test snippet for this project.</p>

      <div role="status" aria-live="polite" class="setup__result" :class="{ 'setup__result--success': confirmed }">
        <template v-if="confirmed">
          <strong>Test event received</strong>
          <p>Stored {{ time(data!.check!.received_at!) }}<template v-if="testExample?.environment"> in {{ testExample.environment }}</template><template v-if="testExample?.sdk?.trim()"> using {{ testExample.sdk.trim() }}</template>.</p>
        </template>
        <p v-else-if="expired">This test has expired. Start a new check to generate a fresh snippet.</p>
        <p v-else-if="checkId && !error">{{ waitingLong ? 'Still waiting. Your app may need a little troubleshooting; the checks below can help.' : 'Listening for this test event. This page updates automatically.' }}</p>
        <p v-else-if="connected">This project has already stored data. You can run a fresh test whenever you need to check a new environment.</p>
        <a v-if="eventLink" class="btn" :href="eventLink">View event</a>
        <p v-else-if="confirmed && testExample" class="setup__muted">Your event is stored. Its issue link will appear once processing finishes.</p>
        <p v-else-if="confirmed" class="setup__muted">The event has been removed or expired, but its receipt is preserved.</p>
      </div>
    </div>

    <details :open="waitingLong || focus === 'errors'" class="setup__section">
      <summary><Icon class="setup__chevron" name="chevron-right" :size="12" />Nothing arriving?</summary>
      <ol>
        <li>Check that the running app uses the DSN above, including its public key and host.</li>
        <li>Inspect the SDK’s request or debug output. Check for blocked network requests, proxy errors, and HTTP 401, 413, or 429 responses.</li>
        <li>Check SDK sampling and event filters, then flush pending events before a short-lived process exits.</li>
      </ol>
      <p>Requests that fail before Tindra can identify the project cannot appear here. No observation is not proof of a network problem.</p>
      <p v-for="o in data?.observations.filter(o => o.kind === 'envelope' && o.outcome === 'rejected')" :key="o.reason">{{ reasonText[o.reason] }} <span class="setup__muted">Observed {{ time(o.observed_at) }}.</span></p>
    </details>

    </div>
    <div v-if="data" class="setup__checks">
      <div class="setup__panel-head"><Icon name="activity" :size="14" /><h3>Data checks</h3></div>
      <p class="setup__muted">Transactions, profiles, and source maps are optional. These checks cover this project across all environments and time filters.</p>
      <details v-for="item in kinds" :key="item.kind" class="setup__section" :open="focus === item.kind || (focus === 'profiles' && item.kind === 'profile_chunks')">
        <summary><Icon class="setup__chevron" name="chevron-right" :size="12" /><span>{{ item.title }}</span><span class="setup__state" :class="{ 'setup__state--ok': state(item.kind) === 'Received', 'setup__state--warning': state(item.kind) === 'Needs attention' }">{{ state(item.kind) }}</span></summary>
        <p v-if="receipt(item.kind)">First stored {{ time(receipt(item.kind)!.first_received_at) }}. Last stored {{ time(receipt(item.kind)!.last_received_at) }}.</p>
        <p v-if="state(item.kind) === 'Queued'">Tindra accepted this data into its queue, but has not confirmed storage. Check again shortly; if this persists, check the server’s database and writer logs.</p>
        <p>{{ item.help }}</p>
        <p v-for="o in observations(item.kind)" :key="o.reason">{{ reasonText[o.reason] }} <span class="setup__muted">Observed {{ time(o.observed_at) }}; other items may have succeeded.</span></p>
        <a v-if="item.kind === 'events' && eventLink" class="btn" :href="eventLink">View event</a>
        <a v-if="item.kind === 'transactions' && data.examples.transactions" class="btn" :href="`/transactions/${data.examples.transactions.id}`">View transaction</a>
        <template v-if="item.kind === 'profile_chunks'">
          <a v-if="data.profile_transaction_id" class="btn" :href="`/transactions/${data.profile_transaction_id}`">Open profiled transaction</a>
          <p v-else-if="receipt('profile_chunks')">A profile was stored, but no matching transaction was found among the 100 most recently received transactions. Check transaction sampling and profile identifiers; older data may have expired.</p>
          <a class="btn" href="/settings/projects">Project settings</a>
          <a class="setup__doc" href="https://docs.sentry.io/platforms/javascript/guides/node/profiling/" target="_blank" rel="noopener noreferrer">Node.js profiling guide</a>
        </template>
        <p v-if="item.kind === 'transactions'"><code>tracesSampleRate: 1.0</code> can help verify JavaScript tracing in development. Choose an appropriate production sampling rate after testing.</p>
      </details>

      <details class="setup__section" :open="focus === 'sourcemaps'">
        <summary><Icon class="setup__chevron" name="chevron-right" :size="12" /><span>Source maps</span><span class="setup__state">{{ verification ? ({ verified: 'Verified', partially_verified: 'Some frames resolved', needs_attention: 'Needs attention', no_frames: 'No stack frames', not_applicable: 'Not applicable' }[verification.status] ?? 'Not verified') : data.sourcemap_count ? `${data.sourcemap_count} uploaded, not verified` : 'Not verified' }}</span></summary>
        <p>Verify an error from your built JavaScript app. Tindra matches its release and file URL against your uploads, then checks the original source location.</p>
        <p v-if="!data.sourcemaps_available">Source map storage is not configured on this server.</p>
        <template v-else>
          <label class="field__label" :for="`${id}-event`">Event ID <span class="setup__muted">(leave empty to use the latest error in this project)</span></label>
          <div class="setup__inline">
            <input :id="`${id}-event`" v-model="sourceEvent" class="field__input mono" placeholder="Tindra event ID" />
            <button class="btn" :disabled="verifying" @click="verify()">{{ verifying ? 'Verifying…' : 'Verify source maps' }}</button>
          </div>
          <div v-if="sourceError" role="alert">{{ sourceError }}</div>
          <div v-if="verification" class="setup__verification" aria-live="polite">
            <p><strong>{{ sourceLabels[verification.status] }}</strong></p>
            <p>Event: <code>{{ verifiedEvent }}</code></p>
            <p>Event release: <code>{{ verification.release || '(missing)' }}</code></p>
            <ul>
              <li v-for="(frame, i) in verification.frames" :key="i">
                <code>{{ frame.url }}:{{ frame.line }}:{{ frame.column }}</code>
                <p>Matching URL: <code>{{ frame.normalized_url }}</code></p>
                <p v-if="frame.status === 'verified'" class="setup__state--ok">Resolved to {{ frame.source }}:{{ frame.original_line }}</p>
                <p v-else>{{ sourceLabels[frame.status] }}</p>
              </li>
            </ul>
            <p v-if="verification.truncated">Showing the first 40 eligible frames.</p>
          </div>
          <details v-if="auth.user?.permissions.manage_projects" class="setup__upload">
            <summary>Upload a source map</summary>
            <form @submit.prevent="upload">
              <label :for="`${id}-release`">Release</label><input :id="`${id}-release`" v-model="uploadRelease" required class="field__input" />
              <label :for="`${id}-url`">Generated file URL</label><input :id="`${id}-url`" v-model="uploadURL" required class="field__input" placeholder="~/assets/app.js" />
              <label :for="`${id}-file`">Source map file</label><input :id="`${id}-file`" type="file" accept=".map,.json" required @change="uploadFile = ($event.target as HTMLInputElement).files?.[0] ?? null" />
              <button class="btn" :disabled="uploading">{{ uploading ? 'Uploading…' : 'Upload and verify' }}</button>
            </form>
          </details>
        </template>
      </details>
    </div>
    </div>
    <footer class="setup__footer">
      <span class="setup__muted">{{ data ? `Last checked ${time(data.checked_at)}` : 'Setup status is not yet available.' }}</span>
      <button class="btn" :class="{ 'btn--primary': connected || confirmed }" @click="finish">{{ connected || confirmed ? 'Continue to dashboard' : 'Set up later' }}</button>
    </footer>
  </section>
</template>

<style scoped>
.setup { width: 100%; min-width: 0; text-align: left; color: var(--text-1); font-size: var(--text-sm); line-height: 1.6; }
.setup__header { display: flex; justify-content: space-between; align-items: center; gap: 24px; padding: 24px; border-bottom: 1px solid var(--border); }
.setup__eyebrow { display: flex; align-items: center; gap: 8px; color: var(--text-3); font-size: var(--text-xs); }
.setup__eyebrow span { color: var(--border); }
.setup h2 { font-size: var(--text-xl); font-weight: 600; letter-spacing: -0.015em; margin: 8px 0 4px; }
.setup h3 { font-size: var(--text-xs); font-weight: 500; letter-spacing: 0.04em; text-transform: uppercase; margin: 0; }
.setup p { margin: 0 0 12px; }
.setup__header p:last-child { margin-bottom: 0; color: var(--text-2); }
.setup__header .btn { flex-shrink: 0; }
.setup__layout { display: grid; grid-template-columns: minmax(0, 1fr) 340px; border-bottom: 1px solid var(--border); }
.setup__main { min-width: 0; }
.setup__connection { display: grid; gap: 16px; padding: 0 24px 20px; }
.setup__panel-head { display: flex; align-items: center; gap: 8px; min-height: 36px; padding: 8px 16px; background: var(--surface); border-bottom: 1px solid var(--border-soft); color: var(--text-3); }
.setup__connection > .setup__panel-head { margin: 0 -24px; }
.setup__connection > p { margin: 0; color: var(--text-2); }
.setup__inline { display: flex; align-items: center; gap: 8px; }
.setup__inline input { flex: 1; min-width: 0; }
.setup__inline select { width: auto; max-width: 100%; }
.setup .field__input { height: 30px; padding-left: 10px; font-size: var(--text-xs); }
.setup .btn { white-space: nowrap; }
.setup__code { min-width: 0; display: flex; flex-direction: column; align-items: flex-start; gap: 8px; }
.setup__code pre { width: 100%; line-height: 1.7; }
.setup__muted { color: var(--text-3); font-size: var(--text-xs); }
.setup__result:empty { display: none; }
.setup__result { padding: 12px; border: 1px solid var(--border-soft); background: var(--surface); border-radius: 3px; }
.setup__result p:last-child { margin-bottom: 0; }
.setup__result--success { background: oklch(from var(--success) l c h / 0.06); border-color: oklch(from var(--success) l c h / 0.25); }
.setup__result--success strong { color: var(--success); }
.setup__notice { padding: 16px 24px; border-bottom: 1px solid var(--border); }
.setup > p[role="status"] { padding: 12px 24px; margin: 0; color: var(--text-3); }
.setup__checks { min-width: 0; border-left: 1px solid var(--border); }
.setup__checks > p { padding: 16px; margin: 0; border-bottom: 1px solid var(--border-soft); }
.setup__section { border-bottom: 1px solid var(--border-soft); padding: 0 16px; }
.setup__section:last-child { border-bottom: 0; }
.setup__main > .setup__section { border-top: 1px solid var(--border-soft); padding: 0 24px; }
.setup__section summary { display: flex; align-items: center; gap: 8px; cursor: pointer; font-weight: 500; min-height: 44px; list-style: none; }
.setup__section summary::-webkit-details-marker { display: none; }
.setup__section summary:hover { color: var(--accent); }
.setup__chevron { flex-shrink: 0; color: var(--text-3); }
.setup__section[open] > summary .setup__chevron { transform: rotate(90deg); }
.setup__section[open] { padding-bottom: 16px; }
.setup__section > p { color: var(--text-2); }
.setup__state { margin-left: auto; color: var(--text-3); font-size: var(--text-xs); font-weight: 400; text-align: right; }
.setup__state--ok { color: var(--success); }
.setup__state--warning { color: var(--warning); }
.setup ol { list-style: decimal; padding-left: 22px; margin-bottom: 12px; color: var(--text-2); }
.setup li { padding: 4px 0; }
.setup__doc { display: inline-block; margin-top: 8px; color: var(--accent); }
.setup__verification { margin-top: 16px; overflow-wrap: anywhere; }
.setup__verification li { padding: 12px 0; border-bottom: 1px solid var(--border-soft); }
.setup__verification p { margin-bottom: 4px; }
.setup__checks .setup__inline { flex-wrap: wrap; }
.setup__checks .setup__inline input { flex-basis: 100%; }
.setup__upload { margin-top: 20px; }
.setup__upload form { display: grid; gap: 8px; margin-top: 12px; }
.setup__upload input { min-width: 0; max-width: 100%; }
.setup__upload .btn { justify-self: start; margin-top: 8px; }
.setup__footer { display: flex; justify-content: space-between; align-items: center; gap: 16px; padding: 16px 24px; }
@media (max-width: 1000px) {
  .setup__layout { grid-template-columns: minmax(0, 1fr); }
  .setup__checks { border-left: 0; border-top: 1px solid var(--border); }
}
@media (max-width: 600px) {
  .setup__header { flex-direction: column; align-items: flex-start; gap: 16px; padding: 20px 16px; }
  .setup__connection { padding: 0 16px 16px; }
  .setup__connection > .setup__panel-head { margin: 0 -16px; }
  .setup__main > .setup__section { padding-left: 16px; padding-right: 16px; }
  .setup__inline { flex-wrap: wrap; }
  .setup__inline input { flex-basis: 100%; }
  .setup .btn, .setup .field__input { min-height: 40px; }
  .setup__footer { padding: 16px; flex-wrap: wrap; }
}
</style>
