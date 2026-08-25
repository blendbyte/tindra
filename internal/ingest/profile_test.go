package ingest_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "profiles", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// Both SDKs we care about disagree on nearly every optional detail, so the
// baseline assertion is that they normalize to the same shape.
func TestParseProfile_v1AcrossSDKs(t *testing.T) {
	tests := []struct {
		fixture     string
		platform    string
		txName      string
		txID        string
		traceID     string
		activeTID   string
		threadID    string
		wantSamples int
	}{
		{
			fixture:     "v1_php_laravel.json",
			platform:    "php",
			txName:      "GET /api/orders",
			txID:        "3c2b1a09f8e7d6c5b4a39281706f5e4d",
			traceID:     "9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
			activeTID:   "0",
			threadID:    "0",
			wantSamples: 5,
		},
		{
			fixture:     "v1_python.json",
			platform:    "python",
			txName:      "GET /api/orders",
			txID:        "5e6f70819a2b3c4d5e6f70819a2b3c4d",
			traceID:     "c4d5e6f70819a2b3c4d5e6f70819a2b3",
			activeTID:   "8412331008",
			threadID:    "8412331008",
			wantSamples: 5,
		},
		{
			fixture:     "v1_cocoa.json",
			platform:    "cocoa",
			txName:      "OrdersViewController",
			txID:        "0718293a4b5c6d7e8f901234b3d4e5f6",
			traceID:     "e5f60718293a4b5c6d7e8f901234b3d4",
			activeTID:   "771",
			threadID:    "771",
			wantSamples: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			p, err := ingest.ParseProfileItem("profile", fixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if p.Format != ingest.ProfileFormatV1 {
				t.Errorf("format = %v, want v1", p.Format)
			}
			if p.Platform != tt.platform {
				t.Errorf("platform = %q, want %q", p.Platform, tt.platform)
			}
			// PHP sends `transaction`, Python and Cocoa send `transactions`.
			// Both must land in the same place.
			if p.TransactionName != tt.txName {
				t.Errorf("transaction name = %q, want %q", p.TransactionName, tt.txName)
			}
			if p.TransactionID != tt.txID {
				t.Errorf("transaction id = %q, want %q", p.TransactionID, tt.txID)
			}
			if p.TraceID != tt.traceID {
				t.Errorf("trace id = %q, want %q", p.TraceID, tt.traceID)
			}
			if p.ActiveThreadID != tt.activeTID {
				t.Errorf("active thread id = %q, want %q", p.ActiveThreadID, tt.activeTID)
			}
			if len(p.Samples) != tt.wantSamples {
				t.Fatalf("samples = %d, want %d", len(p.Samples), tt.wantSamples)
			}
			for _, s := range p.Samples {
				if s.ThreadID != tt.threadID {
					t.Errorf("sample thread id = %q, want %q", s.ThreadID, tt.threadID)
				}
			}
			if p.StartNs >= p.EndNs {
				t.Errorf("start %d should precede end %d", p.StartNs, p.EndNs)
			}
		})
	}
}

// The PHP SDK sends elapsed_since_start_ns as a JSON number and the Python SDK
// sends the identical field as a JSON string. Both must resolve to the same
// absolute nanosecond timestamp.
func TestParseProfile_numberOrStringElapsed(t *testing.T) {
	php, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse php: %v", err)
	}
	py, err := ingest.ParseProfile(fixture(t, "v1_python.json"))
	if err != nil {
		t.Fatalf("parse python: %v", err)
	}

	// Both fixtures sample on the same 9.9ms cadence starting at the same offset.
	phpOffsets := sampleOffsets(php)
	pyOffsets := sampleOffsets(py)
	if len(phpOffsets) != len(pyOffsets) {
		t.Fatalf("sample counts differ: php %d, python %d", len(phpOffsets), len(pyOffsets))
	}
	for i := range phpOffsets {
		if phpOffsets[i] != pyOffsets[i] {
			t.Errorf("offset %d: php %d ns, python %d ns (numeric and string forms must agree)",
				i, phpOffsets[i], pyOffsets[i])
		}
	}
}

