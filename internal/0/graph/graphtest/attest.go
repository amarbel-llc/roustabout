package graphtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"code.linenisgreat.com/roustabout/internal/0/graph"
)

const (
	AttestModel   = "sonnet"
	AttestBrief   = "# the attestation agent's brief\n"
	CheckProgram  = "/nix/store/test-check/bin/attest-check"
	AttestMoxin   = "/nix/store/test-attest-moxin"
	AttestGrammar = `{"grammar":"attest-seq","version":1,"request":"attest/1","kinds":["agent","check","commit","sink"],` +
		`"edges":[["agent","ok","agent"],["agent","ok","check"],["agent","fail","sink"],["check","ok","commit"],["check","fail","sink"],["commit","fail","sink"]],` +
		`"constraints":{"entry_kind":"agent","one_fail_target":true,"max_nodes_per_kind":{"check":1,"commit":1,"sink":1},"gates":"none"},` +
		`"result":[{"step":"resolve"}]}`
)

// AttestOptions vary the attestation document; the zero value is two agents,
// a check, a commit and a sink.
type AttestOptions struct {
	// Agents is how many agent nodes run in sequence (default 2).
	Agents int
	// NoCommit leaves the commit node out: the check's ok then ends the run.
	NoCommit bool
	// Worktree replaces the agents' worktree access ("ro").
	Worktree string
}

// AttestExpect is the host's expectation for the documents Files builds.
func AttestExpect() graph.Expect {
	return graph.Expect{CommitProgram: CommitProgram, Grammar: []byte(AttestGrammar), GrammarDigest: graph.Digest([]byte(AttestGrammar))}
}

// LoadAttest builds and loads an attestation graph document.
func LoadAttest(t testing.TB, o AttestOptions) *graph.AttestGraph {
	t.Helper()
	g, err := graph.LoadAttest(AttestFiles(t, o), AttestExpect())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// WriteAttestDir writes the attestation document to dir.
func WriteAttestDir(t testing.TB, dir string, o AttestOptions) {
	t.Helper()
	for name, f := range AttestFiles(t, o) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// AttestFiles builds the attestation document's files.
func AttestFiles(t testing.TB, o AttestOptions) fstest.MapFS {
	t.Helper()
	agents := o.Agents
	if agents == 0 {
		agents = 2
	}
	worktree := graph.WorktreeRO
	if o.Worktree != "" {
		worktree = o.Worktree
	}
	files := fstest.MapFS{}
	ids := map[string]string{}
	byID := map[string]any{}
	add := func(label string, n map[string]any) {
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		ids[label] = graph.Digest(b)
		byID[ids[label]] = n
		files["nodes/"+ids[label]+".json"] = &fstest.MapFile{Data: b}
	}
	labels := []string{}
	for i := 0; i < agents; i++ {
		label := "agent-" + string(rune('a'+i))
		labels = append(labels, label)
		brief := AttestBrief + label + "\n"
		files["briefs/"+graph.Digest([]byte(brief))+".toml"] = &fstest.MapFile{Data: []byte(brief)}
		add(label, map[string]any{"kind": "agent", "label": label, "brief": graph.Digest([]byte(brief)), "moxin": AttestMoxin, "model": AttestModel, "worktree": worktree})
	}
	add("check", map[string]any{"kind": "check", "label": "check", "program": CheckProgram})
	add("sink", map[string]any{"kind": "sink", "label": "sink", "program": SinkProgram})
	e := func(a, c, b string) [3]string { return [3]string{ids[a], c, ids[b]} }
	var edges [][3]string
	for i, l := range labels {
		next := "check"
		if i+1 < len(labels) {
			next = labels[i+1]
		}
		edges = append(edges, e(l, "ok", next), e(l, "fail", "sink"))
	}
	edges = append(edges, e("check", "fail", "sink"))
	if !o.NoCommit {
		add("commit", map[string]any{"kind": "commit", "label": "commit", "program": CommitProgram})
		edges = append(edges, e("check", "ok", "commit"), e("commit", "fail", "sink"))
	}
	raw, err := json.Marshal(map[string]any{
		"schema": 1, "review": "all-or-nothing", "nodes": byID,
		"result": []map[string]string{{"step": "resolve", "builder": "attest/1"}},
		"edges":  edges,
	})
	if err != nil {
		t.Fatal(err)
	}
	files["graph.json"] = &fstest.MapFile{Data: raw}
	files["graph.digest"] = &fstest.MapFile{Data: []byte(graph.Digest(raw) + "\n")}
	return files
}
