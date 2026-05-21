package issues

import (
	"encoding/json"
	"strings"
)

const tagMaxLen = 200

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
			URL string `json:"url"`
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
	for ctxKey, raw := range p.Contexts {
		var ctx struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Build   string `json:"build"`
		}
		if err := json.Unmarshal(raw, &ctx); err != nil || ctx.Name == "" {
			continue
		}
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
