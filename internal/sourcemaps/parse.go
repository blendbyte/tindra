package sourcemaps

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SourceMap is a parsed JavaScript Source Map version 3.
type SourceMap struct {
	Version        int      `json:"version"`
	Sources        []string `json:"sources"`
	SourcesContent []string `json:"sourcesContent"`
	Names          []string `json:"names"`
	Mappings       string   `json:"mappings"`

	lines [][]segment
}

type segment struct {
	genCol    int
	sourceIdx int
	origLine  int
	origCol   int
	hasSource bool
}

// ResolvedFrame is the result of mapping a generated position to its original source.
type ResolvedFrame struct {
	Source      string // original source filename
	Line        int    // 1-indexed
	Col         int    // 0-indexed
	ContextLine string // source line text, if sourcesContent is present
}

// Parse decodes raw source map JSON and builds the segment index.
func Parse(data []byte) (*SourceMap, error) {
	var sm SourceMap
	if err := json.Unmarshal(data, &sm); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	if sm.Version != 3 {
		return nil, fmt.Errorf("unsupported source map version %d (only v3 supported)", sm.Version)
	}
	if err := sm.parseMappings(); err != nil {
		return nil, fmt.Errorf("mappings: %w", err)
	}
	return &sm, nil
}

// Resolve maps a generated (line, col) to its original source position.
// line is 1-indexed; col is 0-indexed. Returns (nil, false) when unmappable.
func (sm *SourceMap) Resolve(line, col int) (*ResolvedFrame, bool) {
	lineIdx := line - 1
	if lineIdx < 0 || lineIdx >= len(sm.lines) {
		return nil, false
	}
	segs := sm.lines[lineIdx]
	if len(segs) == 0 {
		return nil, false
	}

	// Binary search: rightmost segment with genCol <= col.
	lo, hi, found := 0, len(segs)-1, -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if segs[mid].genCol <= col {
			found = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if found < 0 || !segs[found].hasSource {
		return nil, false
	}

	seg := segs[found]
	source := ""
	if seg.sourceIdx < len(sm.Sources) {
		source = sm.Sources[seg.sourceIdx]
	}

	ctxLine := ""
	if seg.sourceIdx < len(sm.SourcesContent) {
		lines := strings.Split(sm.SourcesContent[seg.sourceIdx], "\n")
		if seg.origLine < len(lines) {
			ctxLine = strings.TrimRight(lines[seg.origLine], "\r")
		}
	}

	return &ResolvedFrame{
		Source:      source,
		Line:        seg.origLine + 1,
		Col:         seg.origCol,
		ContextLine: ctxLine,
	}, true
}

func (sm *SourceMap) parseMappings() error {
	rawLines := strings.Split(sm.Mappings, ";")
	sm.lines = make([][]segment, len(rawLines))

	// All source fields are cumulative across the entire mappings string.
	srcIdx, origLine, origCol := 0, 0, 0

	for li, rawLine := range rawLines {
		if rawLine == "" {
			continue
		}
		genCol := 0 // resets at each new generated line
		for rawSeg := range strings.SplitSeq(rawLine, ",") {
			if rawSeg == "" {
				continue
			}
			pos := 0

			delta, newPos, err := decodeVLQ(rawSeg, pos)
			if err != nil {
				return fmt.Errorf("line %d genCol: %w", li+1, err)
			}
			genCol += delta
			pos = newPos

			seg := segment{genCol: genCol}

			if pos < len(rawSeg) {
				// source index
				delta, newPos, err = decodeVLQ(rawSeg, pos)
				if err != nil {
					return fmt.Errorf("line %d srcIdx: %w", li+1, err)
				}
				srcIdx += delta
				pos = newPos

				// original line
				delta, newPos, err = decodeVLQ(rawSeg, pos)
				if err != nil {
					return fmt.Errorf("line %d origLine: %w", li+1, err)
				}
				origLine += delta
				pos = newPos

				// original column
				delta, newPos, err = decodeVLQ(rawSeg, pos)
				if err != nil {
					return fmt.Errorf("line %d origCol: %w", li+1, err)
				}
				origCol += delta
				pos = newPos

				seg.sourceIdx = srcIdx
				seg.origLine = origLine
				seg.origCol = origCol
				seg.hasSource = true

				// names index is optional - skip if present
				if pos < len(rawSeg) {
					_, _, _ = decodeVLQ(rawSeg, pos)
				}
			}

			sm.lines[li] = append(sm.lines[li], seg)
		}
	}
	return nil
}

const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// decodeVLQ decodes one Base64-VLQ integer from s starting at pos.
// Returns (value, newPos, error).
func decodeVLQ(s string, pos int) (int, int, error) {
	result, shift := 0, 0
	for {
		if pos >= len(s) {
			return 0, pos, fmt.Errorf("unexpected end of VLQ string")
		}
		digit := strings.IndexByte(b64chars, s[pos])
		if digit < 0 {
			return 0, pos, fmt.Errorf("invalid base64 char %q", s[pos])
		}
		pos++
		result |= (digit & 0x1f) << shift
		shift += 5
		if digit&0x20 == 0 {
			break
		}
	}
	if result&1 != 0 {
		return -(result >> 1), pos, nil
	}
	return result >> 1, pos, nil
}
