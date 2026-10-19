package graph

import (
	"encoding/json"
	"strings"
	"testing"
)

// issueFilerSeq is circus's `issue-filer-seq` grammar (bricks-reconciliation
// note, section 1), with a hook to change one part of it.
func issueFilerSeq(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	doc := map[string]any{
		"grammar": "issue-filer-seq",
		"version": 1,
		"kinds":   []string{"agent", "decide", "commit", "sink"},
		"edges": [][3]string{
			{"agent", "ok", "decide"},
			{"decide", "ok", "commit"},
			{"agent", "fail", "sink"},
			{"decide", "fail", "sink"},
			{"commit", "fail", "sink"},
		},
		"constraints": map[string]any{
			"entry_kind":         "agent",
			"one_fail_target":    true,
			"max_nodes_per_kind": map[string]int{"agent": 1, "decide": 1, "commit": 1, "sink": 1},
			"gates":              "none",
		},
		"result": []map[string]any{{"step": "freeze-store"}, {"step": "wake", "optional": true}, {"step": "resolve"}},
	}
	if mutate != nil {
		mutate(doc)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loadUnder(t *testing.T, f *fixture, grammar []byte, pin string) error {
	t.Helper()
	if pin == "" {
		pin = Digest(grammar)
	}
	_, err := Load(f.fs(t), Expect{Grammar: grammar, GrammarDigest: pin})
	return err
}

func TestGrammarAcceptsTheIssueFilerSentence(t *testing.T) {
	if err := loadUnder(t, newFixture(), issueFilerSeq(t, nil), ""); err != nil {
		t.Fatal(err)
	}
}

// A step the grammar marks optional may be left out of the sentence, and the
// loader's own result rules allow that for wake.
func TestGrammarOptionalStepMayBeLeftOut(t *testing.T) {
	f := newFixture()
	f.result = issueFilerResult()
	f.result = append(f.result[:1], f.result[2])
	if err := loadUnder(t, f, issueFilerSeq(t, nil), ""); err != nil {
		t.Fatal(err)
	}
	// And the same sentence is refused by a grammar that does not mark wake optional.
	strict := issueFilerSeq(t, func(d map[string]any) {
		d["result"] = []map[string]any{{"step": "freeze-store"}, {"step": "wake"}, {"step": "resolve"}}
	})
	if err := loadUnder(t, f, strict, ""); err == nil || !strings.Contains(err.Error(), "result steps") {
		t.Fatalf("want a result-steps refusal, got %v", err)
	}
}

func TestGrammarRefusesASentenceItDoesNotAllow(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"an edge the grammar lacks", func(d map[string]any) {
			d["edges"] = [][3]string{{"agent", "ok", "decide"}, {"decide", "ok", "commit"}, {"agent", "fail", "sink"}, {"commit", "fail", "sink"}}
		}, "not one the grammar allows"},
		{"another entry kind", func(d map[string]any) { d["constraints"].(map[string]any)["entry_kind"] = "decide" }, "the grammar wants a decide"},
		{"too many nodes of a kind", func(d map[string]any) {
			d["constraints"].(map[string]any)["max_nodes_per_kind"] = map[string]int{"sink": 0}
		}, "allows 0"},
		{"a kind the grammar lacks", func(d map[string]any) {
			d["kinds"] = []string{"agent", "decide", "commit"}
			d["edges"] = [][3]string{{"agent", "ok", "decide"}, {"decide", "ok", "commit"}}
			d["constraints"].(map[string]any)["max_nodes_per_kind"] = map[string]int{}
		}, "does not have"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadUnder(t, newFixture(), issueFilerSeq(t, tc.mutate), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// The host pins the grammar by digest; a different document is refused before
// it is read.
func TestGrammarMustBeTheOneThePinNames(t *testing.T) {
	err := loadUnder(t, newFixture(), issueFilerSeq(t, nil), Digest([]byte("another grammar")))
	if err == nil || !strings.Contains(err.Error(), "not the one the host pinned") {
		t.Fatalf("got %v", err)
	}
	if _, err := Load(newFixture().fs(t), Expect{Grammar: issueFilerSeq(t, nil)}); err == nil {
		t.Fatal("a grammar without a pin should be refused")
	}
}

func TestParseGrammarRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"a version it does not know", func(d map[string]any) { d["version"] = 2 }, "version 2"},
		{"a kind that is not in the table", func(d map[string]any) { d["kinds"] = []string{"agent", "script"} }, "not a node kind"},
		{"a condition outside the kind's outcomes", func(d map[string]any) {
			d["edges"] = [][3]string{{"sink", "fail", "commit"}}
		}, "not one of its outcomes"},
		{"gates other than none", func(d map[string]any) { d["constraints"].(map[string]any)["gates"] = "any" }, "gates"},
		{"a result step out of order", func(d map[string]any) {
			d["result"] = []map[string]any{{"step": "resolve"}, {"step": "freeze-store"}}
		}, "out of order"},
		{"a field the schema does not define", func(d map[string]any) { d["extra"] = 1 }, "unknown field"},
		{"an entry kind that is not a grammar kind", func(d map[string]any) { d["constraints"].(map[string]any)["entry_kind"] = "switch" }, "entry_kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseGrammar(issueFilerSeq(t, tc.mutate)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
