package api_test

// coverage9_test.go — additional tests targeting uncovered branches in
// cron.go and issues.go.
//
// Cron gaps covered:
//   - handleUpdateMonitor: status=active (resume from paused)
//   - handleCronCheckinFinish: empty body (EOF) triggers "status required" 400
//   - handleCronCheckinStart: completely empty body defaults status to "in_progress"
//   - handleListCheckins: limit=0 silently falls back to default
//   - handleListCheckins: non-numeric limit silently falls back to default
//   - handleListCheckins: resolveMonitor not-found path
//   - handleCronPing: paused monitor produces no checkin record
//   - resolveMonitor: enforceTokenProject mismatch (wrong-project bearer token)
//
// Issues gaps covered:
//   - handleGetIssue: issue.ProjectID != project.ID returns 404
//   - handleGetIssueFingerprints: issue.ProjectID != project.ID returns 404
//   - handleListIssues: cursor_time without cursor_id (inner if skipped)
//   - handleListIssues: cursor_time with unparseable value (silently ignored)
//   - handleUpdateIssue: status=ignored with ignore_count_limit (project-slug route)
//   - handleUpdateIssue: status=ignored with ignore_until (project-slug route)
//   - handleMergeIssues: bad JSON body returns 400
//   - handleUnmergeIssue: empty fingerprints array returns 400
//   - handleListPerfEvents: same-project bearer token (pass-through path)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// ---------------------------------------------------------------------------
// Cron: handleUpdateMonitor — resume (status=active) from paused
// ---------------------------------------------------------------------------

