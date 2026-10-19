package graph

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	testBrief     = "brief template\n"
	testQuestions = `{"clean":{},"complete":{},"dup":{},"faithful":{},"repo":{},"request":{}}`
	testMoxin     = "/nix/store/aaa-issues"
	testCommit    = "/nix/store/bbb-commit/bin/issues-commit"
)

// fixture is a mutable description of an issue-filer graph document.
type fixture struct {
	nodes     map[string]map[string]any // by label
	edges     [][3]string               // by label
	questions string
	brief     string
	review    string
	badDigest bool
}

func newFixture() *fixture {
	return &fixture{
		nodes: map[string]map[string]any{
			"draft":  {"kind": "agent", "label": "draft", "brief": Digest([]byte(testBrief)), "moxin": testMoxin},
			"judge":  {"kind": "decide", "label": "judge", "questions": Digest([]byte(testQuestions)), "min_confidence": 0.5},
			"commit": {"kind": "script", "label": "commit", "program": testCommit},
			"sink":   {"kind": "script", "label": "sink", "program": "none"},
		},
		edges: [][3]string{
			{"draft", "ok", "judge"},
			{"judge", "ok", "commit"},
			{"draft", "fail", "sink"},
			{"judge", "fail", "sink"},
			{"commit", "fail", "sink"},
		},
		questions: testQuestions,
		brief:     testBrief,
		review:    "all-or-nothing",
	}
}

func (f *fixture) fs(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	ids := map[string]string{}
	nodes := map[string]any{}
	for label, n := range f.nodes {
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		ids[label] = Digest(b)
		nodes[ids[label]] = n
		files["nodes/"+ids[label]+".json"] = &fstest.MapFile{Data: b}
	}
	edges := [][3]string{}
	for _, e := range f.edges {
		edges = append(edges, [3]string{ids[e[0]], e[1], ids[e[2]]})
	}
	raw, err := json.Marshal(map[string]any{"schema": 1, "review": f.review, "nodes": nodes, "edges": edges})
	if err != nil {
		t.Fatal(err)
	}
	digest := Digest(raw)
	if f.badDigest {
		digest = Digest([]byte("something else"))
	}
	files["graph.json"] = &fstest.MapFile{Data: raw}
	files["graph.digest"] = &fstest.MapFile{Data: []byte(digest + "\n")}
	files["questions.json"] = &fstest.MapFile{Data: []byte(f.questions)}
	files["drafter.brief.toml"] = &fstest.MapFile{Data: []byte(f.brief)}
	return files
}

func TestLoadValid(t *testing.T) {
	f := newFixture()
	g, err := Load(f.fs(t), Expect{Moxin: testMoxin, CommitProgram: testCommit})
	if err != nil {
		t.Fatal(err)
	}
	if g.Threshold() != 0.5 {
		t.Fatalf("threshold %v", g.Threshold())
	}
	if g.HasSinkProgram() {
		t.Fatal("sink program is none")
	}
	for _, q := range QuestionNames {
		if g.QuestionModes()[q] != ModeEnforce {
			t.Fatalf("question %s should default to enforce", q)
		}
	}
	if g.Draft().Label != "draft" || g.Commit().Program != testCommit {
		t.Fatal("nodes not found by label")
	}
}

func TestLoadModes(t *testing.T) {
	f := newFixture()
	f.nodes["judge"]["modes"] = map[string]string{
		"request": "enforce", "complete": "shadow", "repo": "enforce-public",
		"faithful": "enforce", "clean": "enforce", "dup": "shadow",
	}
	g, err := Load(f.fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if g.QuestionModes()["dup"] != ModeShadow {
		t.Fatal("modes not read")
	}
}

func TestLoadRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fixture)
		expect Expect
		want   string
	}{
		{"digest mismatch", func(f *fixture) { f.badDigest = true }, Expect{}, "does not match graph.json"},
		{"wrong kind", func(f *fixture) { f.nodes["judge"]["kind"] = "agent" }, Expect{}, "kind"},
		{"extra node field", func(f *fixture) { f.nodes["sink"]["extra"] = 1 }, Expect{}, "unknown field"},
		{"missing edge", func(f *fixture) { f.edges = f.edges[:4] }, Expect{}, "edges"},
		{"extra edge", func(f *fixture) { f.edges = append(f.edges, [3]string{"commit", "ok", "sink"}) }, Expect{}, "edges"},
		{"no threshold", func(f *fixture) { delete(f.nodes["judge"], "min_confidence") }, Expect{}, "min_confidence"},
		{"string threshold", func(f *fixture) { f.nodes["judge"]["min_confidence"] = "0.5" }, Expect{}, "min_confidence"},
		{"bad mode", func(f *fixture) {
			f.nodes["judge"]["modes"] = map[string]string{"request": "maybe", "complete": "enforce", "repo": "enforce", "faithful": "enforce", "clean": "enforce", "dup": "enforce"}
		}, Expect{}, "mode"},
		{"short modes", func(f *fixture) { f.nodes["judge"]["modes"] = map[string]string{"request": "enforce"} }, Expect{}, "six questions"},
		{"five questions", func(f *fixture) {
			f.questions = `{"clean":{},"complete":{},"dup":{},"faithful":{},"repo":{}}`
			f.nodes["judge"]["questions"] = Digest([]byte(f.questions))
		}, Expect{}, "questions.json keys"},
		{"questions file swapped", func(f *fixture) { f.questions = `{"other":1}` }, Expect{}, "is not the judge's questions"},
		{"brief file swapped", func(f *fixture) { f.brief = "different" }, Expect{}, "is not the draft node's brief"},
		{"wrong moxin", func(f *fixture) {}, Expect{Moxin: "/nix/store/zzz"}, "moxin"},
		{"wrong commit program", func(f *fixture) {}, Expect{CommitProgram: "/nix/store/zzz"}, "program"},
		{"empty sink program", func(f *fixture) { f.nodes["sink"]["program"] = "" }, Expect{}, "sink node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.mutate(f)
			_, err := Load(f.fs(t), tc.expect)
			if err == nil {
				t.Fatal("want refusal, got nil")
			}
			if !strings.Contains(err.Error(), "not the shape roustabout runs") && !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("not a refusal: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

func TestLoadRefusesTamperedNodeFile(t *testing.T) {
	f := newFixture()
	files := f.fs(t)
	for name := range files {
		if strings.HasPrefix(name, "nodes/") {
			files[name] = &fstest.MapFile{Data: []byte(`{"kind":"script","label":"sink","program":"x"}`)}
			break
		}
	}
	if _, err := Load(files, Expect{}); err == nil || !strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("want node hash refusal, got %v", err)
	}
}

func TestLoadRefusesMissingFiles(t *testing.T) {
	for _, name := range []string{"graph.json", "graph.digest", "questions.json", "drafter.brief.toml"} {
		files := newFixture().fs(t)
		delete(files, name)
		if _, err := Load(files, Expect{}); err == nil {
			t.Fatalf("missing %s should be refused", name)
		}
	}
}
