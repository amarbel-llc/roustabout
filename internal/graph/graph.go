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

	// commit and sink
	Program string `json:"program,omitempty"`
}

// The result contract's closed vocabulary (FDR-0043 decision 18): the steps a
// document may declare, from which node's artifact, and the record builders
// roustabout ships. A document declares which steps run; it cannot add a step,
// reorder them or write the record's schema.
const (
	StepFreezeStore = "freeze-store"
	StepWake        = "wake"
	StepResolve     = "resolve"

	// BuilderIssueFiler1 is the record builder that turns outcomes into
	// CONTRACT.md's {state, failed_checks, reason, ...}. Version 1's semantics
	// are in docs/generalizing-the-graph.md.
	BuilderIssueFiler1 = "issue-filer/1"
)

// ResultStep is one step of a document's `result` section.
type ResultStep struct {
	Step string `json:"step"`
	// Node is the agent node whose traffic file freeze-store keeps.
	Node string `json:"node,omitempty"`
	// Builder is the record builder resolve uses.
	Builder string `json:"builder,omitempty"`
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

// Outcomes are the outcomes a node can end in, and so the conditions its
// outgoing edges may carry (decision 19). Today they come from the kind alone;
// a switch node's will come from its document too. A kind not in the table has
// none.
func Outcomes(n Node) []string { return kindTable[n.Kind].outcomes }

// MayWrite reports whether the node's kind may be conferred a write tool.
func MayWrite(n Node) bool { return kindTable[n.Kind].mayWrite }

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
	Schema  int      `json:"schema"`
	Review  string   `json:"review"`
	Runtime *Runtime `json:"runtime,omitempty"`
	// Entry names the node the walk starts at. Without it the entry is the one
	// node no edge leads to.
	Entry string `json:"entry,omitempty"`
	// Result declares the result contract's steps; see ResultStep.
	Result []ResultStep    `json:"result,omitempty"`
	Nodes  map[string]Node `json:"nodes"`
	Edges  []Edge          `json:"edges"`
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

	// EntryID is the node the walk starts at.
	EntryID string

	// Result is the declared result contract; a document without a `result`
	// section is refused.
	Result []ResultStep

	// The four nodes, found by kind.
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
	// Grammar is the grammar document the host supplies, and GrammarDigest the
	// digest the host pins it by (FDR-0043 decision 21): the graph document
	// never carries the grammar that validates it. Both or neither; with
	// neither, no grammar is checked.
	Grammar       []byte
	GrammarDigest string
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
	if err := g.validateShape(expect, doc.Entry); err != nil {
		return nil, err
	}
	if err := g.checkResult(doc.Result); err != nil {
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
	if err := checkGrammar(g, expect); err != nil {
		return nil, err
	}
	return g, nil
}

// checkGrammar checks the sentence against the host's pinned grammar, if the
// host supplied one.
func checkGrammar(g *Graph, expect Expect) error {
	if expect.Grammar == nil && expect.GrammarDigest == "" {
		return nil
	}
	if got := Digest(expect.Grammar); expect.GrammarDigest == "" || got != expect.GrammarDigest {
		return fmt.Errorf("the grammar document %s is not the one the host pinned (%q)", got, expect.GrammarDigest)
	}
	gr, err := ParseGrammar(expect.Grammar)
	if err != nil {
		return err
	}
	return gr.Check(g)
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("graph document is not the shape roustabout runs: "+format, args...)
}

func (g *Graph) validateShape(expect Expect, entry string) error {
	if len(g.Nodes) != 4 {
		return refuse("want 4 nodes, got %d", len(g.Nodes))
	}
	// The nodes are found by kind; a label is a display name only.
	byKind := map[string][]string{}
	for id, n := range g.Nodes {
		if !digestPattern.MatchString(id) {
			return refuse("node id %q is not sha256:<64 hex>", id)
		}
		byKind[n.Kind] = append(byKind[n.Kind], id)
	}
	for _, kind := range []string{KindAgent, KindDecide, KindCommit, KindSink} {
		if len(byKind[kind]) != 1 {
			return refuse("want exactly one node of kind %q, got %d", kind, len(byKind[kind]))
		}
	}
	g.DraftID, g.JudgeID, g.CommitID, g.SinkID = byKind[KindAgent][0], byKind[KindDecide][0], byKind[KindCommit][0], byKind[KindSink][0]

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
	if err := g.checkWalk(entry); err != nil {
		return err
	}
	if g.EntryID != g.DraftID {
		return refuse("the entry is %q, not the draft node", g.Nodes[g.EntryID].Label)
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
				e, from.Kind, from.Label, e.Condition(), Outcomes(from))
		}
		k := key{e.From(), e.Condition()}
		if seen[k] {
			return refuse("edges: the %s node %q has two edges for %q", from.Kind, from.Label, e.Condition())
		}
		seen[k] = true
	}
	for id, n := range g.Nodes {
		for _, cond := range kindTable[n.Kind].required {
			if !seen[key{id, cond}] {
				return refuse("edges: the %s node %q has no edge for %q", n.Kind, n.Label, cond)
			}
		}
	}
	return nil
}

