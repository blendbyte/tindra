package api

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/blendbyte/tindra/internal/storage"
)

// get_issue returns a trimmed event so a model reads the crash without paying
// for modules, request headers, frame variables and long breadcrumb trails.
// Every section it trims or drops is named in "omitted", and get_event_detail
// returns those sections in full.

const (
	// mcpTrimBreadcrumbs is how many of the newest breadcrumbs are kept.
	mcpTrimBreadcrumbs = 10
	// mcpTrimTailFrames is how many frames from the crash end are kept when
	// no frame is marked in-app.
	mcpTrimTailFrames = 10
)

// mcpKeptFields are copied into the trimmed event as sent. They are small and
// are what a reader needs to place the event.
var mcpKeptFields = []string{
	"event_id", "timestamp", "level", "platform", "logger", "transaction",
	"culprit", "server_name", "release", "dist", "environment",
	"message", "logentry", "tags", "user",
}

// mcpKeptRequestFields and mcpKeptContextFields are the parts of request and
// contexts that stay in the trimmed event.
var (
	mcpKeptRequestFields = []string{"url", "method"}
	mcpKeptContextFields = []string{"name", "version", "trace_id", "span_id", "op", "status"}
)

// mcpTrimEvent returns the trimmed payload and the sorted names of the
// top-level sections that were dropped or shortened. A payload that is not a
// JSON object is returned unchanged.
func mcpTrimEvent(raw json.RawMessage) (json.RawMessage, []string) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return raw, nil
	}

	out := make(map[string]any, len(mcpKeptFields)+5)
	for _, key := range mcpKeptFields {
		if v, ok := data[key]; ok {
			out[key] = v
		}
	}
	exc, hasExc := data["exception"].(map[string]any)
	if hasExc {
		out["exception"] = mcpTrimValues(exc, nil)
	}
	// Message events with attach_stacktrace, and Cocoa and native crashes
	// whose exception has no stack of its own, carry the stack only on the
	// thread that crashed or was current.
	if threads, ok := data["threads"].(map[string]any); ok && !mcpHasStacktrace(exc) {
		out["threads"] = mcpTrimValues(threads, mcpCrashedThreads)
	}
	if crumbs, ok := mcpTrimBreadcrumbsOf(data["breadcrumbs"]); ok {
		out["breadcrumbs"] = crumbs
	}
	if req, ok := data["request"].(map[string]any); ok {
		if kept := mcpPick(req, mcpKeptRequestFields); len(kept) > 0 {
			out["request"] = kept
		}
	}
	if contexts, ok := data["contexts"].(map[string]any); ok {
		compact := map[string]any{}
		for name, v := range contexts {
			if ctx, ok := v.(map[string]any); ok {
				if kept := mcpPick(ctx, mcpKeptContextFields); len(kept) > 0 {
					compact[name] = kept
				}
			}
		}
		if len(compact) > 0 {
			out["contexts"] = compact
		}
	}

	var omitted []string
	for key, v := range data {
		if kept, ok := out[key]; !ok || !reflect.DeepEqual(kept, v) {
			omitted = append(omitted, key)
		}
	}
	sort.Strings(omitted)

	b, err := json.Marshal(out)
	if err != nil {
		return raw, nil
	}
	return b, omitted
}

// mcpTrimValues trims the stack traces of an exception or threads interface
// ({"values": [...]}). keep, when set, selects which values stay.
func mcpTrimValues(iface map[string]any, keep func([]any) []any) map[string]any {
	values, _ := iface["values"].([]any)
	if keep != nil {
		values = keep(values)
	}
	trimmed := make([]any, 0, len(values))
	for _, v := range values {
		val, ok := v.(map[string]any)
		if !ok {
			continue
		}
		copied := make(map[string]any, len(val))
		for k, field := range val {
			if k == "raw_stacktrace" {
				continue
			}
			copied[k] = field
		}
		if st, ok := val["stacktrace"].(map[string]any); ok {
			copied["stacktrace"] = mcpTrimStacktrace(st)
		}
		trimmed = append(trimmed, copied)
	}
	return map[string]any{"values": trimmed}
}

// mcpHasStacktrace reports whether any value of an exception interface has
// stack frames.
func mcpHasStacktrace(iface map[string]any) bool {
	values, _ := iface["values"].([]any)
	return slices.ContainsFunc(values, func(v any) bool {
		val, _ := v.(map[string]any)
		st, _ := val["stacktrace"].(map[string]any)
		frames, _ := st["frames"].([]any)
		return len(frames) > 0
	})
}

// mcpCrashedThreads keeps the threads marked crashed, else those marked
// current, else the first one.
func mcpCrashedThreads(values []any) []any {
	for _, flag := range []string{"crashed", "current"} {
		var picked []any
		for _, v := range values {
			if t, ok := v.(map[string]any); ok && t[flag] == true {
				picked = append(picked, v)
			}
		}
		if len(picked) > 0 {
			return picked
		}
	}
	if len(values) > 0 {
		return values[:1]
	}
	return nil
}

