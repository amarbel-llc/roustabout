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
	runtime   any
	entry     string // a label; empty leaves the entry to the edges
	// result is the document's result section; a step's "node" is a label.
	result []map[string]string
}

// issueFilerResult is the section circus's builder will write.
func issueFilerResult() []map[string]string {
	return []map[string]string{
		{"step": "freeze-store", "node": "draft"},
		{"step": "wake"},
		{"step": "resolve", "builder": "issue-filer/1"},
	}
}

func newFixture() *fixture {
	return &fixture{
		nodes: map[string]map[string]any{
			"draft":  {"kind": "agent", "label": "draft", "brief": Digest([]byte(testBrief)), "moxin": testMoxin},
			"judge":  {"kind": "decide", "label": "judge", "questions": Digest([]byte(testQuestions)), "min_confidence": 0.5, "gate": "request"},
			"commit": {"kind": "commit", "label": "commit", "program": testCommit},
			"sink":   {"kind": "sink", "label": "sink", "program": "none"},
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
	doc := map[string]any{"schema": 1, "review": f.review, "nodes": nodes, "edges": edges}
	if f.runtime != nil {
		doc["runtime"] = f.runtime
	}
	if f.entry != "" {
		doc["entry"] = ids[f.entry]
	}
	if f.result != nil {
		var steps []map[string]string
		for _, s := range f.result {
			c := map[string]string{}
			for k, v := range s {
				if k == "node" {
					v = ids[v]
				}
				c[k] = v
			}
			steps = append(steps, c)
		}
		doc["result"] = steps
	}
	raw, err := json.Marshal(doc)
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
	if got := strings.Join(g.Questions, ","); got != "clean,complete,dup,faithful,repo,request" {
		t.Fatalf("questions %s", got)
	}
	for _, q := range g.Questions {
		if g.QuestionModes()[q] != ModeEnforce {
			t.Fatalf("question %s should default to enforce", q)
		}
	}
	if g.Draft().Label != "draft" || g.Commit().Program != testCommit {
		t.Fatal("nodes not found by label")
	}
}

// The nodes are found by kind and the walk starts at the document's entry: a
// label is a display name only, and an explicit entry is honoured.
func TestLabelsAreDisplayNamesAndTheEntryIsTheDocuments(t *testing.T) {
	f := newFixture()
	f.entry = "draft"
	byLabel := map[string]string{"draft": "intake", "judge": "gatekeeper", "commit": "file-it", "sink": "bin"}
	for old, name := range byLabel {
		f.nodes[old]["label"] = name
	}
	g, err := Load(f.fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Draft().Label != "intake" || g.Judge().Label != "gatekeeper" || g.Commit().Label != "file-it" || g.Sink().Label != "bin" {
		t.Fatal("nodes not found by kind")
	}
	if g.EntryID != g.DraftID {
		t.Fatalf("entry %s, draft %s", g.EntryID, g.DraftID)
	}
}

// FDR-0043 decision 18: the result contract is declared in the document; a
// document that declares nothing reads as the issue-filer's list.
func TestResultSection(t *testing.T) {
	absent, err := Load(newFixture().fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture()
	f.result = issueFilerResult()
	declared, err := Load(f.fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	for name, g := range map[string]*Graph{"absent": absent, "declared": declared} {
		if len(g.Result) != 3 || !g.Declares(StepWake) || !g.FreezesStoresTrafficOf(g.DraftID) || g.FreezesStoresTrafficOf(g.JudgeID) {
			t.Fatalf("%s: %+v", name, g.Result)
		}
		if g.Result[2].Builder != BuilderIssueFiler1 {
			t.Fatalf("%s: builder %q", name, g.Result[2].Builder)
		}
	}
	if absent.Digest == declared.Digest {
		t.Fatal("the section is part of the document, so of its digest")
	}
}

// FDR-0043 decision 21: commit and sink are kinds of their own, and only the
// commit may be conferred a write tool. The old kind script, which circus's
// builder wrote for both, is refused.
func TestCommitAndSinkKinds(t *testing.T) {
	g, err := Load(newFixture().fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if !MayWrite(g.Commit()) || MayWrite(g.Sink()) || MayWrite(g.Draft()) || MayWrite(g.Judge()) {
		t.Fatal("only the commit may be conferred a write tool")
	}
	f := newFixture()
	f.nodes["commit"]["kind"], f.nodes["sink"]["kind"] = "script", "script"
	if _, err := Load(f.fs(t), Expect{}); err == nil {
		t.Fatal("the old script kind should be refused")
	}
}

// circus#314: the document names its runtime, and the digest covers it.
func TestLoadRuntime(t *testing.T) {
	withoutRuntime, err := Load(newFixture().fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if withoutRuntime.Runtime != nil {
		t.Fatal("a document that names no runtime has none")
	}
	f := newFixture()
	f.runtime = map[string]string{"juggler": "/nix/store/aaa-clown/bin/juggler", "moxy": "/nix/store/bbb-moxy", "glue": "/nix/store/ccc-roustabout"}
	g, err := Load(f.fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Runtime == nil || g.Runtime.Juggler != "/nix/store/aaa-clown/bin/juggler" || g.Runtime.Glue != "/nix/store/ccc-roustabout" {
		t.Fatalf("%+v", g.Runtime)
	}
	// Two graphs that differ only in the clown build are two graphs.
	f.runtime = map[string]string{"juggler": "/nix/store/zzz-clown/bin/juggler", "moxy": "/nix/store/bbb-moxy", "glue": "/nix/store/ccc-roustabout"}
	other, err := Load(f.fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if other.Digest == g.Digest {
		t.Fatal("the digest should cover the runtime")
	}
}

func TestLoadRefusesAnIncompleteRuntime(t *testing.T) {
	for name, rt := range map[string]any{
		"no glue":       map[string]string{"juggler": "/j", "moxy": "/m"},
		"empty moxy":    map[string]string{"juggler": "/j", "moxy": "", "glue": "/g"},
		"unknown field": map[string]string{"juggler": "/j", "moxy": "/m", "glue": "/g", "clown": "/c"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			f.runtime = rt
			if _, err := Load(f.fs(t), Expect{}); err == nil {
				t.Fatal("want refusal")
			}
		})
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

// The question names are the graph's: renamed questions load, and the modes
// have to cover exactly those keys.
func TestLoadReadsQuestionNamesFromTheGraph(t *testing.T) {
	f := newFixture()
	f.questions = `{"is-clean":{},"is-faithful":{},"voice-matches-request":{}}`
	f.nodes["judge"]["questions"] = Digest([]byte(f.questions))
	f.nodes["judge"]["gate"] = "is-clean"
	g, err := Load(f.fs(t), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Judge().Gate != "is-clean" {
		t.Fatalf("gate %q", g.Judge().Gate)
	}
	if got := strings.Join(g.Questions, ","); got != "is-clean,is-faithful,voice-matches-request" {
		t.Fatalf("questions %s", got)
	}
	if len(g.QuestionModes()) != 3 || g.QuestionModes()["is-clean"] != ModeEnforce {
		t.Fatalf("default modes %v", g.QuestionModes())
	}
	f.nodes["judge"]["modes"] = map[string]string{"is-clean": "enforce", "is-faithful": "shadow", "voice-matches-request": "enforce"}
	if g, err = Load(f.fs(t), Expect{}); err != nil || g.QuestionModes()["is-faithful"] != ModeShadow {
		t.Fatalf("modes over renamed questions: %v", err)
	}
	f.nodes["judge"]["modes"] = map[string]string{"clean": "enforce", "is-faithful": "enforce", "voice-matches-request": "enforce"}
	if _, err := Load(f.fs(t), Expect{}); err == nil {
		t.Fatal("modes that miss a question should be refused")
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
		{"a cycle", func(f *fixture) {
			f.edges = append(f.edges, [3]string{"commit", "ok", "draft"})
			f.entry = "draft"
		}, Expect{}, "cycle"},
		{"no entry because of a cycle", func(f *fixture) { f.edges = append(f.edges, [3]string{"commit", "ok", "draft"}) }, Expect{}, "no entry"},
		{"a node the entry does not reach", func(f *fixture) { f.entry = "judge" }, Expect{}, "cannot be reached"},
		{"extra edge", func(f *fixture) { f.edges = append(f.edges, [3]string{"commit", "ok", "sink"}) }, Expect{}, "edges"},
		{"an edge out of the sink", func(f *fixture) { f.edges = append(f.edges, [3]string{"sink", "fail", "commit"}) }, Expect{}, "not one of its outcomes"},
		{"two edges for one outcome", func(f *fixture) { f.edges = append(f.edges, [3]string{"draft", "ok", "commit"}) }, Expect{}, "two edges"},
		{"no edge for a required outcome", func(f *fixture) { f.edges = f.edges[:2] }, Expect{}, "no edge for"},
		{"commit kind on the sink", func(f *fixture) { f.nodes["sink"]["kind"] = "commit" }, Expect{}, "kind"},
		{"result with an unknown step", func(f *fixture) {
			f.result = issueFilerResult()
			f.result[1] = map[string]string{"step": "email"}
		}, Expect{}, "result:"},
		{"result out of order", func(f *fixture) {
			f.result = issueFilerResult()
			f.result[0], f.result[1] = f.result[1], f.result[0]
		}, Expect{}, "result:"},
		{"result without resolve", func(f *fixture) { f.result = issueFilerResult()[:2] }, Expect{}, "result:"},
		{"freeze-store of a node that is not an agent", func(f *fixture) {
			f.result = issueFilerResult()
			f.result[0]["node"] = "judge"
		}, Expect{}, "agent node"},
		{"freeze-store without a node", func(f *fixture) {
			f.result = issueFilerResult()
			delete(f.result[0], "node")
		}, Expect{}, "agent node"},
		{"an unknown record builder", func(f *fixture) {
			f.result = issueFilerResult()
			f.result[2]["builder"] = "issue-filer/2"
		}, Expect{}, "record builder"},
		{"a field the step does not take", func(f *fixture) {
			f.result = issueFilerResult()
			f.result[1]["builder"] = "issue-filer/1"
		}, Expect{}, "only the fields"},
		{"no threshold", func(f *fixture) { delete(f.nodes["judge"], "min_confidence") }, Expect{}, "min_confidence"},
		{"string threshold", func(f *fixture) { f.nodes["judge"]["min_confidence"] = "0.5" }, Expect{}, "min_confidence"},
		{"bad mode", func(f *fixture) {
			f.nodes["judge"]["modes"] = map[string]string{"request": "maybe", "complete": "enforce", "repo": "enforce", "faithful": "enforce", "clean": "enforce", "dup": "enforce"}
		}, Expect{}, "mode"},
		{"short modes", func(f *fixture) { f.nodes["judge"]["modes"] = map[string]string{"request": "enforce"} }, Expect{}, "are not the questions"},
		{"modes naming another question", func(f *fixture) {
			f.nodes["judge"]["modes"] = map[string]string{"request": "enforce", "complete": "enforce", "repo": "enforce", "faithful": "enforce", "clean": "enforce", "other": "enforce"}
		}, Expect{}, "mode for \"dup\""},
		{"no gate", func(f *fixture) { delete(f.nodes["judge"], "gate") }, Expect{}, "names no gate"},
		{"gate that is not a question", func(f *fixture) { f.nodes["judge"]["gate"] = "nope" }, Expect{}, "not one of the questions"},
		{"no questions", func(f *fixture) {
			f.questions = `{}`
			f.nodes["judge"]["questions"] = Digest([]byte(f.questions))
		}, Expect{}, "names no questions"},
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
			files[name] = &fstest.MapFile{Data: []byte(`{"kind":"sink","label":"sink","program":"x"}`)}
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
