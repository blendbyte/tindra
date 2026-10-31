<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { useFormatters } from '@/composables/useFormatters'
import { apiFetch } from '@/api/client'
import type { AlertRule, AlertFiring, AlertTrigger, AlertChannel } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { usePermissions, useProjectByID, useProjects } from './shared'
import { alertVerdict } from './alertState'

const { show: showToast } = useToast()
const { formatRel } = useFormatters()
const qc = useQueryClient()
const router = useRouter()
const route = useRoute()
const { canManageAlerts } = usePermissions()

// Alert rules state (UI-only for create)
const showNewRule = ref(false)
const expandedRule = ref<string | null>(null)

const newRule = reactive({
  projectIDs: [] as string[],
  name: '',
  trigger: 'new_issue' as AlertTrigger,
  threshold: 100,
  window_mins: 60,
  channel: 'webhook' as AlertChannel,
  webhook_url: '',
  email_to: '',
  cooldown_mins: 60,
  filter_level: '',
  filter_environment: '',
  min_occurrences: 0,
  filter_search: '',
})

function resetNewRule() {
  newRule.projectIDs = []
  newRule.name = ''
  newRule.trigger = 'new_issue'
  newRule.threshold = 100
  newRule.window_mins = 60
  newRule.channel = 'webhook'
  newRule.webhook_url = ''
  newRule.email_to = ''
  newRule.cooldown_mins = 60
  newRule.filter_level = ''
  newRule.filter_environment = ''
  newRule.min_occurrences = 0
  newRule.filter_search = ''
}

const { projects: alertProjects } = useProjects()
const projectByID = useProjectByID(alertProjects)

const { data: alertRulesData } = useQuery({
  queryKey: ['alert-rules'],
  queryFn: ({ signal }) => apiFetch<{ rules: AlertRule[] }>('/api/alert-rules', { signal }).then(r => r.rules ?? []),
})

const alertRules = computed(() => alertRulesData.value ?? [])
const verdict = computed(() => alertVerdict(alertRules.value))

function firingTriggerLabel(trigger: string): string {
  const labels: Record<string, string> = {
    new_issue: 'New issue',
    regressed: 'Regression',
    new_or_regressed: 'New issue / regression',
    event_count: 'Event count',
    log_count: 'Log count',
    cron_missed: 'Cron missed',
    cron_error: 'Cron error',
    uptime_down: 'Uptime down',
    uptime_recovered: 'Uptime recovered',
    issue_auto_resolved: 'Auto-resolved',
  }
  return labels[trigger] ?? trigger
}

function ruleProjectNames(rule: AlertRule): string {
  if (!rule.project_ids?.length) return rule.is_global === false ? 'No projects (inactive)' : 'Global'
  return rule.project_ids.map(id => projectByID.value.get(id)?.name ?? id).join(', ')
}

function triggerLabel(rule: AlertRule): string {
  if (rule.trigger === 'new_issue') return 'New issue'
  if (rule.trigger === 'regressed') return 'Regression'
  if (rule.trigger === 'new_or_regressed') return 'New issue or regression'
  if (rule.trigger === 'event_count') return `>${rule.threshold} events in ${rule.window_mins}m`
  if (rule.trigger === 'log_count') return `>${rule.threshold} logs in ${rule.window_mins}m`
  if (rule.trigger === 'cron_missed') return 'Cron monitor missed'
  if (rule.trigger === 'cron_error') return 'Cron monitor error'
  if (rule.trigger === 'uptime_down') return 'Uptime monitor down'
  if (rule.trigger === 'uptime_recovered') return 'Uptime monitor recovered'
  return rule.trigger
}

function openNewRule() {
  showNewRule.value = !showNewRule.value
}

function queryParam(v: unknown): string {
  if (Array.isArray(v)) return String(v[0] ?? '')
  return typeof v === 'string' ? v : ''
}
function queryParamList(v: unknown): string[] {
  if (Array.isArray(v)) return v.filter((x): x is string => typeof x === 'string' && x !== '')
  if (typeof v === 'string' && v) return [v]
  return []
}

