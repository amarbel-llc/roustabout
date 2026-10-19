// Package graphtest builds issue-filer graph documents in memory for tests of
// code that consumes a validated graph.
package graphtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"code.linenisgreat.com/roustabout/internal/graph"
)

const (
	Moxin         = "/nix/store/test-issues-moxin"
	CommitProgram = "/nix/store/test-commit/bin/issues-commit"
	SinkProgram   = "/nix/store/test-sink"
	BriefTemplate = "# the drafter's brief\n@@REPO_LIST@@\nISSUES_TRAFFIC_FILE = \"@@TRAFFIC_FILE@@\"\n"
	Questions     = `{"clean":{},"complete":{},"dup":{},"faithful":{},"repo":{},"request":{}}`
)

// QuestionNames are Questions' keys, sorted.
var QuestionNames = []string{"clean", "complete", "dup", "faithful", "repo", "request"}

// Options vary the document; the zero value is the default all-enforce graph
// at threshold 0.5 whose sink runs SinkProgram.
type Options struct {
	Threshold *float64
	Modes     map[string]string
	// QuestionsJSON replaces Questions as the judge's questions file.
	QuestionsJSON string
	// Gate replaces the default gate, "request". NoGate leaves it out.
	Gate   string
	NoGate bool
	// NoSink makes the sink's program "none".
	NoSink bool
}

// New builds and loads a graph document.
func New(t testing.TB, o Options) *graph.Graph {
	t.Helper()
	g, err := graph.Load(Files(t, o), graph.Expect{Moxin: Moxin, CommitProgram: CommitProgram})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// WriteDir writes the document to dir, as circus issue-filer-graph lays it out.
func WriteDir(t testing.TB, dir string, o Options) {
	t.Helper()
	files := Files(t, o)
	for name, f := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Files builds the document's files.
func Files(t testing.TB, o Options) fstest.MapFS {
	t.Helper()
	threshold := 0.5
	if o.Threshold != nil {
		threshold = *o.Threshold
	}
	sink := SinkProgram
	if o.NoSink {
		sink = graph.NoSinkProgram
	}
	questions := Questions
	if o.QuestionsJSON != "" {
		questions = o.QuestionsJSON
	}
	judge := map[string]any{"kind": "decide", "label": "judge", "questions": graph.Digest([]byte(questions)), "min_confidence": threshold}
	if !o.NoGate {
		judge["gate"] = "request"
		if o.Gate != "" {
			judge["gate"] = o.Gate
		}
	}
	if o.Modes != nil {
		judge["modes"] = o.Modes
	}
	nodes := map[string]map[string]any{
		"draft":  {"kind": "agent", "label": "draft", "brief": graph.Digest([]byte(BriefTemplate)), "moxin": Moxin},
		"judge":  judge,
		"commit": {"kind": "commit", "label": "commit", "program": CommitProgram},
		"sink":   {"kind": "sink", "label": "sink", "program": sink},
	}
	files := fstest.MapFS{}
	ids := map[string]string{}
	byID := map[string]any{}
	for label, n := range nodes {
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		ids[label] = graph.Digest(b)
		byID[ids[label]] = n
		files["nodes/"+ids[label]+".json"] = &fstest.MapFile{Data: b}
	}
	e := func(a, c, b string) [3]string { return [3]string{ids[a], c, ids[b]} }
	raw, err := json.Marshal(map[string]any{
		"schema": 1, "review": "all-or-nothing", "nodes": byID,
		"edges": [][3]string{e("draft", "ok", "judge"), e("judge", "ok", "commit"), e("draft", "fail", "sink"), e("judge", "fail", "sink"), e("commit", "fail", "sink")},
	})
	if err != nil {
		t.Fatal(err)
	}
	files["graph.json"] = &fstest.MapFile{Data: raw}
	files["graph.digest"] = &fstest.MapFile{Data: []byte(graph.Digest(raw) + "\n")}
	files["questions.json"] = &fstest.MapFile{Data: []byte(questions)}
	files["drafter.brief.toml"] = &fstest.MapFile{Data: []byte(BriefTemplate)}
	return files
}
