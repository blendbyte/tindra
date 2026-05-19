package sourcemaps

import (
	"encoding/json"
	"testing"
)

// --- VLQ decoder ---

func TestDecodeVLQ_zero(t *testing.T) {
	v, pos, err := decodeVLQ("A", 0)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 || pos != 1 {
		t.Errorf("got (%d, %d), want (0, 1)", v, pos)
	}
}

func TestDecodeVLQ_positives(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"C", 1},
		{"E", 2},
		{"G", 3},
		{"I", 4},
		{"gB", 16}, // multi-char: (0 | 0x20) + (1 << 5) = 32 → value = 16
	}
	for _, tc := range cases {
		v, _, err := decodeVLQ(tc.s, 0)
		if err != nil {
			t.Fatalf("decodeVLQ(%q): %v", tc.s, err)
		}
		if v != tc.want {
			t.Errorf("decodeVLQ(%q) = %d, want %d", tc.s, v, tc.want)
		}
	}
}

func TestDecodeVLQ_negatives(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"D", -1},
		{"F", -2},
		{"H", -3},
		{"J", -4},
	}
	for _, tc := range cases {
		v, _, err := decodeVLQ(tc.s, 0)
		if err != nil {
			t.Fatalf("decodeVLQ(%q): %v", tc.s, err)
		}
		if v != tc.want {
			t.Errorf("decodeVLQ(%q) = %d, want %d", tc.s, v, tc.want)
		}
	}
}

func TestDecodeVLQ_offset(t *testing.T) {
	// Decode starting mid-string
	v, pos, err := decodeVLQ("AAAC", 3)
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 || pos != 4 {
		t.Errorf("got (%d, %d), want (1, 4)", v, pos)
	}
}

func TestDecodeVLQ_invalidChar(t *testing.T) {
	_, _, err := decodeVLQ("!", 0)
	if err == nil {
		t.Error("expected error for invalid char")
	}
}

func TestDecodeVLQ_emptyAtPos(t *testing.T) {
	_, _, err := decodeVLQ("A", 1) // pos past end
	if err == nil {
		t.Error("expected error for pos past end")
	}
}

// --- SourceMap parse & resolve ---

// minimalSourceMap builds a simple two-line source map by hand.
//
// Generated line 1:
//
//	col 0  → src[0] line 0 col 0   (AAAA)
//	col 4  → src[0] line 0 col 4   (IAAI)
//
// Generated line 2:
//
//	col 0  → src[0] line 1 col 0   (AACJ)
//	          ↑ origLine +1 = 1, origCol -4+0=0 (delta origCol = 0-4 = -4 → J)
func minimalSourceMap() []byte {
	sm := map[string]any{
		"version":        3,
		"sources":        []string{"src/original.js"},
		"sourcesContent": []string{"var a = 1;\nvar b = 2;"},
		"mappings":       "AAAA,IAAI;AACJ",
	}
	data, _ := json.Marshal(sm)
	return data
}

func TestParse_valid(t *testing.T) {
	sm, err := Parse(minimalSourceMap())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sm.lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(sm.lines))
	}
	if len(sm.lines[0]) != 2 {
		t.Fatalf("expected 2 segments on line 1, got %d", len(sm.lines[0]))
	}
	if len(sm.lines[1]) != 1 {
		t.Fatalf("expected 1 segment on line 2, got %d", len(sm.lines[1]))
	}
}

func TestParse_wrongVersion(t *testing.T) {
	data, _ := json.Marshal(map[string]any{"version": 2, "mappings": ""})
	_, err := Parse(data)
	if err == nil {
		t.Error("expected error for version 2")
	}
}

func TestParse_invalidJSON(t *testing.T) {
	_, err := Parse([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestResolve_firstSegment(t *testing.T) {
	sm, _ := Parse(minimalSourceMap())

	got, ok := sm.Resolve(1, 0)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	if got.Source != "src/original.js" {
		t.Errorf("Source: got %q", got.Source)
	}
	if got.Line != 1 {
		t.Errorf("Line: got %d, want 1", got.Line)
	}
	if got.Col != 0 {
		t.Errorf("Col: got %d, want 0", got.Col)
	}
	if got.ContextLine != "var a = 1;" {
		t.Errorf("ContextLine: got %q", got.ContextLine)
	}
}

func TestResolve_midLine(t *testing.T) {
	sm, _ := Parse(minimalSourceMap())

	// col 6 is between seg1(col=0) and seg2(col=4); should pick seg2
	got, ok := sm.Resolve(1, 6)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	if got.Col != 4 {
		t.Errorf("Col: got %d, want 4", got.Col)
	}
}

func TestResolve_secondLine(t *testing.T) {
	sm, _ := Parse(minimalSourceMap())

	got, ok := sm.Resolve(2, 0)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	if got.Line != 2 {
		t.Errorf("Line: got %d, want 2", got.Line)
	}
	if got.Col != 0 {
		t.Errorf("Col: got %d, want 0", got.Col)
	}
	if got.ContextLine != "var b = 2;" {
		t.Errorf("ContextLine: got %q", got.ContextLine)
	}
}

func TestResolve_outOfBounds(t *testing.T) {
	sm, _ := Parse(minimalSourceMap())

	if _, ok := sm.Resolve(0, 0); ok {
		t.Error("line 0 should not resolve")
	}
	if _, ok := sm.Resolve(99, 0); ok {
		t.Error("line 99 should not resolve")
	}
}

func TestResolve_colBeforeFirstSegment(t *testing.T) {
	sm, _ := Parse(minimalSourceMap())

	// Segment on line 1 starts at col 4; col 2 is before it.
	// There's also a segment at col 0 so col 2 should still resolve to col 0.
	got, ok := sm.Resolve(1, 2)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	if got.Col != 0 {
		t.Errorf("Col: got %d, want 0", got.Col)
	}
}

func TestResolve_emptyLine(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"version":  3,
		"sources":  []string{"src/x.js"},
		"mappings": ";AAAA", // line 1 empty, line 2 has one segment
	})
	sm, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := sm.Resolve(1, 0); ok {
		t.Error("empty generated line should not resolve")
	}
	if got, ok := sm.Resolve(2, 0); !ok || got.Line != 1 {
		t.Errorf("line 2 should resolve to original line 1, got %+v ok=%v", got, ok)
	}
}

func TestResolve_noSourcesContent(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"version":  3,
		"sources":  []string{"src/x.js"},
		"mappings": "AAAA",
	})
	sm, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, ok := sm.Resolve(1, 0)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	if got.ContextLine != "" {
		t.Errorf("ContextLine should be empty without sourcesContent, got %q", got.ContextLine)
	}
}