// mcpTrimStacktrace keeps the in-app frames and the crashing frame (the last
// one), or the last mcpTrimTailFrames frames when no frame is in-app. Frame
// variables and registers are dropped. frames_omitted counts dropped frames.
func mcpTrimStacktrace(st map[string]any) map[string]any {
	frames, _ := st["frames"].([]any)
	anyInApp := slices.ContainsFunc(frames, func(f any) bool {
		frame, _ := f.(map[string]any)
		return frame["in_app"] == true
	})
	kept := make([]any, 0, len(frames))
	for i, f := range frames {
		frame, ok := f.(map[string]any)
		if !ok {
			continue
		}
		last := i == len(frames)-1
		if anyInApp && frame["in_app"] != true && !last {
			continue
		}
		if !anyInApp && i < len(frames)-mcpTrimTailFrames {
			continue
		}
		copied := make(map[string]any, len(frame))
		for k, v := range frame {
			if k != "vars" {
				copied[k] = v
			}
		}
		kept = append(kept, copied)
	}
	out := map[string]any{"frames": kept}
	if dropped := len(frames) - len(kept); dropped > 0 {
		out["frames_omitted"] = dropped
	}
	return out
}

// mcpTrimBreadcrumbsOf keeps the newest breadcrumbs, in either the
// {"values": [...]} form or the older bare array.
func mcpTrimBreadcrumbsOf(v any) (map[string]any, bool) {
	var values []any
	switch crumbs := v.(type) {
	case map[string]any:
		values, _ = crumbs["values"].([]any)
	case []any:
		values = crumbs
	default:
		return nil, false
	}
	if len(values) == 0 {
		return nil, false
	}
	if len(values) > mcpTrimBreadcrumbs {
		values = values[len(values)-mcpTrimBreadcrumbs:]
	}
	return map[string]any{"values": values}, true
}

func mcpPick(m map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// mcpIssueInScope loads an issue, hiding issues outside a Bearer token's
// project.
func (ro *router) mcpIssueInScope(ctx context.Context, ref string) (*storage.Issue, error) {
	if ref == "" {
		return nil, mcpToolError{"issue id is required"}
	}
	id, found, err := ro.resolveIssueRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, mcpToolError{"issue not found"}
	}
	issue, err := storage.GetIssue(ctx, ro.pool, id)
	if err != nil {
		return nil, fmt.Errorf("get issue: %w", err)
	}
	if issue == nil {
		return nil, mcpToolError{"issue not found"}
	}
	if !projectAllowed(ctx, issue.ProjectID) {
		return nil, mcpToolError{"issue not found"}
	}
	return issue, nil
}

// mcpIssueEventAt returns the issue's event at offset (0 = newest) with its
// stack traces source-mapped, or nil when there is none.
func (ro *router) mcpIssueEventAt(ctx context.Context, issue *storage.Issue, offset int) (*storage.EventRow, json.RawMessage, error) {
	ev, err := storage.GetEventForIssueAtOffset(ctx, ro.pool, issue.ID, max(offset, 0))
	if err != nil {
		return nil, nil, fmt.Errorf("get event: %w", err)
	}
	if ev == nil {
		return nil, nil, nil
	}
	payload := ev.Payload
	if ro.smStore != nil {
		release := ""
		if ev.Release != nil {
			release = *ev.Release
		}
		payload = ro.smStore.ResolveEventPayload(ctx, issue.ProjectID, release, payload)
	}
	return ev, payload, nil
}

func (ro *router) mcpGetEventDetail(ctx context.Context, args map[string]any) (string, error) {
	issue, err := ro.mcpIssueInScope(ctx, mcpArgString(args, "issue_id"))
	if err != nil {
		return "", err
	}
	ev, payload, err := ro.mcpIssueEventAt(ctx, issue, mcpArgInt(args, "offset", 0))
	if err != nil {
		return "", err
	}
	if ev == nil {
		return "", mcpToolError{"issue has no event at this offset"}
	}

	sections := slices.DeleteFunc(mcpArgStrings(args, "sections"), func(s string) bool { return s == "" })
	out := struct {
		EventID string                     `json:"event_id"`
		Payload json.RawMessage            `json:"payload,omitempty"`
		Section map[string]json.RawMessage `json:"sections,omitempty"`
		Missing []string                   `json:"missing,omitempty"`
	}{EventID: ev.ID}

	if len(sections) == 0 || slices.Contains(sections, "all") {
		out.Payload = payload
	} else {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(payload, &data); err != nil {
			return "", mcpToolError{"event payload is not a JSON object"}
		}
		out.Section = map[string]json.RawMessage{}
		for _, name := range sections {
			if v, ok := data[name]; ok {
				out.Section[name] = v
			} else {
				out.Missing = append(out.Missing, name)
			}
		}
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}
