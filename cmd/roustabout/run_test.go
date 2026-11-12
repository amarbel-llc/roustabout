package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	"code.linenisgreat.com/roustabout/internal/0/graph/graphtest"
	"code.linenisgreat.com/roustabout/internal/bravo/run"
)

// okPorts is every port answering as a clean run does.
type okPorts struct{ cancelOnSpawn func() }

func (okPorts) NewRun(context.Context, run.NewRunRequest) run.NewRunResponse {
	return run.NewRunResponse{RunJob: "job-run-1", Room: "r@runs.example.invalid", RootStanza: "chat-1"}
}

func (p okPorts) SpawnAgent(context.Context, run.SpawnRequest) run.SpawnResponse {
	if p.cancelOnSpawn != nil {
		p.cancelOnSpawn()
	}
	// The result is an attestation agent's findings; an issue-filer drafter's
	// is not read.
	return run.SpawnResponse{State: "succeeded", Result: json.RawMessage(`{"findings":[]}`), HasResult: true}
}

func (okPorts) Decide(context.Context, run.DecideRequest) run.DecideResponse {
	answers := map[string]map[string]any{}
	for _, q := range graphtest.QuestionNames {
		answers[q] = map[string]any{"choice": "ok", "confidence": 0.9}
	}
	b, _ := json.Marshal(answers)
	return run.DecideResponse{Answers: b, HasAnswersObject: true}
}

func (okPorts) Resolve(context.Context, run.ResolveRequest) run.ResolveResponse {
	return run.ResolveResponse{}
}

func (okPorts) ListRepos(context.Context, run.ListReposRequest) run.ListReposResponse {
	return run.ListReposResponse{OK: true, Complete: true, Repos: []run.Repo{{Name: "linenisgreat/moxy"}}}
}

func (okPorts) WriteFile(context.Context, string, time.Duration) run.MadderResponse {
	return run.MadderResponse{Blob: "madder://blobs/fake"}
}

func (okPorts) DM(context.Context, run.WakeRequest) run.WakeResponse { return run.WakeResponse{} }

func (okPorts) RunScript(context.Context, run.ScriptSpec) run.ScriptResult {
	return run.ScriptResult{JobID: "job-1", Stdout: []byte(`{"state":"filed","issues":[{"uri":"smith://f/o/r/issues/1"}],"not_filed":[]}`)}
}

func fakePorts(p okPorts) func(runOptions, []string, io.Writer) run.Ports {
	return func(runOptions, []string, io.Writer) run.Ports {
		return run.Ports{Juggler: p, Repos: p, Madder: p, Troupe: p, Scripts: p}
	}
}

func flags(graphDir string, extra ...string) []string {
	return append(append([]string{
		"--issues-moxin", graphtest.Moxin, "--room-domain", "runs.example.invalid",
		"--operator-jid", "operator@xmpp.example.invalid", "--resolve-retry-pause", "0s",
	}, extra...), graphDir)
}

func runVerb(t *testing.T, args []string, stdin string, p okPorts, sigs <-chan os.Signal) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), env{
		environ: []string{"TMPDIR=" + t.TempDir()}, stdin: strings.NewReader(stdin), stdout: &stdout, stderr: &stderr,
		ports: fakePorts(p), signals: sigs,
	}, args)
	return code, stdout.String(), stderr.String()
}

func TestRunVerbRunsTheGraphDocument(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	code, stdout, stderr := runVerb(t, flags(dir, "--commit-program", graphtest.CommitProgram), `{"text":"file an issue"}`, okPorts{}, nil)
	if code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	var s map[string]any
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("stdout is not one JSON object: %q", stdout)
	}
	if s["outcome"] != "success" || s["state"] != "filed" || s["resolved"] != true {
		t.Fatalf("summary %v", s)
	}
	g := graphtest.New(t, graphtest.Options{})
	if s["graph_digest"] != g.Digest {
		t.Fatalf("graph digest %v, want %s", s["graph_digest"], g.Digest)
	}
	if s["roustabout_version"] != version || s["roustabout_commit"] != commit {
		t.Fatalf("runtime %v+%v, want %s+%s", s["roustabout_version"], s["roustabout_commit"], version, commit)
	}
}

