export type SetupKind = 'events' | 'transactions' | 'profile_chunks'
export interface SetupCheck {
  id: string
  created_at: string
  expires_at: string
  received_at: string | null
  event_id: string | null
}
export interface SetupExample {
  id: string
  issue_id: string | null
  received_at: string
  environment: string
  sdk: string
  release: string
}
export interface SetupStatus {
  checked_at: string
  profiling_enabled: boolean
  receipts: { kind: SetupKind; first_received_at: string; last_received_at: string; latest_id: string }[]
  observations: { kind: SetupKind | 'envelope'; outcome: 'queued' | 'rejected'; reason: string; observed_at: string }[]
  check: SetupCheck | null
  examples: Partial<Record<SetupKind | 'test', SetupExample>>
  profile_transaction_id: string | null
  sourcemap_count: number
  sourcemaps_available: boolean
}
export interface SourceMapVerification {
  release: string
  status: string
  truncated: boolean
  frames: { url: string; normalized_url: string; line: number; column: number; status: string; map_id?: string; source?: string; original_line?: number }[]
}
