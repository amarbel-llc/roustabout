package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	"code.linenisgreat.com/roustabout/internal/0/graph/graphtest"
)

func check(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	var out, errOut strings.Builder
	code := executeCheck(args, &out, &errOut)
	var v map[string]any
	if err := json.Unmarshal([]byte(out.String()), &v); err != nil {
		t.Fatalf("stdout %q (stderr %q): %v", out.String(), errOut.String(), err)
	}
	return code, v
}

// `check` loads the document as `run` does and starts nothing.
func TestCheckSaysWhetherTheDocumentWouldBeRun(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	host := []string{"--issues-moxin", graphtest.Moxin, "--commit-program", graphtest.CommitProgram}

	code, v := check(t, append(host, dir)...)
	if code != 0 || v["outcome"] != "ok" || v["grammar_checked"] != false {
		t.Fatalf("%d %v", code, v)
	}
	if steps, _ := json.Marshal(v["result"]); string(steps) != `["freeze-store","wake","resolve"]` {
		t.Fatalf("result %s", steps)
	}

	grammarFile := filepath.Join(t.TempDir(), "grammar.json")
	grammar := `{"grammar":"issue-filer-seq","version":1,"kinds":["agent","decide","commit","sink"],` +
		`"edges":[["agent","ok","decide"],["decide","ok","commit"],["agent","fail","sink"],["decide","fail","sink"],["commit","fail","sink"]],` +
		`"constraints":{"entry_kind":"agent","one_fail_target":true,"max_nodes_per_kind":{"agent":1,"decide":1,"commit":1,"sink":1},"gates":"none"},` +
		`"result":[{"step":"freeze-store"},{"step":"wake","optional":true},{"step":"resolve"}]}`
	if err := os.WriteFile(grammarFile, []byte(grammar), 0o600); err != nil {
		t.Fatal(err)
	}
	pin := graph.Digest([]byte(grammar))
	code, v = check(t, append(host, "--grammar", grammarFile, "--grammar-digest", pin, dir)...)
	if code != 0 || v["grammar_checked"] != true {
		t.Fatalf("%d %v", code, v)
	}

	code, v = check(t, append(host, "--grammar", grammarFile, "--grammar-digest", graph.Digest([]byte("other")), dir)...)
	if code != 2 || v["outcome"] != "refused" || !strings.Contains(v["reason"].(string), "not the one the host pinned") {
		t.Fatalf("%d %v", code, v)
	}
	if code, _ := check(t, append(host, "--grammar", grammarFile, dir)...); code != 2 {
		t.Fatalf("a grammar without its pin should be refused, got %d", code)
	}

	bad := t.TempDir()
	graphtest.WriteDir(t, bad, graphtest.Options{NoGate: true})
	code, v = check(t, append(host, bad)...)
	if code != 2 || !strings.Contains(v["reason"].(string), "names no gate") {
		t.Fatalf("%d %v", code, v)
	}
}
