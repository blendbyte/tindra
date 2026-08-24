package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sentry ships profiling data in two formats and both are live in the field:
//
//	v1 "profile"        one profile per transaction, samples timed relative to
//	                    the profile start, linked to its transaction by event id.
//	v2 "profile_chunk"  continuous profiling. Chunks carry no transaction link
//	                    at all; the transaction points back at them via
//	                    contexts.profile.profiler_id and a thread id.
//
// Everything below normalizes the two into one Profile so that storage, the
// read path, and the UI only ever deal with a single shape. See
// testdata/profiles/README.md for the per-SDK divergences this has to absorb.

// ProfileFormat identifies which wire format a Profile was decoded from.
type ProfileFormat int

const (
	ProfileFormatV1 ProfileFormat = 1
	ProfileFormatV2 ProfileFormat = 2
)

// Duration ceilings enforced by Relay, mirrored here so we reject the same
// payloads the upstream ingest would. See relay-profiling/src/lib.rs.
const (
	maxProfileDuration      = 30 * time.Second
	maxProfileChunkDuration = 66 * time.Second
)

// maxProfileSamples bounds a single profile. A 66s chunk at the default 101 Hz
// across 30 threads lands near 200k samples, so this leaves generous headroom
// while keeping a malformed or hostile payload from pinning memory.
const maxProfileSamples = 200_000

var (
	ErrProfileUnsupportedVersion = errors.New("unsupported profile version")
	ErrProfileNoSamples          = errors.New("profile has no usable samples")
	ErrProfileMalformedSamples   = errors.New("profile sample references a missing stack")
	ErrProfileMalformedStacks    = errors.New("profile stack references a missing frame")
	ErrProfileTooLong            = errors.New("profile exceeds the maximum duration")
	ErrProfileTooManySamples     = errors.New("profile exceeds the maximum sample count")
)

// ProfileFrame is one function in a stack. Which fields are populated depends
// on the platform: PHP and Python arrive symbolicated, native platforms carry
// only InstructionAddr and would need debug files we do not have.
type ProfileFrame struct {
	Function        string `json:"function,omitempty"`
	Module          string `json:"module,omitempty"`
	Filename        string `json:"filename,omitempty"`
	AbsPath         string `json:"abs_path,omitempty"`
	Package         string `json:"package,omitempty"`
	Platform        string `json:"platform,omitempty"`
	InstructionAddr string `json:"instruction_addr,omitempty"`
	Lineno          int    `json:"lineno,omitempty"`
	Colno           int    `json:"colno,omitempty"`
	// InApp is a pointer because absent and false mean different things: the
	// Python SDK sets it, the PHP SDK never does.
	InApp *bool `json:"in_app,omitempty"`
}

// ProfileSample is one stack capture. TimestampNs is absolute Unix nanoseconds
// for both formats, which is what makes the v1 and v2 read paths identical.
type ProfileSample struct {
	ThreadID    string `json:"thread_id"`
	StackID     int32  `json:"stack_id"`
	TimestampNs int64  `json:"timestamp_ns"`
}

// Profile is the normalized form of either wire format.
type Profile struct {
	Format ProfileFormat `json:"format"`

	// EventID is the profile's own id (v1 only).
	EventID string `json:"event_id,omitempty"`
	// ChunkID and ProfilerID identify a continuous chunk (v2 only).
	ChunkID    string `json:"chunk_id,omitempty"`
	ProfilerID string `json:"profiler_id,omitempty"`

	// Transaction linkage, populated for v1 only. A v2 chunk is linked from
	// the other direction, by the transaction that names its profiler id.
	TransactionID   string `json:"transaction_id,omitempty"`
	TransactionName string `json:"transaction_name,omitempty"`
	TraceID         string `json:"trace_id,omitempty"`
	ActiveThreadID  string `json:"active_thread_id,omitempty"`

	Platform    string `json:"platform,omitempty"`
	Release     string `json:"release,omitempty"`
	Environment string `json:"environment,omitempty"`

	Frames      []ProfileFrame    `json:"frames"`
	Stacks      [][]int32         `json:"stacks"`
	Samples     []ProfileSample   `json:"samples"`
	ThreadNames map[string]string `json:"thread_names,omitempty"`

	// StartNs and EndNs are the first and last sample, absolute Unix nanos.
	StartNs int64 `json:"start_ns"`
	EndNs   int64 `json:"end_ns"`

	// BaseNs is the profile's own origin: the declared start for v1, the first
	// sample for v2. Duration is measured from here so that the v1 check
	// matches Relay, which measures elapsed time from the profile start rather
	// than from whenever the first sample happened to land. Carried through
	// storage so that a decoded profile reports the same duration as a fresh one.
	BaseNs int64 `json:"base_ns"`
}

