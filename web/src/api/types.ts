export interface UserPermissions {
  manage_projects: boolean
  manage_users: boolean
  manage_alerts: boolean
  manage_issues: boolean
}

export interface User {
  id: string
  email: string
  name: string
  has_password: boolean
  mfa_enabled: boolean
  weekly_digest: boolean
  timezone: string
  permissions: UserPermissions
  created_at: string
}

export interface ScrubPattern {
  name: string
  pattern: string
  builtin: boolean
  enabled: boolean
}

export interface Project {
  id: string
  slug: string
  name: string
  public_key: string
  passthrough_dsn: string | null
  scrub_fields: string[]
  scrub_patterns: ScrubPattern[]
  created_at: string
  event_count: number
  events_24h: number
  storage_bytes: number
}

export interface ProjectQuota {
  events_this_month: number
  /** Monthly event limit (errors + transactions). 0 = unlimited. */
  event_limit: number
  /** Configured envelope rate limit per minute. 0 = disabled. */
  rate_limit_per_min: number
  /** Events consumed in the current 1-minute window. */
  rate_limit_used: number
  /** ISO-8601 timestamp when the current window resets. Absent when no active window. */
  rate_limit_reset_at: string | null
  /** Per-day event counts for the last 30 days, oldest-first. */
  daily_volume: number[]
}

export interface ServerSettings {
  project_limit: number
  /** Monthly per-project event limit (errors + transactions). 0 = unlimited. */
  event_limit: number
  user_limit: number
  version: string
  commit: string
  billing_url?: string
  latest_version?: string
  update_available: boolean
  release_url?: string
}

/** Returned by GET /api/stats - for the managed hosting control plane. */
export interface InstanceStats {
  projects: number
  users: number
  events_this_month: number
  events_last_month: number
  period_start: string
  last_period_start: string
  event_limit: number
}

export interface InstanceHealth {
  db_size_bytes: number
  events_total: number
  tx_total: number
  logs_total: number
  events_24h: number
  tx_24h: number
  logs_24h: number
  oldest_event_at: string | null
  oldest_tx_at: string | null
  oldest_log_at: string | null
  retention_days: number
  events_size_bytes: number
  tx_size_bytes: number
  logs_size_bytes: number
}

export type IssueStatus = 'open' | 'resolved' | 'ignored' | 'regressed'
export type IssueLevel = 'fatal' | 'error' | 'warning' | 'info'

export interface IssueListPage {
  issues: Issue[]
  total: number
  has_more: boolean
  next_cursor_time?: string
  next_cursor_id?: string
}

export type IssueKind = 'error' | 'n1_query'

export interface Issue {
  id: string
  project_id: string
  fingerprint: string
  title: string
  level: IssueLevel
  kind: IssueKind
  status: IssueStatus
  event_count: number
  user_count: number
  first_seen: string
  last_seen: string
  environment: string | null
  release: string | null
  release_id: string | null
  assignee_id: string | null
  assignee_email: string | null
  sparkline?: number[]
  ignore_until?: string | null
  ignore_count_limit?: number | null
  ignore_count?: number
}

export interface PerfEvent {
  id: string
  issue_id: string
  transaction_id: string
  transaction: string
  span_count: number
  total_ms: number
  created_at: string
}

export interface Event {
  id: string
  project_id: string
  issue_id: string
  timestamp: string
  received_at: string
  level: string | null
  environment: string | null
  release: string | null
  platform: string | null
  trace_id?: string | null
  payload: Record<string, unknown>
}

export interface Transaction {
  id: string
  project_id: string
  trace_id: string
  transaction: string
  op: string
  status: string
  duration_ms: number
  start_timestamp: string
  environment: string | null
}

export interface TransactionSummary {
  transaction: string
  op: string
  project_id: string
  sample_count: number
  tpm: number
  p50: number
  p95: number
  apdex: number
  failure_rate: number
  time_spent_ms: number
}

export interface Span {
  id: string
  transaction_id: string
  span_id: string
  parent_span_id: string
  op: string
  description: string
  status: string
  start_offset_ms: number
  duration_ms: number
  is_critical: boolean
  start_timestamp_ms: number
  data?: Record<string, unknown>
}

export interface TraceError {
  event_id: string
  span_id: string
  timestamp: string
  issue_id: string
  title: string
  level: string
  status: string
}

export interface ApiToken {
  id: string
  project_id: string
  name: string
  writable: boolean
  created_at: string
  last_used_at: string | null
  expires_at: string
}

export interface Release {
  id: string
  project_id: string
  version: string
  deployed_at: string
  new_issues: number
  regressed_issues: number
  tx_count: number
  tx_p50: number
  tx_error_rate: number
  created_at: string
}

export interface ReleaseIssue {
  id: string
  title: string
  status: string
  level: string
  first_seen: string
  last_seen: string
  event_count: number
  category: 'new' | 'regressed' | 'ongoing'
}