function applyLogCountDefaults() {
  newRule.threshold = 10
  newRule.window_mins = 5
  if (!['fatal', 'error', 'warning'].includes(newRule.filter_level)) {
    newRule.filter_level = 'error'
  }
}

watch(() => newRule.trigger, (t, prev) => {
  if (t === 'log_count' && prev !== 'log_count') applyLogCountDefaults()
})

// Links into the alerts page can open the new-rule form via ?new=1,
// prefilled from the query, then drop the query from the URL.
function applyAlertQueryPrefill() {
  if (route.query.new !== '1') return
  showNewRule.value = true
  if (queryParam(route.query.trigger) === 'log_count') {
    newRule.trigger = 'log_count'
    applyLogCountDefaults()
    const level = queryParam(route.query.level)
    if (['fatal', 'error', 'warning'].includes(level)) newRule.filter_level = level
    newRule.filter_environment = queryParam(route.query.environment)
    newRule.filter_search = queryParam(route.query.search)
    const pids = queryParamList(route.query.project_id)
    if (pids.length) newRule.projectIDs = pids
  }
  router.replace({ name: 'alerts' })
}

onMounted(applyAlertQueryPrefill)
watch(() => route.query.new, applyAlertQueryPrefill)

const { mutate: createAlertRule, isPending: creatingRule } = useMutation({
  mutationFn: () => {
    const body: Record<string, unknown> = {
      name: newRule.name,
      trigger: newRule.trigger,
      channel: newRule.channel,
      cooldown_mins: Number(newRule.cooldown_mins),
      project_ids: newRule.projectIDs,
    }
    if (newRule.trigger === 'log_count' && newRule.projectIDs.length === 0) {
      throw new Error('Pick at least one project for a log count alert')
    }
    if (newRule.trigger === 'event_count' || newRule.trigger === 'log_count') {
      body.threshold = Number(newRule.threshold)
      body.window_mins = Number(newRule.window_mins)
    }
    if (newRule.channel === 'email') {
      body.email_to = newRule.email_to
    } else {
      body.webhook_url = newRule.webhook_url
    }
    if (newRule.filter_level) body.filter_level = newRule.filter_level
    if (newRule.filter_environment) body.filter_environment = newRule.filter_environment
    if (newRule.min_occurrences > 0) body.min_occurrences = Number(newRule.min_occurrences)
    if (newRule.trigger === 'log_count') body.filter_search = newRule.filter_search.trim() || null
    return apiFetch<AlertRule>('/api/alert-rules', {
      method: 'POST',
      body: JSON.stringify(body),
    })
  },
  onSuccess: () => {
    showNewRule.value = false
    resetNewRule()
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    showToast('Alert rule created')
  },
  onError: (err: unknown) => showToast(err instanceof Error ? err.message : 'Failed to create rule'),
})

const { mutate: deleteAlertRule } = useMutation({
  mutationFn: ({ id }: { id: string }) =>
    apiFetch(`/api/alert-rules/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    showToast('Rule deleted')
  },
})

const { mutate: toggleAlertRule } = useMutation({
  mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
    apiFetch(`/api/alert-rules/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ enabled }),
    }),
  onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
})

const testingRuleID = ref<string | null>(null)

const { mutate: testAlertRule } = useMutation({
  mutationFn: ({ id }: { id: string }) =>
    apiFetch(`/api/alert-rules/${id}/test`, { method: 'POST' }),
  onMutate: ({ id }) => { testingRuleID.value = id },
  onSettled: () => { testingRuleID.value = null },
  onSuccess: () => showToast('Test alert sent'),
  onError: (err: unknown) => showToast(err instanceof Error ? err.message : 'Test failed'),
})

const editingRuleID = ref<string | null>(null)
const editRule = reactive({
  projectIDs: [] as string[],
  name: '',
  trigger: 'new_issue' as AlertTrigger,
  threshold: 100,
  window_mins: 60,
  channel: 'webhook' as AlertChannel,
  webhook_url: '',
  email_to: '',
  cooldown_mins: 60,
  filter_level: '',
  filter_environment: '',
  min_occurrences: 0,
  filter_search: '',
})

