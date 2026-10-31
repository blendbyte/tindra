<script setup lang="ts">
import { computed } from 'vue'
import { useInvestigationStore } from '@/stores/investigation'
import { useProjectsStore } from '@/stores/projects'
import { useFormatters } from '@/composables/useFormatters'
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import QueryFeedback from '@/components/QueryFeedback.vue'
import { fmt, useWindowLabel, type DashboardIssue } from './shared'
import { useAttentionQuery, useMonitorQueries } from './briefingData'
import {
  brokenMonitors, brokenTitle, formatSpan, healthyMonitorCount, isUnseen, joinParts,
  plural, sinceVisitParts, splitTitle, verdict,
  type AttentionIssue, type BrokenMonitor,
} from './briefing'

const props = defineProps<{
  loading: boolean
  openCount: number
  busiest: DashboardIssue[]
  lastVisit: number | null
}>()

const investigation = useInvestigationStore()
const projects = useProjectsStore()
const { formatRel } = useFormatters()
const windowLabel = useWindowLabel()
const windowPhrase = computed(() =>
  windowLabel.value === 'selected range' ? 'in the selected range'
    : windowLabel.value === 'All' ? 'in the last 90 days'
      : `in the last ${windowLabel.value}`)

const attention = useAttentionQuery(props.lastVisit)
const { uptime, cron } = useMonitorQueries()
const uptimeMonitors = uptime.data
const cronMonitors = cron.data
const uptimeFetching = uptime.isFetching
const cronFetching = cron.isFetching

const attentionPage = computed(() => attention.data.value)
const broken = computed(() => brokenMonitors(uptimeMonitors.value ?? [], cronMonitors.value ?? []))
const healthy = computed(() => healthyMonitorCount(uptimeMonitors.value ?? [], cronMonitors.value ?? []))
const regressed = computed(() => attentionPage.value?.regressed ?? [])
const fresh = computed(() => attentionPage.value?.new ?? [])
const changedTotal = computed(() => (attentionPage.value?.regressed_total ?? 0) + (attentionPage.value?.new_total ?? 0))
const hiddenChanged = computed(() => changedTotal.value - regressed.value.length - fresh.value.length)

const monitorsLoading = computed(() =>
  (uptimeFetching.value && !uptimeMonitors.value) || (cronFetching.value && !cronMonitors.value))
const attentionLoading = computed(() => attention.isFetching.value && !attentionPage.value && !attention.isError.value)
const briefingLoading = computed(() => monitorsLoading.value || attentionLoading.value)

// A source that failed to load can never be reported as clear: the verdict
// speaks only for what was checked.
const issuesUnknown = computed(() => !!attention.isError.value && !attentionPage.value)
const monitorsUnknown = computed(() =>
  (!!uptime.isError.value && !uptimeMonitors.value) || (!!cron.isError.value && !cronMonitors.value))
const v = computed(() => {
  const full = verdict(broken.value.length, issuesUnknown.value ? 0 : changedTotal.value)
  if (!issuesUnknown.value && !monitorsUnknown.value) return full
  if (issuesUnknown.value && monitorsUnknown.value) return { broken: '', changed: "Couldn't load the briefing.", clear: false }
  if (issuesUnknown.value) return { broken: full.broken, changed: full.broken ? '' : 'No monitors are failing.', clear: false }
  return { broken: full.broken, changed: full.changed || 'No new or regressed issues.', clear: false }
})
function retryMonitors() {
  uptime.refetch()
  cron.refetch()
}

// Busiest open issues not already listed above, so each issue appears once.
const busiestRest = computed(() => {
  const shown = new Set([...regressed.value, ...fresh.value].map(i => i.id))
  return props.busiest.filter(i => !shown.has(i.id)).sort((a, b) => b.event_count - a.event_count).slice(0, 5)
})

