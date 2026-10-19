package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// GrammarVersion is the one grammar document version the checker knows
// (circus docs/plans/2026-10-19-bricks-reconciliation.md, section 1, "Rules").
const GrammarVersion = 1

// Grammar is a host-supplied document: a graph of kinds plus constraints. It
// says shape; binding a node to reviewed content (a questions digest, a program
// allowlist) is Expect's. A sentence never carries the grammar that validates
// it: the host pins the grammar by digest in Expect.
type Grammar struct {
	Grammar     string             `json:"grammar"`
	Version     int                `json:"version"`
	Kinds       []string           `json:"kinds"`
	Edges       []Edge             `json:"edges"`
	Constraints GrammarConstraints `json:"constraints"`
	Result      []GrammarStep      `json:"result"`
}

// GrammarConstraints are the rules that are not edges.
type GrammarConstraints struct {
	// EntryKind is the kind of the node the walk starts at.
	EntryKind string `json:"entry_kind"`
	// OneFailTarget requires every fail edge to lead to the same node.
	OneFailTarget bool `json:"one_fail_target"`
	// MaxNodesPerKind caps how many nodes of a kind a sentence may have; a kind
	// it does not name is uncapped.
	MaxNodesPerKind map[string]int `json:"max_nodes_per_kind"`
	// Gates says whether gate kinds (all, any, not, switch) may appear. Only
	// "none" is defined; roustabout has no gate kind yet.
	Gates string `json:"gates"`
}

// GrammarStep is one step of the grammar's result list.
type GrammarStep struct {
	Step string `json:"step"`
	// Optional lets a sentence leave the step out. A sentence's result list
	// must otherwise equal the grammar's, in order.
	Optional bool `json:"optional,omitempty"`
}

// ParseGrammar reads and validates a grammar document. It refuses a version it
// does not know, a kind not in the kind table, an edge condition outside its
// source kind's outcome set, and anything the schema does not define.
func ParseGrammar(raw []byte) (*Grammar, error) {
	var gr Grammar
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&gr); err != nil {
		return nil, fmt.Errorf("grammar: %v", err)
	}
	if gr.Version != GrammarVersion {
		return nil, fmt.Errorf("grammar: version %d is not one this checker knows (it knows %d)", gr.Version, GrammarVersion)
	}
	if gr.Grammar == "" {
		return nil, fmt.Errorf("grammar: it has no name")
	}
	if len(gr.Kinds) == 0 {
		return nil, fmt.Errorf("grammar %q: it names no kinds", gr.Grammar)
	}
	for _, k := range gr.Kinds {
		if _, ok := kindTable[k]; !ok {
			return nil, fmt.Errorf("grammar %q: %q is not a node kind", gr.Grammar, k)
		}
	}
	for _, e := range gr.Edges {
		from, cond, to := e.From(), e.Condition(), e.To()
		if !slices.Contains(gr.Kinds, from) || !slices.Contains(gr.Kinds, to) {
			return nil, fmt.Errorf("grammar %q: the edge %v names a kind outside its kinds", gr.Grammar, e)
		}
		if !slices.Contains(kindTable[from].outcomes, cond) {
			return nil, fmt.Errorf("grammar %q: the edge %v leaves %s on %q, which is not one of its outcomes %v",
				gr.Grammar, e, from, cond, kindTable[from].outcomes)
		}
	}
	c := gr.Constraints
	if !slices.Contains(gr.Kinds, c.EntryKind) {
		return nil, fmt.Errorf("grammar %q: entry_kind %q is not one of its kinds", gr.Grammar, c.EntryKind)
	}
	for k, n := range c.MaxNodesPerKind {
		if !slices.Contains(gr.Kinds, k) || n < 0 {
			return nil, fmt.Errorf("grammar %q: max_nodes_per_kind names %q: %d, which is not a kind of the grammar and a count", gr.Grammar, k, n)
		}
	}
	if c.Gates != "none" {
		return nil, fmt.Errorf("grammar %q: gates %q is not defined; only \"none\" is", gr.Grammar, c.Gates)
	}
	order := []string{StepFreezeStore, StepWake, StepResolve}
	at := -1
	for _, s := range gr.Result {
		i := slices.Index(order, s.Step)
		if i <= at {
			return nil, fmt.Errorf("grammar %q: result step %q is unknown, repeated or out of order (the vocabulary, in order: %v)", gr.Grammar, s.Step, order)
		}
		at = i
	}
	return &gr, nil
}

// Check requires the sentence to be well-formed under the grammar. Every check
// is local to the flat document. Required outcomes per kind and cycle refusal
// are the loader's, from the kind table, not the grammar's.
func (gr *Grammar) Check(g *Graph) error {
	no := func(format string, args ...any) error {
		return fmt.Errorf("graph document does not satisfy the grammar %q: "+format, append([]any{gr.Grammar}, args...)...)
	}
	count := map[string]int{}
	for _, n := range g.Nodes {
		if !slices.Contains(gr.Kinds, n.Kind) {
			return no("the node %q is kind %q, which the grammar does not have", n.Label, n.Kind)
		}
		count[n.Kind]++
	}
	for _, e := range g.Edges {
		want := Edge{g.Nodes[e.From()].Kind, e.Condition(), g.Nodes[e.To()].Kind}
		if !slices.Contains(gr.Edges, want) {
			return no("an edge %v is not one the grammar allows", want)
		}
	}
	c := gr.Constraints
	if kind := g.Nodes[g.EntryID].Kind; kind != c.EntryKind {
		return no("the entry is a %s node, the grammar wants a %s", kind, c.EntryKind)
	}
	if c.OneFailTarget {
		targets := map[string]bool{}
		for _, e := range g.Edges {
			if e.Condition() == EdgeFail {
				targets[e.To()] = true
			}
		}
		if len(targets) > 1 {
			return no("the fail edges lead to %d different nodes, the grammar wants one", len(targets))
		}
	}
	kinds := make([]string, 0, len(c.MaxNodesPerKind))
	for k := range c.MaxNodesPerKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		if count[k] > c.MaxNodesPerKind[k] {
			return no("%d nodes of kind %s, the grammar allows %d", count[k], k, c.MaxNodesPerKind[k])
		}
	}
	// The sentence's result list equals the grammar's, in order, except that a
	// step the grammar marks optional may be absent. Not a subset: a sentence
	// that could drop freeze-store would judge an unfrozen traffic file.
	have := make([]string, 0, len(g.Result))
	for _, s := range g.Result {
		have = append(have, s.Step)
	}
	var want []string
	for _, s := range gr.Result {
		if s.Optional && !slices.Contains(have, s.Step) {
			continue
		}
		want = append(want, s.Step)
	}
	if !slices.Equal(have, want) {
		return no("the result steps are %v, the grammar wants %v (steps it marks optional may be left out)", have, want)
	}
	return nil
}
