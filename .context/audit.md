# Security Audit Findings
_Full codebase audit: all .go, .vue, .js files. Conducted 2026-05-19._

Walk through one-by-one with user. Mark done as each is resolved.

---

## CRITICAL

### C1 - Bearer token project scope bypass [IN PROGRESS]
Global REST handlers (20+) accept resource IDs without checking the bearer token's
project scope. A token for project A can read data from project B.

Affected: `handleGetIssueGlobal`, `handleGetIssueHistory`, `handleGetIssueTags`,
`handleGetIssueHistogram`, `handleListEventsForIssue`, `handleGetLatestEventGlobal`,
`handleGetTransactionGlobal`, `handleGetSpansGlobal`, `handleGetTransactionErrors`,
`handleListPerfEvents`, `handleListLogs`, `handleListComments`, `handleListReleases`,
`handleGetRelease`, `handleGetReleaseTransactions`, `handleGetReleaseIssues`,
`handleListAlertRules`, `handleGetAlertRule`, `handleListMonitors`, `handleGetMonitor`,
`handleListCheckins`, `handleListAllTransactions`, `handleSpanSummaries`, `handleSpanSamples`,
`handleSpanTimeseries`, `handleGetWebVitals`, `handleGetWebVitalsPages`, `handleListAllTokens`

Fix: `bearerProjectIDs()` helper for list endpoints, `enforceTokenProject()` for get-by-ID.

---

### C2 - OIDC account takeover via unverified email
`FindOrCreateOAuthUser` in `internal/api/oauth.go` trusts the `email` claim from any
OIDC provider without verifying `email_verified=true`. An attacker controlling an OIDC
provider (or using one that issues unverified emails) can authenticate as any existing
user by claiming their email.

Affected: `internal/api/oauth.go` - `FindOrCreateOAuthUser` / OIDC callback handler.

Fix: Check `email_verified` claim; reject or warn when false. For providers that don't
send the claim, make it configurable (allow_unverified_email per provider).

---

### C3 - MFA bypass via password reset and invite flows
After a password reset or invite acceptance, the session is created without requiring
TOTP for accounts that have MFA enabled. An attacker who can trigger a password reset
(e.g., by knowing the email) bypasses MFA entirely.

Affected: `handleDoPasswordReset`, `handleAcceptInvite` in `internal/api/auth.go`.

Fix: After password reset / invite accept, if the account has MFA enabled, return a
partial session that requires TOTP challenge before issuing a full session cookie.

---

## HIGH

### H1 - SMTP header injection in email sender
User-controlled values (name, email) are interpolated into MIME headers without
sanitization. A newline in an email address would inject arbitrary headers.

Affected: `internal/alerts/email.go` (or wherever SMTP sending is implemented).

Fix: Strip/reject `\r` and `\n` from all header values before insertion.

---

### H2 - Login DoS via shared email rate limiter
The login rate limiter keys by email address. An attacker can lock out a target user by
sending repeated failed login attempts against their email, consuming the rate limit
bucket shared with legitimate logins from that user.

Affected: `internal/api/auth.go` - `handleLogin`, `loginEmailRL`.

Fix: Rate limit by (IP, email) pair rather than email alone, or use exponential backoff
per IP rather than per email.

---

### H3 - TOTP brute force (no rate limit on MFA verify endpoint)
`POST /api/auth/mfa/verify` has no rate limiting. An attacker with a valid session
(e.g., correct password) can brute-force the 6-digit TOTP code (~1M attempts).

Affected: `internal/api/auth.go` - `handleMFAVerify`.

Fix: Rate limit MFA verify attempts per session (e.g., 5 attempts then lock for 15 min).

---

### H4 - Regex DoS via scrub patterns
Issue title / event payload scrubbing uses caller-supplied regex patterns without
complexity limits. A pathological regex causes ReDoS.

Affected: Wherever user-configurable scrub patterns are compiled and applied.

Fix: Compile regexes at config load time (not per-request), add a timeout via
`regexp.Copy` + goroutine with timeout, or restrict pattern syntax.

---

### H5 - Envelope OOM via attacker-controlled `length` field
The envelope ingest parser trusts the `length` field in the envelope header to
pre-allocate a buffer. An attacker sending `length: 9999999999` causes an OOM.

Affected: `internal/ingest/envelope.go` (envelope parser).

Fix: Cap the pre-allocation at a reasonable max (e.g., 10MB) independent of the
`length` header; rely on `io.LimitReader` instead.

---

### H6 - SSE broker wired but events never sent (dead real-time feature)
The SSE broker is initialized and the `/api/stream` endpoint exists, but no code path
actually publishes events to it. The UI polls as a fallback. This is a latent bug -
events that were supposed to be real-time are silently dropped.

Affected: SSE broker in `internal/api/` and wherever issues/events are written.

Fix: Wire the broker to publish on issue create/update, or remove the dead endpoint.

---

### H7 - Session cookie missing `Secure` flag on MFA setup path
The `tindra_session` cookie set after MFA confirm (`/api/auth/mfa/confirm`) may not
have the `Secure` flag set consistently, allowing interception over HTTP.

Affected: `internal/api/auth.go` - MFA confirm / session cookie issuance.

Fix: Always set `Secure: cookieSecure` on session cookies; ensure this flag is threaded
through all session-creation paths, not just the main login handler.

