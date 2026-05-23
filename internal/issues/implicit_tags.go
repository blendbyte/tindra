package issues

import (
	"encoding/json"
	"regexp"
	"strings"
)

const tagMaxLen = 200

var (
	reBrowserEdge    = regexp.MustCompile(`Edg/(\d+(?:\.\d+)?)`)
	reBrowserChrome  = regexp.MustCompile(`Chrome/(\d+(?:\.\d+)?)`)
	reBrowserFirefox = regexp.MustCompile(`Firefox/(\d+(?:\.\d+)?)`)
	reBrowserSafari  = regexp.MustCompile(`Version/(\d+(?:\.\d+)?)[^)]*Safari`)
	reOSAndroid      = regexp.MustCompile(`Android (\d+(?:\.\d+)?)`)
	reOSiOS          = regexp.MustCompile(`(?:iPhone|iPad).*OS (\d+[._]\d+)`)
	reOSWindows      = regexp.MustCompile(`Windows NT (\d+\.\d+)`)
	reOSMac          = regexp.MustCompile(`Mac OS X (\d+[._]\d+)`)
)

// extractImplicitTags derives the standard set of tags that Sentry synthesises
// from well-known event fields: level, environment, transaction, server_name,
// request URL, contexts (browser/os/runtime/device/client_os), and the
// exception mechanism (handled, mechanism type).
//
// These are merged with any explicit SDK-supplied tags, with explicit tags
// taking priority on key conflicts.
func extractImplicitTags(payload json.RawMessage) [][2]string {
	var p struct {
		Level       string `json:"level"`
		Environment string `json:"environment"`
		Transaction string `json:"transaction"`
		ServerName  string `json:"server_name"`
		Request     struct {
			URL     string                     `json:"url"`
			Headers map[string]json.RawMessage `json:"headers"`
		} `json:"request"`
		Contexts  map[string]json.RawMessage `json:"contexts"`
		Exception struct {
			Values []struct {
				Mechanism struct {
					Type    string `json:"type"`
					Handled *bool  `json:"handled"`
				} `json:"mechanism"`
			} `json:"values"`
		} `json:"exception"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}

	var tags [][2]string
	add := func(k, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if len(v) > tagMaxLen {
			v = v[:tagMaxLen]
		}
		tags = append(tags, [2]string{k, v})
	}

	add("level", p.Level)
	add("environment", p.Environment)
	add("transaction", p.Transaction)
	add("server_name", p.ServerName)
	add("url", p.Request.URL)

	// Extract from contexts: browser, os, runtime, device, client_os, etc.
	addedContexts := make(map[string]bool)
	for ctxKey, raw := range p.Contexts {
		var ctx struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Build   string `json:"build"`
		}
		if err := json.Unmarshal(raw, &ctx); err != nil || ctx.Name == "" {
			continue
		}
		addedContexts[ctxKey] = true
		composite := ctx.Name
		if ctx.Version != "" {
			composite += " " + ctx.Version
		}
		add(ctxKey, composite)
		add(ctxKey+".name", ctx.Name)
		if ctx.Version != "" {
			add(ctxKey+".version", ctx.Version)
		}
		if ctx.Build != "" {
			add(ctxKey+".build", ctx.Build)
		}
	}

	// Fall back to User-Agent parsing for browser/os when the SDK did not
	// supply explicit context objects (e.g. PHP SDK only sends headers).
	if ua := extractUserAgent(p.Request.Headers); ua != "" {
		if !addedContexts["browser"] {
			if name, version := parseBrowserFromUA(ua); name != "" {
				composite := name
				if version != "" {
					composite += " " + version
				}
				add("browser", composite)
				add("browser.name", name)
				if version != "" {
					add("browser.version", version)
				}
			}
		}
		if !addedContexts["os"] {
			if name, version := parseOSFromUA(ua); name != "" {
				composite := name
				if version != "" {
					composite += " " + version
				}
				add("os", composite)
				add("os.name", name)
				if version != "" {
					add("os.version", version)
				}
			}
		}
	}

	// Exception mechanism
	if len(p.Exception.Values) > 0 {
		mech := p.Exception.Values[0].Mechanism
		add("mechanism", mech.Type)
		if mech.Handled != nil {
			if *mech.Handled {
				add("handled", "yes")
			} else {
				add("handled", "no")
			}
		}
	}

	return tags
}

// extractUserAgent finds the User-Agent value from a Sentry-format headers map.
// Sentry serialises header values as either a plain string or a JSON array of
// strings (["value"]); both forms are handled.
func extractUserAgent(headers map[string]json.RawMessage) string {
	for k, raw := range headers {
		if !strings.EqualFold(k, "user-agent") {
			continue
		}
		// Try plain string first.
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		// Try array form ["value"].
		var arr []string
		if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
			return arr[0]
		}
	}
	return ""
}

func parseBrowserFromUA(ua string) (name, version string) {
	if m := reBrowserEdge.FindStringSubmatch(ua); m != nil {
		return "Edge", m[1]
	}
	if m := reBrowserChrome.FindStringSubmatch(ua); m != nil {
		return "Chrome", m[1]
	}
	if m := reBrowserFirefox.FindStringSubmatch(ua); m != nil {
		return "Firefox", m[1]
	}
	if m := reBrowserSafari.FindStringSubmatch(ua); m != nil {
		return "Safari", m[1]
	}
	return "", ""
}

func parseOSFromUA(ua string) (name, version string) {
	if m := reOSAndroid.FindStringSubmatch(ua); m != nil {
		return "Android", m[1]
	}
	if m := reOSiOS.FindStringSubmatch(ua); m != nil {
		ver := strings.ReplaceAll(m[1], "_", ".")
		return "iOS", ver
	}
	if m := reOSWindows.FindStringSubmatch(ua); m != nil {
		// Map common NT versions to marketing names.
		switch m[1] {
		case "10.0":
			return "Windows", "10"
		case "6.3":
			return "Windows", "8.1"
		case "6.2":
			return "Windows", "8"
		case "6.1":
			return "Windows", "7"
		default:
			return "Windows", m[1]
		}
	}
	if m := reOSMac.FindStringSubmatch(ua); m != nil {
		ver := strings.ReplaceAll(m[1], "_", ".")
		return "macOS", ver
	}
	if strings.Contains(ua, "Linux") {
		return "Linux", ""
	}
	return "", ""
}

// mergeImplicitTags appends implicit tags to explicit ones, skipping any key
// already present in the explicit set so that SDK-supplied values take priority.
func mergeImplicitTags(explicit [][2]string, implicit [][2]string) [][2]string {
	if len(implicit) == 0 {
		return explicit
	}
	seen := make(map[string]bool, len(explicit))
	for _, t := range explicit {
		seen[t[0]] = true
	}
	for _, t := range implicit {
		if !seen[t[0]] {
			explicit = append(explicit, t)
		}
	}
	return explicit
}
