package graph_test

import (
	"strings"
	"testing"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	"code.linenisgreat.com/roustabout/internal/0/graph/graphtest"
)

func TestLoadAttestAcceptsTheSequence(t *testing.T) {
	g := graphtest.LoadAttest(t, graphtest.AttestOptions{})
	if g.Nodes[g.EntryID].Kind != graph.KindAgent {
		t.Fatalf("entry is %v", g.Nodes[g.EntryID])
	}
	if g.CommitID == "" || g.SinkID == "" {
		t.Fatalf("commit %q sink %q", g.CommitID, g.SinkID)
	}
	if len(g.Briefs) != 2 {
		t.Fatalf("%d briefs, want one per agent", len(g.Briefs))
	}
	if len(g.Result) != 1 || g.Result[0].Builder != graph.BuilderAttest1 {
		t.Fatalf("result %v", g.Result)
	}
}

func TestLoadAttestWithoutACommit(t *testing.T) {
	g := graphtest.LoadAttest(t, graphtest.AttestOptions{NoCommit: true, Agents: 1})
	if g.CommitID != "" {
		t.Fatalf("commit %q", g.CommitID)
	}
}

func TestLoadAttestRefusals(t *testing.T) {
	notAttest := strings.Replace(graphtest.AttestGrammar, `"request":"attest/1",`, "", 1)
	for name, c := range map[string]struct {
		opts   graphtest.AttestOptions
		expect func(graph.Expect) graph.Expect
		want   string
	}{
		"no grammar": {expect: func(e graph.Expect) graph.Expect { return graph.Expect{CommitProgram: e.CommitProgram} }, want: "needs the host's pinned grammar"},
		"grammar of another request type": {expect: func(e graph.Expect) graph.Expect {
			e.Grammar, e.GrammarDigest = []byte(notAttest), graph.Digest([]byte(notAttest))
			return e
		}, want: "takes a issue-filer/1 request"},
		"wrong pin":     {expect: func(e graph.Expect) graph.Expect { e.GrammarDigest = graph.Digest([]byte("x")); return e }, want: "not the one the host pinned"},
		"writable tree": {opts: graphtest.AttestOptions{Worktree: "rw"}, want: "reads the worktree and never writes it"},
		"other commit":  {expect: func(e graph.Expect) graph.Expect { e.CommitProgram = "/other"; return e }, want: "commit node program"},
	} {
		t.Run(name, func(t *testing.T) {
			e := graphtest.AttestExpect()
			if c.expect != nil {
				e = c.expect(e)
			}
			_, err := graph.LoadAttest(graphtest.AttestFiles(t, c.opts), e)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestLoadAttestRefusesAMissingBrief(t *testing.T) {
	files := graphtest.AttestFiles(t, graphtest.AttestOptions{})
	for name := range files {
		if strings.HasPrefix(name, "briefs/") {
			delete(files, name)
			break
		}
	}
	if _, err := graph.LoadAttest(files, graphtest.AttestExpect()); err == nil || !strings.Contains(err.Error(), "no brief file") {
		t.Fatalf("err %v", err)
	}
}

func TestLoadAttestRefusesATamperedBrief(t *testing.T) {
	files := graphtest.AttestFiles(t, graphtest.AttestOptions{})
	for name, f := range files {
		if strings.HasPrefix(name, "briefs/") {
			f.Data = append(f.Data, "x"...)
			break
		}
	}
	if _, err := graph.LoadAttest(files, graphtest.AttestExpect()); err == nil || !strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("err %v", err)
	}
}

func TestPinnedRequestType(t *testing.T) {
	if got, err := graph.PinnedRequestType(graph.Expect{}); err != nil || got != graph.RequestIssueFiler {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := graph.PinnedRequestType(graphtest.AttestExpect()); err != nil || got != graph.RequestAttest {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := graph.PinnedRequestType(graph.Expect{Grammar: []byte("{}"), GrammarDigest: graph.Digest([]byte("other"))}); err == nil {
		t.Fatal("an unpinned grammar was accepted")
	}
}