// Start returns the timestamp of the first sample.
func (p *Profile) Start() time.Time { return time.Unix(0, p.StartNs).UTC() }

// End returns the timestamp of the last sample.
func (p *Profile) End() time.Time { return time.Unix(0, p.EndNs).UTC() }

// Duration is the profile's span measured from its origin.
func (p *Profile) Duration() time.Duration { return time.Duration(p.EndNs - p.BaseNs) }

// ParseProfileItem normalizes a "profile" or "profile_chunk" envelope item.
func ParseProfileItem(itemType string, payload []byte) (*Profile, error) {
	switch itemType {
	case "profile":
		return ParseProfile(payload)
	case "profile_chunk":
		return ParseProfileChunk(payload)
	default:
		return nil, fmt.Errorf("%w: item type %q", ErrProfileUnsupportedVersion, itemType)
	}
}

// ParseProfile decodes a v1 transaction-based profile.
func ParseProfile(payload []byte) (*Profile, error) {
	var raw rawProfileV1
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	// An SDK that omits the version is taken at the item header's word.
	if raw.Version != "" && raw.Version != "1" {
		return nil, fmt.Errorf("%w: %q for a profile item", ErrProfileUnsupportedVersion, raw.Version)
	}

	// The profile's declared start is the origin every v1 sample is offset from.
	baseNs := ParseSentryTimestamp(raw.Timestamp).UnixNano()

	p := &Profile{
		Format:      ProfileFormatV1,
		EventID:     firstNonEmpty(raw.EventID, raw.ProfileID),
		Platform:    raw.Platform,
		Release:     raw.Release,
		Environment: raw.Environment,
		BaseNs:      baseNs,
	}

	// PHP sends `transaction`, Python sends a one-element `transactions`.
	// Relay prefers the singular and falls back to the first of the array.
	tx := raw.Transaction
	if tx == nil && len(raw.Transactions) > 0 {
		tx = &raw.Transactions[0]
	}
	if tx != nil {
		p.TransactionID = tx.ID
		p.TransactionName = tx.Name
		p.TraceID = tx.TraceID
		p.ActiveThreadID = tx.ActiveThreadID.String()
	}

	if err := p.adoptSampleData(raw.Profile, func(s *rawSample) int64 {
		return baseNs + int64(s.ElapsedSinceStartNs)
	}); err != nil {
		return nil, err
	}
	if err := p.normalize(maxProfileDuration); err != nil {
		return nil, err
	}
	return p, nil
}

// ParseProfileChunk decodes a v2 continuous profiling chunk.
func ParseProfileChunk(payload []byte) (*Profile, error) {
	var raw rawProfileV2
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("parse profile chunk: %w", err)
	}
	if raw.Version != "" && raw.Version != "2" {
		return nil, fmt.Errorf("%w: %q for a profile_chunk item", ErrProfileUnsupportedVersion, raw.Version)
	}

	p := &Profile{
		Format:      ProfileFormatV2,
		ChunkID:     raw.ChunkID,
		ProfilerID:  raw.ProfilerID,
		Platform:    raw.Platform,
		Release:     raw.Release,
		Environment: raw.Environment,
	}

	// v2 samples carry absolute Unix seconds. The top-level timestamp is not
	// usable as an origin: the Python SDK omits it entirely, so the chunk's
	// bounds come from the samples and baseNs is set after sorting.
	if err := p.adoptSampleData(raw.Profile, func(s *rawSample) int64 {
		return int64(float64(s.Timestamp) * 1e9)
	}); err != nil {
		return nil, err
	}
	if err := p.normalize(maxProfileChunkDuration); err != nil {
		return nil, err
	}
	return p, nil
}