func TestRunVerbRefusesAGraphOfAnotherShape(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	if err := os.WriteFile(dir+"/graph.digest", []byte("sha256:"+strings.Repeat("0", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runVerb(t, flags(dir), `{"text":"x"}`, okPorts{}, nil)
	if code != 2 || !strings.Contains(stdout, `"outcome":"refused"`) || !strings.Contains(stdout, "not the shape roustabout runs") {
		t.Fatalf("exit %d stdout %s", code, stdout)
	}
}

func attestFlags(t *testing.T, dir string) []string {
	t.Helper()
	file := dir + "/grammar.json"
	if err := os.WriteFile(file, []byte(graphtest.AttestGrammar), 0o600); err != nil {
		t.Fatal(err)
	}
	return flags(dir, "--commit-program", graphtest.CommitProgram, "--grammar", file, "--grammar-digest", graph.Digest([]byte(graphtest.AttestGrammar)))
}

const attestRequest = `{"session_key":"roustabout/mild-yew","worktree":"/work/wt","merge_base":"5e25791","head":"abf3213","principal":"p-1"}`

// A grammar whose request type is attest/1 sends the run to the attestation
// runner: an attestation graph runs, on an attestation request.
func TestRunVerbRunsAnAttestationGraphUnderAnAttestGrammar(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteAttestDir(t, dir, graphtest.AttestOptions{})
	code, stdout, stderr := runVerb(t, attestFlags(t, dir), attestRequest, okPorts{}, nil)
	var s map[string]any
	if err := json.Unmarshal([]byte(stdout), &s); err != nil || code != 0 {
		t.Fatalf("exit %d stdout %q stderr %s", code, stdout, stderr)
	}
	g := graphtest.LoadAttest(t, graphtest.AttestOptions{})
	if s["outcome"] != "success" || s["state"] != "passed" || s["graph_digest"] != g.Digest ||
		s["roustabout_version"] != version || s["roustabout_commit"] != commit {
		t.Fatalf("summary %v", s)
	}
}

// An issue-filer graph under an attest/1 grammar is refused, not run on a
// request of the wrong shape; and an issue-filer request is refused by the
// attestation runner.
func TestRunVerbRefusesAnIssueFilerGraphUnderAnAttestGrammar(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	code, stdout, _ := runVerb(t, attestFlags(t, dir), attestRequest, okPorts{}, nil)
	if code != 2 || !strings.Contains(stdout, `"outcome":"refused"`) {
		t.Fatalf("exit %d stdout %s", code, stdout)
	}
}

func TestRunVerbAttestGrammarRefusesAnIssueFilerRequest(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteAttestDir(t, dir, graphtest.AttestOptions{})
	code, stdout, _ := runVerb(t, attestFlags(t, dir), `{"text":"file an issue"}`, okPorts{}, nil)
	if code != 2 || !strings.Contains(stdout, "session_key is required") {
		t.Fatalf("exit %d stdout %s", code, stdout)
	}
}

func TestRunVerbRefusesABadRequest(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	code, stdout, _ := runVerb(t, flags(dir), `{"request_id":"x"}`, okPorts{}, nil)
	if code != 2 || !strings.Contains(stdout, `"outcome":"refused"`) {
		t.Fatalf("exit %d stdout %s", code, stdout)
	}
}

func TestRunVerbUsageErrorsExit2(t *testing.T) {
	for name, args := range map[string][]string{
		"no graph dir":    {"--issues-moxin", "m", "--room-domain", "d", "--operator-jid", "o@d"},
		"no issues moxin": {"--room-domain", "d", "--operator-jid", "o@d", "dir"},
		"no room domain":  {"--issues-moxin", "m", "--operator-jid", "o@d", "dir"},
		"no operator":     {"--issues-moxin", "m", "--room-domain", "d", "dir"},
		"unknown flag":    {"--nope", "dir"},
		"two graph dirs":  {"--issues-moxin", "m", "--room-domain", "d", "--operator-jid", "o@d", "a", "b"},
		"a bad duration":  {"--agent-timeout", "soon", "dir"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runVerb(t, args, `{"text":"x"}`, okPorts{}, nil)
			if code != 2 || stdout != "" || stderr == "" {
				t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
			}
		})
	}
}

func TestRunVerbReportsASignalAsItsExitStatus(t *testing.T) {
	dir := t.TempDir()
	graphtest.WriteDir(t, dir, graphtest.Options{})
	sigs := make(chan os.Signal, 1)
	// The signal arrives while the drafter runs; the goroutine delivering it
	// to the run's context is given a moment to do so.
	p := okPorts{cancelOnSpawn: func() {
		sigs <- syscall.SIGTERM
		time.Sleep(200 * time.Millisecond)
	}}
	code, stdout, _ := runVerb(t, flags(dir), `{"text":"x"}`, p, sigs)
	if code != 143 || !strings.Contains(stdout, `"outcome":"interrupted"`) || !strings.Contains(stdout, `"signal":"TERM"`) {
		t.Fatalf("exit %d stdout %s", code, stdout)
	}
}

func TestRunIsRegistered(t *testing.T) {
	if _, ok := buildApp().GetCommand("run"); !ok {
		t.Fatal("run command not registered")
	}
}
