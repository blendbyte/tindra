<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { ref, computed, watch, watchEffect, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { apiFetch } from '@/api/client'
import { useFormatters } from '@/composables/useFormatters'
import { useTimezone } from '@/composables/useTimezone'
import { useIssueNavStore } from '@/stores/issueNav'
import type { Issue, Event as TindraEvent, User, EventSummary, HistogramBucket } from '@/api/types'
import Icon from '@/components/Icon.vue'
import TimeseriesChart from '@/components/TimeseriesChart.vue'
import IgnoreButton from '@/components/IgnoreButton.vue'
import type { IgnorePayload } from '@/components/IgnoreButton.vue'
import { useMe } from '@/composables/useMe'
import { prefetchIssueSections } from './issue/sectionQueries'
import IssuePerfTransactions from './issue/IssuePerfTransactions.vue'
import IssueEventList from './issue/IssueEventList.vue'
import IssueStackTrace from './issue/IssueStackTrace.vue'
import IssueHttpRequest from './issue/IssueHttpRequest.vue'
import IssueTracePreview from './issue/IssueTracePreview.vue'
import IssueBreadcrumbs from './issue/IssueBreadcrumbs.vue'
import IssueContexts from './issue/IssueContexts.vue'
import IssuePackages from './issue/IssuePackages.vue'
import IssueUserCard from './issue/IssueUserCard.vue'
import IssueTags from './issue/IssueTags.vue'
import IssueActivity from './issue/IssueActivity.vue'

// Body of PATCH /api/issues/:id: a status change (with optional snooze) or an assignee change.
type IssueUpdate = { status?: string; ignore_until?: string; ignore_count_limit?: number; assignee_id?: string | null }

const route = useRoute()
const router = useRouter()
const { show: showToast } = useToast()
const qc = useQueryClient()
const navStore = useIssueNavStore()
const { formatRel } = useFormatters()
const tz = useTimezone()

const { data: me } = useMe()

const issueId = computed(() => route.params.id as string)
const prevIssueId = computed(() => navStore.prevId(issueId.value))
const nextIssueId = computed(() => navStore.nextId(issueId.value))

function goPrev() { if (prevIssueId.value) router.push(`/issues/${prevIssueId.value}`) }
function goNext() { if (nextIssueId.value) router.push(`/issues/${nextIssueId.value}`) }

const eventIndex = ref(0)
const pinnedEvent = computed(() => typeof route.query?.event_id === 'string' ? route.query.event_id : '')
const eventParams = computed(() => pinnedEvent.value ? `event_id=${encodeURIComponent(pinnedEvent.value)}` : `offset=${eventIndex.value}`)
const assignOpen = ref(false)
const assignEl = ref<HTMLElement | null>(null)
const showEventList = ref(false)

const { data: issue, isError: issueError, refetch: refetchIssue } = useQuery({
  queryKey: computed(() => ['issues', issueId.value]),
  queryFn: ({ signal }) => apiFetch<Issue>(`/api/issues/${issueId.value}`, { signal }),
})

watchEffect(() => {
  if (issue.value?.title) document.title = `${issue.value.title} - Tindra`
})

const { data: currentEvent, error: currentEventError } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'events', eventIndex.value, pinnedEvent.value]),
  queryFn: ({ signal }) => apiFetch<TindraEvent>(`/api/issues/${issueId.value}/events/latest?${eventParams.value}`, { signal }),
  enabled: computed(() => !!issueId.value && !!issue.value && issue.value.kind !== 'n1_query'),
  placeholderData: keepPreviousData,
})

const { data: usersData } = useQuery({
  queryKey: ['users'],
  queryFn: ({ signal }) => apiFetch<User[]>('/api/users', { signal }),
})
const users = computed(() => usersData.value ?? [])

const { mutate: updateStatus, isPending: updatingStatus } = useMutation({
  mutationFn: (body: IssueUpdate) =>
    apiFetch(`/api/issues/${issueId.value}`, { method: 'PATCH', body: JSON.stringify(body) }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['issues'] })
    qc.invalidateQueries({ queryKey: ['issues', issueId.value] })
  },
})

const { mutate: assignIssue } = useMutation({
  mutationFn: (assigneeId: string | null) =>
    apiFetch(`/api/issues/${issueId.value}`, {
      method: 'PATCH',
      body: JSON.stringify({ assignee_id: assigneeId }),
    }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['issues', issueId.value] })
    qc.invalidateQueries({ queryKey: ['issues'] })
    assignOpen.value = false
  },
})

// Status and assignment changes need the manage_issues permission on the server.
const canManageIssues = computed(() => !!me.value?.permissions.manage_issues)