const lede = computed(() => {
  if (props.lastVisit === null || issuesUnknown.value) return ''
  const ago = formatRel(new Date(props.lastVisit).toISOString())
  const parts = sinceVisitParts({
    monitors: broken.value.filter(b => isUnseen(b.since, props.lastVisit)).length,
    regressed: attentionPage.value?.unseen_regressed ?? 0,
    fresh: attentionPage.value?.unseen_new ?? 0,
  })
  return parts.length ? `Since you last looked ${ago}: ${joinParts(parts)}.` : `Nothing new since you last looked ${ago}.`
})

const multiProject = computed(() => projects.projects.length > 1)
const projectNames = computed(() => new Map(projects.projects.map(p => [p.id, p.name])))
function projectName(id: string) {
  return multiProject.value ? projectNames.value.get(id) ?? null : null
}

function brokenMeta(b: BrokenMonitor): string[] {
  const parts = [projectName(b.monitor.project_id)]
  if (b.kind === 'uptime') {
    const m = b.monitor
    parts.push(m.last_status_code ? `HTTP ${m.last_status_code}` : m.last_error)
    if (m.consecutive_failures > 1) parts.push(`${m.consecutive_failures} failed checks in a row`)
  } else {
    const m = b.monitor
    parts.push(m.last_ok_at ? `last ok ${formatRel(m.last_ok_at)}` : 'no successful run yet')
  }
  return parts.filter((p): p is string => !!p)
}

function issueMeta(issue: AttentionIssue): string[] {
  return [
    projectName(issue.project_id),
    issue.first_release ? `first seen in ${issue.first_release}` : null,
    issue.status === 'regressed' ? null : plural(issue.event_count, 'event', 'events'),
    issue.user_count > 0 ? plural(issue.user_count, 'user', 'users') : null,
    issue.status === 'regressed' ? (issue.assignee ?? 'unassigned') : issue.assignee,
  ].filter((p): p is string => !!p)
}

function historySlots(history: BrokenMonitor['history']) {
  const recent = history.slice(-20)
  return [...Array(20 - recent.length).fill('empty'), ...recent] as string[]
}

const monitorLink = (b: BrokenMonitor) => investigation.link(b.kind === 'uptime' ? '/monitors/uptime' : '/monitors/cron')
</script>

