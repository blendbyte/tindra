package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func TestEnvelopeCheckinCorrelation(t *testing.T) {
	h := newHandler(ingest.NewBuffer(1))
	for _, terminal := range []string{"ok", "error"} {
		t.Run(terminal, func(t *testing.T) {
			m := createTestMonitor(t, "SDK "+terminal, "* * * * *")
			send := func(status, extra string) {
				t.Helper()
				payload := fmt.Sprintf(`{"monitor_slug":%q,"check_in_id":"0123456789abcdef0123456789abcdef","status":%q%s}`, m.ID, status, extra)
				require.Equal(t, http.StatusOK, postEnvelope(t, h, checkinEnvelope(payload)).Code)
			}
			send("in_progress", `,"environment":"production"`)
			started, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
			require.NoError(t, err)
			require.Len(t, started, 1)
			require.Equal(t, "in_progress", started[0].Status)
			require.NotNil(t, started[0].StartedAt)
			require.Nil(t, started[0].FinishedAt)
			send("in_progress", "")
			send(terminal, `,"duration":1.25`)
			finished, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
			require.NoError(t, err)
			require.Len(t, finished, 1)
			require.Equal(t, started[0].ID, finished[0].ID)
			require.Equal(t, started[0].StartedAt, finished[0].StartedAt)
			require.Equal(t, terminal, finished[0].Status)
			require.NotNil(t, finished[0].FinishedAt)
			require.Equal(t, 1250, *finished[0].DurationMs)
			require.Equal(t, "production", *finished[0].Environment)
			send(terminal, `,"duration":99`)
			send("in_progress", "")
			after, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
			require.NoError(t, err)
			require.Equal(t, finished, after)
			monitor, err := storage.GetCronMonitor(t.Context(), testPool, m.ID)
			require.NoError(t, err)
			require.False(t, monitor.IsRunning)
			require.Equal(t, terminal, *monitor.LastCheckinStatus)
		})
	}
}

func TestEnvelopeCheckinSingleShotAndMissingID(t *testing.T) {
	h := newHandler(ingest.NewBuffer(1))
	for _, withID := range []bool{true, false} {
		t.Run(fmt.Sprint(withID), func(t *testing.T) {
			m := createTestMonitor(t, "single shot", "* * * * *")
			extra := ""
			if withID {
				extra = `,"check_in_id":"finish-before-start"`
			}
			for _, status := range []string{"error", "error", "in_progress"} {
				payload := fmt.Sprintf(`{"monitor_slug":%q,"status":%q%s}`, m.ID, status, extra)
				require.Equal(t, http.StatusOK, postEnvelope(t, h, checkinEnvelope(payload)).Code)
			}
			rows, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
			require.NoError(t, err)
			if withID {
				require.Len(t, rows, 1)
				require.Equal(t, "error", rows[0].Status)
				require.Nil(t, rows[0].StartedAt)
			} else {
				require.Len(t, rows, 3, "deliveries without an ID remain independent")
			}
		})
	}
}

func TestEnvelopeCheckinMonitorIsolation(t *testing.T) {
	h := newHandler(ingest.NewBuffer(1))
	first := createTestMonitor(t, "first SDK monitor", "* * * * *")
	second := createTestMonitor(t, "second SDK monitor", "* * * * *")
	for _, m := range []*storage.CronMonitor{first, second} {
		payload := fmt.Sprintf(`{"monitor_slug":%q,"check_in_id":"same-id","status":"in_progress"}`, m.ID)
		require.Equal(t, http.StatusOK, postEnvelope(t, h, checkinEnvelope(payload)).Code)
	}
	payload := fmt.Sprintf(`{"monitor_slug":%q,"check_in_id":"same-id","status":"ok"}`, second.ID)
	require.Equal(t, http.StatusOK, postEnvelope(t, h, checkinEnvelope(payload)).Code)
	for i, m := range []*storage.CronMonitor{first, second} {
		rows, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, []string{"in_progress", "ok"}[i], rows[0].Status)
	}
}

func TestEnvelopeCheckinInvalidDeliveries(t *testing.T) {
	h := newHandler(ingest.NewBuffer(1))
	m := createTestMonitor(t, "invalid SDK delivery", "* * * * *")
	for _, payload := range []string{
		`{`,
		`{"status":"ok"}`,
		`{"monitor_slug":"not-a-uuid","status":"ok"}`,
		fmt.Sprintf(`{"monitor_slug":%q,"status":"unknown"}`, m.ID),
	} {
		require.Equal(t, http.StatusOK, postEnvelope(t, h, checkinEnvelope(payload)).Code)
	}
	rows, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = testPool.Exec(t.Context(), `UPDATE cron_monitors SET status='paused' WHERE id=$1`, m.ID)
	require.NoError(t, err)
	payload := fmt.Sprintf(`{"monitor_slug":%q,"status":"ok"}`, m.ID)
	require.Equal(t, http.StatusOK, postEnvelope(t, h, checkinEnvelope(payload)).Code)
	rows, err = storage.ListCheckins(t.Context(), testPool, m.ID, 10)
	require.NoError(t, err)
	require.Empty(t, rows)
}