func TestUpdateMonitor_setStatus_activeResumesFromPaused(t *testing.T) {
	m := createTestMonitor(t, "Resume From Paused Cov9", "0 * * * *")

	// Pause the monitor first via the API.
	pauseBody, _ := json.Marshal(map[string]any{"status": "paused"})
	pauseReq := httptest.NewRequest(http.MethodPatch, "/api/monitors/"+m.ID, bytes.NewReader(pauseBody))
	pauseReq.Header.Set("Content-Type", "application/json")
	pauseReq.AddCookie(authCookie())
	pauseRec := httptest.NewRecorder()
	cronHandler().ServeHTTP(pauseRec, pauseReq)
	if pauseRec.Code != http.StatusOK {
		t.Fatalf("pause: expected 200, got %d: %s", pauseRec.Code, pauseRec.Body.String())
	}

	// Confirm paused.
	got, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got == nil || got.Status != "paused" {
		t.Fatalf("expected monitor to be paused before resume")
	}

	// Resume via PATCH status=active — exercises the previously-untested branch.
	resumeBody, _ := json.Marshal(map[string]any{"status": "active"})
	resumeReq := httptest.NewRequest(http.MethodPatch, "/api/monitors/"+m.ID, bytes.NewReader(resumeBody))
	resumeReq.Header.Set("Content-Type", "application/json")
	resumeReq.AddCookie(authCookie())
	resumeRec := httptest.NewRecorder()
	cronHandler().ServeHTTP(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusOK {
		t.Fatalf("resume: expected 200, got %d: %s", resumeRec.Code, resumeRec.Body.String())
	}

	var updated storage.CronMonitor
	if err := json.NewDecoder(resumeRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Status != "active" {
		t.Errorf("status: got %q, want active", updated.Status)
	}
}

// ---------------------------------------------------------------------------
// Cron: handleCronCheckinFinish — empty body (EOF) returns 400 "status required"
// coverage2 tests a body of `{}` (missing key); this tests a zero-byte body
// which triggers the json.Decode error path in the same condition.
// ---------------------------------------------------------------------------

func TestCronCheckinFinish_emptyBodyEOF(t *testing.T) {
	m := createTestMonitor(t, "Finish EOF Body Cov9", "0 * * * *")

	// Start a checkin so we have a valid checkinID.
	startReq := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/cron/%s/checkins/", m.ID),
		bytes.NewBufferString(`{"status":"in_progress"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	cronHandler().ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start: expected 201, got %d", startRec.Code)
	}
	var startResp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(startRec.Body).Decode(&startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}

	// PUT with zero-byte body — json.Decode returns EOF which satisfies the
	// "err != nil || req.Status == ''" condition at cron.go:297.
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/cron/%s/checkins/%s/", m.ID, startResp.ID),
		bytes.NewBufferString(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty finish body, got %d: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "status required") {
		t.Errorf("expected 'status required' error, got: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Cron: handleCronCheckinStart — nil body defaults status to "in_progress"
// coverage2 tests `{}` (empty JSON object); this sends a nil body
// so json.Decode fails and req.Status stays "" which defaults to "in_progress".
// ---------------------------------------------------------------------------

func TestCronCheckinStart_nilBodyDefaultsToInProgress(t *testing.T) {
	m := createTestMonitor(t, "Start Nil Body Cov9", "0 * * * *")

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/cron/%s/checkins/", m.ID), nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for nil-body start, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" || resp.ID == "00000000-0000-0000-0000-000000000000" {
		t.Errorf("expected a real checkin ID, got %q", resp.ID)
	}

	// State should be in_progress.
	got, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got.State != "in_progress" {
		t.Errorf("state after nil-body start: got %q, want in_progress", got.State)
	}
}

// ---------------------------------------------------------------------------
// Cron: handleListCheckins — limit=0 falls back to default 50
// (the `if n, err := strconv.Atoi(s); err == nil && n > 0` condition is false)
// ---------------------------------------------------------------------------

func TestListCheckins_limitZeroFallsBackToDefault(t *testing.T) {
	m := createTestMonitor(t, "Limit Zero Cov9", "0 * * * *")

	req := httptest.NewRequest(http.MethodGet,
		"/api/monitors/"+m.ID+"/checkins?limit=0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for limit=0, got %d: %s", rec.Code, rec.Body.String())
	}
	var checkins []*storage.CronCheckin
	if err := json.NewDecoder(rec.Body).Decode(&checkins); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cron: handleListCheckins — non-numeric limit falls back to default
// (strconv.Atoi returns error; the if-branch is skipped)
// ---------------------------------------------------------------------------

func TestListCheckins_limitNonNumericFallsBack(t *testing.T) {
	m := createTestMonitor(t, "Limit Non-Numeric Cov9", "0 * * * *")

	req := httptest.NewRequest(http.MethodGet,
		"/api/monitors/"+m.ID+"/checkins?limit=abc", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-numeric limit, got %d: %s", rec.Code, rec.Body.String())
	}
	var checkins []*storage.CronCheckin
	if err := json.NewDecoder(rec.Body).Decode(&checkins); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cron: handleListCheckins — resolveMonitor returns 404 (monitor not found)
// ---------------------------------------------------------------------------

func TestListCheckins_monitorNotFoundCov9(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/monitors/00000000-0000-0000-0000-000000000000/checkins", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown monitor checkins, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Cron: handleCronPing — paused monitor: verify no checkin is recorded
// The existing TestCronPing_pausedMonitor (coverage2) only checks the 200
// response code. This additionally asserts zero checkins are stored.
// ---------------------------------------------------------------------------

func TestCronPing_pausedMonitor_zeroCheckinsCov9(t *testing.T) {
	m := createTestMonitor(t, "Paused No Checkin Cov9", "0 * * * *")

	m.Status = "paused"
	if _, err := storage.UpdateCronMonitor(context.Background(), testPool, m); err != nil {
		t.Fatalf("pause monitor: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"?status=ok", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for paused monitor ping, got %d: %s", rec.Code, rec.Body.String())
	}

	checkins, err := storage.ListCheckins(context.Background(), testPool, m.ID, 10)
	if err != nil {
		t.Fatalf("list checkins: %v", err)
	}
	if len(checkins) != 0 {
		t.Errorf("expected 0 checkins for paused monitor ping, got %d", len(checkins))
	}
}

// ---------------------------------------------------------------------------
// Cron: resolveMonitor — enforceTokenProject mismatch (wrong-project bearer)
// A bearer token scoped to project B cannot read a monitor from project A.
// (cron.go:419 enforceTokenProject returns false, writes 404)
// ---------------------------------------------------------------------------

func TestGetMonitor_wrongProjectBearerCov9(t *testing.T) {
	m := createTestMonitor(t, "Wrong Bearer Cov9", "0 * * * *")

	other, err := storage.CreateProject(context.Background(), testPool,
		"cov9-cron-other", "Cov9 Cron Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/monitors/"+m.ID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	// enforceTokenProject writes 404 to avoid leaking resource existence.
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 or 404 for wrong-project bearer on monitor, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Issues: handleGetIssue — issue belongs to a different project returns 404
// (issues.go:97: issue.ProjectID != project.ID)
// ---------------------------------------------------------------------------

func TestGetIssue_crossProjectReturns404(t *testing.T) {
	truncateIssues(t)

	other, err := storage.CreateProject(context.Background(), testPool,
		"cov9-get-issue-other", "Cov9 Get Issue Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})

	foreignIss, _, _, err := storage.UpsertIssue(context.Background(), testPool,
		other.ID, "fp-foreign-get-cov9", "Foreign Get Issue Cov9",
		"error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("seed foreign issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues/"+foreignIss.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-project issue GET, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Issues: handleGetIssueFingerprints — issue belongs to different project
// (issues.go:191: issue.ProjectID != project.ID)
// ---------------------------------------------------------------------------

func TestGetIssueFingerprints_crossProjectReturns404(t *testing.T) {
	truncateIssues(t)

	other, err := storage.CreateProject(context.Background(), testPool,
		"cov9-fps-other", "Cov9 Fingerprints Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})

	foreignIss, _, _, err := storage.UpsertIssue(context.Background(), testPool,
		other.ID, "fp-foreign-fps-cov9", "Foreign FPs Cov9",
		"error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("seed foreign issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/test-project/issues/%s/fingerprints", foreignIss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-project fingerprints, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Issues: handleListIssues — cursor_time without cursor_id (inner if not entered)
// (issues.go:57-63: outer if(ct!="") entered but inner if(cid!="") skipped)
// ---------------------------------------------------------------------------

func TestListIssues_cursorTimeAloneIsIgnored(t *testing.T) {
	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues?cursor_time="+cursorTime, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for cursor_time without cursor_id, got %d: %s",
			rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []json.RawMessage `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Issues: handleListIssues — cursor_time with unparseable value silently ignored
// (time.Parse fails at issues.go:58; filter.CursorTime stays nil)
// ---------------------------------------------------------------------------

func TestListIssues_unparseableCursorTimeIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues?cursor_time=not-a-date&cursor_id=some-id", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for unparseable cursor_time, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Issues: handleUpdateIssue — status=ignored with ignore_count_limit
// via the project-slug route (not the global /api/issues route)
// (issues.go:133-136 ignoreOpts with CountLimit set)
// ---------------------------------------------------------------------------

func TestUpdateIssue_ignoredWithCountLimit_slugRoute(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-ignored-count-cov9", "Ignored Count Cov9")

	limit := 3
	body, _ := json.Marshal(map[string]any{
		"status":             "ignored",
		"ignore_count_limit": limit,
	})
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/test-project/issues/"+iss.ID, bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ignored" {
		t.Errorf("status: got %q, want ignored", got.Status)
	}
}

// ---------------------------------------------------------------------------
// Issues: handleUpdateIssue — status=ignored with ignore_until
// via the project-slug route
// (issues.go:133-136 ignoreOpts with Until set; history records ignore_until)
// ---------------------------------------------------------------------------

func TestUpdateIssue_ignoredWithIgnoreUntil_slugRoute(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-ignored-until-cov9", "Ignored Until Cov9")

	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"status":"ignored","ignore_until":"%s"}`, future)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/test-project/issues/"+iss.ID,
		bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ignored" {
		t.Errorf("status: got %q, want ignored", got.Status)
	}
}

// ---------------------------------------------------------------------------
// Issues: handleMergeIssues — bad JSON body triggers len(IssueIDs)<2 path
// (issues.go:213: decode error → req.IssueIDs is nil → len < 2 → 400)
// ---------------------------------------------------------------------------

func TestMergeIssues_badJsonBodyCov9(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/test-project/issues/merge",
		bytes.NewBufferString("{not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON body on merge, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Issues: handleUnmergeIssue — empty fingerprints array returns 400
// (issues.go:297: len(req.Fingerprints) == 0)
// ---------------------------------------------------------------------------

func TestUnmergeIssue_emptyFingerprintsCov9(t *testing.T) {
	truncateIssues(t)
	iss := seedIssueFull(t, "fp-unm-empty-cov9", "Unmerge Empty Cov9")

	body := bytes.NewBufferString(`{"fingerprints":[]}`)
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/projects/test-project/issues/%s/unmerge", iss.ID),
		body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty fingerprints on unmerge, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Issues: handleListPerfEvents — same-project bearer token (pass-through path)
// enforceIssueProject returns true when token.ProjectID == issue.ProjectID.
// (scope.go:36-37 "return true" after isBearer && projID == tokenProjID check)
// ---------------------------------------------------------------------------

func TestListPerfEvents_sameProjectBearerCov9(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-perf-bearer-cov9", "Perf Bearer Cov9")

	truncateTokens(t)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/perf-events", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for same-project bearer on perf-events, got %d: %s",
			rec.Code, rec.Body.String())
	}
}