<template>
  <section class="brief" aria-labelledby="brief-verdict">
    <header class="brief__head">
      <div v-if="briefingLoading" class="brief__verdict-skel" role="status" aria-label="Loading briefing">
        <span class="skel" style="width: 62%; height: 30px" />
        <span class="skel" style="width: 44%; height: 14px" />
      </div>
      <template v-else>
        <h1 id="brief-verdict" class="brief__verdict">
          <template v-if="v.clear">
            <Icon name="check-circle" :size="26" class="brief__clear-icon" />
            All clear.
          </template>
          <template v-else>
            <span v-if="v.broken" class="brief__verdict-broken">{{ v.broken }}</span>{{ v.broken && v.changed ? ' ' : '' }}<span v-if="v.changed" class="brief__verdict-changed">{{ v.changed }}</span>
          </template>
        </h1>
        <p v-if="v.clear" class="brief__lede">
          No monitors failing and no new or regressed issues {{ windowPhrase }}.
        </p>
        <p v-else-if="lede" class="brief__lede">{{ lede }}</p>
      </template>
    </header>

    <QueryFeedback
      resource="monitors"
      :failed="!!uptime.isError.value || !!cron.isError.value"
      :has-data="uptimeMonitors !== undefined && cronMonitors !== undefined"
      :refreshing="uptimeFetching || cronFetching"
      @retry="retryMonitors"
    />
    <QueryFeedback
      resource="new and regressed issues"
      :failed="!!attention.isError.value"
      :has-data="attentionPage !== undefined"
      :refreshing="attention.isFetching.value"
      :paused="attention.fetchStatus.value === 'paused'"
      :updated-at="attention.dataUpdatedAt.value"
      @retry="attention.refetch()"
    />

    <!-- Broken now ─────────────────────────────────────────────────────────── -->
    <section v-if="broken.length" class="brief__group brief__group--broken" aria-labelledby="brief-broken">
      <h2 id="brief-broken" class="brief__group-title">Broken now <span class="brief__count">{{ broken.length }}</span></h2>
      <ul class="brief__rows">
        <li v-for="b in broken" :key="`${b.kind}-${b.monitor.id}`">
          <RouterLink :to="monitorLink(b)" class="brief-row" :class="{ 'brief-row--unseen': isUnseen(b.since, lastVisit) }">
            <span class="brief-chip brief-chip--danger">{{ b.kind === 'uptime' ? 'Uptime' : 'Cron' }}</span>
            <span class="brief-row__main">
              <span class="brief-row__title">
                <span v-if="isUnseen(b.since, lastVisit)" class="brief-row__new"><span class="sr-only">New since your last visit:</span></span>
                {{ brokenTitle(b) }}
              </span>
              <span class="brief-row__meta">{{ brokenMeta(b).join(' · ') }}</span>
            </span>
            <span class="brief-row__viz brief-history" role="img" :aria-label="`Last ${b.history.length} checks`">
              <i v-for="(s, i) in historySlots(b.history)" :key="i" :class="`brief-history__tick--${s}`" />
            </span>
            <span class="brief-row__age brief-row__age--danger" :title="b.since ? `Failing since ${new Date(b.since).toLocaleString()}` : undefined">
              {{ formatSpan(b.since) }}
            </span>
          </RouterLink>
        </li>
      </ul>
    </section>

    <!-- New or regressed ───────────────────────────────────────────────────── -->
    <section v-if="regressed.length || fresh.length" class="brief__group" aria-labelledby="brief-changed">
      <h2 id="brief-changed" class="brief__group-title">
        New or regressed <span class="brief__count">{{ changedTotal }}</span>
      </h2>
      <ul class="brief__rows">
        <li v-for="issue in [...regressed, ...fresh]" :key="issue.id">
          <RouterLink
            :to="investigation.link(`/issues/${issue.id}`)"
            class="brief-row"
            :class="{ 'brief-row--unseen': isUnseen(issue.status === 'regressed' ? issue.regressed_at : issue.first_seen, lastVisit) }"
          >
            <span v-if="issue.status === 'regressed'" class="brief-chip brief-chip--warning">Regressed</span>
            <span v-else class="brief-chip brief-chip--accent">New</span>
            <span class="brief-row__main">
              <span class="brief-row__title">
                <span
                  v-if="isUnseen(issue.status === 'regressed' ? issue.regressed_at : issue.first_seen, lastVisit)"
                  class="brief-row__new"
                ><span class="sr-only">New since your last visit:</span></span>
                <span v-if="splitTitle(issue.title).type" class="brief-row__type mono">{{ splitTitle(issue.title).type }}</span><template v-if="splitTitle(issue.title).type">: </template>{{ splitTitle(issue.title).message }}
              </span>
              <span class="brief-row__meta">{{ issueMeta(issue).join(' · ') }}</span>
            </span>
            <span class="brief-row__viz">
              <Sparkline
                v-if="issue.sparkline?.length"
                :data="issue.sparkline"
                :width="132"
                :height="20"
                :color="issue.status === 'regressed' ? 'var(--warning)' : 'var(--accent)'"
              />
            </span>
            <span class="brief-row__age">
              {{ formatRel(issue.status === 'regressed' && issue.regressed_at ? issue.regressed_at : issue.first_seen) }}
            </span>
          </RouterLink>
        </li>
      </ul>
      <RouterLink v-if="hiddenChanged > 0" :to="investigation.link('/issues?sort=first_seen')" class="brief__more">
        {{ hiddenChanged }} more, newest first <Icon name="arrow-right" :size="12" />
      </RouterLink>
    </section>

    <!-- Busiest ongoing ────────────────────────────────────────────────────── -->
    <section v-if="!loading && busiestRest.length" class="brief__group brief__group--quiet" aria-labelledby="brief-busiest">
      <h2 id="brief-busiest" class="brief__group-title">Busiest open issues</h2>
      <ul class="brief__rows">
        <li v-for="issue in busiestRest" :key="issue.id">
          <RouterLink :to="investigation.link(`/issues/${issue.id}`)" class="brief-row brief-row--quiet">
            <span :class="`leveldot leveldot--${issue.level}`" />
            <span class="brief-row__main">
              <span class="brief-row__title">
                <span v-if="splitTitle(issue.title).type" class="brief-row__type mono">{{ splitTitle(issue.title).type }}</span><template v-if="splitTitle(issue.title).type">: </template>{{ splitTitle(issue.title).message }}
              </span>
            </span>
            <span class="brief-row__viz">
              <Sparkline v-if="issue.sparkline?.length" :data="issue.sparkline" :width="132" :height="16" color="var(--text-3)" />
            </span>
            <span class="brief-row__age mono">{{ fmt(issue.event_count) }}</span>
          </RouterLink>
        </li>
      </ul>
    </section>

    <p v-if="!briefingLoading && !loading" class="brief__calm">
      <template v-if="v.clear">{{ plural(openCount, 'open issue', 'open issues') }}<template v-if="healthy">, {{ plural(healthy, 'monitor', 'monitors') }} healthy</template>.</template>
      <template v-else>Everything else is steady: {{ plural(openCount, 'open issue', 'open issues') }} in total<template v-if="healthy">, {{ plural(healthy, 'monitor', 'monitors') }} healthy</template>.</template>
      <RouterLink :to="investigation.link('/issues')" class="brief__calm-link">All issues <Icon name="arrow-right" :size="12" /></RouterLink>
    </p>
  </section>