// Declares reports whether the document's result contract has the step.
func (g *Graph) Declares(step string) bool {
	for _, s := range g.Result {
		if s.Step == step {
			return true
		}
	}
	return false
}

// FreezesStoresTrafficOf reports whether the result contract keeps the traffic
// file of the node.
func (g *Graph) FreezesStoresTrafficOf(id string) bool {
	for _, s := range g.Result {
		if s.Step == StepFreezeStore && s.Node == id {
			return true
		}
	}
	return false
}

// checkResult validates the declared result contract against the closed
// vocabulary: freeze-store of an agent node's traffic, then wake, then resolve
// with a record builder roustabout ships, in that order and nothing else. A
// document that declares nothing is refused.
func (g *Graph) checkResult(declared []ResultStep) error {
	if len(declared) == 0 {
		return refuse("result: the document declares no result section (freeze-store, wake, resolve)")
	}
	// freeze-store, then wake (which a grammar may mark optional), then resolve.
	var names []string
	for _, s := range declared {
		names = append(names, s.Step)
	}
	if !slices.Equal(names, []string{StepFreezeStore, StepWake, StepResolve}) && !slices.Equal(names, []string{StepFreezeStore, StepResolve}) {
		return refuse("result: want the steps %s, %s, %s in that order (%s may be left out), got %v", StepFreezeStore, StepWake, StepResolve, StepWake, names)
	}
	fs, resolve := declared[0], declared[len(declared)-1]
	if n, ok := g.Nodes[fs.Node]; !ok || n.Kind != KindAgent {
		return refuse("result: %s must name an agent node of the document, got %q", StepFreezeStore, fs.Node)
	}
	for _, s := range declared {
		if (s.Step != StepFreezeStore && s.Node != "") || (s.Step != StepResolve && s.Builder != "") {
			return refuse("result: a step carries only the fields its kind of step takes")
		}
	}
	if resolve.Builder != BuilderIssueFiler1 {
		return refuse("result: %s names the record builder %q, which roustabout does not ship (it ships %s)", StepResolve, resolve.Builder, BuilderIssueFiler1)
	}
	g.Result = declared
	return nil
}

// checkWalk is the part of the structure rules that names no topology: the
// walk starts at one entry (the document's `entry`, else the one node no edge
// leads to), every node is reachable from it, and the graph has no cycle (until
// a bound on one is part of the document).
func (g *Graph) checkWalk(entry string) error {
	inbound := map[string]bool{}
	next := map[string][]string{}
	for _, e := range g.Edges {
		inbound[e.To()] = true
		next[e.From()] = append(next[e.From()], e.To())
	}
	if entry != "" {
		if _, ok := g.Nodes[entry]; !ok {
			return refuse("the entry %q is not a node of the document", entry)
		}
		g.EntryID = entry
	} else {
		var starts []string
		for id := range g.Nodes {
			if !inbound[id] {
				starts = append(starts, id)
			}
		}
		sort.Strings(starts)
		switch len(starts) {
		case 0:
			return refuse("no entry: every node has an edge leading to it, so the document has a cycle or needs an explicit entry")
		case 1:
			g.EntryID = starts[0]
		default:
			return refuse("%d nodes have no edge leading to them and the document names no entry", len(starts))
		}
	}

	reached := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		if reached[id] {
			return
		}
		reached[id] = true
		for _, to := range next[id] {
			visit(to)
		}
	}
	visit(g.EntryID)
	for id, n := range g.Nodes {
		if !reached[id] {
			return refuse("the %s node %q cannot be reached from the entry", n.Kind, n.Label)
		}
	}

	const (
		onPath = 1
		done   = 2
	)
	state := map[string]int{}
	var cyclic func(id string) bool
	cyclic = func(id string) bool {
		switch state[id] {
		case onPath:
			return true
		case done:
			return false
		}
		state[id] = onPath
		for _, to := range next[id] {
			if cyclic(to) {
				return true
			}
		}
		state[id] = done
		return false
	}
	if cyclic(g.EntryID) {
		return refuse("the document has a cycle; a cycle is refused until a bound on it is part of the document")
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
