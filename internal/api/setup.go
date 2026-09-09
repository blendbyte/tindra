package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

type setupExample struct {
	ID          string    `json:"id"`
	IssueID     *string   `json:"issue_id"`
	ReceivedAt  time.Time `json:"received_at"`
	Environment string    `json:"environment"`
	SDK         string    `json:"sdk"`
	Release     string    `json:"release"`
}

func setupError(w http.ResponseWriter, err error) {
	slog.Error("project setup", "err", err)
	http.Error(w, "could not check project setup", http.StatusInternalServerError)
}

func (ro *router) handleSetupProjects(w http.ResponseWriter, r *http.Request) {
	ids := bearerProjectIDs(r, nil)
	rows, err := ro.pool.Query(r.Context(), `SELECT p.id, EXISTS(SELECT 1 FROM project_setup_receipts s WHERE s.project_id=p.id AND s.kind IN ('events','transactions')) FROM projects p WHERE $1::uuid[] IS NULL OR p.id=ANY($1::uuid[]) ORDER BY p.id`, ids)
	if err != nil {
		setupError(w, err)
		return
	}
	type status struct {
		ID       string `json:"id"`
		Complete bool   `json:"setup_complete"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[status])
	if err != nil {
		setupError(w, err)
		return
	}
	writeJSON(w, out)
}

func (ro *router) handleStartSetupCheck(w http.ResponseWriter, r *http.Request) {
	if _, token := r.Context().Value(ctxTokenProjID).(string); token {
		if writable, _ := r.Context().Value(ctxTokenWritable).(bool); !writable {
			http.Error(w, "this token is read-only", http.StatusForbidden)
			return
		}
	}
	p, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	check, err := storage.StartSetupCheck(r.Context(), ro.pool, p.ID)
	if err != nil {
		setupError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, check)
}

func (ro *router) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	receipts, err := storage.ListSetupReceipts(ctx, ro.pool, p.ID)
	if err != nil {
		setupError(w, err)
		return
	}
	observations, err := storage.ListSetupObservations(ctx, ro.pool, p.ID)
	if err != nil {
		setupError(w, err)
		return
	}
	var check *storage.SetupCheck
	if id := r.URL.Query().Get("check_id"); id != "" {
		if _, err := uuid.Parse(id); err != nil {
			http.Error(w, "invalid check id", http.StatusBadRequest)
			return
		}
		check, err = storage.GetSetupCheck(ctx, ro.pool, p.ID, id)
		if err != nil {
			setupError(w, err)
			return
		}
		if check == nil {
			http.Error(w, "setup check expired or not found", http.StatusNotFound)
			return
		}
	}
	examples := map[string]*setupExample{}
	for _, receipt := range receipts {
		var example setupExample
		switch receipt.Kind {
		case "events":
			err = ro.pool.QueryRow(ctx, `SELECT id, issue_id, received_at, COALESCE(payload->>'environment',''),
 COALESCE(payload#>>'{sdk,name}','') || ' ' || COALESCE(payload#>>'{sdk,version}',''), COALESCE(payload->>'release','')
 FROM events WHERE id=$1 AND project_id=$2`, receipt.LatestID, p.ID).Scan(&example.ID, &example.IssueID, &example.ReceivedAt, &example.Environment, &example.SDK, &example.Release)
		case "transactions":
			err = ro.pool.QueryRow(ctx, `SELECT id, received_at, COALESCE(environment,''), COALESCE(release,'') FROM transactions WHERE id=$1 AND project_id=$2`, receipt.LatestID, p.ID).Scan(&example.ID, &example.ReceivedAt, &example.Environment, &example.Release)
		default:
			continue
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			setupError(w, err)
			return
		}
		if err == nil {
			examples[receipt.Kind] = &example
		}
	}
	if check != nil && check.EventID != nil {
		var example setupExample
		err = ro.pool.QueryRow(ctx, `SELECT id, issue_id, received_at, COALESCE(payload->>'environment',''),
 COALESCE(payload#>>'{sdk,name}','') || ' ' || COALESCE(payload#>>'{sdk,version}',''), COALESCE(payload->>'release','')
 FROM events WHERE id=$1 AND project_id=$2`, *check.EventID, p.ID).Scan(&example.ID, &example.IssueID, &example.ReceivedAt, &example.Environment, &example.SDK, &example.Release)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			setupError(w, err)
			return
		}
		if err == nil {
			examples["test"] = &example
		}
	}
	// Bound the reverse lookup; normal profile reads start from a transaction.
	var profileTransaction *string
	err = ro.pool.QueryRow(ctx, `SELECT t.id FROM
 (SELECT id,event_id,profiler_id,start_timestamp,timestamp,received_at FROM transactions WHERE project_id=$1 ORDER BY received_at DESC,id DESC LIMIT 100) t
 WHERE EXISTS (SELECT 1 FROM profile_chunks p WHERE p.project_id=$1 AND
 ((p.transaction_event_id IS NOT NULL AND p.transaction_event_id=t.event_id) OR
 (p.profiler_id IS NOT NULL AND p.profiler_id=t.profiler_id AND p.start_ts >= t.start_timestamp - interval '70 seconds' AND p.start_ts <= t.timestamp AND p.end_ts >= t.start_timestamp))) ORDER BY t.received_at DESC,t.id DESC LIMIT 1`, p.ID).Scan(&profileTransaction)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		setupError(w, err)
		return
	}
	var mapCount int
	if err = ro.pool.QueryRow(ctx, `SELECT count(*) FROM sourcemaps WHERE project_id=$1`, p.ID).Scan(&mapCount); err != nil {
		setupError(w, err)
		return
	}
	writeJSON(w, struct {
		CheckedAt           time.Time                  `json:"checked_at"`
		ProfilingEnabled    bool                       `json:"profiling_enabled"`
		Receipts            []storage.SetupReceipt     `json:"receipts"`
		Observations        []storage.SetupObservation `json:"observations"`
		Check               *storage.SetupCheck        `json:"check"`
		Examples            map[string]*setupExample   `json:"examples"`
		ProfileTransaction  *string                    `json:"profile_transaction_id"`
		SourceMapCount      int                        `json:"sourcemap_count"`
		SourceMapsAvailable bool                       `json:"sourcemaps_available"`
	}{time.Now().UTC(), p.ProfilingEnabled, receipts, observations, check, examples, profileTransaction, mapCount, ro.smStore != nil})
}

func (ro *router) handleVerifySetupSourcemaps(w http.ResponseWriter, r *http.Request) {
	p, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	if ro.smStore == nil {
		http.Error(w, "source map storage is not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("event_id")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "choose an event to verify", http.StatusBadRequest)
		return
	}
	var payload json.RawMessage
	err := ro.pool.QueryRow(r.Context(), `SELECT payload FROM events WHERE id=$1 AND project_id=$2`, id, p.ID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "event not found in this project", http.StatusNotFound)
		return
	}
	if err != nil {
		setupError(w, err)
		return
	}
	result, err := ro.smStore.VerifyEvent(r.Context(), p.ID, payload)
	if err != nil {
		setupError(w, err)
		return
	}
	writeJSON(w, result)
}
