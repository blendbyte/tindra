// Package profiles turns stored profile chunks into something a UI can draw.
//
// The folding lives here rather than in the client because a profile is
// thousands of samples over a handful of distinct stacks: sending raw samples
// to the browser would be megabytes of JSON to render a tree of a few hundred
// nodes. Folding server side keeps the response small and the frontend simple.
package profiles

import (
	"sort"
	"strings"

	"github.com/blendbyte/tindra/internal/ingest"
)

// FlameNode is one function in the folded call tree.
type FlameNode struct {
	Function string `json:"function"`
	Module   string `json:"module,omitempty"`
	Filename string `json:"filename,omitempty"`
	Lineno   int    `json:"lineno,omitempty"`
	InApp    *bool  `json:"in_app,omitempty"`

	// SelfSamples counted this frame as the leaf; TotalSamples counted it
	// anywhere in the stack. Self is what "this function is slow" means;
	// total is what "this call path is slow" means.
	SelfSamples  int `json:"self_samples"`
	TotalSamples int `json:"total_samples"`

	Children []*FlameNode `json:"children,omitempty"`
}

// FlameGraph is the folded result for one thread over one window.
type FlameGraph struct {
	// SampleCount is the number of samples that reached the tree, so it always
	// equals Root.TotalSamples. Idle samples are reported separately.
	SampleCount int `json:"sample_count"`
	IdleSamples int `json:"idle_samples"`

	// SampleIntervalNs is measured from the samples themselves rather than
	// assumed to be the SDK default, so the millisecond figures stay honest
	// across SDKs and sample rates.
	SampleIntervalNs int64 `json:"sample_interval_ns"`
	DurationNs       int64 `json:"duration_ns"`

	ThreadID   string     `json:"thread_id,omitempty"`
	ThreadName string     `json:"thread_name,omitempty"`
	Root       *FlameNode `json:"root"`
}

// FoldOptions narrows which samples are folded.
type FoldOptions struct {
	// ThreadID selects one thread. Empty folds every thread together, which is
	// rarely what you want for a transaction but is useful for a whole chunk.
	ThreadID string
	// StartNs and EndNs bound the window, inclusive. Zero means unbounded.
	// For a v2 chunk this is how a transaction's slice is cut out of a chunk
	// that may span far more than the transaction did.
	StartNs int64
	EndNs   int64
}

// Fold merges samples from one or more profiles into a single call tree.
//
// Several profiles can contribute because continuous profiling splits a
// transaction across chunk boundaries. Frame and stack indices are per-profile,
// so each profile is walked against its own tables and accumulated into the
// shared tree.
func Fold(profs []*ingest.Profile, opt FoldOptions) *FlameGraph {
	g := &FlameGraph{Root: &FlameNode{}}
	// Children are keyed for merging while building, then dropped: the wire
	// form is a plain tree.
	index := map[*FlameNode]map[string]*FlameNode{}

	var times []int64
	for _, p := range profs {
		if p == nil {
			continue
		}
		if g.ThreadName == "" && opt.ThreadID != "" {
			g.ThreadName = p.ThreadNames[opt.ThreadID]
		}

		for _, s := range p.Samples {
			if opt.ThreadID != "" && s.ThreadID != opt.ThreadID {
				continue
			}
			if opt.StartNs != 0 && s.TimestampNs < opt.StartNs {
				continue
			}
			if opt.EndNs != 0 && s.TimestampNs > opt.EndNs {
				continue
			}
			if int(s.StackID) >= len(p.Stacks) {
				continue
			}
			stack := p.Stacks[s.StackID]
			times = append(times, s.TimestampNs)
			if len(stack) == 0 {
				g.IdleSamples++
				continue
			}

			g.SampleCount++
			node := g.Root
			node.TotalSamples++
			// Stacks are leaf first, so walking backwards descends from the
			// entry point toward the running function.
			for i := len(stack) - 1; i >= 0; i-- {
				frameID := stack[i]
				if int(frameID) >= len(p.Frames) {
					break
				}
				node = childFor(index, node, p.Frames[frameID])
				node.TotalSamples++
			}
			node.SelfSamples++
		}
	}

	g.SampleIntervalNs = medianInterval(times)
	if len(times) > 0 {
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		g.DurationNs = times[len(times)-1] - times[0]
	}
	sortTree(g.Root)
	return g
}

// childFor finds or creates the child of node matching frame.
//
// Frames merge on function, module and file but deliberately not on line
// number. SDKs record the line currently executing, so keying on it would
// split one function into a node per line and turn the graph into noise.
func childFor(index map[*FlameNode]map[string]*FlameNode, node *FlameNode, f ingest.ProfileFrame) *FlameNode {
	key := frameKey(f)
	kids, ok := index[node]
	if !ok {
		kids = map[string]*FlameNode{}
		index[node] = kids
	}
	if child, ok := kids[key]; ok {
		return child
	}
	child := &FlameNode{
		Function: displayName(f),
		Module:   f.Module,
		Filename: f.Filename,
		Lineno:   f.Lineno,
		InApp:    f.InApp,
	}
	kids[key] = child
	node.Children = append(node.Children, child)
	return child
}

func frameKey(f ingest.ProfileFrame) string {
	return displayName(f) + "\x00" + f.Module + "\x00" + f.Filename
}

// displayName falls back through the identifiers a frame might carry. Native
// platforms arrive unsymbolicated with only an address, and showing that beats
// showing an unnamed node.
func displayName(f ingest.ProfileFrame) string {
	if f.Function != "" {
		return f.Function
	}
	if f.Filename != "" {
		return f.Filename
	}
	if f.InstructionAddr != "" {
		return f.InstructionAddr
	}
	return "<unknown>"
}

// medianInterval estimates the sampling period from observed timestamps. The
// median rejects the outliers that chunk boundaries and scheduler stalls
// introduce, which a mean would fold into every duration on screen.
func medianInterval(times []int64) int64 {
	if len(times) < 2 {
		return 0
	}
	sorted := make([]int64, len(times))
	copy(sorted, times)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	gaps := make([]int64, 0, len(sorted)-1)
	for i := 1; i < len(sorted); i++ {
		// Samples taken on several threads share a timestamp; a zero gap says
		// nothing about the sampling period.
		if d := sorted[i] - sorted[i-1]; d > 0 {
			gaps = append(gaps, d)
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return gaps[len(gaps)/2]
}

// sortTree orders siblings by cost so the heaviest path reads first, with the
// name as a tiebreak to keep the output stable between identical requests.
func sortTree(n *FlameNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.TotalSamples != b.TotalSamples {
			return a.TotalSamples > b.TotalSamples
		}
		return strings.Compare(a.Function, b.Function) < 0
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}
