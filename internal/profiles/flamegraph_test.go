package profiles_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/profiles"
)

func fixture(t *testing.T, name, itemType string) *ingest.Profile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "ingest", "testdata", "profiles", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	p, err := ingest.ParseProfileItem(itemType, raw)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return p
}

// child looks up a node by function name among a node's children.
func child(t *testing.T, n *profiles.FlameNode, function string) *profiles.FlameNode {
	t.Helper()
	for _, c := range n.Children {
		if c.Function == function {
			return c
		}
	}
	t.Fatalf("no child %q under %q (have %v)", function, n.Function, names(n))
	return nil
}

func names(n *profiles.FlameNode) []string {
	out := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		out = append(out, c.Function)
	}
	return out
}

// The tree has to descend from the entry point, not from the running function.
// Stacks arrive leaf first, so folding them in wire order would invert every
// graph while still producing plausible-looking totals.
func TestFold_descendsFromEntryPoint(t *testing.T) {
	p := fixture(t, "v1_php_laravel.json", "profile")
	g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})

	if len(g.Root.Children) != 1 {
		t.Fatalf("root has %d children, want 1 entry point: %v", len(g.Root.Children), names(g.Root))
	}
	entry := g.Root.Children[0]
	if entry.Function != "/var/www/app/public/index.php" {
		t.Fatalf("entry point = %q, want index.php", entry.Function)
	}

	kernel := child(t, entry, `Illuminate\Foundation\Http\Kernel::handle`)
	controller := child(t, kernel, `App\Http\Controllers\OrderController::index`)

	// The fixture's five samples all pass through the controller.
	if controller.TotalSamples != 5 {
		t.Errorf("controller total = %d, want 5", controller.TotalSamples)
	}
	// Two of them stop there; the rest descend into Builder::get and Money::format.
	if controller.SelfSamples != 2 {
		t.Errorf("controller self = %d, want 2", controller.SelfSamples)
	}

	builder := child(t, controller, `Illuminate\Database\Query\Builder::get`)
	if builder.TotalSamples != 2 || builder.SelfSamples != 2 {
		t.Errorf("builder total/self = %d/%d, want 2/2", builder.TotalSamples, builder.SelfSamples)
	}
	money := child(t, controller, `App\Support\Money::format`)
	if money.TotalSamples != 1 || money.SelfSamples != 1 {
		t.Errorf("money total/self = %d/%d, want 1/1", money.TotalSamples, money.SelfSamples)
	}
}

// Total is "this call path", self is "this function". Conflating them is the
// classic flame graph bug, so the invariants are pinned down directly.
func TestFold_selfAndTotalInvariants(t *testing.T) {
	p := fixture(t, "v1_php_laravel.json", "profile")
	g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})

	if g.Root.TotalSamples != g.SampleCount {
		t.Errorf("root total = %d but sample count = %d", g.Root.TotalSamples, g.SampleCount)
	}

	var walk func(n *profiles.FlameNode)
	walk = func(n *profiles.FlameNode) {
		sum := n.SelfSamples
		for _, c := range n.Children {
			sum += c.TotalSamples
			walk(c)
		}
		if n != g.Root && sum != n.TotalSamples {
			t.Errorf("%q: self %d + children %d != total %d",
				n.Function, n.SelfSamples, sum-n.SelfSamples, n.TotalSamples)
		}
	}
	walk(g.Root)
}

