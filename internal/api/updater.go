package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var updateCheckURL = "https://www.tindra.sh/version-update-check"

type githubRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

// StartVersionChecker starts a background goroutine that polls GitHub Releases
// every 6 hours and caches the latest version on the router.
func (h *Handle) StartVersionChecker(ctx context.Context) {
	go func() {
		h.ro.checkLatestVersion(ctx)
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.ro.checkLatestVersion(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (ro *router) checkLatestVersion(ctx context.Context) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, updateCheckURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Tindra/"+AppVersion)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("version check failed", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return
	}
	if rel.Prerelease || rel.Draft || rel.TagName == "" {
		return
	}

	ro.versionMu.Lock()
	ro.latestVersion = rel.TagName
	ro.releaseURL = rel.HTMLURL
	ro.versionMu.Unlock()

	if semverGT(rel.TagName, AppVersion) {
		slog.Info("new tindra version available", "latest", rel.TagName, "current", AppVersion)
	}
}

func (ro *router) getLatestRelease() (version, url string) {
	ro.versionMu.RLock()
	defer ro.versionMu.RUnlock()
	return ro.latestVersion, ro.releaseURL
}

// semverGT reports whether version a is strictly greater than b.
// Both must be of the form vMAJOR.MINOR.PATCH; returns false for anything
// that doesn't parse (e.g. "dev"), so local builds never show a false update notice.
func semverGT(a, b string) bool {
	parse := func(s string) (major, minor, patch int, ok bool) {
		s = strings.TrimPrefix(s, "v")
		parts := strings.SplitN(s, ".", 3)
		if len(parts) != 3 {
			return
		}
		patchStr := strings.SplitN(parts[2], "-", 2)[0] // strip pre-release suffix
		maj, e1 := strconv.Atoi(parts[0])
		min, e2 := strconv.Atoi(parts[1])
		pat, e3 := strconv.Atoi(patchStr)
		if e1 != nil || e2 != nil || e3 != nil {
			return
		}
		return maj, min, pat, true
	}

	aMaj, aMin, aPat, aOK := parse(a)
	bMaj, bMin, bPat, bOK := parse(b)
	if !aOK || !bOK {
		return false
	}
	if aMaj != bMaj {
		return aMaj > bMaj
	}
	if aMin != bMin {
		return aMin > bMin
	}
	return aPat > bPat
}