// changeStatus confirms a change only once the server accepted it, with an undo
// that restores the previous status.
function changeStatus(body: IssueUpdate, message: string, undoStatus: string) {
  updateStatus(body, { onSuccess: () => showToast(message, () => updateStatus({ status: undoStatus })) })
}

function setStatus(status: string, undoStatus: string) {
  changeStatus({ status }, status === 'resolved' ? 'Resolved' : 'Ignored', undoStatus)
}

function handleIgnore(payload: IgnorePayload) {
  changeStatus(payload, 'Ignored', 'open')
}

function handleUnignore() {
  changeStatus({ status: 'open' }, 'Unignored', 'ignored')
}

const titleExpanded = ref(false)
// The view is reused when moving to the next or previous issue, so per-issue
// state must be reset; otherwise e.g. "event 6" of the old issue is requested
// for the new one. The sections reset their own state the same way.
watch(issueId, (id) => {
  if (id) prefetchIssueSections(qc, id)
}, { immediate: true })

watch(issueId, () => {
  titleExpanded.value = false
  eventIndex.value = 0
  showEventList.value = false
})

// Stored issue titles are capped at ingest time, so recover the untruncated
// text from the current event payload to offer it behind a toggle.
const fullTitle = computed(() => {
  const payload = currentEvent.value?.payload as { message?: unknown } | undefined
  const exc = currentEvent.value?.payload?.exception as { values?: { type?: string; value?: string }[] } | undefined
  const values = exc?.values
  if (values?.length) {
    const last = values[values.length - 1]
    if (last?.value) return [last.type, last.value].filter(Boolean).join(': ')
    return last?.type ?? ''
  }
  return eventMessageText(payload?.message) || eventMessageText((payload as { logentry?: unknown } | undefined)?.logentry)
})

// SDKs send message as a string or as {formatted, message, params}
// (Java, .NET, PHP, and Python's logentry). Prefer the formatted text.
function eventMessageText(message: unknown): string {
  if (typeof message === 'string') return message
  if (message && typeof message === 'object') {
    const m = message as { formatted?: unknown; message?: unknown }
    if (typeof m.formatted === 'string' && m.formatted) return m.formatted
    if (typeof m.message === 'string') return m.message
  }
  return ''
}

const titleTruncated = computed(() => {
  const stored = issue.value?.title ?? ''
  const full = fullTitle.value
  if (!stored || !full || full.length <= stored.length) return false
  // Titles stored before the ellipsis marker was added simply end mid-sentence.
  const base = stored.endsWith('\u2026') ? stored.slice(0, -1) : stored
  return full.startsWith(base)
})

const displayTitle = computed(() => {
  const stored = issue.value?.title ?? ''
  if (!titleTruncated.value) return stored
  if (titleExpanded.value) return fullTitle.value
  // The toggle button renders the ellipsis, so drop the stored one.
  return stored.endsWith('\u2026') ? stored.slice(0, -1) : stored
})

const titleType = computed(() => displayTitle.value.split(':')[0])
const titleRest = computed(() => {
  const i = displayTitle.value.indexOf(':')
  return i === -1 ? '' : displayTitle.value.slice(i + 1).trim()
})

const isUnhandled = computed(() => {
  const exc = currentEvent.value?.payload?.exception as { values?: { mechanism?: { handled?: boolean } }[] } | undefined
  const handled = exc?.values?.[0]?.mechanism?.handled
  return handled === false || handled === null
})

const { data: histogram } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'histogram']),
  queryFn: ({ signal }) => apiFetch<{ buckets: HistogramBucket[]; bucket_size: 'hour' | 'day' | 'week' }>(`/api/issues/${issueId.value}/events/histogram`, { signal }),
  enabled: computed(() => !!issueId.value),
})

function formatHistogramTime(iso: string): string {
  const d = new Date(iso)
  const size = histogram.value?.bucket_size
  const zone = tz.value
  if (size === 'hour') {
    const dateStr = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: zone })
    const fmt = new Intl.DateTimeFormat('en', { hour: 'numeric', hour12: false, timeZone: zone })
    const h = String(parseInt(fmt.format(d), 10) % 24).padStart(2, '0')
    const hNext = String((parseInt(fmt.format(d), 10) % 24 + 1) % 24).padStart(2, '0')
    return `${dateStr}  ${h}:00 – ${hNext}:00`
  }
  if (size === 'day') {
    return d.toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric', timeZone: zone })
  }
  const end = new Date(d.getTime() + 6 * 24 * 60 * 60 * 1000)
  const s = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: zone })
  const e = end.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: zone })
  return `${s} – ${e}`
}