function startEditRule(rule: AlertRule) {
  editingRuleID.value = rule.id
  editRule.projectIDs = rule.project_ids ? [...rule.project_ids] : []
  editRule.name = rule.name
  editRule.trigger = rule.trigger
  editRule.threshold = rule.threshold ?? (rule.trigger === 'log_count' ? 10 : 100)
  editRule.window_mins = rule.window_mins ?? (rule.trigger === 'log_count' ? 5 : 60)
  editRule.channel = rule.channel
  editRule.webhook_url = rule.webhook_url ?? ''
  editRule.email_to = rule.email_to ?? ''
  editRule.cooldown_mins = rule.cooldown_mins
  editRule.filter_level = rule.filter_level ?? ''
  editRule.filter_environment = rule.filter_environment ?? ''
  editRule.min_occurrences = rule.min_occurrences ?? 0
  editRule.filter_search = rule.filter_search ?? ''
}

watch(() => editRule.trigger, (t, prev) => {
  if (t === 'log_count' && prev !== 'log_count') {
    if (editRule.window_mins > 60 || editRule.window_mins < 1) editRule.window_mins = 5
    if (!['fatal', 'error', 'warning'].includes(editRule.filter_level)) {
      editRule.filter_level = 'error'
    }
  }
})

function cancelEditRule() {
  editingRuleID.value = null
}