</template>

<style scoped>
.brief {
  padding: 28px 32px 36px;
  min-width: 0;
}

/* ── Verdict ────────────────────────────────────────────────────────────────── */

.brief__head { max-width: 62rem; }

.brief__verdict {
  margin: 0;
  font-size: clamp(1.625rem, 1.2rem + 1.2vw, 2.125rem);
  font-weight: 600;
  line-height: 1.15;
  letter-spacing: -0.022em;
  color: var(--text-1);
  text-wrap: balance;
}

.brief__verdict-broken { color: var(--danger); }
.brief__verdict-changed { color: var(--text-1); }
.brief__clear-icon { color: var(--success); vertical-align: -3px; margin-right: 4px; }

.brief__lede {
  margin: 8px 0 0;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--text-2);
  max-width: 70ch;
}

.brief__verdict-skel { display: flex; flex-direction: column; gap: 12px; }

.brief :deep(.query-feedback) { margin: 20px 0 0; }

/* ── Groups ─────────────────────────────────────────────────────────────────── */

.brief__group { margin-top: 32px; }
.brief__group + .brief__group--quiet { margin-top: 40px; }

.brief__group-title {
  margin: 0 -12px;
  padding: 0 12px 10px;
  border-bottom: 1px solid var(--border);
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--text-1);
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.brief__group--quiet .brief__group-title { color: var(--text-2); }

.brief__count {
  font-family: var(--mono);
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
}

.brief__rows { list-style: none; margin: 0; padding: 0; }

/* ── Rows ───────────────────────────────────────────────────────────────────── */

.brief-row {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr) 132px 72px;
  align-items: center;
  gap: 16px;
  padding: 13px 12px;
  margin: 0 -12px;
  border-bottom: 1px solid var(--border-soft);
  color: inherit;
  text-decoration: none;
  transition: background-color 120ms cubic-bezier(0.16, 1, 0.3, 1);
}

.brief-row:hover { background: var(--surface-2); }
.brief-row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

/* Broken monitors outrank everything else on the page, so they get more room
   and weight; new and regressed issues step down from them. */