const assigneeUser = computed(() =>
  issue.value?.assignee_id
    ? (users.value ?? []).find((u) => u.id === issue.value?.assignee_id)
    : null,
)

function toggleEventList() {
  showEventList.value = !showEventList.value
}

// Open the picked event by ID. Its position in the (client-sorted) list is not
// its position on the server, so navigating by index showed a different event.
function selectEvent(ev: EventSummary) {
  router.replace({ query: { ...route.query, event_id: ev.id } })
}

function onMouseDown(e: MouseEvent) {
  if (assignEl.value && !assignEl.value.contains(e.target as Node)) {
    assignOpen.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if ((e.target as HTMLElement).tagName === 'INPUT' || (e.target as HTMLElement).tagName === 'TEXTAREA') return
  if (e.metaKey || e.ctrlKey) return
  if (e.key === 'Escape') router.push('/issues')
  else if (e.key === '[') { e.preventDefault(); goPrev() }
  else if (e.key === ']') { e.preventDefault(); goNext() }
  else if (!canManageIssues.value) return
  else if (e.key.toLowerCase() === 'e' && !updatingStatus.value && issue.value?.status !== 'resolved') setStatus('resolved', 'open')
  else if (e.key.toLowerCase() === 'i' && !updatingStatus.value && issue.value?.status !== 'ignored') setStatus('ignored', 'open')
  else if (e.key.toLowerCase() === 'a') { e.preventDefault(); assignOpen.value = !assignOpen.value }
  else if (e.key === 'ArrowLeft' && !pinnedEvent.value) eventIndex.value = Math.max(0, eventIndex.value - 1)
  else if (e.key === 'ArrowRight' && !pinnedEvent.value) eventIndex.value = Math.min((issue.value?.event_count ?? 1) - 1, eventIndex.value + 1)
}

onMounted(() => {
  document.addEventListener('keydown', onKey)
  document.addEventListener('mousedown', onMouseDown)
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKey)
  document.removeEventListener('mousedown', onMouseDown)
})
</script>