func sampleOffsets(p *ingest.Profile) []int64 {
	out := make([]int64, len(p.Samples))
	for i, s := range p.Samples {
		out[i] = s.TimestampNs - p.StartNs
	}
	return out
}

// The v1 timestamp is the origin every sample is offset from, so the absolute
// times have to land on the declared wall clock rather than on ingest time.
func TestParseProfile_v1SamplesAreAbsolute(t *testing.T) {
	p, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	base, err := time.Parse(time.RFC3339Nano, "2026-08-24T10:15:32.123+00:00")
	if err != nil {
		t.Fatalf("parse expected base: %v", err)
	}
	// First sample sits 9,900,990 ns past the profile start.
	want := base.Add(9900990 * time.Nanosecond)
	if !p.Start().Equal(want) {
		t.Errorf("first sample at %s, want %s", p.Start(), want)
	}
}

func TestParseProfileChunk_v2(t *testing.T) {
	tests := []struct {
		fixture    string
		platform   string
		profilerID string
		chunkID    string
		threadID   string
	}{
		{
			fixture:    "v2_python_chunk.json",
			platform:   "python",
			profilerID: "4d229f1d3807421ba62a5f8bc295d836",
			chunkID:    "7f8e9d0c1b2a3948576655443322110f",
			threadID:   "8412331008",
		},
		{
			fixture:    "v2_cocoa_chunk.json",
			platform:   "cocoa",
			profilerID: "60718293a4b5c6d7e8f901234b3d4e5f",
			chunkID:    "a1b2c3d4e5f60718293a4b5c6d7e8f90",
			threadID:   "0x000000016b2f4300",
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			p, err := ingest.ParseProfileItem("profile_chunk", fixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if p.Format != ingest.ProfileFormatV2 {
				t.Errorf("format = %v, want v2", p.Format)
			}
			if p.ProfilerID != tt.profilerID {
				t.Errorf("profiler id = %q, want %q", p.ProfilerID, tt.profilerID)
			}
			if p.ChunkID != tt.chunkID {
				t.Errorf("chunk id = %q, want %q", p.ChunkID, tt.chunkID)
			}
			// v2 thread ids are opaque strings, hex addresses on Cocoa.
			for _, s := range p.Samples {
				if s.ThreadID != tt.threadID {
					t.Errorf("sample thread id = %q, want %q", s.ThreadID, tt.threadID)
				}
			}
			// A chunk carries no transaction link of its own.
			if p.TransactionID != "" || p.TransactionName != "" {
				t.Errorf("chunk should carry no transaction link, got id=%q name=%q",
					p.TransactionID, p.TransactionName)
			}
		})
	}
}

// The Python SDK omits the top-level timestamp entirely, so a chunk's bounds
// have to come from its samples.
func TestParseProfileChunk_boundsFromSamplesWhenTimestampAbsent(t *testing.T) {
	raw := fixture(t, "v2_python_chunk.json")

	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if _, present := probe["timestamp"]; present {
		t.Fatal("fixture should have no top-level timestamp; it is the case under test")
	}

	p, err := ingest.ParseProfileChunk(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wantStart := time.Unix(0, int64(1787911732.101*1e9)).UTC()
	wantEnd := time.Unix(0, int64(1787911732.141*1e9)).UTC()
	if d := p.Start().Sub(wantStart); d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("start = %s, want ~%s", p.Start(), wantStart)
	}
	if d := p.End().Sub(wantEnd); d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("end = %s, want ~%s", p.End(), wantEnd)
	}
}

