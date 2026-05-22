package api

// SetLatestVersionForTest injects a fake version into a Handle for testing purposes.
func SetLatestVersionForTest(h *Handle, version, url string) {
	h.ro.versionMu.Lock()
	h.ro.latestVersion = version
	h.ro.releaseURL = url
	h.ro.versionMu.Unlock()
}