// adoptSampleData copies the shared frames/stacks/samples block across,
// converting each sample's time to absolute Unix nanos via at.
func (p *Profile) adoptSampleData(raw rawSampleData, at func(*rawSample) int64) error {
	if len(raw.Samples) > maxProfileSamples {
		return fmt.Errorf("%w: %d samples", ErrProfileTooManySamples, len(raw.Samples))
	}

	p.Frames = make([]ProfileFrame, len(raw.Frames))
	for i, f := range raw.Frames {
		p.Frames[i] = f.normalize()
	}

	p.Stacks = raw.Stacks

	p.Samples = make([]ProfileSample, 0, len(raw.Samples))
	for i := range raw.Samples {
		s := &raw.Samples[i]
		p.Samples = append(p.Samples, ProfileSample{
			ThreadID:    s.ThreadID.String(),
			StackID:     s.StackID,
			TimestampNs: at(s),
		})
	}

	if len(raw.ThreadMetadata) > 0 {
		p.ThreadNames = make(map[string]string, len(raw.ThreadMetadata))
		for id, meta := range raw.ThreadMetadata {
			if meta.Name != "" {
				p.ThreadNames[id] = meta.Name
			}
		}
	}
	return nil
}

// normalize applies the same cleanup and validation Relay performs before it
// stores a profile, in the same order, so that we accept and reject the same
// payloads. Pointer authentication stripping is deliberately skipped: it only
// affects native platforms, and only matters for symbolication we do not do.
func (p *Profile) normalize(maxDuration time.Duration) error {
	p.dropSingleSampleThreads()

	if len(p.Samples) == 0 {
		return ErrProfileNoSamples
	}
	for _, s := range p.Samples {
		if s.StackID < 0 || int(s.StackID) >= len(p.Stacks) {
			return ErrProfileMalformedSamples
		}
	}
	for _, stack := range p.Stacks {
		for _, frameID := range stack {
			if frameID < 0 || int(frameID) >= len(p.Frames) {
				return ErrProfileMalformedStacks
			}
		}
	}

	sort.SliceStable(p.Samples, func(i, j int) bool {
		return p.Samples[i].TimestampNs < p.Samples[j].TimestampNs
	})
	p.StartNs = p.Samples[0].TimestampNs
	p.EndNs = p.Samples[len(p.Samples)-1].TimestampNs
	// v2 has no declared origin, so the first sample is it.
	if p.BaseNs == 0 {
		p.BaseNs = p.StartNs
	}

	if p.Duration() > maxDuration {
		return fmt.Errorf("%w: %s exceeds %s", ErrProfileTooLong, p.Duration(), maxDuration)
	}

	p.dropUnreferencedThreadNames()
	return nil
}

// dropSingleSampleThreads removes every sample belonging to a thread that has
// at most one non-idle sample. A lone sample says nothing about where time
// went, and keeping it produces a flame graph implying certainty we don't have.
// Idle samples (an empty stack) do not count toward the total.
func (p *Profile) dropSingleSampleThreads() {
	active := make(map[string]int, 4)
	for _, s := range p.Samples {
		if s.StackID < 0 || int(s.StackID) >= len(p.Stacks) {
			continue
		}
		if len(p.Stacks[s.StackID]) == 0 {
			continue
		}
		active[s.ThreadID]++
	}

	kept := p.Samples[:0]
	for _, s := range p.Samples {
		if active[s.ThreadID] > 1 {
			kept = append(kept, s)
		}
	}
	p.Samples = kept
}

// dropUnreferencedThreadNames removes metadata for threads that no longer have
// samples, either because they were filtered above or were never sampled.
func (p *Profile) dropUnreferencedThreadNames() {
	if len(p.ThreadNames) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(p.ThreadNames))
	for _, s := range p.Samples {
		seen[s.ThreadID] = struct{}{}
	}
	for id := range p.ThreadNames {
		if _, ok := seen[id]; !ok {
			delete(p.ThreadNames, id)
		}
	}
	if len(p.ThreadNames) == 0 {
		p.ThreadNames = nil
	}
}

// --- wire types ---

type rawProfileV1 struct {
	Version      string          `json:"version"`
	EventID      string          `json:"event_id"`
	ProfileID    string          `json:"profile_id"` // alias for event_id
	Platform     string          `json:"platform"`
	Release      string          `json:"release"`
	Environment  string          `json:"environment"`
	Timestamp    json.RawMessage `json:"timestamp"`
	Profile      rawSampleData   `json:"profile"`
	Transaction  *rawTxMeta      `json:"transaction"`
	Transactions []rawTxMeta     `json:"transactions"`
}

