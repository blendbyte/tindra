package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/profiles"
)

// Seed data is only worth anything if the real ingest path accepts it, so
// these run the generated payloads through the actual parser rather than
// asserting on the maps we just built.

var (
	testReleases = []string{"1.4.2", "1.5.0"}
	testEnvs     = []string{"production", "staging"}
)

func parseItem(t *testing.T, itemType string, payload map[string]any) *ingest.Profile {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p, err := ingest.ParseProfileItem(itemType, raw)
	if err != nil {
		t.Fatalf("parse %s: %v", itemType, err)
	}
	return p
}

func TestBuildV1Profile_isAcceptedByTheParser(t *testing.T) {
	for _, tmpl := range laravelProfiles {
		t.Run(tmpl.txName, func(t *testing.T) {
			tx, profile := buildV1Profile(tmpl, time.Now().UTC().Add(-time.Hour), testReleases, testEnvs)
			p := parseItem(t, "profile", profile)

			if p.Format != ingest.ProfileFormatV1 {
				t.Errorf("format = %v, want v1", p.Format)
			}
			if p.TransactionName != tmpl.txName {
				t.Errorf("transaction name = %q, want %q", p.TransactionName, tmpl.txName)
			}
			if len(p.Samples) < 20 {
				t.Errorf("only %d samples; the flame graph would be empty", len(p.Samples))
			}
			// Under the 30s ceiling the format enforces.
			if p.Duration() > 30*time.Second {
				t.Errorf("duration %s exceeds the v1 limit", p.Duration())
			}

			// The link the read path follows: the profile names the
			// transaction's event id. Get this wrong and the graph is
			// unreachable even though both rows exist.
			if p.TransactionID != tx["event_id"] {
				t.Errorf("profile points at %q but the transaction is %q",
					p.TransactionID, tx["event_id"])
			}
		})
	}
}

// The PHP SDK never sends in_app, and the UI mutes only an explicit false. If
// the seeder invented the field, seeded Laravel graphs would look different
// from real ones.
func TestBuildV1Profile_omitsInApp(t *testing.T) {
	_, profile := buildV1Profile(laravelProfiles[0], time.Now().UTC(), testReleases, testEnvs)

	raw, _ := json.Marshal(profile)
	var probe struct {
		Profile struct {
			Frames []map[string]json.RawMessage `json:"frames"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(probe.Profile.Frames) == 0 {
		t.Fatal("no frames")
	}
	for i, f := range probe.Profile.Frames {
		if _, present := f["in_app"]; present {
			t.Errorf("frame %d carries in_app; the PHP SDK omits it entirely", i)
		}
	}
}

// The two SDKs disagree on the type of this field, which is exactly the
// divergence the parser's lenient scalars exist for.
func TestSeededProfiles_matchTheirSDKsNumberTypes(t *testing.T) {
	_, profile := buildV1Profile(laravelProfiles[0], time.Now().UTC(), testReleases, testEnvs)
	raw, _ := json.Marshal(profile)

	var probe struct {
		Profile struct {
			Samples []map[string]json.RawMessage `json:"samples"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	elapsed := string(probe.Profile.Samples[0]["elapsed_since_start_ns"])
	if elapsed == "" || elapsed[0] == '"' {
		t.Errorf("elapsed_since_start_ns = %s, want a bare number as the PHP SDK sends", elapsed)
	}
}

func TestBuildV2Session_isAcceptedByTheParser(t *testing.T) {
	for _, tmpl := range pythonProfiles {
		t.Run(tmpl.txName, func(t *testing.T) {
			start := time.Now().UTC().Add(-time.Hour)
			chunk, txs := buildV2Session(tmpl, start, 3, testReleases, testEnvs)
			p := parseItem(t, "profile_chunk", chunk)

			if p.Format != ingest.ProfileFormatV2 {
				t.Errorf("format = %v, want v2", p.Format)
			}
			if p.ProfilerID == "" {
				t.Error("chunk has no profiler id, so nothing could ever find it")
			}
			// Under the 66s ceiling Relay enforces on chunks.
			if p.Duration() > 66*time.Second {
				t.Errorf("chunk duration %s exceeds the v2 limit", p.Duration())
			}
			if len(txs) != 3 {
				t.Fatalf("got %d transactions, want 3", len(txs))
			}
		})
	}
}

// A chunk that does not actually cover its transactions produces an empty
// flame graph, which looks like a bug in the product rather than the seeder.
func TestBuildV2Session_everyTransactionFoldsToSomething(t *testing.T) {
	for _, tmpl := range pythonProfiles {
		t.Run(tmpl.txName, func(t *testing.T) {
			start := time.Now().UTC().Add(-time.Hour)
			chunk, txs := buildV2Session(tmpl, start, 3, testReleases, testEnvs)
			p := parseItem(t, "profile_chunk", chunk)

			for i, tx := range txs {
				txStart, err := time.Parse(time.RFC3339Nano, tx["start_timestamp"].(string))
				if err != nil {
					t.Fatalf("parse start: %v", err)
				}
				txEnd, err := time.Parse(time.RFC3339Nano, tx["timestamp"].(string))
				if err != nil {
					t.Fatalf("parse end: %v", err)
				}

				contexts := tx["contexts"].(map[string]any)
				profilerID := contexts["profile"].(map[string]any)["profiler_id"].(string)
				threadID := contexts["trace"].(map[string]any)["data"].(map[string]any)["thread.id"].(string)

				if profilerID != p.ProfilerID {
					t.Errorf("tx %d names profiler %q but the chunk is %q", i, profilerID, p.ProfilerID)
				}

				// Fold exactly as the read path does.
				g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{
					ThreadID: threadID,
					StartNs:  txStart.UnixNano(),
					EndNs:    txEnd.UnixNano(),
				})
				if g.SampleCount == 0 {
					t.Errorf("tx %d (%s..%s) folds to nothing; the chunk does not cover it",
						i, txStart, txEnd)
				}
				if len(g.Root.Children) == 0 {
					t.Errorf("tx %d produced no tree", i)
				}
			}
		})
	}
}

// The whole point of the weights is an uneven graph. A profile where every
// branch is the same size demonstrates nothing.
func TestSeededProfilesHaveADominantBranch(t *testing.T) {
	_, profile := buildV1Profile(laravelProfiles[0], time.Now().UTC(), testReleases, testEnvs)
	p := parseItem(t, "profile", profile)

	g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})
	if len(g.Root.Children) != 1 {
		t.Fatalf("want a single entry point, got %d", len(g.Root.Children))
	}

	// Walk to the deepest node that still holds most of the samples.
	node := g.Root.Children[0]
	for len(node.Children) > 0 {
		node = node.Children[0] // sorted heaviest first by Fold
	}
	if node.SelfSamples == 0 {
		t.Error("the heaviest path has no self time")
	}

	share := float64(g.Root.Children[0].TotalSamples) / float64(g.SampleCount)
	if share < 0.99 {
		t.Errorf("entry point covers %.0f%% of samples, want ~100%%", share*100)
	}
}