export interface ReleaseListPage {
  releases: Release[]
  total: number
  has_more: boolean
  next_cursor_time?: string
  next_cursor_id?: string
}

export interface ReleaseTxSummary {
  transaction: string
  op: string
  sample_count: number
  p50: number
  p95: number
  error_rate: number
}

export interface Comment {
  id: string
  issue_id: string
  user_id: string
  user_email: string
  user_name: string
  body: string
  created_at: string
  updated_at: string
}

export interface Invite {
  token: string
  email: string
  name?: string
  inviter_id?: string
  expires_at: string
  accepted_at?: string
}

export type AlertTrigger =
  | 'new_issue'
  | 'regressed'
  | 'new_or_regressed'
  | 'event_count'
  | 'cron_missed'
  | 'cron_error'

export type CronMonitorState = 'unknown' | 'ok' | 'missed' | 'error' | 'in_progress'
export type CronMonitorStatus = 'active' | 'paused'

export interface CheckinDot {
  status: 'ok' | 'error' | 'in_progress'
  received_at: string
}

export interface CronMonitor {
  id: string
  project_id: string
  name: string
  schedule: string
  grace_period_secs: number
  status: CronMonitorStatus
  is_running: boolean
  last_ok_at: string | null
  next_expected_at: string | null
  last_checkin_status: string | null
  last_checkin_at: string | null
  created_at: string
  state: CronMonitorState
  recent_checkins: CheckinDot[]
}

export interface CronCheckin {
  id: string
  monitor_id: string
  status: 'in_progress' | 'ok' | 'error'
  duration_ms: number | null
  environment: string | null
  started_at: string | null
  finished_at: string | null
  received_at: string
}
export type AlertChannel = 'webhook' | 'slack' | 'discord' | 'email'

export interface AlertRule {
  id: string
  project_ids: string[]
  name: string
  enabled: boolean
  trigger: AlertTrigger
  threshold: number | null
  window_mins: number | null
  channel: AlertChannel
  webhook_url: string | null
  email_to: string | null
  cooldown_mins: number
  filter_level: string | null
  filter_environment: string | null
  min_occurrences: number | null
  last_fired_at: string | null
  created_at: string
}

export interface Log {
  id: string
  project_id: string
  timestamp: string
  received_at: string
  level: string
  body: string
  trace_id?: string | null
  span_id?: string | null
  environment?: string | null
  release?: string | null
  attributes: Record<string, unknown>
}

export interface LogListPage {
  logs: Log[]
  has_more: boolean
  next_cursor_time?: string
  next_cursor_id?: string
}

export interface HistogramBucket {
  time: string
  count: number
}

export interface HistogramResult {
  buckets: HistogramBucket[]
  bucket_size: 'hour' | 'day' | 'week'
}

export interface EventSummary {
  id: string
  timestamp: string
  received_at: string
  level: string | null
  environment: string | null
  release: string | null
  tags: Record<string, string>
}

export interface EventListPage {
  events: EventSummary[]
  has_more: boolean
  next_cursor_time?: string
  next_cursor_id?: string
}

export interface IssueHistoryEntry {
  id: string
  issue_id: string
  actor_id: string | null
  event_type: 'created' | 'regressed' | 'status_changed' | 'assigned' | string
  details: Record<string, unknown>
  created_at: string
}

export interface TagValue {
  value: string
  count: number
  pct: number
}

export interface TagSummary {
  key: string
  total: number
  values: TagValue[]
}

export interface TxBucket {
  time: string
  count: number
  p50: number
  p95: number
}

export interface TxTimeseries {
  buckets: TxBucket[]
  bucket_size: '5min' | 'hour' | 'day'
}

export interface TxListPage {
  transactions: Transaction[]
  next_cursor_time?: string
  next_cursor_id?: string
}

export interface AuditRow {
  id: string
  event_type: string
  actor_id: string | null
  actor_email: string | null
  project_id: string | null
  target_id: string | null
  ip: string
  details: Record<string, unknown>
  created_at: string
}

export interface SpanSummary {
  op: string
  description: string
  sample_count: number
  rate: number
  p50: number
  p95: number
  total_ms: number
  time_pct: number
  error_rate: number
  miss_rate?: number | null
}

export interface SpanBucket {
  time: string
  count: number
  p50: number
}

export interface SpanTimeseries {
  buckets: SpanBucket[]
  bucket_size: '5min' | 'hour' | 'day'
}

export interface SpanSample {
  span_id: string
  transaction_id: string
  op: string
  description: string
  duration_ms: number
  status: string
  start_timestamp: string
  transaction_name: string
  trace_id: string
}

export interface VitalStat {
  p75: number
  pass_rate: number
  count: number
}

export interface WebVitalsSummary {
  lcp: VitalStat
  fcp: VitalStat
  cls: VitalStat
  inp: VitalStat
  ttfb: VitalStat
}

export interface WebVitalsPage {
  transaction: string
  sessions: number
  lcp_p75: number
  inp_p75: number
  cls_p75: number
  pass_rate: number
}
