package issues

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/ua-parser/uap-go/uaparser"
)

const tagMaxLen = 200

// uaParser is initialised once at startup using the bundled ua-parser database,
// the same one Sentry uses, so browser/OS detection matches Sentry's output.
var uaParser = uaparser.NewFromSaved()

func implicitTagsFrom(p eventPayload) [][2]string {
	var request struct {
		URL     looseString                `json:"url"`
		Headers map[string]json.RawMessage `json:"headers"`
	}
	_ = json.Unmarshal(p.Request, &request)
	var contexts map[string]json.RawMessage
	_ = json.Unmarshal(p.Contexts, &contexts)

	var tags [][2]string
	add := func(k, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		v = truncateTag(v)
		tags = append(tags, [2]string{k, v})
	}

	add("level", string(p.Level))
	add("environment", string(p.Environment))
	add("transaction", string(p.Transaction))
	add("server_name", string(p.ServerName))
	add("url", string(request.URL))

	// Extract from contexts: browser, os, runtime, device, client_os, etc.
	addedContexts := make(map[string]bool)
	for ctxKey, raw := range contexts {
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
	// supply explicit context objects (e.g. PHP SDK sends headers only; JS
	// SDK may omit browser context for unrecognised user agents).
	// uap-go uses the same database as Sentry's server-side enrichment.
	if ua := extractUserAgent(request.Headers); ua != "" {
		client := uaParser.Parse(ua)

		if !addedContexts["browser"] && client.UserAgent.Family != "Other" {
			name := client.UserAgent.Family
			version := joinVersion(client.UserAgent.Major, client.UserAgent.Minor)
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
		if !addedContexts["os"] && client.Os.Family != "Other" {
			name := client.Os.Family
			version := joinVersion(client.Os.Major, client.Os.Minor)
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

	// Exception mechanism: use the innermost (last) value, matching fingerprint behaviour.
	if values := p.exceptions(); len(values) > 0 {
		var mech struct {
			Type    looseString `json:"type"`
			Handled *bool       `json:"handled"`
		}
		_ = json.Unmarshal(values[len(values)-1].Mechanism, &mech)
		add("mechanism", string(mech.Type))
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
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var arr []string
		if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
			return arr[0]
		}
	}
	return ""
}

// joinVersion concatenates major.minor, omitting empty components.
func joinVersion(major, minor string) string {
	if major == "" {
		return ""
	}
	if minor == "" {
		return major
	}
	return major + "." + minor
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

// capTags applies tagMaxLen to every tag key and value. Tags are indexed, so an
// oversized key or value would otherwise fail the tag insert for the whole event.
func capTags(tags [][2]string) [][2]string {
	for i := range tags {
		tags[i][0] = truncateTag(tags[i][0])
		tags[i][1] = truncateTag(tags[i][1])
	}
	return tags
}

// truncateTag shortens v to tagMaxLen characters.
func truncateTag(v string) string {
	if utf8.RuneCountInString(v) <= tagMaxLen {
		return v
	}
	return string([]rune(v)[:tagMaxLen])
}
