package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	"code.linenisgreat.com/roustabout/internal/0/graph/graphtest"
)

func attestCheckArgs(t *testing.T, dir string) []string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "grammar.json")
	if err := os.WriteFile(file, []byte(graphtest.AttestGrammar), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"--commit-program", graphtest.CommitProgram, "--grammar", file, "--grammar-digest", graph.Digest([]byte(graphtest.AttestGrammar)), dir}
}

// `check` loads an attestation graph under an attest/1 grammar, as `run` does,
// and says which request type and right it was checked under.
func TestCheckLoadsAnAttestationGraph(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteAttestDir(t, dir, graphtest.AttestOptions{})
	code, v := check(t, attestCheckArgs(t, dir)...)
	if code != 0 || v["outcome"] != "ok" || v["request_type"] != "attest/1" || v["grammar_checked"] != true {
		t.Fatalf("%d %v", code, v)
	}
	if v["grammar_right"] != graph.GrammarRight(graph.Digest([]byte(graphtest.AttestGrammar))) {
		t.Fatalf("right %v", v["grammar_right"])
	}
	if steps, _ := json.Marshal(v["result"]); string(steps) != `["resolve"]` {
		t.Fatalf("result %s", steps)
	}
	if v["graph_digest"] != graphtest.LoadAttest(t, graphtest.AttestOptions{}).Digest {
		t.Fatalf("digest %v", v["graph_digest"])
	}
}

func TestCheckRefusesAnIssueFilerGraphUnderAnAttestGrammar(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	code, v := check(t, attestCheckArgs(t, dir)...)
	if code != 2 || v["outcome"] != "refused" {
		t.Fatalf("%d %v", code, v)
	}
}

func TestCheckNamesTheIssueFilerRequestTypeByDefault(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	code, v := check(t, "--issues-moxin", graphtest.Moxin, "--commit-program", graphtest.CommitProgram, dir)
	if code != 0 || v["request_type"] != "issue-filer/1" {
		t.Fatalf("%d %v", code, v)
	}
}