type rawProfileV2 struct {
	Version     string        `json:"version"`
	ChunkID     string        `json:"chunk_id"`
	ProfilerID  string        `json:"profiler_id"`
	Platform    string        `json:"platform"`
	Release     string        `json:"release"`
	Environment string        `json:"environment"`
	Profile     rawSampleData `json:"profile"`
}

type rawSampleData struct {
	Frames         []rawFrame               `json:"frames"`
	Stacks         [][]int32                `json:"stacks"`
	Samples        []rawSample              `json:"samples"`
	ThreadMetadata map[string]rawThreadMeta `json:"thread_metadata"`
}

type rawThreadMeta struct {
	Name string `json:"name"`
}

type rawTxMeta struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	TraceID        string     `json:"trace_id"`
	ActiveThreadID flexString `json:"active_thread_id"`
}

type rawSample struct {
	StackID  int32      `json:"stack_id"`
	ThreadID flexString `json:"thread_id"`
	// v1: nanoseconds since the profile start, sent as a number by the PHP SDK
	// and as a string by the Python SDK.
	ElapsedSinceStartNs flexUint64 `json:"elapsed_since_start_ns"`
	// v2: absolute Unix seconds.
	Timestamp flexFloat64 `json:"timestamp"`
}

// rawFrame carries both spellings of every aliased key. Relay accepts `name`,
// `file`, `line` and `column` as aliases, and SDKs differ on which they send.
type rawFrame struct {
	Function        string  `json:"function"`
	Name            string  `json:"name"`
	Filename        string  `json:"filename"`
	File            string  `json:"file"`
	AbsPath         string  `json:"abs_path"`
	Module          string  `json:"module"`
	Package         string  `json:"package"`
	Platform        string  `json:"platform"`
	InstructionAddr string  `json:"instruction_addr"`
	Lineno          flexInt `json:"lineno"`
	Line            flexInt `json:"line"`
	Colno           flexInt `json:"colno"`
	Column          flexInt `json:"column"`
	InApp           *bool   `json:"in_app"`
}

func (f rawFrame) normalize() ProfileFrame {
	return ProfileFrame{
		Function:        firstNonEmpty(f.Function, f.Name),
		Module:          f.Module,
		Filename:        firstNonEmpty(f.Filename, f.File),
		AbsPath:         f.AbsPath,
		Package:         f.Package,
		Platform:        f.Platform,
		InstructionAddr: f.InstructionAddr,
		Lineno:          firstNonZero(int(f.Lineno), int(f.Line)),
		Colno:           firstNonZero(int(f.Colno), int(f.Column)),
		InApp:           f.InApp,
	}
}

// --- lenient scalars ---
//
// Relay decodes these with deserialize_number_from_string, so a value may
// arrive as a JSON number or as a JSON string holding one. The PHP and Python
// SDKs disagree on nearly every such field, so leniency is required, not
// defensive.

type flexUint64 uint64

func (v *flexUint64) UnmarshalJSON(b []byte) error {
	s, ok := unquoteNumber(b)
	if !ok {
		return nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		// Fall back through float for values like "1.2e9".
		f, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil || f < 0 {
			return fmt.Errorf("parse unsigned number %q: %w", s, err)
		}
		n = uint64(f)
	}
	*v = flexUint64(n)
	return nil
}

type flexInt int64

func (v *flexInt) UnmarshalJSON(b []byte) error {
	s, ok := unquoteNumber(b)
	if !ok {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		f, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return fmt.Errorf("parse number %q: %w", s, err)
		}
		n = int64(f)
	}
	*v = flexInt(n)
	return nil
}

type flexFloat64 float64

func (v *flexFloat64) UnmarshalJSON(b []byte) error {
	s, ok := unquoteNumber(b)
	if !ok {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("parse float %q: %w", s, err)
	}
	*v = flexFloat64(f)
	return nil
}

// flexString accepts a JSON string or number and always yields a string. Thread
// ids are integers in v1 and opaque strings in v2, often hex addresses.
type flexString string

func (v *flexString) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
		var out string
		if err := json.Unmarshal(b, &out); err != nil {
			return err
		}
		*v = flexString(out)
		return nil
	}
	*v = flexString(strings.TrimSpace(s))
	return nil
}

func (v flexString) String() string { return string(v) }

// unquoteNumber strips the quotes from a JSON scalar that may or may not be
// quoted. It reports false for null or empty input, which callers treat as
// "leave the zero value alone".
func unquoteNumber(b []byte) (string, bool) {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return "", false
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return "", false
	}
	return s, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}
