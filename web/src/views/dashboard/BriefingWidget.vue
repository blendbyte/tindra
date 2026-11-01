<script setup lang="ts">
import { computed } from 'vue'
import { useInvestigationStore } from '@/stores/investigation'
import { useProjectsStore } from '@/stores/projects'
import { useFormatters } from '@/composables/useFormatters'
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import QueryFeedback from '@/components/QueryFeedback.vue'
import CheckHistory from './CheckHistory.vue'
import { useWindowLabel } from './shared'
import { degradedVitals, type PerformanceOverview } from './overview'
import { plural } from '@/utils/plural'
import { useNow } from '@/composables/useNow'
import { useAttentionQuery, useMonitorQueries } from '@/composables/useBriefingData'
import {
  brokenMonitors, brokenTitle, formatSpan, healthyMonitorCount, isUnseen, joinParts,
  sinceVisitParts, splitTitle, verdict,
  type AttentionIssue, type BrokenMonitor,
} from './briefing'

const props = defineProps<{
  loading: boolean
  openCount: number
  lastVisit: number | null
  overview?: PerformanceOverview
}>()

const investigation = useInvestigationStore()
const projects = useProjectsStore()
const { formatRel } = useFormatters()
// Keeps outage lengths and relative times counting between refreshes.
const now = useNow()
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
// Rows past the briefing's five per group, each linked to the issue list
// filtered to exactly that group.
const hiddenRegressed = computed(() => Math.max(0, (attentionPage.value?.regressed_total ?? 0) - regressed.value.length))
const hiddenNew = computed(() => Math.max(0, (attentionPage.value?.new_total ?? 0) - fresh.value.length))

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

// Nothing failing and nothing new, yet requests can still be failing or slow:
// that is not "all clear", so the verdict names what is out of range.
const outOfRange = computed(() => degradedVitals(props.overview?.total))
const degraded = computed(() => v.value.clear ? outOfRange.value : [])

