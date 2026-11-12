package graph

import (
	"io/fs"
)

const (
	// BuilderAttest1 is the record builder an attestation graph resolves with:
	// the verdict, the graph digest and the run-level ledger, in the record a
	// gate digests (docs/attestation-graph.md, decision 2).
	BuilderAttest1 = "attest/1"

	// WorktreeRO is the one access an attestation agent has to the worktree.
	WorktreeRO = "ro"
)

// AttestGraph is a validated attestation graph document: agents in a sequence
// of ok edges, check scripts, at most one commit (emit_issue) and one sink. It
// has no judge and no questions.
type AttestGraph struct {
	// Digest is "sha256:<hex>" of the raw bytes of graph.json.
	Digest  string
	Review  string
	Runtime *Runtime
	Nodes   map[string]Node
	Edges   []Edge
	// EntryID is the node the walk starts at; it is an agent.
	EntryID string
	Result  []ResultStep
	// SinkID is the one sink; CommitID is the commit node, or "".
	SinkID, CommitID string
	// Briefs are the agents' brief files, by the digest their node names.
	Briefs map[string][]byte
}

// structure is the part of the graph the topology-free checks and the grammar
// read, as the *Graph they were written against.
func (a *AttestGraph) structure() *Graph {
	return &Graph{Digest: a.Digest, Review: a.Review, Runtime: a.Runtime, Nodes: a.Nodes, Edges: a.Edges, EntryID: a.EntryID, Result: a.Result}
}

// PinnedRequestType is the request type the host's pinned grammar names, or
// RequestIssueFiler when the host pinned none. It refuses a grammar that is
// not the one pinned or does not parse.
func PinnedRequestType(expect Expect) (string, error) {
	gr, err := pinnedGrammar(expect)
	if err != nil || gr == nil {
		return RequestIssueFiler, err
	}
	return gr.RequestType(), nil
}

// pinnedGrammar parses the host's grammar after checking it is the pinned one;
// nil when the host supplied none.
func pinnedGrammar(expect Expect) (*Grammar, error) {
	if expect.Grammar == nil && expect.GrammarDigest == "" {
		return nil, nil
	}
	if got := Digest(expect.Grammar); expect.GrammarDigest == "" || got != expect.GrammarDigest {
		return nil, errNotPinned(got, expect.GrammarDigest)
	}
	return ParseGrammar(expect.Grammar)
}

// LoadAttest reads and validates the attestation graph document in root:
// graph.json, graph.digest, nodes/<id>.json and briefs/<brief digest>.toml for
// each agent. The host must pin a grammar whose request type is attest/1; the
// right to run it is named by that grammar's digest (GrammarRight).
func LoadAttest(root fs.FS, expect Expect) (*AttestGraph, error) {
	gr, err := pinnedGrammar(expect)
	if err != nil {
		return nil, err
	}
	if gr == nil {
		return nil, refuse("an attestation graph needs the host's pinned grammar")
	}
	if rt := gr.RequestType(); rt != RequestAttest {
		return nil, refuse("the pinned grammar takes a %s request, not %s", rt, RequestAttest)
	}
	declared, doc, err := readDocument(root)
	if err != nil {
		return nil, err
	}
	a := &AttestGraph{Digest: declared, Review: doc.Review, Runtime: doc.Runtime, Nodes: doc.Nodes, Edges: doc.Edges}
	if err := a.validateShape(expect); err != nil {
		return nil, err
	}
	g := a.structure()
	if err := g.checkEdges(); err != nil {
		return nil, err
	}
	if err := g.checkWalk(doc.Entry); err != nil {
		return nil, err
	}
	a.EntryID = g.EntryID
	if a.Nodes[a.EntryID].Kind != KindAgent {
		return nil, refuse("the entry is the %s node %q, not an agent", a.Nodes[a.EntryID].Kind, a.Nodes[a.EntryID].Label)
	}
	if err := a.checkResult(doc.Result); err != nil {
		return nil, err
	}
	g = a.structure()
	if err := g.verifyNodeFiles(root); err != nil {
		return nil, err
	}
	a.Briefs = map[string][]byte{}
	for _, n := range a.Nodes {
		if n.Kind != KindAgent {
			continue
		}
		b, err := fs.ReadFile(root, "briefs/"+n.Brief+".toml")
		if err != nil {
			return nil, refuse("no brief file for %s: %v", n.Brief, err)
		}
		if got := Digest(b); got != n.Brief {
			return nil, refuse("briefs/%s.toml hashes to %s", n.Brief, got)
		}
		a.Briefs[n.Brief] = b
	}
	if err := gr.Check(g); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *AttestGraph) validateShape(expect Expect) error {
	var agents, commits, sinks int
	for id, n := range a.Nodes {
		if !digestPattern.MatchString(id) {
			return refuse("node id %q is not sha256:<64 hex>", id)
		}
		switch n.Kind {
		case KindAgent:
			agents++
			if !digestPattern.MatchString(n.Brief) || n.Moxin == "" || n.Model == "" {
				return refuse("agent node %q needs a brief digest, a moxin and a model", n.Label)
			}
			if n.Worktree != WorktreeRO {
				return refuse("agent node %q has worktree %q, want %q: an attestation agent reads the worktree and never writes it", n.Label, n.Worktree, WorktreeRO)
			}
		case KindCheck:
			if n.Program == "" {
				return refuse("check node %q needs a program", n.Label)
			}
		case KindCommit:
			commits++
			a.CommitID = id
			if n.Program == "" {
				return refuse("commit node %q needs a program", n.Label)
			}
			if expect.CommitProgram != "" && n.Program != expect.CommitProgram {
				return refuse("commit node program %q is not %q", n.Program, expect.CommitProgram)
			}
		case KindSink:
			sinks++
			a.SinkID = id
			if n.Program == "" {
				return refuse("sink node %q needs a program (%q for none)", n.Label, NoSinkProgram)
			}
		default:
			return refuse("node %q is kind %q, which an attestation graph does not have", n.Label, n.Kind)
		}
	}
	if agents == 0 {
		return refuse("an attestation graph needs at least one agent node")
	}
	if commits > 1 {
		return refuse("an attestation graph has at most one commit node, got %d", commits)
	}
	if sinks != 1 {
		return refuse("an attestation graph needs exactly one sink node, got %d", sinks)
	}
	return nil
}

// checkResult requires the one result an attestation graph declares: resolve
// with the attest/1 record builder. There is no judged traffic to freeze, and
// the wake is the requester's, not the graph's.
func (a *AttestGraph) checkResult(declared []ResultStep) error {
	if len(declared) != 1 || declared[0].Step != StepResolve || declared[0].Node != "" {
		return refuse("result: an attestation graph declares exactly one step, %s with the record builder %s", StepResolve, BuilderAttest1)
	}
	if declared[0].Builder != BuilderAttest1 {
		return refuse("result: %s names the record builder %q, which an attestation graph does not use (it uses %s)", StepResolve, declared[0].Builder, BuilderAttest1)
	}
	a.Result = declared
	return nil
}