func TestFold_sortsHeaviestFirst(t *testing.T) {
	p := fixture(t, "v1_php_laravel.json", "profile")
	g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})

	var walk func(n *profiles.FlameNode)
	walk = func(n *profiles.FlameNode) {
		for i := 1; i < len(n.Children); i++ {
			if n.Children[i-1].TotalSamples < n.Children[i].TotalSamples {
				t.Errorf("children of %q are not heaviest first: %v", n.Function, names(n))
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(g.Root)
}

// Continuous profiling splits a transaction across chunks, and frame and stack
// indices are per chunk. Folding several profiles has to walk each against its
// own tables rather than assuming a shared index space.
func TestFold_mergesAcrossProfilesWithIndependentIndices(t *testing.T) {
	first := &ingest.Profile{
		Frames: []ingest.ProfileFrame{{Function: "main"}, {Function: "work"}},
		Stacks: [][]int32{{1, 0}},
		Samples: []ingest.ProfileSample{
			{ThreadID: "1", StackID: 0, TimestampNs: 1_000_000_000},
			{ThreadID: "1", StackID: 0, TimestampNs: 1_010_000_000},
		},
	}
	// Same functions, deliberately different index order.
	second := &ingest.Profile{
		Frames: []ingest.ProfileFrame{{Function: "work"}, {Function: "main"}},
		Stacks: [][]int32{{0, 1}},
		Samples: []ingest.ProfileSample{
			{ThreadID: "1", StackID: 0, TimestampNs: 1_020_000_000},
		},
	}

	g := profiles.Fold([]*ingest.Profile{first, second}, profiles.FoldOptions{})

	if len(g.Root.Children) != 1 {
		t.Fatalf("root has %d children, want main merged across both: %v",
			len(g.Root.Children), names(g.Root))
	}
	main := child(t, g.Root, "main")
	if main.TotalSamples != 3 {
		t.Errorf("main total = %d, want 3 across both profiles", main.TotalSamples)
	}
	work := child(t, main, "work")
	if work.SelfSamples != 3 {
		t.Errorf("work self = %d, want 3", work.SelfSamples)
	}
}

// A chunk can span far more than the transaction did, so the window is what
// makes a continuous profile mean anything for one request.
func TestFold_windowAndThreadFiltering(t *testing.T) {
	p := &ingest.Profile{
		Frames: []ingest.ProfileFrame{{Function: "main"}},
		Stacks: [][]int32{{0}},
		Samples: []ingest.ProfileSample{
			{ThreadID: "main", StackID: 0, TimestampNs: 100},
			{ThreadID: "main", StackID: 0, TimestampNs: 200},
			{ThreadID: "main", StackID: 0, TimestampNs: 300},
			{ThreadID: "other", StackID: 0, TimestampNs: 200},
		},
		ThreadNames: map[string]string{"main": "MainThread"},
	}

	t.Run("window is inclusive", func(t *testing.T) {
		g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{StartNs: 200, EndNs: 300})
		if g.SampleCount != 3 {
			t.Errorf("count = %d, want 3 (two main plus one other, both bounds inclusive)", g.SampleCount)
		}
	})

	t.Run("thread narrows to one", func(t *testing.T) {
		g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{ThreadID: "main"})
		if g.SampleCount != 3 {
			t.Errorf("count = %d, want 3 main-thread samples", g.SampleCount)
		}
		if g.ThreadName != "MainThread" {
			t.Errorf("thread name = %q, want MainThread", g.ThreadName)
		}
	})

	t.Run("thread and window together", func(t *testing.T) {
		g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{
			ThreadID: "main", StartNs: 150, EndNs: 250,
		})
		if g.SampleCount != 1 {
			t.Errorf("count = %d, want 1", g.SampleCount)
		}
	})
}

// An empty stack means the thread was idle. Counting those as time spent in
// the entry point would invent work that never happened.
func TestFold_idleSamplesAreSeparate(t *testing.T) {
	p := &ingest.Profile{
		Frames: []ingest.ProfileFrame{{Function: "main"}},
		Stacks: [][]int32{{0}, {}},
		Samples: []ingest.ProfileSample{
			{ThreadID: "1", StackID: 0, TimestampNs: 100},
			{ThreadID: "1", StackID: 1, TimestampNs: 200},
			{ThreadID: "1", StackID: 1, TimestampNs: 300},
		},
	}
	g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})

	if g.SampleCount != 1 {
		t.Errorf("sample count = %d, want 1 non-idle", g.SampleCount)
	}
	if g.IdleSamples != 2 {
		t.Errorf("idle = %d, want 2", g.IdleSamples)
	}
	if g.Root.TotalSamples != 1 {
		t.Errorf("root total = %d, want idle samples excluded from the tree", g.Root.TotalSamples)
	}
}