const lede = computed(() => {
  if (props.lastVisit === null || issuesUnknown.value) return ''
  void now.value
  const ago = formatRel(new Date(props.lastVisit).toISOString())
  const parts = sinceVisitParts({
    monitors: broken.value.filter(b => isUnseen(b.since, props.lastVisit)).length,
    regressed: attentionPage.value?.unseen_regressed ?? 0,
    fresh: attentionPage.value?.unseen_new ?? 0,
  })
  if (parts.length) return `Since you last looked ${ago}: ${joinParts(parts)}.`
  // Without monitor data, "nothing new" would speak for monitors nobody checked.
  return monitorsUnknown.value ? '' : `Nothing new since you last looked ${ago}.`
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

const monitorLink = (b: BrokenMonitor) => investigation.link(b.kind === 'uptime' ? '/monitors/uptime' : '/monitors/cron')
</script>

<template>
  <section class="brief" aria-labelledby="brief-verdict">
    <header class="brief__head">
      <div v-if="briefingLoading" class="brief__verdict-skel" role="status" aria-label="Loading briefing">
        <span class="skel" style="width: 62%; height: 30px" />
        <span class="skel" style="width: 44%; height: 14px" />
      </div>
      <div v-else-if="v.clear" class="brief__calm-head">
        <h1 id="brief-verdict" class="brief__verdict brief__verdict--calm">
          <template v-if="degraded.length">Nothing is broken.</template>
          <template v-else>
            <Icon name="check-circle" :size="20" class="brief__clear-icon" />
            All clear.
          </template>
        </h1>
        <p class="brief__lede">
          No monitors failing and no new or regressed issues {{ windowPhrase }}.
          <span v-if="degraded.length" class="brief__lede-line">
            Outside the healthy range:
            <template v-for="(d, i) in degraded" :key="d.key">{{ i ? ', ' : '' }}{{ d.label }} <span class="brief__degraded" :class="`brief__degraded--${d.tone}`">{{ d.value }}</span></template>.
          </span>
        </p>
      </div>
      <template v-else>
        <h1 id="brief-verdict" class="brief__verdict">
          <span v-if="v.broken" class="brief__verdict-broken">{{ v.broken }}</span>{{ v.broken && v.changed ? ' ' : '' }}<span v-if="v.changed" class="brief__verdict-changed">{{ v.changed }}</span>
        </h1>
        <p v-if="lede" class="brief__lede">{{ lede }}</p>
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
            <span class="brief-row__viz"><CheckHistory :history="b.history" /></span>
            <span class="brief-row__age brief-row__age--danger" :title="b.since ? `Failing since ${new Date(b.since).toLocaleString()}` : undefined">
              {{ formatSpan(b.since, now) }}
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
      <div v-if="hiddenRegressed || hiddenNew" class="brief__more-links">
        <RouterLink v-if="hiddenRegressed" :to="investigation.link('/issues?status=Regressed')" class="brief__more">
          {{ hiddenRegressed }} more regressed <Icon name="arrow-right" :size="12" />
        </RouterLink>
        <RouterLink v-if="hiddenNew" :to="investigation.link('/issues?status=New')" class="brief__more">
          {{ hiddenNew }} more new <Icon name="arrow-right" :size="12" />
        </RouterLink>
      </div>
    </section>

    <p v-if="!v.clear && !briefingLoading && !loading" class="brief__calm">
      <template v-if="!outOfRange.length">Everything else is steady: </template>{{ plural(openCount, 'open issue', 'open issues') }} in total<template v-if="healthy">, {{ plural(healthy, 'monitor', 'monitors') }} healthy</template>.
      <RouterLink :to="investigation.link('/issues')" class="brief__calm-link">All issues <Icon name="arrow-right" :size="12" /></RouterLink>
    </p>
  </section>
</template>

<style scoped>
.brief { min-width: 0; }

/* ── Verdict ────────────────────────────────────────────────────────────────── */

.brief__head { max-width: 62rem; padding-inline: 16px; }

/* Nothing to report takes one line, so the overview starts right below it. */
.brief__calm-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 14px;
}

.brief__verdict {
  margin: 0;
  font-size: clamp(1.625rem, 1.2rem + 1.2vw, 2.125rem);
  font-weight: 600;
  line-height: 1.15;
  letter-spacing: -0.022em;
  color: var(--text-1);
  text-wrap: balance;
}

.brief__verdict--calm {
  flex: none;
  font-size: var(--text-xl);
  letter-spacing: -0.02em;
}

.brief__verdict-broken { color: var(--danger); }
.brief__verdict-changed { color: var(--text-1); }
.brief__clear-icon { display: inline-block; vertical-align: -3px; margin-right: 6px; color: var(--success); }

.brief__lede {
  margin: 8px 0 0;
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--text-2);
  max-width: 70ch;
}

.brief__calm-head .brief__lede { margin: 0; flex: 1 1 28rem; }
.brief__lede-line { display: block; }
.brief__degraded { font-family: var(--mono); font-size: 0.93em; font-weight: 500; font-variant-numeric: tabular-nums; }
.brief__degraded--danger { color: var(--danger); }
.brief__degraded--warning { color: var(--warning); }

.brief__verdict-skel { display: flex; flex-direction: column; gap: 12px; }

.brief :deep(.query-feedback) { margin: 20px 0 0; }

/* ── Groups ─────────────────────────────────────────────────────────────────── */

.brief__group { margin-top: 32px; }

.brief__group-title {
  margin: 0;
  padding: 0 16px 10px;
  border-bottom: 1px solid var(--border);
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--text-1);
  display: flex;
  align-items: baseline;
  gap: 8px;
}

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
  padding: 13px 16px;
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
.brief__group--broken .brief-row__viz :deep(.check-history) { height: 22px; }

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

.brief__more-links { display: flex; flex-wrap: wrap; gap: 4px 20px; margin-top: 12px; padding-inline: 16px; }
.brief__more:hover,
.brief__calm-link:hover { text-decoration: underline; text-underline-offset: 3px; }

.brief__calm {
  margin: 20px 0 0;
  padding-inline: 16px;
  font-size: var(--text-sm);
  color: var(--text-3);
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 12px;
}

@media (max-width: 768px) {
  .brief-row {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas: "chip age" "main main";
    gap: 6px 12px;
    padding: 12px 16px;
  }
  .brief-chip { grid-area: chip; }
  .brief-row__main { grid-area: main; }
  .brief-row__age { grid-area: age; }
  .brief-row__viz { display: none; }
  .brief-row__title { white-space: normal; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
}

@media (prefers-reduced-motion: reduce) {
  .brief-row { transition: none; }
}
</style>