<template>
  <div v-if="issue" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/issues')">
        <Icon name="arrow-left" :size="12" />
        Issues
      </a>
      <div v-if="navStore.ids.length > 0" class="detail-breadcrumb__nav">
        <button class="detail-nav-btn" :disabled="!prevIssueId" title="Previous issue [" aria-label="Previous issue" @click="goPrev">
          <Icon name="chevron-left" :size="14" />
        </button>
        <button class="detail-nav-btn" :disabled="!nextIssueId" title="Next issue ]" aria-label="Next issue" @click="goNext">
          <Icon name="chevron-right" :size="14" />
        </button>
      </div>
      <div class="detail-breadcrumb__title"><span>{{ issue.id }}</span></div>
      <div class="detail-breadcrumb__actions">
        <template v-if="canManageIssues">
          <!-- Assign button -->
          <div ref="assignEl" style="position: relative">
            <button
              class="btn"
              :class="{ 'btn--active': !!issue.assignee_id }"
              title="Assign (A)"
              aria-haspopup="true"
              :aria-expanded="assignOpen"
              @click="assignOpen = !assignOpen"
            >
              <Icon name="user" :size="12" />
              {{ assigneeUser ? (assigneeUser.name || assigneeUser.email.split('@')[0]) : 'Assign' }}
              <span class="btn__kbd">A</span>
            </button>

            <div v-if="assignOpen" class="popover assign-popover" style="min-width: 220px; right: 0; top: calc(100% + 4px)">
              <div class="popover__list">
                <div
                  v-clickable
                  class="popover__item"
                  :class="{ 'popover__item--active': !issue.assignee_id }"
                  :aria-pressed="!issue.assignee_id"
                  @click="assignIssue(null)"
                >
                  <span class="popover__check" :class="{ 'popover__check--on': !issue.assignee_id }">
                    <Icon v-if="!issue.assignee_id" name="check" :size="10" />
                  </span>
                  <span style="color: var(--text-2)">Unassigned</span>
                </div>
                <div
                  v-for="u in users"
                  :key="u.id"
                  v-clickable
                  class="popover__item"
                  :class="{ 'popover__item--active': issue.assignee_id === u.id }"
                  :aria-pressed="issue.assignee_id === u.id"
                  @click="assignIssue(u.id)"
                >
                  <span class="popover__check" :class="{ 'popover__check--on': issue.assignee_id === u.id }">
                    <Icon v-if="issue.assignee_id === u.id" name="check" :size="10" />
                  </span>
                  <span class="assign-avatar">{{ (u.name || u.email)[0].toUpperCase() }}</span>
                  <span>{{ u.name || u.email }}</span>
                </div>
              </div>
            </div>
          </div>

          <IgnoreButton
            v-if="issue?.status !== 'ignored'"
            direction="down"
            :disabled="updatingStatus"
            @ignore="handleIgnore"
          />
          <button
            v-else
            class="btn"
            :disabled="updatingStatus"
            @click="handleUnignore"
          >
            Unignore
          </button>
          <button class="btn btn--primary" :disabled="issue?.status === 'resolved' || updatingStatus" @click="setStatus('resolved', 'open')">
            Resolve <span class="btn__kbd">E</span>
          </button>
        </template>
      </div>
    </div>

    <div class="detail-body">
      <div class="detail-hero">
        <div class="detail-hero__eyebrow">
          <template v-if="issue.kind === 'n1_query'">
            <span class="kindbadge kindbadge--filled">N+1</span>
            <span class="detail-hero__level">performance</span>
          </template>
          <template v-else>
            <span class="leveldot" :class="`leveldot--${issue.level}`" />
            <span class="detail-hero__level">{{ issue.level }}</span>
          </template>
          <span v-if="isUnhandled" class="detail-hero__unhandled">Unhandled</span>
          <span class="detail-hero__sep">·</span>
          <span class="detail-hero__project">{{ issue.project_id }}</span>
          <template v-if="issue.environment">
            <span class="detail-hero__sep">·</span>
            <span class="detail-hero__env" :class="issue.environment === 'production' ? 'detail-hero__env--prod' : ''">{{ issue.environment }}</span>
          </template>
          <template v-if="issue.release">
            <span class="detail-hero__sep">·</span>
            <RouterLink v-if="issue.release_id" :to="`/releases/${issue.release_id}`" class="detail-hero__release detail-hero__release--link">{{ issue.release }}</RouterLink>
            <span v-else class="detail-hero__release">{{ issue.release }}</span>
          </template>
        </div>

        <h1 class="detail-hero__title">
          <span class="mono">{{ titleType }}</span><template v-if="titleRest">: {{ titleRest }}</template><button
            v-if="titleTruncated"
            class="detail-hero__more"
            :aria-expanded="titleExpanded"
            :title="titleExpanded ? 'Collapse the message' : 'Show the full message'"
            @click="titleExpanded = !titleExpanded"
          >{{ titleExpanded ? 'Show less' : '\u2026' }}</button>
        </h1>

        <div class="issue-meta">
          <span class="issue-meta__item" :title="new Date(issue.first_seen).toUTCString()">
            <span class="issue-meta__k">First</span> {{ formatRel(issue.first_seen) }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item" :title="new Date(issue.last_seen).toUTCString()">
            <span class="issue-meta__k">Last</span> {{ formatRel(issue.last_seen) }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            {{ issue.event_count.toLocaleString() }} {{ issue.event_count === 1 ? 'event' : 'events' }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            {{ (issue.user_count ?? 0).toLocaleString() }} {{ (issue.user_count ?? 0) === 1 ? 'user' : 'users' }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            <span class="statuspill" :class="`statuspill--${issue.status}`">{{ issue.status }}</span>
            <template v-if="issue.status === 'ignored' && issue.ignore_until">
              <span class="issue-meta__k"> until {{ new Date(issue.ignore_until).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: tz }) }}</span>
            </template>
            <template v-else-if="issue.status === 'ignored' && issue.ignore_count_limit != null">
              <span class="issue-meta__k"> · {{ Math.max(0, issue.ignore_count_limit - (issue.ignore_count ?? 0)) }} left</span>
            </template>
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            <span class="issue-meta__k">{{ assigneeUser ? '' : 'Unassigned' }}</span>
            <span v-if="assigneeUser">{{ assigneeUser.name || assigneeUser.email }}</span>
          </span>
        </div>

        <!-- Frequency histogram -->
        <div v-if="histogram && histogram.buckets.length > 1" class="hgram-wrap">
          <TimeseriesChart
            :times="histogram.buckets.map(b => b.time)"
            :series="[{ id: 'events', label: 'Events', type: 'bar', values: histogram.buckets.map(b => b.count) }]"
            :bucket-size="histogram.bucket_size"
            :height="104"
            :grid-lines="1"
            :format-time="formatHistogramTime"
          />
        </div>
      </div>

      <div class="detail-main">

      <!-- N+1 performance issue section -->
      <IssuePerfTransactions v-if="issue.kind === 'n1_query'" :issue-id="issueId" :issue="issue" />

      <!-- Event instances (error issues only) -->
      <template v-if="issue.kind !== 'n1_query'">
      <div class="section">
        <div class="section__head" style="cursor: default">
          <h2 class="section__title">Event instances</h2>
          <span class="section__count">{{ issue.event_count.toLocaleString() }} occurrences</span>
          <div v-if="issue.event_count > 1" class="section__actions">
            <button class="btn btn--ghost" style="font-size: var(--text-xs)" @click="toggleEventList">
              {{ showEventList ? 'Hide list' : 'Show all' }}
            </button>
          </div>
        </div>
        <p v-if="pinnedEvent && currentEventError" role="alert" style="padding: 12px 16px">Could not load this linked event. It may have expired; you can still view the latest event below.</p>
        <div v-if="pinnedEvent" class="eventnav">
          <span class="eventnav__cur">Viewing linked event</span>
          <button class="btn" @click="router.replace({ query: { ...route.query, event_id: undefined } })">View latest event</button>
          <a v-if="currentEvent" class="eventnav__raw" :href="`/api/issues/${issueId}/events/latest?${eventParams}`" target="_blank" rel="noopener">Raw</a>
        </div>
        <div v-else class="eventnav">
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex >= issue.event_count - 1 }"
            title="Jump to first occurrence"
            aria-label="Jump to first occurrence"
            @click="eventIndex = issue.event_count - 1"
          >
            <Icon name="chevrons-left" :size="12" />
          </button>
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex === 0 }"
            @click="eventIndex = Math.max(0, eventIndex - 1)"
          >
            <Icon name="chevron-left" :size="12" />
            Newer
          </button>
          <div class="eventnav__cur">
            <span>Event</span>
            <span style="color: var(--text-1)">{{ eventIndex + 1 }} / {{ issue.event_count.toLocaleString() }}</span>
          </div>
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex >= issue.event_count - 1 }"
            @click="eventIndex = Math.min(issue.event_count - 1, eventIndex + 1)"
          >
            Older
            <Icon name="chevron-right" :size="12" />
          </button>
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex === 0 }"
            title="Jump to latest occurrence"
            aria-label="Jump to latest occurrence"
            @click="eventIndex = 0"
          >
            <Icon name="chevrons-right" :size="12" />
          </button>
          <a
            v-if="currentEvent"
            class="eventnav__raw"
            :href="`/api/issues/${issueId}/events/latest?offset=${eventIndex}`"
            target="_blank"
            rel="noopener"
          >
            <Icon name="braces" :size="11" />
            Raw
          </a>
        </div>

        <!-- Full event list -->
        <IssueEventList :issue-id="issueId" :open="showEventList" :current-event-id="currentEvent?.id" @select="selectEvent" />
      </div>

      <IssueStackTrace :event="currentEvent" />
      <IssueHttpRequest :event="currentEvent" />
      <IssueTracePreview
        :issue-id="issueId"
        :event="currentEvent"
        :event-index="eventIndex"
        :pinned-event="pinnedEvent"
        :event-params="eventParams"
      />
      <IssueBreadcrumbs :event="currentEvent" />
      <IssueContexts :event="currentEvent" />
      <IssuePackages :event="currentEvent" />

      </template><!-- /error-only sections -->

      </div><!-- /detail-main -->

      <div class="detail-sidebar">

      <IssueUserCard :issue="issue" :event="currentEvent" />
      <IssueTags :issue-id="issueId" />
      <IssueActivity :issue-id="issueId" :me="me" :users="users" />

      </div><!-- /detail-sidebar -->
    </div>
  </div>

  <div v-else-if="issueError" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/issues')">
        <Icon name="arrow-left" :size="12" />
        Issues
      </a>
    </div>
    <div class="txerror" style="margin: 24px">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      <span>Failed to load issue.</span>
      <button class="btn" @click="refetchIssue()">Try again</button>
    </div>
  </div>

  <div v-else class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/issues')">
        <Icon name="arrow-left" :size="12" />
        Issues
      </a>
    </div>
    <div class="detail-body">
      <div class="detail-hero">
        <div class="skel-hero">
          <div class="skel skel--eyebrow" />
          <div class="skel skel--title" />
          <div class="skel skel--title skel--title-short" />
          <div class="skel skel--meta" />
        </div>
      </div>
      <div class="detail-main">
        <div class="skel-section">
          <div class="skel skel--section-head" />
          <div class="skel skel--stack" />
        </div>
      </div>
      <div class="detail-sidebar">
        <div class="skel-section">
          <div class="skel skel--section-head skel--section-head-short" />
          <div class="skel skel--sidebar-block" />
          <div class="skel skel--sidebar-block skel--sidebar-block-short" />
        </div>
      </div>
    </div>
  </div>
</template>