// SDKs record the line currently executing, so keying frames on it would split
// one function into a node per line and turn the graph into noise.
func TestFold_mergesFramesAcrossLineNumbers(t *testing.T) {
	p := &ingest.Profile{
		Frames: []ingest.ProfileFrame{
			{Function: "loop", Filename: "a.py", Module: "a", Lineno: 10},
			{Function: "loop", Filename: "a.py", Module: "a", Lineno: 11},
		},
		Stacks: [][]int32{{0}, {1}},
		Samples: []ingest.ProfileSample{
			{ThreadID: "1", StackID: 0, TimestampNs: 100},
			{ThreadID: "1", StackID: 1, TimestampNs: 200},
		},
	}
	g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})

	if len(g.Root.Children) != 1 {
		t.Fatalf("root has %d children, want one merged node: %v", len(g.Root.Children), names(g.Root))
	}
	if g.Root.Children[0].TotalSamples != 2 {
		t.Errorf("merged total = %d, want 2", g.Root.Children[0].TotalSamples)
	}
}

// The interval is measured rather than assumed, so the millisecond figures on
// screen stay honest across SDKs and sample rates.
func TestFold_sampleIntervalIsMeasured(t *testing.T) {
	t.Run("median rejects a stall", func(t *testing.T) {
		p := &ingest.Profile{
			Frames: []ingest.ProfileFrame{{Function: "main"}},
			Stacks: [][]int32{{0}},
			Samples: []ingest.ProfileSample{
				{ThreadID: "1", StackID: 0, TimestampNs: 0},
				{ThreadID: "1", StackID: 0, TimestampNs: 10_000_000},
				{ThreadID: "1", StackID: 0, TimestampNs: 20_000_000},
				// A scheduler stall a hundred times the sampling period.
				{ThreadID: "1", StackID: 0, TimestampNs: 1_020_000_000},
			},
		}
		g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})
		if g.SampleIntervalNs != 10_000_000 {
			t.Errorf("interval = %d ns, want 10ms despite the stall", g.SampleIntervalNs)
		}
	})

	t.Run("simultaneous samples on separate threads do not count", func(t *testing.T) {
		p := &ingest.Profile{
			Frames: []ingest.ProfileFrame{{Function: "main"}},
			Stacks: [][]int32{{0}},
			Samples: []ingest.ProfileSample{
				{ThreadID: "a", StackID: 0, TimestampNs: 0},
				{ThreadID: "b", StackID: 0, TimestampNs: 0},
				{ThreadID: "a", StackID: 0, TimestampNs: 10_000_000},
				{ThreadID: "b", StackID: 0, TimestampNs: 10_000_000},
			},
		}
		g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})
		if g.SampleIntervalNs != 10_000_000 {
			t.Errorf("interval = %d ns, want 10ms with zero gaps ignored", g.SampleIntervalNs)
		}
	})

	t.Run("a single sample has no measurable interval", func(t *testing.T) {
		p := &ingest.Profile{
			Frames:  []ingest.ProfileFrame{{Function: "main"}},
			Stacks:  [][]int32{{0}},
			Samples: []ingest.ProfileSample{{ThreadID: "1", StackID: 0, TimestampNs: 100}},
		}
		g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})
		if g.SampleIntervalNs != 0 {
			t.Errorf("interval = %d, want 0 rather than a fabricated figure", g.SampleIntervalNs)
		}
	})
}

// Native frames have no function name. Showing the address beats an unnamed box.
func TestFold_unsymbolicatedFramesFallBackToAddress(t *testing.T) {
	p := fixture(t, "v1_cocoa.json", "profile")
	g := profiles.Fold([]*ingest.Profile{p}, profiles.FoldOptions{})

	if len(g.Root.Children) == 0 {
		t.Fatal("expected at least one node")
	}
	for _, c := range g.Root.Children {
		if c.Function == "" || c.Function == "<unknown>" {
			t.Errorf("frame rendered as %q, want the instruction address", c.Function)
		}
	}
}

func TestFold_emptyInput(t *testing.T) {
	g := profiles.Fold(nil, profiles.FoldOptions{})
	if g == nil || g.Root == nil {
		t.Fatal("expected an empty graph rather than nil")
	}
	if g.SampleCount != 0 || len(g.Root.Children) != 0 {
		t.Error("expected no samples and no children")
	}

	// A nil entry in the slice must not panic: a failed decode can produce one.
	g = profiles.Fold([]*ingest.Profile{nil}, profiles.FoldOptions{})
	if g.SampleCount != 0 {
		t.Error("expected nil profiles to be skipped")
	}
}