// Seeded profiles must outlive the default PROFILE_RETENTION_DAYS of 7, or the
// retention worker deletes half of them on its first hourly pass and the demo
// data quietly thins out.
func TestProfileAge_staysInsideTheDefaultRetentionWindow(t *testing.T) {
	for i := 0; i < 200; i++ {
		if age := profileAge(); age > 7*24*time.Hour {
			t.Fatalf("profileAge returned %s, past the 7 day default", age)
		}
	}
}

func TestProfileSections_registered(t *testing.T) {
	seed, err := parseSeedSections("profiles")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !seed["profiles"] {
		t.Error("profiles section did not resolve")
	}

	all, err := parseSeedSections("all")
	if err != nil {
		t.Fatalf("parse all: %v", err)
	}
	if !all["profiles"] {
		t.Error("profiles should be included in all")
	}
}

// A profiled transaction with no spans leaves the detail page dominated by an
// empty waterfall and pushes the flame graph off the bottom of the screen.
func TestSeededProfiledTransactionsHaveSpans(t *testing.T) {
	t.Run("v1", func(t *testing.T) {
		for _, tmpl := range laravelProfiles {
			tx, _ := buildV1Profile(tmpl, time.Now().UTC(), testReleases, testEnvs)
			spans, ok := tx["spans"].([]map[string]any)
			if !ok || len(spans) == 0 {
				t.Errorf("%s produced no spans", tmpl.txName)
			}
		}
	})

	t.Run("v2", func(t *testing.T) {
		for _, tmpl := range pythonProfiles {
			_, txs := buildV2Session(tmpl, time.Now().UTC().Add(-time.Hour), 3, testReleases, testEnvs)
			for i, tx := range txs {
				spans, ok := tx["spans"].([]map[string]any)
				if !ok || len(spans) == 0 {
					t.Errorf("%s tx %d produced no spans", tmpl.txName, i)
				}
			}
		}
	})
}

// Profiled transactions have to be findable. When a profiled template shares a
// name with an ordinary one, clicking that name in the UI usually lands on a
// transaction with no profile and the panel simply is not there.
func TestProfiledTransactionNamesAreUnique(t *testing.T) {
	ordinary := map[string]bool{}
	for _, def := range projectRegistry {
		for _, tmpl := range def.txs {
			ordinary[tmpl.name] = true
		}
	}

	seen := map[string]bool{}
	for _, tmpl := range append(append([]profileTemplate{}, laravelProfiles...), pythonProfiles...) {
		if ordinary[tmpl.txName] {
			t.Errorf("%q is also an ordinary transaction template, so most of them have no profile", tmpl.txName)
		}
		if seen[tmpl.txName] {
			t.Errorf("%q is used by two profile templates", tmpl.txName)
		}
		seen[tmpl.txName] = true
	}
}
