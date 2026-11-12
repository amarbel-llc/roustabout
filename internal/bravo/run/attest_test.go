package run

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	"code.linenisgreat.com/roustabout/internal/0/graph/graphtest"
	"code.linenisgreat.com/roustabout/internal/0/request"
)

// attestHarness is the juggler and the scripts, answering as a clean
// attestation run does unless a scenario says otherwise.
type attestHarness struct {
	t     *testing.T
	calls []string

	spawnState string
	// failScript names a script label that exits 1.
	failScript string
	resolveRC  int

	spawns    []SpawnRequest
	briefs    []string
	tasks     []string
	scripts   []ScriptSpec
	tokenMode os.FileMode
	resolves  []ResolveRequest
	onSpawn   func()
}

func newAttestHarness(t *testing.T) *attestHarness {
	return &attestHarness{t: t, spawnState: "succeeded"}
}

func (h *attestHarness) NewRun(context.Context, NewRunRequest) NewRunResponse {
	h.calls = append(h.calls, "new-run")
	return NewRunResponse{RunJob: "job-run-1", Room: "attest-x@runs.example.invalid", RootStanza: "chat-1", Root: Identity{Principal: "root-1"}}
}

func (h *attestHarness) SpawnAgent(_ context.Context, r SpawnRequest) SpawnResponse {
	h.calls = append(h.calls, "agent")
	h.spawns = append(h.spawns, r)
	b, _ := os.ReadFile(r.BriefFile)
	h.briefs = append(h.briefs, string(b))
	task, _ := os.ReadFile(r.TaskFile)
	h.tasks = append(h.tasks, string(task))
	if h.onSpawn != nil {
		h.onSpawn()
	}
	return SpawnResponse{State: h.spawnState}
}

func (h *attestHarness) Decide(context.Context, DecideRequest) DecideResponse {
	h.t.Fatal("an attestation run asked a judge")
	return DecideResponse{}
}

func (h *attestHarness) Resolve(_ context.Context, r ResolveRequest) ResolveResponse {
	h.calls = append(h.calls, "resolve")
	h.resolves = append(h.resolves, r)
	torn := true
	return ResolveResponse{ExitCode: h.resolveRC, TornDown: &torn}
}

func (h *attestHarness) RunScript(_ context.Context, s ScriptSpec) ScriptResult {
	h.calls = append(h.calls, s.Label)
	h.scripts = append(h.scripts, s)
	if s.Label == "commit" && len(s.Args) > 1 {
		if st, err := os.Stat(s.Args[1]); err == nil {
			h.tokenMode = st.Mode().Perm()
		}
	}
	rc := 0
	if s.Label == h.failScript {
		rc = 1
	}
	return ScriptResult{JobID: "job-" + s.Label + "-1", ExitCode: rc}
}

func (h *attestHarness) runner(g *graph.AttestGraph) (*AttestRunner, *bytes.Buffer) {
	log := &bytes.Buffer{}
	return &AttestRunner{
		Graph: g, Version: "9.9", Commit: "abc1234",
		Base: Runner{
			Config: Config{
				RoomDomain: "runs.example.invalid", OperatorJID: "operator@xmpp.example.invalid",
				NewRunTimeout: time.Second, AgentTimeout: 150 * time.Second, AgentBackstop: 320 * time.Second,
				CommitTimeout: 120 * time.Second, SinkTimeout: time.Second, ResolveTimeout: time.Second,
				SignalResolveTimeout: time.Second, ResolveAttempts: 3, TmpDirs: []string{h.t.TempDir()},
			},
			Ports: Ports{Juggler: h, Scripts: h},
			Sleep: func(time.Duration) {},
			Log:   log,
		},
	}, log
}

func attestInput(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"session_key": "roustabout/mild-yew", "worktree": "/work/roustabout/.worktrees/mild-yew",
		"merge_base": "5e25791", "head": "abf3213", "principal": "principal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func summaryOf(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAttestRunPassesAndResolvesWithTheLedgerInTheRecord(t *testing.T) {
	h := newAttestHarness(t)
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	out, code := r.Run(context.Background(), attestInput(t))
	if code != 0 {
		t.Fatalf("exit %d: %v", code, out)
	}
	if got := strings.Join(h.calls, ","); got != "new-run,agent,agent,check,commit,resolve" {
		t.Fatalf("calls %s", got)
	}
	s := summaryOf(t, out)
	if s["outcome"] != "success" || s["state"] != "passed" || s["resolved"] != true ||
		s["roustabout_version"] != "9.9" || s["roustabout_commit"] != "abc1234" {
		t.Fatalf("summary %v", s)
	}
	var rec map[string]any
	if err := json.Unmarshal(h.resolves[0].Record, &rec); err != nil {
		t.Fatal(err)
	}
	if h.resolves[0].State != "succeeded" || rec["state"] != "passed" || rec["graph_digest"] == "" || len(rec["ledger"].([]any)) != 4 {
		t.Fatalf("resolve %s %v", h.resolves[0].State, rec)
	}
	if _, ok := rec["reason"]; ok {
		t.Fatalf("a passed record carries a reason: %v", rec)
	}
}