// A v2 chunk is reached from the transaction, not the other way round, so the
// ids on both sides have to line up.
func TestProfileChunkLinksToTransaction(t *testing.T) {
	chunk, err := ingest.ParseProfileChunk(fixture(t, "v2_python_chunk.json"))
	if err != nil {
		t.Fatalf("parse chunk: %v", err)
	}

	var tx struct {
		Contexts struct {
			Trace struct {
				Data map[string]string `json:"data"`
			} `json:"trace"`
			Profile struct {
				ProfilerID string `json:"profiler_id"`
			} `json:"profile"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(fixture(t, "v2_transaction_link.json"), &tx); err != nil {
		t.Fatalf("unmarshal transaction: %v", err)
	}

	if tx.Contexts.Profile.ProfilerID != chunk.ProfilerID {
		t.Errorf("transaction profiler_id = %q, chunk = %q",
			tx.Contexts.Profile.ProfilerID, chunk.ProfilerID)
	}

	threadID := tx.Contexts.Trace.Data["thread.id"]
	if threadID == "" {
		t.Fatal("transaction carries no thread.id")
	}
	var found bool
	for _, s := range chunk.Samples {
		if s.ThreadID == threadID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no chunk sample on the transaction's thread %q", threadID)
	}
}

// Stacks run leaf first. Getting this backwards renders every flame graph
// upside down, so it is asserted rather than assumed.
func TestParseProfile_stacksAreLeafFirst(t *testing.T) {
	p, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Fixture stack 1 is Builder::get called from OrderController::index,
	// called from Kernel::handle, called from index.php.
	stack := p.Stacks[1]
	want := []string{
		"Illuminate\\Database\\Query\\Builder::get",
		"App\\Http\\Controllers\\OrderController::index",
		"Illuminate\\Foundation\\Http\\Kernel::handle",
		"/var/www/app/public/index.php",
	}
	if len(stack) != len(want) {
		t.Fatalf("stack depth = %d, want %d", len(stack), len(want))
	}
	for i, frameID := range stack {
		if got := p.Frames[frameID].Function; got != want[i] {
			t.Errorf("frame %d = %q, want %q (stacks must be leaf first)", i, got, want[i])
		}
	}
}

func TestParseProfile_frameFieldsAndInApp(t *testing.T) {
	php, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse php: %v", err)
	}
	// The PHP SDK never sets in_app, and absent must not collapse to false.
	for i, f := range php.Frames {
		if f.InApp != nil {
			t.Errorf("php frame %d has in_app set to %v, want absent", i, *f.InApp)
		}
	}
	if got := php.Frames[3].Lineno; got != 2723 {
		t.Errorf("php frame lineno = %d, want 2723", got)
	}
	if got := php.Frames[3].AbsPath; got == "" {
		t.Error("php frame should carry abs_path")
	}

	py, err := ingest.ParseProfile(fixture(t, "v1_python.json"))
	if err != nil {
		t.Fatalf("parse python: %v", err)
	}
	// The Python SDK does set it, including false for stdlib frames.
	var sawTrue, sawFalse bool
	for _, f := range py.Frames {
		if f.InApp == nil {
			t.Fatal("python frames should all carry in_app")
		}
		if *f.InApp {
			sawTrue = true
		} else {
			sawFalse = true
		}
	}
	if !sawTrue || !sawFalse {
		t.Errorf("expected both in_app values in the python fixture, got true=%v false=%v", sawTrue, sawFalse)
	}
}

// Native platforms arrive unsymbolicated. We keep the addresses rather than
// dropping the frames, even though we cannot resolve them.
func TestParseProfile_unsymbolicatedFrames(t *testing.T) {
	p, err := ingest.ParseProfile(fixture(t, "v1_cocoa.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for i, f := range p.Frames {
		if f.InstructionAddr == "" {
			t.Errorf("cocoa frame %d has no instruction_addr", i)
		}
		if f.Function != "" {
			t.Errorf("cocoa frame %d unexpectedly has a function name %q", i, f.Function)
		}
	}
}

// Relay accepts name/file/line/column as aliases for function/filename/lineno/colno.
func TestParseProfile_frameKeyAliases(t *testing.T) {
	payload := `{
		"version":"1","platform":"node","timestamp":"2026-08-24T10:00:00Z",
		"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{
			"frames":[{"name":"handler","file":"server.js","line":42,"column":7}],
			"stacks":[[0]],
			"samples":[
				{"stack_id":0,"thread_id":"0","elapsed_since_start_ns":1000000},
				{"stack_id":0,"thread_id":"0","elapsed_since_start_ns":2000000}
			]
		}
	}`
	p, err := ingest.ParseProfile([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	f := p.Frames[0]
	if f.Function != "handler" {
		t.Errorf(`function = %q, want "handler" (alias "name")`, f.Function)
	}
	if f.Filename != "server.js" {
		t.Errorf(`filename = %q, want "server.js" (alias "file")`, f.Filename)
	}
	if f.Lineno != 42 {
		t.Errorf(`lineno = %d, want 42 (alias "line")`, f.Lineno)
	}
	if f.Colno != 7 {
		t.Errorf(`colno = %d, want 7 (alias "column")`, f.Colno)
	}
}

func TestParseProfile_profileIDAliasesEventID(t *testing.T) {
	p, err := ingest.ParseProfile([]byte(twoSampleProfile(`"profile_id":"fedcba98"`)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.EventID != "fedcba98" {
		t.Errorf("event id = %q, want %q from the profile_id alias", p.EventID, "fedcba98")
	}
}

// Relay drops every sample on a thread with at most one non-idle sample: a
// lone sample says nothing about where time went.
func TestParseProfile_dropsSingleSampleThreads(t *testing.T) {
	payload := `{
		"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",
		"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{
			"frames":[{"function":"busy"},{"function":"lonely"}],
			"stacks":[[0],[1]],
			"samples":[
				{"stack_id":0,"thread_id":"main","elapsed_since_start_ns":1000000},
				{"stack_id":0,"thread_id":"main","elapsed_since_start_ns":2000000},
				{"stack_id":1,"thread_id":"worker","elapsed_since_start_ns":1500000}
			],
			"thread_metadata":{"main":{"name":"MainThread"},"worker":{"name":"Worker-1"}}
		}
	}`
	p, err := ingest.ParseProfile([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Samples) != 2 {
		t.Fatalf("samples = %d, want 2 after dropping the single-sample thread", len(p.Samples))
	}
	for _, s := range p.Samples {
		if s.ThreadID == "worker" {
			t.Error("worker thread had one sample and should have been dropped")
		}
	}
	// Its metadata should go with it.
	if _, ok := p.ThreadNames["worker"]; ok {
		t.Error("thread metadata for the dropped thread should be removed")
	}
	if p.ThreadNames["main"] != "MainThread" {
		t.Errorf("surviving thread name = %q, want MainThread", p.ThreadNames["main"])
	}
}

// Idle samples (an empty stack) do not count toward a thread staying alive.
func TestParseProfile_idleSamplesDoNotKeepAThread(t *testing.T) {
	payload := `{
		"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",
		"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{
			"frames":[{"function":"busy"}],
			"stacks":[[0],[]],
			"samples":[
				{"stack_id":0,"thread_id":"idle","elapsed_since_start_ns":1000000},
				{"stack_id":1,"thread_id":"idle","elapsed_since_start_ns":2000000},
				{"stack_id":1,"thread_id":"idle","elapsed_since_start_ns":3000000}
			]
		}
	}`
	_, err := ingest.ParseProfile([]byte(payload))
	if !errors.Is(err, ingest.ErrProfileNoSamples) {
		t.Errorf("err = %v, want ErrProfileNoSamples", err)
	}
}

func TestParseProfile_rejectsMalformedReferences(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		want    error
	}{
		{
			// The thread needs two valid samples to survive the single-sample
			// filter, which runs first and ignores samples pointing at missing
			// stacks. A thread made only of dangling samples is dropped as
			// empty rather than reported as malformed, matching Relay.
			name: "sample points at a missing stack",
			profile: `{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000},
				{"stack_id":9,"thread_id":"1","elapsed_since_start_ns":3000000}]}`,
			want: ingest.ErrProfileMalformedSamples,
		},
		{
			name: "every sample on a thread dangles",
			profile: `{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
				{"stack_id":9,"thread_id":"1","elapsed_since_start_ns":1000000},
				{"stack_id":9,"thread_id":"1","elapsed_since_start_ns":2000000}]}`,
			want: ingest.ErrProfileNoSamples,
		},
		{
			name: "stack points at a missing frame",
			profile: `{"frames":[{"function":"f"}],"stacks":[[0],[7]],"samples":[
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
				{"stack_id":1,"thread_id":"1","elapsed_since_start_ns":2000000}]}`,
			want: ingest.ErrProfileMalformedStacks,
		},
		{
			name:    "no samples at all",
			profile: `{"frames":[],"stacks":[],"samples":[]}`,
			want:    ingest.ErrProfileNoSamples,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"version":"1","platform":"python",
				"timestamp":"2026-08-24T10:00:00Z","event_id":"aa",
				"transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
				"profile":%s}`, tt.profile)
			_, err := ingest.ParseProfile([]byte(payload))
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// v1 caps at 30s and v2 at 66s, matching Relay.
func TestParseProfile_durationCaps(t *testing.T) {
	t.Run("v1 over 30s", func(t *testing.T) {
		payload := `{
			"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",
			"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
			"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":31000000000}]}}`
		_, err := ingest.ParseProfile([]byte(payload))
		if !errors.Is(err, ingest.ErrProfileTooLong) {
			t.Errorf("err = %v, want ErrProfileTooLong", err)
		}
	})

	t.Run("v1 measures from the declared start, not the first sample", func(t *testing.T) {
		// Samples span only 5s but sit 31s past the profile origin, which is
		// what Relay rejects on.
		payload := `{
			"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",
			"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
			"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":26000000000},
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":31000000000}]}}`
		_, err := ingest.ParseProfile([]byte(payload))
		if !errors.Is(err, ingest.ErrProfileTooLong) {
			t.Errorf("err = %v, want ErrProfileTooLong", err)
		}
	})

	t.Run("v2 over 66s", func(t *testing.T) {
		payload := `{
			"version":"2","platform":"python","chunk_id":"aa","profiler_id":"bb",
			"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
				{"stack_id":0,"thread_id":"1","timestamp":1787911732.0},
				{"stack_id":0,"thread_id":"1","timestamp":1787911799.0}]}}`
		_, err := ingest.ParseProfileChunk([]byte(payload))
		if !errors.Is(err, ingest.ErrProfileTooLong) {
			t.Errorf("err = %v, want ErrProfileTooLong", err)
		}
	})

	t.Run("v2 just under 66s is accepted", func(t *testing.T) {
		payload := `{
			"version":"2","platform":"python","chunk_id":"aa","profiler_id":"bb",
			"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
				{"stack_id":0,"thread_id":"1","timestamp":1787911732.0},
				{"stack_id":0,"thread_id":"1","timestamp":1787911797.0}]}}`
		if _, err := ingest.ParseProfileChunk([]byte(payload)); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestParseProfile_samplesAreSorted(t *testing.T) {
	payload := `{
		"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",
		"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":3000000},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000}]}}`
	p, err := ingest.ParseProfile([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for i := 1; i < len(p.Samples); i++ {
		if p.Samples[i-1].TimestampNs > p.Samples[i].TimestampNs {
			t.Fatalf("samples are not sorted: %v", sampleOffsets(p))
		}
	}
	if p.StartNs != p.Samples[0].TimestampNs || p.EndNs != p.Samples[len(p.Samples)-1].TimestampNs {
		t.Error("bounds should follow the sorted samples")
	}
}

func TestParseProfileItem_versionMismatch(t *testing.T) {
	tests := []struct {
		name     string
		itemType string
		payload  string
	}{
		{"chunk payload sent as a profile", "profile", `{"version":"2","profile":{}}`},
		{"profile payload sent as a chunk", "profile_chunk", `{"version":"1","profile":{}}`},
		{"unknown item type", "replay_event", `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ingest.ParseProfileItem(tt.itemType, []byte(tt.payload))
			if !errors.Is(err, ingest.ErrProfileUnsupportedVersion) {
				t.Errorf("err = %v, want ErrProfileUnsupportedVersion", err)
			}
		})
	}
}

func TestParseProfile_invalidJSON(t *testing.T) {
	if _, err := ingest.ParseProfile([]byte(`{not json`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
	if _, err := ingest.ParseProfileChunk([]byte(`{not json`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}

func TestParseProfile_tooManySamples(t *testing.T) {
	var b []byte
	b = append(b, `{"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",
		"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[`...)
	for i := 0; i < 200_001; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, fmt.Sprintf(`{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":%d}`, i)...)
	}
	b = append(b, `]}}`...)

	_, err := ingest.ParseProfile(b)
	if !errors.Is(err, ingest.ErrProfileTooManySamples) {
		t.Errorf("err = %v, want ErrProfileTooManySamples", err)
	}
}

// twoSampleProfile builds a minimal valid v1 payload with the given extra
// top-level field spliced in.
func twoSampleProfile(extra string) string {
	return fmt.Sprintf(`{
		"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",%s,
		"transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000}]}}`, extra)
}

// The lenient scalar decoders exist only to absorb cross-SDK variation, so the
// odd shapes they are meant to handle are worth pinning down explicitly.
func TestParseProfile_lenientScalars(t *testing.T) {
	tests := []struct {
		name         string
		threadID     string // raw JSON for thread_id
		elapsed      string // raw JSON for elapsed_since_start_ns
		wantThreadID string
		wantOffsetNs int64
		wantErr      bool
	}{
		{name: "bare number thread id", threadID: `42`, elapsed: `2000000`, wantThreadID: "42", wantOffsetNs: 1000000},
		{name: "quoted thread id", threadID: `"42"`, elapsed: `"2000000"`, wantThreadID: "42", wantOffsetNs: 1000000},
		{name: "hex address thread id", threadID: `"0x16b2f4300"`, elapsed: `2000000`, wantThreadID: "0x16b2f4300", wantOffsetNs: 1000000},
		{name: "null thread id", threadID: `null`, elapsed: `2000000`, wantThreadID: "", wantOffsetNs: 1000000},
		{name: "exponent in a string", threadID: `"1"`, elapsed: `"2.0e6"`, wantThreadID: "1", wantOffsetNs: 1000000},
		{name: "bare float elapsed", threadID: `"1"`, elapsed: `2000000.9`, wantThreadID: "1", wantOffsetNs: 1000000},
		// A null offset means "at the profile origin", so it sorts ahead of the
		// 1ms sample rather than failing the parse.
		{name: "null elapsed collapses to the origin", threadID: `"1"`, elapsed: `null`, wantThreadID: "1", wantOffsetNs: 1000000},
		{name: "garbage elapsed", threadID: `"1"`, elapsed: `"not-a-number"`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{
				"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z",
				"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
				"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
					{"stack_id":0,"thread_id":%s,"elapsed_since_start_ns":1000000},
					{"stack_id":0,"thread_id":%s,"elapsed_since_start_ns":%s}]}}`,
				tt.threadID, tt.threadID, tt.elapsed)

			p, err := ingest.ParseProfile([]byte(payload))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := p.Samples[0].ThreadID; got != tt.wantThreadID {
				t.Errorf("thread id = %q, want %q", got, tt.wantThreadID)
			}
			if got := p.EndNs - p.StartNs; got != tt.wantOffsetNs {
				t.Errorf("span = %d ns, want %d", got, tt.wantOffsetNs)
			}
		})
	}
}

// v2 sample timestamps are floats on the wire, but a string form should still
// decode rather than silently zeroing the sample.
func TestParseProfileChunk_stringTimestamps(t *testing.T) {
	payload := `{
		"version":"2","platform":"python","chunk_id":"aa","profiler_id":"bb",
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"1","timestamp":"1787911732.101"},
			{"stack_id":0,"thread_id":"1","timestamp":"1787911732.141"}]}}`
	p, err := ingest.ParseProfileChunk([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := p.EndNs - p.StartNs; got < 39_000_000 || got > 41_000_000 {
		t.Errorf("span = %d ns, want ~40ms", got)
	}
}

// Line and column numbers are the remaining fields SDKs stringify.
func TestParseProfile_stringLineNumbers(t *testing.T) {
	payload := `{
		"version":"1","platform":"node","timestamp":"2026-08-24T10:00:00Z",
		"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{
			"frames":[{"function":"handler","lineno":"99","colno":"4"}],
			"stacks":[[0]],
			"samples":[
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
				{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000}]}}`
	p, err := ingest.ParseProfile([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Frames[0].Lineno != 99 {
		t.Errorf("lineno = %d, want 99 from the string form", p.Frames[0].Lineno)
	}
	if p.Frames[0].Colno != 4 {
		t.Errorf("colno = %d, want 4 from the string form", p.Frames[0].Colno)
	}
}

// Relay drops v1 samples outside [relative_start_ns, relative_end_ns] before
// storing. The profiler runs either side of the transaction, and on platforms
// that report real offsets those extra samples are work the request never did.
func TestParseProfile_windowsSamplesToTheTransaction(t *testing.T) {
	payload := `{
		"version":"1","platform":"cocoa","timestamp":"2026-08-24T10:00:00Z","event_id":"aa",
		"transactions":[{"id":"bb","name":"GET /","trace_id":"cc",
			"relative_start_ns":"20000000","relative_end_ns":"40000000"}],
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":"10000000"},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":"20000000"},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":"30000000"},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":"40000000"},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":"50000000"}]}}`

	p, err := ingest.ParseProfile([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The three inside the window, bounds inclusive.
	if len(p.Samples) != 3 {
		t.Errorf("kept %d samples, want the 3 inside the transaction window", len(p.Samples))
	}
	base := p.BaseNs
	for _, s := range p.Samples {
		off := s.TimestampNs - base
		if off < 20_000_000 || off > 40_000_000 {
			t.Errorf("sample at +%dns is outside the transaction window", off)
		}
	}
}

// Relay applies the window only when the SDK reports one, for compatibility
// with older versions that omit it. Dropping everything in that case would
// empty the graph.
func TestParseProfile_keepsEverythingWhenNoWindowReported(t *testing.T) {
	payload := `{
		"version":"1","platform":"php","timestamp":"2026-08-24T10:00:00Z","event_id":"aa",
		"transaction":{"id":"bb","name":"GET /","trace_id":"cc","active_thread_id":"0"},
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"0","elapsed_since_start_ns":10000000},
			{"stack_id":0,"thread_id":"0","elapsed_since_start_ns":20000000},
			{"stack_id":0,"thread_id":"0","elapsed_since_start_ns":30000000}]}}`

	p, err := ingest.ParseProfile([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Samples) != 3 {
		t.Errorf("kept %d samples, want all 3 when the SDK reports no window", len(p.Samples))
	}
}

// The lenient scalars exist to absorb SDK variation, so their odd branches are
// the ones most likely to meet a real payload nobody anticipated.
func TestParseProfile_scalarEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		frame   string
		sample  string
		wantErr bool
		check   func(t *testing.T, p *ingest.Profile)
	}{
		{
			name:   "float line number truncates",
			frame:  `{"function":"f","lineno":42.9}`,
			sample: `{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000}`,
			check: func(t *testing.T, p *ingest.Profile) {
				if p.Frames[0].Lineno != 42 {
					t.Errorf("lineno = %d, want 42", p.Frames[0].Lineno)
				}
			},
		},
		{
			name:   "null line number stays zero",
			frame:  `{"function":"f","lineno":null}`,
			sample: `{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000}`,
			check: func(t *testing.T, p *ingest.Profile) {
				if p.Frames[0].Lineno != 0 {
					t.Errorf("lineno = %d, want 0", p.Frames[0].Lineno)
				}
			},
		},
		{
			name:    "unparseable line number is rejected",
			frame:   `{"function":"f","lineno":"not-a-line"}`,
			sample:  `{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000}`,
			wantErr: true,
		},
		{
			name:   "empty string offset stays at the origin",
			frame:  `{"function":"f"}`,
			sample: `{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":""}`,
			check: func(t *testing.T, p *ingest.Profile) {
				if len(p.Samples) != 2 {
					t.Errorf("samples = %d, want 2", len(p.Samples))
				}
			},
		},
		{
			name:    "a thread id that is neither string nor number is rejected",
			frame:   `{"function":"f"}`,
			sample:  `{"stack_id":0,"thread_id":{"nested":true},"elapsed_since_start_ns":2000000}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{
				"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z","event_id":"aa",
				"transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
				"profile":{"frames":[%s],"stacks":[[0]],"samples":[
					{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
					%s]}}`, tt.frame, tt.sample)

			p, err := ingest.ParseProfile([]byte(payload))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			tt.check(t, p)
		})
	}
}

// v2 timestamps are floats, and a value that will not parse must fail rather
// than silently place the sample at the epoch.
func TestParseProfileChunk_unparseableTimestamp(t *testing.T) {
	payload := `{"version":"2","platform":"python","chunk_id":"a","profiler_id":"b",
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"1","timestamp":"not-a-time"},
			{"stack_id":0,"thread_id":"1","timestamp":1787911732.1}]}}`
	if _, err := ingest.ParseProfileChunk([]byte(payload)); err == nil {
		t.Error("expected an error for an unparseable sample timestamp")
	}
}

// A chunk over the sample ceiling has to be rejected on the v2 path too, not
// just on v1.
func TestParseProfileChunk_tooManySamples(t *testing.T) {
	var b []byte
	b = append(b, `{"version":"2","platform":"python","chunk_id":"a","profiler_id":"b",
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[`...)
	for i := range 200_001 {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, fmt.Sprintf(`{"stack_id":0,"thread_id":"1","timestamp":%d.0}`, 1787911732+i)...)
	}
	b = append(b, `]}}`...)

	if _, err := ingest.ParseProfileChunk(b); !errors.Is(err, ingest.ErrProfileTooManySamples) {
		t.Errorf("err = %v, want ErrProfileTooManySamples", err)
	}
}

// Every named thread being filtered out should leave no map behind, so the
// response does not carry an empty object.
func TestParseProfile_dropsThreadNamesEntirelyWhenNoneSurvive(t *testing.T) {
	payload := `{
		"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z","event_id":"aa",
		"transaction":{"id":"bb","name":"GET /","trace_id":"cc"},
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"kept","elapsed_since_start_ns":1000000},
			{"stack_id":0,"thread_id":"kept","elapsed_since_start_ns":2000000}],
		"thread_metadata":{"gone":{"name":"Worker-1"}}}}`

	p, err := ingest.ParseProfile([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.ThreadNames != nil {
		t.Errorf("thread names = %v, want nil once none are referenced", p.ThreadNames)
	}
}

// A null sample timestamp in a chunk places it at the epoch rather than
// failing, which then trips the duration ceiling instead of silently drawing a
// graph spanning fifty years.
func TestParseProfileChunk_nullSampleTimestamp(t *testing.T) {
	payload := `{"version":"2","platform":"python","chunk_id":"a","profiler_id":"b",
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"1","timestamp":null},
			{"stack_id":0,"thread_id":"1","timestamp":null}]}}`

	p, err := ingest.ParseProfileChunk([]byte(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.StartNs != 0 || p.EndNs != 0 {
		t.Errorf("bounds = [%d, %d], want both at the epoch", p.StartNs, p.EndNs)
	}
}

// Relay rejects a v1 profile with no transaction metadata. Storing one leaves a
// row neither fold path can reach, and it sits outside both partial unique
// indexes, so every retry writes another copy of the largest rows in the schema.
func TestParseProfile_rejectsAProfileWithNoTransaction(t *testing.T) {
	payload := `{
		"version":"1","platform":"python","timestamp":"2026-08-24T10:00:00Z","event_id":"aa",
		"profile":{"frames":[{"function":"f"}],"stacks":[[0]],"samples":[
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":1000000},
			{"stack_id":0,"thread_id":"1","elapsed_since_start_ns":2000000}]}}`

	if _, err := ingest.ParseProfile([]byte(payload)); !errors.Is(err, ingest.ErrProfileNoTransaction) {
		t.Errorf("err = %v, want ErrProfileNoTransaction", err)
	}
}