const { mutate: saveAlertRule, isPending: savingRule } = useMutation({
  mutationFn: ({ id }: { id: string }) => {
    const body: Record<string, unknown> = {
      name: editRule.name,
      trigger: editRule.trigger,
      channel: editRule.channel,
      cooldown_mins: Number(editRule.cooldown_mins),
      filter_level: editRule.filter_level || null,
      filter_environment: editRule.filter_environment || null,
      min_occurrences: editRule.min_occurrences > 0 ? Number(editRule.min_occurrences) : null,
      project_ids: editRule.projectIDs,
    }
    if (editRule.trigger === 'log_count' && editRule.projectIDs.length === 0) {
      throw new Error('Pick at least one project for a log count alert')
    }
    if (editRule.trigger === 'event_count' || editRule.trigger === 'log_count') {
      body.threshold = Number(editRule.threshold)
      body.window_mins = Number(editRule.window_mins)
    }
    if (editRule.channel === 'email') {
      body.email_to = editRule.email_to
    } else {
      body.webhook_url = editRule.webhook_url
    }
    body.filter_search = editRule.trigger === 'log_count' ? (editRule.filter_search.trim() || null) : null
    return apiFetch(`/api/alert-rules/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    })
  },
  onSuccess: () => {
    editingRuleID.value = null
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    showToast('Rule updated')
  },
  onError: (err: unknown) => showToast(err instanceof Error ? err.message : 'Failed to save rule'),
})

const { data: expandedRuleFiringsData } = useQuery({
  queryKey: computed(() => ['alert-firings', expandedRule.value]),
  queryFn: ({ signal }) => apiFetch<{ firings: AlertFiring[] }>(`/api/alert-rules/${expandedRule.value}/firings`, { signal }).then(r => r.firings ?? []),
  enabled: computed(() => expandedRule.value !== null),
})
const expandedRuleFirings = computed(() => expandedRuleFiringsData.value ?? [])

const logPreviewSource = computed(() => {
  const rule = editingRuleID.value ? editRule : newRule
  const formOpen = editingRuleID.value ? true : showNewRule.value
  if (!formOpen || rule.trigger !== 'log_count' || !rule.projectIDs.length || !rule.filter_level) return ''
  const p = new URLSearchParams()
  for (const id of rule.projectIDs) p.append('project_id', id)
  p.set('level', rule.filter_level)
  p.set('window_mins', String(rule.window_mins || 5))
  if (rule.filter_environment) p.set('environment', rule.filter_environment)
  if (rule.filter_search.trim()) p.set('search', rule.filter_search.trim())
  return p.toString()
})

const logPreviewKey = ref('')
let previewTimer: ReturnType<typeof setTimeout>
watch(logPreviewSource, (v) => {
  clearTimeout(previewTimer)
  previewTimer = setTimeout(() => { logPreviewKey.value = v }, 300)
}, { immediate: true })
onUnmounted(() => clearTimeout(previewTimer))

const { data: logPreview } = useQuery({
  queryKey: computed(() => ['logs-count', logPreviewKey.value]),
  queryFn: ({ signal }) => apiFetch<{ count: number; window_mins: number }>(`/api/logs/count?${logPreviewKey.value}`, { signal }),
  enabled: computed(() => logPreviewKey.value !== ''),
})
</script>

<template>
  <div class="pane-head">
    <div>
      <div class="pane-head__title">Alert rules</div>
      <div class="pane-head__sub" :class="{ 'pane-head__sub--warning': verdict.silent }">{{ verdict.text }}</div>
    </div>
    <button v-if="canManageAlerts" class="btn btn--primary" @click="openNewRule()">
      <Icon name="plus" :size="12" />
      New rule
    </button>
  </div>

  <div v-if="showNewRule" class="alert-create">
    <div class="alert-create__title">Create alert rule</div>
    <div class="alert-create__grid">
      <div class="field">
        <label class="field__label">Name</label>
        <input v-model="newRule.name" class="field__input" placeholder="e.g. High error rate" />
      </div>
      <div v-if="alertProjects.length > 0" class="field" style="grid-column: 1 / -1">
        <label class="field__label">Projects <span style="font-weight: normal; color: var(--text-2)">{{ newRule.trigger === 'log_count' ? '(at least one required)' : '(none = global)' }}</span></label>
        <div class="rule__project-checks">
          <label v-for="p in alertProjects" :key="p.id" class="rule__project-check">
            <input type="checkbox" :value="p.id" v-model="newRule.projectIDs" />
            {{ p.name }}
          </label>
        </div>
      </div>
      <div class="field">
        <label class="field__label">Trigger</label>
        <select v-model="newRule.trigger" class="field__input">
          <option value="new_issue">New issue</option>
          <option value="regressed">Regression (resolved issue breaks again)</option>
          <option value="new_or_regressed">New issue or regression</option>
          <option value="event_count">Event count above threshold</option>
          <option value="log_count">Log count above threshold</option>
          <option value="cron_missed">Cron monitor missed check-in</option>
          <option value="cron_error">Cron monitor check-in error</option>
          <option value="uptime_down">Uptime monitor went down</option>
          <option value="uptime_recovered">Uptime monitor recovered</option>
        </select>
      </div>
      <template v-if="newRule.trigger === 'event_count' || newRule.trigger === 'log_count'">
        <div class="field">
          <label class="field__label">{{ newRule.trigger === 'log_count' ? 'Threshold (logs)' : 'Threshold (events)' }}</label>
          <input v-model.number="newRule.threshold" class="field__input" type="number" min="1" />
        </div>
        <div class="field">
          <label class="field__label">Window (minutes)</label>
          <input v-model.number="newRule.window_mins" class="field__input" type="number" min="1" :max="newRule.trigger === 'log_count' ? 60 : undefined" />
        </div>
      </template>
      <div class="field">
        <label class="field__label">Channel</label>
        <select v-model="newRule.channel" class="field__input">
          <option value="webhook">Webhook</option>
          <option value="slack">Slack</option>
          <option value="discord">Discord</option>
          <option value="teams">Microsoft Teams</option>
          <option value="email">Email</option>
        </select>
      </div>
      <div v-if="newRule.channel === 'webhook' || newRule.channel === 'slack' || newRule.channel === 'discord' || newRule.channel === 'teams'" class="field" style="grid-column: 1 / -1">
        <label class="field__label">{{ newRule.channel === 'discord' ? 'Discord webhook URL' : newRule.channel === 'slack' ? 'Slack webhook URL' : newRule.channel === 'teams' ? 'Teams webhook URL' : 'Webhook URL' }}</label>
        <input v-model="newRule.webhook_url" class="field__input" type="url" :placeholder="newRule.channel === 'discord' ? 'https://discord.com/api/webhooks/...' : newRule.channel === 'slack' ? 'https://hooks.slack.com/services/...' : newRule.channel === 'teams' ? 'https://outlook.office.com/webhook/...' : 'https://hooks.example.com/...'" />
      </div>
      <div v-else class="field">
        <label class="field__label">Email to</label>
        <input v-model="newRule.email_to" class="field__input" type="email" placeholder="you@example.com" />
      </div>
      <div class="field">
        <label class="field__label">Cooldown (minutes)</label>
        <input v-model.number="newRule.cooldown_mins" class="field__input" type="number" min="1" />
      </div>
      <div class="field">
        <label class="field__label">Min. level</label>
        <select v-model="newRule.filter_level" class="field__input">
          <option v-if="newRule.trigger !== 'log_count'" value="">Any level</option>
          <option value="fatal">Fatal only</option>
          <option value="error">Error and above</option>
          <option value="warning">{{ newRule.trigger === 'log_count' ? 'Warning and above (requires search)' : 'Warning and above' }}</option>
          <option v-if="newRule.trigger !== 'log_count'" value="info">Info and above</option>
          <option v-if="newRule.trigger !== 'log_count'" value="performance">Performance issues only</option>
        </select>
      </div>
      <div class="field">
        <label class="field__label">Environment</label>
        <input v-model="newRule.filter_environment" class="field__input" placeholder="e.g. production" />
      </div>
      <div v-if="newRule.trigger === 'log_count'" class="field" style="grid-column: 1 / -1">
        <label class="field__label">Search</label>
        <input v-model="newRule.filter_search" class="field__input" placeholder="Same as the Logs search box" maxlength="200" />
        <div class="field__hint">Required if min level is warning. Matches the log message.</div>
      </div>
      <div v-if="newRule.trigger !== 'event_count' && newRule.trigger !== 'log_count'" class="field">
        <label class="field__label">Min. occurrences</label>
        <input v-model.number="newRule.min_occurrences" class="field__input" type="number" min="0" placeholder="Any" />
      </div>
    </div>
    <p v-if="newRule.trigger === 'log_count' && logPreview" class="field__hint" style="margin-top: 10px">
      {{ logPreview.count }} matching log{{ logPreview.count === 1 ? '' : 's' }} in the last {{ logPreview.window_mins }}m
      <span v-if="logPreview.count >= Number(newRule.threshold)" style="color: var(--warning)">(this would fire immediately)</span>
    </p>
    <div style="display: flex; gap: 8px; margin-top: 14px">
      <button class="btn btn--primary" :disabled="creatingRule || (newRule.trigger === 'log_count' && newRule.projectIDs.length === 0)" @click="createAlertRule()">
        {{ creatingRule ? 'Creating…' : 'Create rule' }}
      </button>
      <button class="btn btn--ghost" @click="showNewRule = false; resetNewRule()">Cancel</button>
    </div>
  </div>

  <div v-if="alertRules.length === 0 && !showNewRule" style="padding: 32px 0; color: var(--text-2); font-size: var(--text-sm); text-align: center">
    No alert rules configured. Create one to get notified when issues occur.
  </div>

  <div v-for="rule in alertRules" :key="rule.id" class="rule" :class="{ 'rule--paused': !rule.enabled }">
    <div class="rule__head" v-clickable :aria-expanded="expandedRule === rule.id" @click="expandedRule = expandedRule === rule.id ? null : rule.id">
      <button
        v-if="canManageAlerts"
        class="rule__toggle"
        :title="rule.enabled ? 'Disable rule' : 'Enable rule'"
        :aria-label="rule.enabled ? `Disable ${rule.name}` : `Enable ${rule.name}`"
        @click.stop="toggleAlertRule({ id: rule.id, enabled: !rule.enabled })"
      >
        <Icon :name="rule.enabled ? 'check' : 'pause'" :size="11" />
      </button>
      <div class="rule__main">
        <div class="rule__name">
          {{ rule.name }}
          <span v-if="!rule.enabled" class="rule__paused-tag">Paused</span>
        </div>
        <div class="rule__cond">
          {{ triggerLabel(rule) }} · {{ rule.channel }} · {{ ruleProjectNames(rule) }}
        </div>
      </div>
      <div class="rule__channels">
        <span class="rule__chan" :title="rule.channel === 'email' ? (rule.email_to ?? '') : (rule.webhook_url ?? '')">
          <Icon :name="rule.channel === 'discord' ? 'discord' : rule.channel === 'slack' ? 'slack' : rule.channel === 'teams' ? 'teams' : rule.channel === 'webhook' ? 'send' : 'mail'" :size="11" />
        </span>
      </div>
      <div class="rule__stats">
        <span>{{ rule.last_fired_at ? formatRel(rule.last_fired_at) : 'never fired' }}</span>
      </div>
      <span class="rule__caret">
        <Icon :name="expandedRule === rule.id ? 'chevron-down' : 'chevron-right'" :size="11" />
      </span>
    </div>
    <div v-if="expandedRule === rule.id" class="rule__body">
      <!-- Edit form -->
      <template v-if="editingRuleID === rule.id">
        <div class="alert-create__grid">
          <div class="field">
            <label class="field__label">Name</label>
            <input v-model="editRule.name" class="field__input" />
          </div>
          <div v-if="alertProjects.length > 0" class="field" style="grid-column: 1 / -1">
            <label class="field__label">Projects <span style="font-weight: normal; color: var(--text-2)">{{ editRule.trigger === 'log_count' ? '(at least one required)' : '(none = global)' }}</span></label>
            <div class="rule__project-checks">
              <label v-for="p in alertProjects" :key="p.id" class="rule__project-check">
                <input type="checkbox" :value="p.id" v-model="editRule.projectIDs" />
                {{ p.name }}
              </label>
            </div>
          </div>
          <div class="field">
            <label class="field__label">Trigger</label>
            <select v-model="editRule.trigger" class="field__input">
              <option value="new_issue">New issue</option>
              <option value="regressed">Regression (resolved issue breaks again)</option>
              <option value="new_or_regressed">New issue or regression</option>
              <option value="event_count">Event count above threshold</option>
              <option value="log_count">Log count above threshold</option>
              <option value="cron_missed">Cron monitor missed check-in</option>
              <option value="cron_error">Cron monitor check-in error</option>
              <option value="uptime_down">Uptime monitor went down</option>
              <option value="uptime_recovered">Uptime monitor recovered</option>
            </select>
          </div>
          <template v-if="editRule.trigger === 'event_count' || editRule.trigger === 'log_count'">
            <div class="field">
              <label class="field__label">{{ editRule.trigger === 'log_count' ? 'Threshold (logs)' : 'Threshold (events)' }}</label>
              <input v-model.number="editRule.threshold" class="field__input" type="number" min="1" />
            </div>
            <div class="field">
              <label class="field__label">Window (minutes)</label>
              <input v-model.number="editRule.window_mins" class="field__input" type="number" min="1" :max="editRule.trigger === 'log_count' ? 60 : undefined" />
            </div>
          </template>
          <div class="field">
            <label class="field__label">Channel</label>
            <select v-model="editRule.channel" class="field__input">
              <option value="webhook">Webhook</option>
              <option value="slack">Slack</option>
              <option value="discord">Discord</option>
              <option value="teams">Microsoft Teams</option>
              <option value="email">Email</option>
            </select>
          </div>
          <div v-if="editRule.channel === 'webhook' || editRule.channel === 'slack' || editRule.channel === 'discord' || editRule.channel === 'teams'" class="field" style="grid-column: 1 / -1">
            <label class="field__label">{{ editRule.channel === 'discord' ? 'Discord webhook URL' : editRule.channel === 'slack' ? 'Slack webhook URL' : editRule.channel === 'teams' ? 'Teams webhook URL' : 'Webhook URL' }}</label>
            <input v-model="editRule.webhook_url" class="field__input" type="url" :placeholder="editRule.channel === 'discord' ? 'https://discord.com/api/webhooks/...' : editRule.channel === 'slack' ? 'https://hooks.slack.com/services/...' : editRule.channel === 'teams' ? 'https://outlook.office.com/webhook/...' : 'https://hooks.example.com/...'" />
          </div>
          <div v-else class="field">
            <label class="field__label">Email to</label>
            <input v-model="editRule.email_to" class="field__input" type="email" placeholder="you@example.com" />
          </div>
          <div class="field">
            <label class="field__label">Cooldown (minutes)</label>
            <input v-model.number="editRule.cooldown_mins" class="field__input" type="number" min="1" />
          </div>
          <div class="field">
            <label class="field__label">Min. level</label>
            <select v-model="editRule.filter_level" class="field__input">
              <option v-if="editRule.trigger !== 'log_count'" value="">Any level</option>
              <option value="fatal">Fatal only</option>
              <option value="error">Error and above</option>
              <option value="warning">{{ editRule.trigger === 'log_count' ? 'Warning and above (requires search)' : 'Warning and above' }}</option>
              <option v-if="editRule.trigger !== 'log_count'" value="info">Info and above</option>
              <option v-if="editRule.trigger !== 'log_count'" value="performance">Performance issues only</option>
            </select>
          </div>
          <div class="field">
            <label class="field__label">Environment</label>
            <input v-model="editRule.filter_environment" class="field__input" placeholder="e.g. production" />
          </div>
          <div v-if="editRule.trigger === 'log_count'" class="field" style="grid-column: 1 / -1">
            <label class="field__label">Search</label>
            <input v-model="editRule.filter_search" class="field__input" placeholder="Same as the Logs search box" maxlength="200" />
          </div>
          <div v-if="editRule.trigger !== 'event_count' && editRule.trigger !== 'log_count'" class="field">
            <label class="field__label">Min. occurrences</label>
            <input v-model.number="editRule.min_occurrences" class="field__input" type="number" min="0" placeholder="Any" />
          </div>
        </div>
        <p v-if="editRule.trigger === 'log_count' && logPreview" class="field__hint" style="margin-top: 10px">
          {{ logPreview.count }} matching log{{ logPreview.count === 1 ? '' : 's' }} in the last {{ logPreview.window_mins }}m
          <span v-if="logPreview.count >= Number(editRule.threshold)" style="color: var(--warning)">(this would fire immediately)</span>
        </p>
        <div style="display: flex; gap: 8px; margin-top: 14px">
          <button class="btn btn--primary" :disabled="savingRule || (editRule.trigger === 'log_count' && editRule.projectIDs.length === 0)" @click="saveAlertRule({ id: rule.id })">
            {{ savingRule ? 'Saving…' : 'Save' }}
          </button>
          <button class="btn btn--ghost" @click="cancelEditRule()">Cancel</button>
          <button
            class="btn btn--ghost"
            style="color: var(--danger); margin-left: auto"
            @click="deleteAlertRule({ id: rule.id })"
          >
            Delete rule
          </button>
        </div>
      </template>

      <!-- Detail view -->
      <template v-else>
        <div class="rule__detail-grid">
          <span class="rule__detail-k">Projects</span>
          <span class="rule__detail-v">{{ ruleProjectNames(rule) }}</span>
          <span class="rule__detail-k">Trigger</span>
          <span class="rule__detail-v">{{ triggerLabel(rule) }}</span>
          <template v-if="rule.trigger === 'event_count' || rule.trigger === 'log_count'">
            <span class="rule__detail-k">Threshold</span>
            <span class="rule__detail-v">{{ rule.threshold }} {{ rule.trigger === 'log_count' ? 'logs' : 'events' }}</span>
            <span class="rule__detail-k">Window</span>
            <span class="rule__detail-v">{{ rule.window_mins }} minutes</span>
          </template>
          <template v-if="rule.trigger === 'log_count' && rule.filter_search">
            <span class="rule__detail-k">Search</span>
            <span class="rule__detail-v">{{ rule.filter_search }}</span>
          </template>
          <span class="rule__detail-k">Channel</span>
          <span class="rule__detail-v">{{ rule.channel }}</span>
          <template v-if="rule.channel === 'webhook' || rule.channel === 'slack' || rule.channel === 'discord' || rule.channel === 'teams'">
            <span class="rule__detail-k">URL</span>
            <span class="rule__detail-v mono" style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ rule.webhook_url }}</span>
          </template>
          <template v-else>
            <span class="rule__detail-k">Email to</span>
            <span class="rule__detail-v">{{ rule.email_to }}</span>
          </template>
          <span class="rule__detail-k">Cooldown</span>
          <span class="rule__detail-v">{{ rule.cooldown_mins }}m</span>
          <template v-if="rule.filter_level">
            <span class="rule__detail-k">Min. level</span>
            <span class="rule__detail-v">{{ rule.filter_level === 'performance' ? 'Performance issues only' : `${rule.filter_level} and above` }}</span>
          </template>
          <template v-if="rule.filter_environment">
            <span class="rule__detail-k">Environment</span>
            <span class="rule__detail-v">{{ rule.filter_environment }}</span>
          </template>
          <template v-if="rule.min_occurrences">
            <span class="rule__detail-k">Min. occurrences</span>
            <span class="rule__detail-v">{{ rule.min_occurrences }}</span>
          </template>
          <span class="rule__detail-k">Last fired</span>
          <span class="rule__detail-v">{{ rule.last_fired_at ? formatRel(rule.last_fired_at) : 'never' }}</span>
        </div>
        <div v-if="canManageAlerts" class="rule__actions">
          <button
            class="btn btn--primary"
            :disabled="testingRuleID === rule.id"
            @click="testAlertRule({ id: rule.id })"
          >
            <Icon name="send" :size="12" />
            {{ testingRuleID === rule.id ? 'Sending…' : 'Send test' }}
          </button>
          <button class="btn" @click="startEditRule(rule)">
            <Icon name="pencil" :size="12" />
            Edit
          </button>
          <button
            class="btn btn--ghost"
            style="color: var(--danger); margin-left: auto"
            @click="deleteAlertRule({ id: rule.id })"
          >
            Delete rule
          </button>
        </div>

        <div class="rule__history">
          <div class="rule__history-head">Delivery history</div>
          <div v-if="expandedRuleFirings.length === 0" class="rule__history-empty">
            No deliveries recorded yet.
          </div>
          <div v-for="f in expandedRuleFirings" :key="f.id" class="rule__history-row">
            <span class="rule__history-dot" :class="`rule__history-dot--${f.status}`"></span>
            <span class="rule__history-time">{{ formatRel(f.fired_at) }}</span>
            <span class="rule__history-trigger">{{ firingTriggerLabel(f.trigger) }}</span>
            <span v-if="f.item_count != null" class="rule__history-count">{{ f.item_count }} item{{ f.item_count !== 1 ? 's' : '' }}</span>
            <span v-if="f.status === 'pending'" class="rule__history-badge rule__history-badge--pending">
              retry {{ f.attempt }}/{{ 3 }}
            </span>
            <span v-else-if="f.status === 'failed'" class="rule__history-badge rule__history-badge--failed">
              failed after {{ f.attempt }} {{ f.attempt === 1 ? 'attempt' : 'attempts' }}
            </span>
            <span v-if="f.status_code" class="rule__history-code">HTTP {{ f.status_code }}</span>
            <span v-if="f.error" class="rule__history-error" :title="f.error">{{ f.error }}</span>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