func TestAttestRunGivesEachAgentItsOwnBriefAndTheDiff(t *testing.T) {
	h := newAttestHarness(t)
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	if _, code := r.Run(context.Background(), attestInput(t)); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(h.briefs) != 2 || h.briefs[0] == h.briefs[1] || !strings.Contains(h.briefs[0], graphtest.AttestBrief) {
		t.Fatalf("briefs %q", h.briefs)
	}
	if h.spawns[0].MoxinPath != graphtest.AttestMoxin || h.spawns[0].AsRoot.Principal != "root-1" {
		t.Fatalf("spawn %+v", h.spawns[0])
	}
	for _, want := range []string{"/work/roustabout/.worktrees/mild-yew", "5e25791", "abf3213", "never write"} {
		if !strings.Contains(h.tasks[0], want) {
			t.Fatalf("task %q does not mention %q", h.tasks[0], want)
		}
	}
}

func TestAttestRunConfersTheTokenOnTheCommitAlone(t *testing.T) {
	h := newAttestHarness(t)
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	if _, code := r.Run(context.Background(), attestInput(t)); code != 0 {
		t.Fatal(code)
	}
	if h.tokenMode != 0o600 {
		t.Fatalf("token mode %v", h.tokenMode)
	}
	for _, s := range h.scripts {
		if s.Label == "check" && len(s.Args) != 1 {
			t.Fatalf("the check was given %v", s.Args)
		}
	}
}

func TestAttestRunAFailingCheckFailsTheRunAndRunsTheSink(t *testing.T) {
	h := newAttestHarness(t)
	h.failScript = "check"
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	out, code := r.Run(context.Background(), attestInput(t))
	if code != 0 {
		t.Fatalf("exit %d: a resolved failed run exits 0", code)
	}
	if got := strings.Join(h.calls, ","); got != "new-run,agent,agent,check,sink,resolve" {
		t.Fatalf("calls %s", got)
	}
	s := summaryOf(t, out)
	if s["outcome"] != "failure" || s["state"] != "failed" || !strings.Contains(s["reason"].(string), "check node exited 1") {
		t.Fatalf("summary %v", s)
	}
	if h.resolves[0].State != "failed" {
		t.Fatalf("resolve state %s", h.resolves[0].State)
	}
}

func TestAttestRunAnAgentThatCannotCompleteStopsTheWalk(t *testing.T) {
	h := newAttestHarness(t)
	h.spawnState = "failed"
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	out, _ := r.Run(context.Background(), attestInput(t))
	if got := strings.Join(h.calls, ","); got != "new-run,agent,sink,resolve" {
		t.Fatalf("calls %s", got)
	}
	if s := summaryOf(t, out); s["state"] != "failed" {
		t.Fatalf("summary %v", s)
	}
}

func TestAttestRunUnresolvedExitsOne(t *testing.T) {
	h := newAttestHarness(t)
	h.resolveRC = 1
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	out, code := r.Run(context.Background(), attestInput(t))
	if code != 1 || summaryOf(t, out)["outcome"] != "unresolved" || len(h.resolves) != 3 {
		t.Fatalf("exit %d, %d resolves, %v", code, len(h.resolves), out)
	}
}

func TestAttestRunRefusesABadRequestAndAGraphError(t *testing.T) {
	h := newAttestHarness(t)
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	if _, code := r.Run(context.Background(), []byte(`{"text":"x"}`)); code != 2 {
		t.Fatalf("exit %d", code)
	}
	r.GraphErr = os.ErrNotExist
	if _, code := r.Run(context.Background(), attestInput(t)); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if len(h.calls) != 0 {
		t.Fatalf("calls %v", h.calls)
	}
}

func TestAttestRunKeyFollowsTheRequestIDElseTheDiff(t *testing.T) {
	a := &request.Attest{SessionKey: "r/s", MergeBase: "aaaaaaa", Head: "bbbbbbb"}
	same := &request.Attest{SessionKey: "r/s", MergeBase: "aaaaaaa", Head: "bbbbbbb"}
	other := &request.Attest{SessionKey: "r/s", MergeBase: "aaaaaaa", Head: "ccccccc"}
	if AttestRunKey(a) != AttestRunKey(same) || AttestRunKey(a) == AttestRunKey(other) {
		t.Fatal("the key does not follow the diff")
	}
	withID := &request.Attest{SessionKey: "x/y", MergeBase: "ddddddd", Head: "eeeeeee", RequestID: "req-1"}
	if AttestRunKey(withID) != AttestRunKey(&request.Attest{RequestID: "req-1"}) {
		t.Fatal("a request_id does not decide the key")
	}
}

func TestAttestRunStoppedBySignalResolvesOnce(t *testing.T) {
	h := newAttestHarness(t)
	r, _ := h.runner(graphtest.LoadAttest(t, graphtest.AttestOptions{}))
	ctx, cancel := context.WithCancelCause(context.Background())
	h.onSpawn = func() { cancel(SignalCause("TERM")) }
	_, code := r.Run(ctx, attestInput(t))
	if code != 143 || len(h.resolves) != 1 || h.resolves[0].State != "failed" {
		t.Fatalf("exit %d, resolves %v", code, h.resolves)
	}
	if !strings.Contains(string(h.resolves[0].Record), "interrupted by SIGTERM") {
		t.Fatalf("record %s", h.resolves[0].Record)
	}
}