.brief__group--broken .brief-row { padding-block: 16px; }
.brief__group--broken .brief-row__title { font-size: var(--text-lg); font-weight: 600; text-wrap: pretty; }
.brief__group--broken .brief-history { height: 22px; }

.brief-row--quiet {
  grid-template-columns: 88px minmax(0, 1fr) 132px 72px;
  padding-block: 10px;
}

.brief-row--quiet .leveldot { justify-self: center; }

.brief-chip {
  justify-self: start;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.02em;
  line-height: 1;
  padding: 5px 7px;
  border-radius: 4px;
  white-space: nowrap;
}

/* Chip text leans toward the foreground so 11px labels stay legible on their tint. */
.brief-chip--danger  { color: color-mix(in oklch, var(--danger) 82%, var(--text-1));  background: oklch(from var(--danger) l c h / 0.13); }
.brief-chip--warning { color: color-mix(in oklch, var(--warning) 72%, var(--text-1)); background: oklch(from var(--warning) l c h / 0.15); }
.brief-chip--accent  { color: color-mix(in oklch, var(--accent) 85%, var(--text-1));  background: var(--accent-soft); }

.brief-row__main { display: flex; flex-direction: column; gap: 3px; min-width: 0; }

.brief-row__title {
  font-size: var(--text-base);
  font-weight: 500;
  color: var(--text-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.brief-row--quiet .brief-row__title { font-size: var(--text-sm); font-weight: 400; color: var(--text-2); }

.brief-row__type { color: var(--accent); font-size: 0.93em; }

.brief-row__new {
  display: inline-block;
  width: 7px;
  height: 7px;
  margin-right: 7px;
  border-radius: 50%;
  background: var(--accent);
  vertical-align: 2px;
}

.brief-row__meta {
  font-size: var(--text-xs);
  /* Between text-3 and text-2: quieter than the title, but readable at xs in light mode. */
  color: color-mix(in oklch, var(--text-3) 50%, var(--text-2));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.brief-row__viz { display: flex; align-items: center; justify-content: flex-start; }

.brief-history { gap: 2px; height: 18px; }
.brief-history i { flex: 1; height: 100%; border-radius: 1px; background: var(--border-soft); }
.brief-history .brief-history__tick--ok { background: oklch(from var(--success) l c h / 0.55); }
.brief-history .brief-history__tick--bad { background: var(--danger); }
.brief-history .brief-history__tick--pending { background: var(--warning); }

.brief-row__age {
  justify-self: end;
  font-family: var(--mono);
  font-size: var(--text-xs);
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
  white-space: nowrap;
}

.brief-row__age--danger { color: var(--danger); font-weight: 500; }

.brief__more,
.brief__calm-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--accent);
  font-size: var(--text-sm);
  text-decoration: none;
}

.brief__more { margin-top: 12px; }
.brief__more:hover,
.brief__calm-link:hover { text-decoration: underline; text-underline-offset: 3px; }

.brief__calm {
  margin: 28px 0 0;
  font-size: var(--text-sm);
  color: var(--text-3);
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 12px;
}

@media (max-width: 768px) {
  .brief { padding: 20px 16px 28px; }
  .brief-row,
  .brief-row--quiet {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas: "chip age" "main main";
    gap: 6px 12px;
    margin: 0 -8px;
    padding: 12px 8px;
  }
  .brief-chip { grid-area: chip; }
  .brief-row--quiet { grid-template-columns: 8px minmax(0, 1fr) auto; grid-template-areas: "dot main age"; align-items: start; }
  .brief-row--quiet .leveldot { grid-area: dot; margin-top: 6px; }
  .brief-row__main { grid-area: main; }
  .brief-row__age { grid-area: age; }
  .brief-row__viz { display: none; }
  .brief-row__title { white-space: normal; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
}

@media (prefers-reduced-motion: reduce) {
  .brief-row { transition: none; }
}
</style>
