package sourcemaps

import (
	"context"
	"encoding/json"

	"github.com/blendbyte/tindra/internal/storage"
)

type FrameVerification struct {
	URL           string `json:"url"`
	NormalizedURL string `json:"normalized_url"`
	Line          int    `json:"line"`
	Column        int    `json:"column"`
	Status        string `json:"status"`
	MapID         string `json:"map_id,omitempty"`
	Source        string `json:"source,omitempty"`
	OriginalLine  int    `json:"original_line,omitempty"`
}

type Verification struct {
	Release   string              `json:"release"`
	Status    string              `json:"status"`
	Frames    []FrameVerification `json:"frames"`
	Truncated bool                `json:"truncated"`
}

// VerifyEvent tests uploaded maps only. Fetching a generated JS file for context
// is useful in the event viewer, but is not evidence that a source map works.
func (s *Store) VerifyEvent(ctx context.Context, projectID string, payload json.RawMessage) (*Verification, error) {
	ctx, cancel := context.WithTimeout(ctx, enrichmentTimeout)
	defer cancel()
	var event struct {
		Release   string `json:"release"`
		Platform  string `json:"platform"`
		Exception struct {
			Values []struct {
				Stacktrace struct {
					Frames []struct {
						AbsPath  string `json:"abs_path"`
						Filename string `json:"filename"`
						Lineno   int    `json:"lineno"`
						Colno    int    `json:"colno"`
					} `json:"frames"`
				} `json:"stacktrace"`
			} `json:"values"`
		} `json:"exception"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, err
	}
	out := &Verification{Release: event.Release, Status: "no_frames", Frames: []FrameVerification{}}
	if event.Platform != "" && event.Platform != "javascript" && event.Platform != "node" {
		out.Status = "not_applicable"
		return out, nil
	}
	for _, exception := range event.Exception.Values {
		for _, frame := range exception.Stacktrace.Frames {
			url := frame.AbsPath
			if url == "" {
				url = frame.Filename
			}
			if url == "" || frame.Lineno <= 0 {
				continue
			}
			if len(out.Frames) >= 40 {
				out.Truncated = true
				break
			}
			f := FrameVerification{URL: url, NormalizedURL: NormalizeURL(url), Line: frame.Lineno, Column: frame.Colno, Status: "missing_release"}
			if event.Release != "" {
				sm, err := storage.GetSourcemap(ctx, s.pool, projectID, event.Release, f.NormalizedURL)
				if err != nil {
					return nil, err
				}
				f.Status = "no_matching_map"
				if sm != nil {
					f.MapID = sm.ID
					parsed := s.parsedSourceMap(ctx, projectID, sm.ContentHash)
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					f.Status = "map_unreadable"
					if parsed != nil {
						f.Status = "no_mapping"
						if resolved, found := parsed.Resolve(f.Line, f.Column); found && resolved.Source != "" {
							f.Status, f.Source, f.OriginalLine = "verified", resolved.Source, resolved.Line
						}
					}
				}
			}
			out.Frames = append(out.Frames, f)
		}
	}
	if len(out.Frames) > 0 {
		out.Status = "needs_attention"
		verified := 0
		for _, f := range out.Frames {
			if f.Status == "verified" {
				verified++
			}
		}
		if verified > 0 {
			out.Status = "partially_verified"
		}
		if verified == len(out.Frames) && !out.Truncated {
			out.Status = "verified"
		}
	}
	return out, nil
}
