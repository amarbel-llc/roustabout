// Package graph reads and validates the content-addressed graph document
// roustabout interprets (circus FDR-0041 P3, FDR-0043). The first interpreter
// accepts exactly one shape, the issue-filer graph; a document of any other
// shape is refused before anything is started.
package graph

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const (
	KindAgent  = "agent"
	KindDecide = "decide"
	// KindCommit and KindSink are the two script kinds (FDR-0043 decision 21):
	// only a commit may be conferred a write tool, and a sink never is.
	KindCommit = "commit"
	KindSink   = "sink"
	// KindScript is the kind circus's graph builder wrote for both before the
	// split. It is read as commit or sink by the node's label, until the builder
	// is renamed.
	KindScript = "script"

	EdgeOK   = "ok"
	EdgeFail = "fail"

	ModeEnforce       = "enforce"
	ModeShadow        = "shadow"
	ModeEnforcePublic = "enforce-public"

	// NoSinkProgram is the sink node's program when the graph has no sink
	// program: the tracer bullet's no-op sink.
	NoSinkProgram = "none"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Node is one node document. Fields not used by a node's kind are zero.
type Node struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`

	// agent
	Brief string `json:"brief,omitempty"`
	Moxin string `json:"moxin,omitempty"`

	// decide
	Questions     string            `json:"questions,omitempty"`
	MinConfidence *float64          `json:"min_confidence,omitempty"`
	Modes         map[string]string `json:"modes,omitempty"`
	// Gate names the question handed to `juggler decide --question`. It must
	// be one of the questions.json keys; nothing is inferred when it is absent.
	Gate string `json:"gate,omitempty"`

	// script
	Program string `json:"program,omitempty"`
}

// kindSpec is one row of roustabout's table of node kinds (FDR-0043 decision
// 21): the outcomes a node of the kind can end in, which of them every node of
// the kind must have an edge for, and whether the kind may be conferred a write
// tool. Only a change here adds a kind.
type kindSpec struct {
	outcomes, required []string
	mayWrite           bool
}

var kindTable = map[string]kindSpec{
	KindAgent:  {outcomes: []string{EdgeOK, EdgeFail}, required: []string{EdgeOK, EdgeFail}},
	KindDecide: {outcomes: []string{EdgeOK, EdgeFail}, required: []string{EdgeOK, EdgeFail}},
	// A commit's ok is the end of the run, so it needs no edge for it.
	KindCommit: {outcomes: []string{EdgeOK, EdgeFail}, mayWrite: true},
	// A sink's end never changes the run's outcome, so it has no outgoing edge.
	KindSink: {},
}

// KindOf is the kind a node is read as: the legacy script kind is a commit or a
// sink by its label, and any other kind is itself.
func KindOf(n Node) string {
	if n.Kind == KindScript {
		switch n.Label {
		case KindCommit:
			return KindCommit
		case KindSink:
			return KindSink
		}
	}
	return n.Kind
}

// Outcomes are the outcomes a node can end in, and so the conditions its
// outgoing edges may carry (decision 19). Today they come from the kind alone;
// a switch node's will come from its document too.
func Outcomes(n Node) []string { return kindTable[KindOf(n)].outcomes }

// MayWrite reports whether the node's kind may be conferred a write tool.
func MayWrite(n Node) bool { return kindTable[KindOf(n)].mayWrite }

// Edge is [from, condition, to].
type Edge [3]string

func (e Edge) From() string      { return e[0] }
func (e Edge) Condition() string { return e[1] }
func (e Edge) To() string        { return e[2] }

// Runtime names the build that interprets the graph (circus#314): the juggler
// store path (which pins the clown build and its commit), the moxy store path
// the agents run under, and the glue's own, which is roustabout's build. It
// is in graph.json, so the graph digest covers it: two graphs that differ only
// in the clown build are two graphs.
type Runtime struct {
	Juggler string `json:"juggler"`
	Moxy    string `json:"moxy"`
	Glue    string `json:"glue"`
}

type document struct {
	Schema  int             `json:"schema"`
	Review  string          `json:"review"`
	Runtime *Runtime        `json:"runtime,omitempty"`
	Nodes   map[string]Node `json:"nodes"`
	Edges   []Edge          `json:"edges"`
}

// Graph is a validated issue-filer graph document.
type Graph struct {
	// Digest is "sha256:<hex>" of the raw bytes of graph.json.
	Digest string
	Review string
	// Runtime is the build the document names, or nil for a document that
	// does not (the digest then does not cover the runtime).
	Runtime *Runtime
	// Nodes by id ("sha256:<hex>" of the node document).
	Nodes map[string]Node
	Edges []Edge

	// The four nodes, found by label.
	DraftID, JudgeID, CommitID, SinkID string

	// QuestionsJSON and BriefTOML are the files the graph names by digest.
	QuestionsJSON []byte
	BriefTOML     []byte

	// Questions are the judge's question names: the keys of questions.json,
	// sorted. The graph names them; roustabout holds no list of its own.
	Questions []string
}

func (g *Graph) Draft() Node  { return g.Nodes[g.DraftID] }
func (g *Graph) Judge() Node  { return g.Nodes[g.JudgeID] }
func (g *Graph) Commit() Node { return g.Nodes[g.CommitID] }
func (g *Graph) Sink() Node   { return g.Nodes[g.SinkID] }

// Threshold is the judge's per-question confidence threshold.
func (g *Graph) Threshold() float64 { return *g.Judge().MinConfidence }

// QuestionModes returns the judge's mode per question. A graph without modes
// reads as all enforce, so older documents keep working.
func (g *Graph) QuestionModes() map[string]string {
	if m := g.Judge().Modes; m != nil {
		return m
	}
	modes := make(map[string]string, len(g.Questions))
	for _, q := range g.Questions {
		modes[q] = ModeEnforce
	}
	return modes
}

// HasSinkProgram reports whether the sink node runs a program.
func (g *Graph) HasSinkProgram() bool { return g.Sink().Program != NoSinkProgram }

// Expect names values the host build supplies and the graph must carry. An
// empty field is not checked.
type Expect struct {
	Moxin         string
	CommitProgram string
}

// Digest returns "sha256:<hex>" of b.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Load reads and validates the graph document in root: graph.json,
// graph.digest, nodes/<id>.json, questions.json and drafter.brief.toml, the
// layout circus issue-filer-graph builds.
func Load(root fs.FS, expect Expect) (*Graph, error) {
	raw, err := fs.ReadFile(root, "graph.json")
	if err != nil {
		return nil, refuse("no graph.json: %v", err)
	}
	digestLine, err := fs.ReadFile(root, "graph.digest")
	if err != nil {
		return nil, refuse("no graph.digest: %v", err)
	}
	declared := strings.TrimSpace(strings.SplitN(string(digestLine), "\n", 2)[0])
	if !digestPattern.MatchString(declared) {
		return nil, refuse("graph.digest is not sha256:<64 hex>: %q", declared)
	}
	if actual := Digest(raw); actual != declared {
		return nil, refuse("graph.digest %s does not match graph.json %s", declared, actual)
	}

	var doc document
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, refuse("graph.json: %v", err)
	}

	if rt := doc.Runtime; rt != nil && (rt.Juggler == "" || rt.Moxy == "" || rt.Glue == "") {
		return nil, refuse("runtime must name the juggler, moxy and glue store paths")
	}
	g := &Graph{Digest: declared, Review: doc.Review, Runtime: doc.Runtime, Nodes: doc.Nodes, Edges: doc.Edges}
	if err := g.validateShape(expect); err != nil {
		return nil, err
	}
	if err := g.verifyNodeFiles(root); err != nil {
		return nil, err
	}

	if g.QuestionsJSON, err = fs.ReadFile(root, "questions.json"); err != nil {
		return nil, refuse("no questions.json: %v", err)
	}
	if got := Digest(g.QuestionsJSON); got != g.Judge().Questions {
		return nil, refuse("questions.json %s is not the judge's questions %s", got, g.Judge().Questions)
	}
	if g.Questions, err = questionNames(g.QuestionsJSON); err != nil {
		return nil, err
	}
	if gate := g.Judge().Gate; gate == "" {
		return nil, refuse("the judge node names no gate; it must name one of the questions %v", g.Questions)
	} else if !slices.Contains(g.Questions, gate) {
		return nil, refuse("the judge's gate %q is not one of the questions %v", gate, g.Questions)
	}
	if m := g.Judge().Modes; m != nil {
		if err := checkModes(m, g.Questions); err != nil {
			return nil, err
		}
	}
	if g.BriefTOML, err = fs.ReadFile(root, "drafter.brief.toml"); err != nil {
		return nil, refuse("no drafter.brief.toml: %v", err)
	}
	if got := Digest(g.BriefTOML); got != g.Draft().Brief {
		return nil, refuse("drafter.brief.toml %s is not the draft node's brief %s", got, g.Draft().Brief)
	}
	return g, nil
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("graph document is not the shape roustabout runs: "+format, args...)
}

func (g *Graph) validateShape(expect Expect) error {
	if len(g.Nodes) != 4 {
		return refuse("want 4 nodes, got %d", len(g.Nodes))
	}
	wantKinds := map[string]string{"draft": KindAgent, "judge": KindDecide, "commit": KindCommit, "sink": KindSink}
	ids := map[string]string{}
	for id, n := range g.Nodes {
		if !digestPattern.MatchString(id) {
			return refuse("node id %q is not sha256:<64 hex>", id)
		}
		kind, ok := wantKinds[n.Label]
		if !ok {
			return refuse("node %s has unexpected label %q", id, n.Label)
		}
		if _, dup := ids[n.Label]; dup {
			return refuse("two nodes labelled %q", n.Label)
		}
		if KindOf(n) != kind {
			return refuse("node %q is kind %q, want %q", n.Label, n.Kind, kind)
		}
		ids[n.Label] = id
	}
	g.DraftID, g.JudgeID, g.CommitID, g.SinkID = ids["draft"], ids["judge"], ids["commit"], ids["sink"]

	draft, judge, commit, sink := g.Draft(), g.Judge(), g.Commit(), g.Sink()
	if !digestPattern.MatchString(draft.Brief) || draft.Moxin == "" {
		return refuse("draft node needs a brief digest and a moxin")
	}
	if expect.Moxin != "" && draft.Moxin != expect.Moxin {
		return refuse("draft node moxin %q is not %q", draft.Moxin, expect.Moxin)
	}
	if !digestPattern.MatchString(judge.Questions) {
		return refuse("judge node needs a questions digest")
	}
	if judge.MinConfidence == nil || *judge.MinConfidence < 0 {
		return refuse("judge node needs a non-negative numeric min_confidence")
	}
	if commit.Program == "" {
		return refuse("commit node needs a program")
	}
	if expect.CommitProgram != "" && commit.Program != expect.CommitProgram {
		return refuse("commit node program %q is not %q", commit.Program, expect.CommitProgram)
	}
	if sink.Program == "" {
		return refuse("sink node needs a program (%q for none)", NoSinkProgram)
	}

	if err := g.checkEdges(); err != nil {
		return err
	}
	want := []Edge{
		{g.DraftID, EdgeOK, g.JudgeID},
		{g.JudgeID, EdgeOK, g.CommitID},
		{g.DraftID, EdgeFail, g.SinkID},
		{g.JudgeID, EdgeFail, g.SinkID},
		{g.CommitID, EdgeFail, g.SinkID},
	}
	if !sameEdges(g.Edges, want) {
		return refuse("edges are not draft-ok->judge, judge-ok->commit and each of draft, judge, commit -fail->sink")
	}
	return nil
}

// checkEdges is the part of the edge rules that names no topology (decision
// 19): both ends are nodes, the condition is one of the source node's
// outcomes, a node has at most one edge per outcome (what runs next is a
// function of the document and the outcome), and a node has an edge for every
// outcome its kind requires.
func (g *Graph) checkEdges() error {
	type key struct{ from, cond string }
	seen := map[key]bool{}
	for _, e := range g.Edges {
		from, ok := g.Nodes[e.From()]
		if !ok {
			return refuse("edges: %v starts at a node the document does not have", e)
		}
		if _, ok := g.Nodes[e.To()]; !ok {
			return refuse("edges: %v ends at a node the document does not have", e)
		}
		if !slices.Contains(Outcomes(from), e.Condition()) {
			return refuse("edges: %v leaves the %s node %q on %q, which is not one of its outcomes %v",
				e, KindOf(from), from.Label, e.Condition(), Outcomes(from))
		}
		k := key{e.From(), e.Condition()}
		if seen[k] {
			return refuse("edges: the %s node %q has two edges for %q", KindOf(from), from.Label, e.Condition())
		}
		seen[k] = true
	}
	for id, n := range g.Nodes {
		for _, cond := range kindTable[KindOf(n)].required {
			if !seen[key{id, cond}] {
				return refuse("edges: the %s node %q has no edge for %q", KindOf(n), n.Label, cond)
			}
		}
	}
	return nil
}

// checkModes requires the judge's modes to cover exactly the questions.
func checkModes(modes map[string]string, questions []string) error {
	if len(modes) != len(questions) {
		return refuse("the judge's modes %v are not the questions %v", sortedKeys(modes), questions)
	}
	for _, q := range questions {
		switch modes[q] {
		case ModeEnforce, ModeShadow, ModeEnforcePublic:
		default:
			return refuse("the judge's mode for %q is %q, want enforce | shadow | enforce-public", q, modes[q])
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// questionNames reads the question names from questions.json's keys.
func questionNames(questionsJSON []byte) ([]string, error) {
	var q map[string]json.RawMessage
	if err := json.Unmarshal(questionsJSON, &q); err != nil {
		return nil, refuse("questions.json is not an object: %v", err)
	}
	if len(q) == 0 {
		return nil, refuse("questions.json names no questions")
	}
	return sortedKeys(q), nil
}

func sameEdges(got, want []Edge) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[Edge]int{}
	for _, e := range got {
		seen[e]++
	}
	for _, e := range want {
		if seen[e] != 1 {
			return false
		}
		delete(seen, e)
	}
	return len(seen) == 0
}

// verifyNodeFiles checks each node's id against nodes/<id>.json: the file's
// sha256 is the id, and the file is the node graph.json carries inline.
func (g *Graph) verifyNodeFiles(root fs.FS) error {
	for id, inline := range g.Nodes {
		b, err := fs.ReadFile(root, "nodes/"+id+".json")
		if err != nil {
			return refuse("no node file for %s: %v", id, err)
		}
		if got := Digest(b); got != id {
			return refuse("nodes/%s.json hashes to %s", id, got)
		}
		var fromFile Node
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&fromFile); err != nil {
			return refuse("nodes/%s.json: %v", id, err)
		}
		a, _ := json.Marshal(inline)
		c, _ := json.Marshal(fromFile)
		if !bytes.Equal(a, c) {
			return refuse("nodes/%s.json differs from the node in graph.json", id)
		}
	}
	return nil
}