---

### H8 - Invited users get zero permissions (silent failure)
When an invite is accepted, the new user is created with all permissions false. There
is no mechanism to assign permissions at invite time. Admins must manually grant
permissions after the user signs up, and there's no notification or reminder.

Affected: `handleAcceptInvite` in `internal/api/auth.go`.

Fix: Allow invites to carry a permissions payload (set at invite creation time); apply
on accept. Or at minimum document the zero-permissions-on-invite behavior clearly.

---

### H9 - Invite redemption race condition
Two concurrent requests using the same invite token can both pass the
"is this token valid?" check and create two user accounts.

Affected: `handleAcceptInvite` in `internal/api/auth.go`.

Fix: Delete (or mark used) the invite token in the same transaction that creates the
user, using a SELECT FOR UPDATE or equivalent atomic operation.

---

### H10 - Audit log goroutine leak
`storage.WriteAuditLog` is called with `go func()` in some handlers but uses a
detached context. If the pool is closed or the DB is unavailable, these goroutines
pile up with no bound.

Affected: `storage.WriteAuditLog` call sites throughout `internal/api/`.

Fix: Pass a bounded context (e.g., `context.WithTimeout(context.Background(), 5*time.Second)`)
to the goroutine, and add a semaphore or worker pool to bound concurrency.

---

### H11 - Session tokens stored unhashed in DB
Session tokens in the `sessions` table appear to be stored as plaintext (or with weak
encoding). If the DB is compromised, all active sessions are immediately usable.

Affected: `internal/storage/sessions.go` and session creation in `internal/api/auth.go`.

Fix: Hash session tokens with SHA-256 (same pattern as API tokens) before storing;
compare by hash on lookup.

---

## MEDIUM

### M1 - SSRF in webhook validation (TOCTOU)
Webhook URL validation does a DNS lookup to block private IPs, but the actual HTTP
request uses a separate connection that may resolve to a different IP (DNS rebinding).

Affected: `internal/api/alerts.go` or webhook delivery code.

Fix: Use a custom `http.Transport` that re-checks the resolved IP after dial, before
sending. See `webhookAllowPrivateIPs` flag - extend that check to the dial phase.

---

### M2 - SSRF in passthrough/proxy client
There is a passthrough HTTP client that doesn't restrict target addresses.

Fix: Apply the same private IP deny-list used for webhooks.

---

### M3 - Unbounded search inputs
Several search/filter query params accept arbitrary-length strings that are interpolated
into SQL LIKE patterns without length limits. Very long patterns waste DB CPU.

Fix: Cap string filter inputs at a reasonable length (e.g., 500 chars) at the handler
boundary.

---

### M4 - N+1 queries in issue list
`attachSparklines` called in `handleListAllIssues` fires one query per issue to fetch
sparkline data, causing N+1 query patterns on large result sets.

Fix: Batch the sparkline query using `WHERE issue_id = ANY($1::uuid[])`.

---

### M5 - Missing project scope on span/transaction endpoints (partially fixed by C1)
Covered by C1 fix.

---

### M6 - Sourcemap files not deleted on disk
When a sourcemap record is deleted from the DB, the underlying file on disk (S3 or
local filesystem) is not removed.

Affected: `handleDeleteSourcemap` in `internal/api/`.

Fix: Delete the file from the store after deleting the DB record.

---

### M7 - Cron pings unauthenticated with no rate limit
`GET/POST /api/cron/{monitorID}` requires no auth and has no rate limit. A monitor
UUID obtained by any means allows unlimited pings, potentially suppressing missed-
check alerts.

Fix: Add per-monitorID rate limiting (e.g., 1 ping per 10s minimum).

---

### M8 - Expensive COUNT(*) on every issue list
`CountAllIssues` runs a full COUNT(*) with the same filters on every paginated request,
which is expensive on large tables.

Fix: Use an estimated count (reltuples) for display and only run exact count when
filters are narrow, or cache the count with a short TTL.

---

### M9 - Mid-UTF8 truncation in title storage
Issue titles are truncated to a fixed byte length, which can split a multi-byte UTF-8
character and produce an invalid string in the DB.

Fix: Truncate at rune boundaries using `utf8.RuneCountInString` / `[]rune` slicing.

---

### M10 - Password reset token not invalidated after first use
After a password reset token is used, it may remain valid for subsequent use until
it expires.

Fix: Delete or mark the token used in the same transaction that updates the password.

---

## LOW / INFO

- Timing leak in token comparison (use `subtle.ConstantTimeCompare` everywhere)
- Unbounded allocations in envelope parser for large tag maps
- Missing indexes on `issues.project_id + status` composite for filtered list queries
- `handleGetStats` returns 404 when `statsAPIKey` is empty (leaks endpoint existence;
  should return 401 instead regardless)
- No HSTS header set in `securityHeaders` middleware
- MCP tool descriptions contain implementation details that help attackers enumerate
  the data model
- `handleExportIssues` has a 10,000-row cap but no timeout; large exports can hold
  a DB connection for a long time
- Project deletion does not cascade-delete API tokens
- Comment `PUT /api/comments/{commentID}` allows updating comments you don't own
  (no author check)
- `DELETE /api/comments/{commentID}` same issue
