package api

import "github.com/blendbyte/tindra/internal/ingest"

// ParseLogsForTest exposes parseLogs so payload shapes can be exercised
// directly, without routing them through the HTTP handler.
func ParseLogsForTest(projectID string, payload []byte) []ingest.BufferedLog {
	var out []ingest.BufferedLog
	parseLogs(projectID, payload, func(l ingest.BufferedLog) { out = append(out, l) })
	return out
}

// SetLatestVersionForTest injects a fake version into a Handle for testing purposes.
func SetLatestVersionForTest(h *Handle, version, url string) {
	h.ro.versionMu.Lock()
	h.ro.latestVersion = version
	h.ro.releaseURL = url
	h.ro.versionMu.Unlock()
}
