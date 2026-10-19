package run

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/roustabout/internal/graph"
	"code.linenisgreat.com/roustabout/internal/graph/graphtest"
	rr "code.linenisgreat.com/roustabout/internal/runrecord"
)

const (
	listingLine = `{"call":"list_repos","result":{"repos":[{"repo":"linenisgreat/moxy"},{"repo":"amarbel-llc/moxy","mirror":true,"tracked_at":"linenisgreat/moxy"},{"repo":"friedenberg/dotfiles","private":true}]}}`
	searchLine  = `{"call":"list_issues","repo":"linenisgreat/moxy","query":"restart","state":"all","results":[]}`
	draftLine   = `{"call":"create_issue","repo":"linenisgreat/moxy","draft":{"repo":"linenisgreat/moxy","title":"Restart should reconnect","body":"the restart tool"},"repo_listing":"linenisgreat/moxy","validation":{"pii_scan":"clean"}}`
	plainText   = "file an issue on moxy that the restart tool should reconnect automatically"
	filedStdout = `{"state":"filed","issues":[{"number":50,"uri":"smith://forge.example.invalid/linenisgreat/moxy/issues/50"}],"not_filed":[]}`
)

// harness is every port at once; each scenario sets what the stand-ins do.
type harness struct {
	t     *testing.T
	calls []string

	newRun  NewRunResponse
	repos   ListReposResponse
	drafts  []string
	spawn   SpawnResponse
	decide  func(DecideRequest) DecideResponse
	madder  MadderResponse
	troupe  WakeResponse
	resolve []int // exit codes by call, then the last repeats

	commitRC     int
	commitStdout string
	sinkRC       int

	// what the stand-ins saw
	briefText  string
	taskText   string
	decideReq  DecideRequest
	madderBody []byte
	commitBody []byte
	commitSpec ScriptSpec
	tokenMode  os.FileMode
	tokenBody  []byte
	sinkStdin  []byte
	wakeReq    WakeRequest
	resolves   []ResolveRequest
	onSpawn    func(ctx context.Context)
	onDecide   func(frozen string)
	frozenSeen string
	trafficAt  string
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t}
	h.newRun = NewRunResponse{
		RunJob: "job-run-1", Room: "issue-filer-x@runs.example.invalid", RootStanza: "chat-request-1",
		Root: Identity{Principal: "root-uuid-1", JID: "root-uuid-1@xmpp.example.invalid", CredentialRef: "/fake/root.pass"},
	}
	h.repos = ListReposResponse{OK: true, Complete: true, Repos: []Repo{
		{Name: "linenisgreat/moxy"}, {Name: "amarbel-llc/moxy", Mirror: true, TrackedAt: "linenisgreat/moxy"}, {Name: "friedenberg/dotfiles", Private: true},
	}}
	h.drafts = []string{searchLine, draftLine}
	h.spawn = SpawnResponse{State: "succeeded"}
	h.decide = func(DecideRequest) DecideResponse { return answers(nil) }
	h.madder = MadderResponse{Blob: "madder://blobs/fake-1"}
	h.commitStdout = filedStdout
	return h
}

func answers(over map[string]string) DecideResponse {
	obj := map[string]json.RawMessage{}
	for _, q := range graphtest.QuestionNames {
		obj[q] = json.RawMessage(`{"choice":"ok","confidence":0.9}`)
	}
	for q, v := range over {
		obj[q] = json.RawMessage(v)
	}
	b, _ := json.Marshal(obj)
	return DecideResponse{Answers: b, HasAnswersObject: true}
}

func (h *harness) call(name string) { h.calls = append(h.calls, name) }

func (h *harness) NewRun(_ context.Context, _ NewRunRequest) NewRunResponse {
	h.call("juggler new-run")
	return h.newRun
}

func (h *harness) ListRepos(_ context.Context, r ListReposRequest) ListReposResponse {
	h.call("list_repos")
	h.trafficAt = r.TrafficFile
	if h.repos.OK {
		appendLine(h.t, r.TrafficFile, listingLine)
	}
	return h.repos
}

func (h *harness) SpawnAgent(ctx context.Context, r SpawnRequest) SpawnResponse {
	h.call("juggler spawn-agent")
	b, _ := os.ReadFile(r.BriefFile)
	h.briefText = string(b)
	task, _ := os.ReadFile(r.TaskFile)
	h.taskText = string(task)
	for _, l := range h.drafts {
		appendLine(h.t, h.trafficAt, l)
	}
	if h.onSpawn != nil {
		h.onSpawn(ctx)
	}
	return h.spawn
}

func (h *harness) Decide(_ context.Context, r DecideRequest) DecideResponse {
	h.call("juggler decide")
	h.decideReq = r
	if h.onDecide != nil {
		h.onDecide(h.frozenSeen)
	}
	return h.decide(r)
}

func (h *harness) Resolve(_ context.Context, r ResolveRequest) ResolveResponse {
	h.call("juggler resolve")
	h.resolves = append(h.resolves, r)
	rc := 0
	if n := len(h.resolves); len(h.resolve) > 0 {
		rc = h.resolve[min(n, len(h.resolve))-1]
	}
	tornDown := true
	return ResolveResponse{ExitCode: rc, Stderr: "", TornDown: &tornDown}
}

func (h *harness) WriteFile(_ context.Context, path string, _ time.Duration) MadderResponse {
	h.call("madder")
	h.frozenSeen = path
	h.madderBody, _ = os.ReadFile(path)
	return h.madder
}

func (h *harness) DM(_ context.Context, r WakeRequest) WakeResponse {
	h.call("troupe")
	h.wakeReq = r
	return h.troupe
}

func (h *harness) RunScript(_ context.Context, s ScriptSpec) ScriptResult {
	switch s.Label {
	case "commit":
		h.call("commit")
		h.commitSpec = s
		h.commitBody, _ = os.ReadFile(s.Args[0])
		if st, err := os.Stat(s.Args[1]); err == nil {
			h.tokenMode = st.Mode().Perm()
		}
		h.tokenBody, _ = os.ReadFile(s.Args[1])
		return ScriptResult{JobID: "job-commit-1", ExitCode: h.commitRC, Stdout: []byte(h.commitStdout)}
	default:
		h.call("sink")
		h.sinkStdin = s.Stdin
		return ScriptResult{JobID: "job-sink-1", ExitCode: h.sinkRC}
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) runner(g *graph.Graph) (*Runner, *bytes.Buffer) {
	log := &bytes.Buffer{}
	return &Runner{
		Graph: g,
		Config: Config{
			JudgeModel: "jev", RoomDomain: "runs.example.invalid", OperatorJID: "operator@xmpp.example.invalid",
			IssuesMoxin: graphtest.Moxin, WakeUser: "issue-filer-wake",
			NewRunTimeout: time.Second, ListReposTimeout: time.Second, AgentTimeout: 150 * time.Second, AgentBackstop: 320 * time.Second,
			MadderTimeout: time.Second, JudgeTimeout: time.Second, CommitTimeout: 120 * time.Second, SinkTimeout: time.Second,
			ResolveTimeout: time.Second, SignalResolveTimeout: time.Second, ResolveAttempts: 3,
			TmpDirs: []string{h.t.TempDir()},
		},
		Ports: Ports{Juggler: h, Repos: h, Madder: h, Troupe: h, Scripts: h},
		Now:   func() time.Time { return time.UnixMilli(1760313600123) },
		Sleep: func(time.Duration) {},
		Log:   log,
	}, log
}

func (h *harness) sequence() string { return strings.Join(h.calls, ",") }

func input(text string) []byte { b, _ := json.Marshal(map[string]string{"text": text}); return b }

func summary(t *testing.T, v any) rr.Summary {
	t.Helper()
	s, ok := v.(rr.Summary)
	if !ok {
		t.Fatalf("summary is %T: %+v", v, v)
	}
	return s
}

func recordOf(t *testing.T, b []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("record %s: %v", b, err)
	}
	return m
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

// scenario a: the happy path.
func TestHappyPath(t *testing.T) {
	h := newHarness(t)
	g := graphtest.New(t, graphtest.Options{})
	r, _ := h.runner(g)
	sum, exit := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "exit", exit, 0)
	eq(t, "outcome", s.Outcome, rr.OutcomeSuccess)
	eq(t, "state", *s.State, rr.StateFiled)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,juggler decide,commit,juggler resolve")
	eq(t, "resolved", *s.Resolved, true)
	eq(t, "torn down", *s.TornDown, true)
	if s.Why != nil {
		t.Fatalf("why %v", *s.Why)
	}
	if !strings.HasPrefix(s.RunKey, "issue-filer-1760313600123-") {
		t.Fatalf("run key %s", s.RunKey)
	}
	eq(t, "min_confidence", *s.MinConfidence, 0.5)
	eq(t, "graph digest", s.GraphDigest, g.Digest)

	// What was stored is what was judged and committed.
	if !bytes.Equal(h.madderBody, h.commitBody) || len(h.madderBody) == 0 {
		t.Fatal("madder and commit saw different traffic")
	}
	eq(t, "traffic blob", *s.TrafficBlob, "madder://blobs/fake-1")
	if lines := strings.Count(string(h.madderBody), "\n"); lines != 3 {
		t.Fatalf("frozen traffic has %d lines", lines)
	}
	if !strings.HasSuffix(h.commitSpec.Args[0], "traffic.frozen.jsonl") {
		t.Fatalf("commit args %v", h.commitSpec.Args)
	}

	// The token: a 0600 file, in no argument and no environment, gone after.
	eq(t, "token mode", h.tokenMode, os.FileMode(0o600))
	if len(h.tokenBody) == 0 {
		t.Fatal("no token")
	}
	for _, a := range append(append([]string{}, h.commitSpec.Args...), h.commitSpec.Env...) {
		if strings.Contains(a, string(h.tokenBody)) {
			t.Fatalf("token leaked into %q", a)
		}
	}
	if _, err := os.Stat(h.commitSpec.Args[1]); !os.IsNotExist(err) {
		t.Fatal("token file should be removed after the commit")
	}
	eq(t, "labels", h.commitSpec.Env[1], "ISSUES_LABELS=[]")
	// circus#314: the commit node gets what its provenance footer says.
	eq(t, "run key for the footer", h.commitSpec.Env[2], "ISSUES_RUN_KEY="+s.RunKey)
	eq(t, "graph digest for the footer", h.commitSpec.Env[3], "ISSUES_GRAPH_DIGEST="+g.Digest)

	// The brief names the listing and the traffic file; the mirror says so.
	for _, want := range []string{
		"linenisgreat/moxy\n", "amarbel-llc/moxy (mirror, do not file here; use linenisgreat/moxy)\n",
		"friedenberg/dotfiles (private)\n", `ISSUES_TRAFFIC_FILE = "` + h.trafficAt + `"`,
	} {
		if !strings.Contains(h.briefText, want) {
			t.Fatalf("brief lacks %q:\n%s", want, h.briefText)
		}
	}
	eq(t, "task", h.taskText, plainText)

	// The judge was given the six questions and the state built from the traffic.
	var payload struct {
		State     map[string]json.RawMessage `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(h.decideReq.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	eq(t, "question count", len(payload.Questions), 6)
	for _, k := range []string{"request", "drafts", "checks", "validation"} {
		if _, ok := payload.State[k]; !ok {
			t.Fatalf("state lacks %s", k)
		}
	}
	eq(t, "decide parent", h.decideReq.Parent, "chat-request-1")
	eq(t, "decide room", h.decideReq.Room, "issue-filer-x@runs.example.invalid")

	// Resolve: succeeded, the record as the reason, the filed uri as an artifact.
	eq(t, "resolve state", h.resolves[0].State, "succeeded")
	rec := recordOf(t, h.resolves[0].Record)
	eq(t, "record state", string(rec["state"]), `"filed"`)
	if _, has := rec["reason"]; has {
		t.Fatal("a filed record has no reason")
	}
	eq(t, "artifacts", string(h.resolves[0].Artifacts),
		`[{"tool":"issues_create_issue","kind":"issue","uris":["smith://forge.example.invalid/linenisgreat/moxy/issues/50"]}]`)

	// The run-level ledger names each node by digest, job and edge.
	eq(t, "ledger nodes", len(s.Ledger), 3)
	eq(t, "commit entry", s.Ledger[2], rr.LedgerEntry{Node: g.CommitID, Label: "commit", Kind: "script", Job: "job-commit-1", Verdict: "filed", Edge: "ok"})

	// The scratch directory (and the token in it) is gone.
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(h.trafficAt), "*"))
	if len(left) != 0 {
		t.Fatalf("scratch left behind: %v", left)
	}
}

// scenario b: the drafter cannot complete.
func TestDrafterCannotComplete(t *testing.T) {
	h := newHarness(t)
	h.spawn = SpawnResponse{State: "succeeded", CannotCompleteIsObject: true, CannotCompleteReason: "no confident repository match"}
	h.drafts = nil
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "exit", exit, 0)
	eq(t, "outcome", s.Outcome, rr.OutcomeFailure)
	eq(t, "state", *s.State, rr.StateFailed)
	eq(t, "why", *s.Why, rr.WhyDrafterFailed)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,sink,juggler resolve")
	if !strings.Contains(*s.Reason, "no confident repository match") {
		t.Fatalf("reason %q", *s.Reason)
	}
	eq(t, "resolve state", h.resolves[0].State, "failed")
	eq(t, "artifacts", string(h.resolves[0].Artifacts), "[]")
	rec := recordOf(t, h.sinkStdin)
	eq(t, "sink got the record", string(rec["state"]), `"failed"`)
	eq(t, "sink rc", *s.SinkRC, 0)
}

// scenario c: Jev finds a duplicate.
func TestJudgeRejects(t *testing.T) {
	h := newHarness(t)
	h.decide = func(DecideRequest) DecideResponse {
		return answers(map[string]string{"dup": `{"choice":"problem","confidence":0.8}`})
	}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "exit", exit, 0)
	eq(t, "state", *s.State, rr.StateRejected)
	eq(t, "failed checks", s.FailedChecks, []string{"dup"})
	eq(t, "why", *s.Why, rr.WhyJudgeRejected)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,juggler decide,sink,juggler resolve")
	eq(t, "resolve state", h.resolves[0].State, "failed")
	rec := recordOf(t, h.resolves[0].Record)
	eq(t, "record state", string(rec["state"]), `"rejected"`)
	eq(t, "record failed checks", string(rec["failed_checks"]), `["dup"]`)
}

// The judge's questions are the graph's: renamed ones are asked, named to
// juggler and judged.
func TestJudgeUsesTheGraphsQuestionNames(t *testing.T) {
	h := newHarness(t)
	h.decide = func(DecideRequest) DecideResponse {
		return DecideResponse{HasAnswersObject: true, Answers: json.RawMessage(
			`{"is-clean":{"choice":"ok","confidence":0.9},"voice-matches-request":{"choice":"problem","confidence":0.8}}`)}
	}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{QuestionsJSON: `{"is-clean":{},"voice-matches-request":{}}`}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "state", *s.State, rr.StateRejected)
	eq(t, "failed checks", s.FailedChecks, []string{"voice-matches-request"})
	eq(t, "gating question", h.decideReq.Question, "is-clean")
}

// scenario d2: Jev cannot be asked.
func TestJudgeFails(t *testing.T) {
	h := newHarness(t)
	h.decide = func(DecideRequest) DecideResponse { return DecideResponse{ExitCode: 2, Reason: "502 bad gateway"} }
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "state", *s.State, rr.StateFailed)
	eq(t, "why", *s.Why, rr.WhyJudgeFailed)
	if !strings.Contains(*s.Reason, "502") {
		t.Fatalf("reason %q", *s.Reason)
	}
	eq(t, "failed checks", s.FailedChecks, []string{})
}

// scenario d4: decide exits 0 with no answers at all.
func TestJudgeNoAnswers(t *testing.T) {
	h := newHarness(t)
	h.decide = func(DecideRequest) DecideResponse { return DecideResponse{} }
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "why", *s.Why, rr.WhyJudgeFailed)
	if !strings.Contains(*s.Reason, "no answers object") {
		t.Fatalf("reason %q", *s.Reason)
	}
}

// scenarios e and e2: the commit is partial, or files nothing.
func TestCommitPartial(t *testing.T) {
	h := newHarness(t)
	h.commitRC = 1
	h.commitStdout = `{"state":"partial","issues":[{"number":50,"uri":"smith://f/o/r/issues/50"}],"not_filed":[{"repo":"linenisgreat/clown","reason":"forge down"}]}`
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "state", *s.State, rr.StateFailed)
	eq(t, "why", *s.Why, rr.WhyCommitPartial)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,juggler decide,commit,sink,juggler resolve")
	eq(t, "resolve state", h.resolves[0].State, "failed")
	// What was filed is still an artifact.
	if !strings.Contains(string(h.resolves[0].Artifacts), "issues/50") {
		t.Fatalf("artifacts %s", h.resolves[0].Artifacts)
	}
	rec := recordOf(t, h.resolves[0].Record)
	if !strings.Contains(string(rec["issues"]), `"number":50`) || !strings.Contains(string(rec["not_filed"]), "clown") {
		t.Fatalf("record %v", rec)
	}
}

func TestCommitFilesNothing(t *testing.T) {
	h := newHarness(t)
	h.commitRC = 2
	h.commitStdout = `{"state":"failed","issues":[],"not_filed":[{"repo":"linenisgreat/moxy"}]}`
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "why", *s.Why, rr.WhyCommitFailed)
	eq(t, "artifacts", string(h.resolves[0].Artifacts), "[]")
}

// scenario t2: the frozen file is changed between the judge and the commit.
func TestTrafficChangedBetweenJudgeAndCommit(t *testing.T) {
	h := newHarness(t)
	h.onDecide = func(frozen string) {
		if err := os.Chmod(frozen, 0o600); err != nil {
			t.Fatal(err)
		}
		appendLine(t, frozen, `{"call":"create_issue","draft":{"title":"smuggled"}}`)
	}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "why", *s.Why, rr.WhyTrafficChanged)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,juggler decide,sink,juggler resolve")
	eq(t, "issues", string(s.Issues), "[]")
}

// scenario h2: madder cannot store the traffic, so nothing is judged.
func TestMadderFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.madder = MadderResponse{ExitCode: 1, Stderr: "permission denied"}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "why", *s.Why, rr.WhyTrafficNotStored)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,sink,juggler resolve")
	if s.TrafficBlob != nil {
		t.Fatal("traffic_blob should be null")
	}
	if !strings.Contains(*s.Reason, "permission denied") {
		t.Fatalf("reason %q", *s.Reason)
	}
}

// scenario h: the repo listing fails, so the drafter is never started.
func TestRepoListFails(t *testing.T) {
	h := newHarness(t)
	h.repos = ListReposResponse{ExitCode: 1, Detail: "the forge did not answer"}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "why", *s.Why, rr.WhyRepoListFailed)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,madder,sink,juggler resolve")
}

func TestRepoListIncomplete(t *testing.T) {
	h := newHarness(t)
	h.repos.Unlisted = []string{"friedenberg"}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "why", *s.Why, rr.WhyRepoListIncomplete)
	if !strings.Contains(*s.Reason, "friedenberg") {
		t.Fatalf("reason %q", *s.Reason)
	}
}

// scenario i: the run cannot be started.
func TestNotStarted(t *testing.T) {
	h := newHarness(t)
	h.newRun = NewRunResponse{ExitCode: 1, Stderr: "mint failed"}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "exit", exit, 1)
	eq(t, "outcome", s.Outcome, rr.OutcomeNotStarted)
	eq(t, "why", *s.Why, rr.WhyRunNotStarted)
	eq(t, "resolved", *s.Resolved, false)
	eq(t, "calls", h.sequence(), "juggler new-run")
}

func TestPendingRunIsNotStarted(t *testing.T) {
	h := newHarness(t)
	h.newRun.Pending = true
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(context.Background(), input(plainText))
	eq(t, "why", *summary(t, sum).Why, rr.WhyRunPending)
	eq(t, "exit", exit, 1)
}

func TestNewRunWithoutJobOrRoom(t *testing.T) {
	h := newHarness(t)
	h.newRun.RunJob = ""
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(context.Background(), input(plainText))
	eq(t, "why", *summary(t, sum).Why, rr.WhyRunNotStarted)
	eq(t, "exit", exit, 1)
}

// scenarios f2 and f4: a known run key is returned as it stands.
func TestExistingRun(t *testing.T) {
	for name, resolved := range map[string]bool{"resolved": true, "running": false} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.newRun.Existing = true
			h.newRun.ExistingResolved = &resolved
			r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
			sum, exit := r.Run(context.Background(), input(plainText))
			s := summary(t, sum)
			eq(t, "exit", exit, 0)
			eq(t, "outcome", s.Outcome, rr.OutcomeExisting)
			eq(t, "existing", s.Existing, true)
			eq(t, "resolved", *s.Resolved, resolved)
			if s.State != nil {
				t.Fatal("state should be null")
			}
			eq(t, "calls", h.sequence(), "juggler new-run")
		})
	}
}

// scenario j: resolve fails every time.
func TestResolveFailsEveryTime(t *testing.T) {
	h := newHarness(t)
	h.resolve = []int{1}
	r, log := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "exit", exit, 1)
	eq(t, "outcome", s.Outcome, rr.OutcomeUnresolved)
	eq(t, "state", *s.State, rr.StateFiled)
	eq(t, "resolved", *s.Resolved, false)
	if s.TornDown != nil {
		t.Fatal("torn_down should be null")
	}
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,juggler decide,commit,juggler resolve,juggler resolve,juggler resolve,sink")
	// Each attempt carried the same record.
	if !bytes.Equal(h.resolves[0].Record, h.resolves[2].Record) {
		t.Fatal("the attempts should carry the same record")
	}
	// The sink saw the filed record, and the recovery command is logged.
	eq(t, "sink saw", string(recordOf(t, h.sinkStdin)["state"]), `"filed"`)
	if !strings.Contains(log.String(), "To finish it by hand: juggler resolve 'job-run-1' --state='succeeded' --reason='{") {
		t.Fatalf("log lacks the recovery command:\n%s", log)
	}
}

func TestResolveSucceedsOnTheSecondAttempt(t *testing.T) {
	h := newHarness(t)
	h.resolve = []int{1, 0}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(context.Background(), input(plainText))
	eq(t, "exit", exit, 0)
	eq(t, "outcome", summary(t, sum).Outcome, rr.OutcomeSuccess)
	eq(t, "attempts", len(h.resolves), 2)
}

// scenario k: the sink fails; the outcome is unchanged.
func TestSinkFails(t *testing.T) {
	h := newHarness(t)
	h.sinkRC = 3
	h.decide = func(DecideRequest) DecideResponse {
		return answers(map[string]string{"dup": `{"choice":"problem","confidence":0.8}`})
	}
	r, log := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "state", *s.State, rr.StateRejected)
	eq(t, "resolved", *s.Resolved, true)
	eq(t, "sink rc", *s.SinkRC, 3)
	if !strings.Contains(log.String(), "exited 3") {
		t.Fatalf("log %s", log)
	}
}

// scenario m1: a graph whose sink program is none.
func TestNoSinkProgram(t *testing.T) {
	h := newHarness(t)
	h.decide = func(DecideRequest) DecideResponse {
		return answers(map[string]string{"dup": `{"choice":"problem","confidence":0.8}`})
	}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{NoSink: true}))
	sum, _ := r.Run(context.Background(), input(plainText))
	s := summary(t, sum)
	eq(t, "state", *s.State, rr.StateRejected)
	if s.SinkRC != nil {
		t.Fatal("sink_rc should be null")
	}
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,juggler decide,juggler resolve")
}

// scenarios sh1 to sh4: shadow modes.
func TestShadowQuestion(t *testing.T) {
	modes := map[string]string{"request": "enforce", "complete": "enforce", "repo": "enforce", "faithful": "shadow", "clean": "enforce-public", "dup": "shadow"}
	cases := []struct {
		name       string
		over       map[string]string
		drafts     []string
		wantState  rr.State
		wantFailed []string
		wantShadow []string
	}{
		{"dup shadow files", map[string]string{"dup": `{"choice":"problem","confidence":0.8}`}, nil, rr.StateFiled, []string{}, []string{"dup"}},
		{"faithful low shadow files", map[string]string{"faithful": `{"choice":"ok","confidence":0.2}`}, nil, rr.StateFiled, []string{}, []string{"faithful"}},
		{"clean on a public repo rejects", map[string]string{"clean": `{"choice":"problem","confidence":0.9}`}, nil, rr.StateRejected, []string{"clean"}, []string{}},
		{
			"clean on a private repo is shadow",
			map[string]string{"clean": `{"choice":"problem","confidence":0.9}`},
			[]string{`{"call":"create_issue","draft":{"repo":"friedenberg/dotfiles","title":"t","body":"b"},"repo_listing":"friedenberg/dotfiles (private)"}`},
			rr.StateFiled,
			[]string{},
			[]string{"clean"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			if c.drafts != nil {
				h.drafts = c.drafts
			}
			over := c.over
			h.decide = func(DecideRequest) DecideResponse { return answers(over) }
			r, _ := h.runner(graphtest.New(t, graphtest.Options{Modes: modes}))
			sum, _ := r.Run(context.Background(), input(plainText))
			s := summary(t, sum)
			eq(t, "state", *s.State, c.wantState)
			eq(t, "failed", s.FailedChecks, c.wantFailed)
			eq(t, "shadow", s.ShadowFailedChecks, c.wantShadow)
			rec := recordOf(t, h.resolves[0].Record)
			shadow, _ := json.Marshal(c.wantShadow)
			eq(t, "record shadow", string(rec["shadow_failed_checks"]), string(shadow))
		})
	}
}

// scenarios w1 to w5: the exit wake.
func TestExitWake(t *testing.T) {
	in := []byte(`{"text":"` + plainText + `","notify":"b0cc-key@flic.xmpp.example.invalid","request_id":"req-wake-a"}`)

	t.Run("sent before resolve with the review", func(t *testing.T) {
		h := newHarness(t)
		r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
		sum, _ := r.Run(context.Background(), in)
		s := summary(t, sum)
		eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,madder,juggler decide,commit,troupe,juggler resolve")
		eq(t, "notified", *s.Notified, true)
		eq(t, "from", h.wakeReq.From, "issue-filer-wake")
		eq(t, "to", h.wakeReq.To, "b0cc-key")
		eq(t, "vhost", h.wakeReq.ToVHost, "flic.xmpp.example.invalid")
		head, body, _ := strings.Cut(h.wakeReq.Message, "\n\n")
		if !strings.HasPrefix(head, "issue-filer issue-filer-") || !strings.HasSuffix(head, " filed") {
			t.Fatalf("summary line %q", head)
		}
		var wake struct {
			Kind      string          `json:"kind"`
			RequestID string          `json:"request_id"`
			Result    json.RawMessage `json:"result"`
			Review    struct {
				Drafts    []struct{ Repo, Title, Body string } `json:"drafts"`
				Answers   map[string]json.RawMessage           `json:"answers"`
				Threshold float64                              `json:"threshold"`
			} `json:"review"`
		}
		if err := json.Unmarshal([]byte(body), &wake); err != nil {
			t.Fatal(err)
		}
		eq(t, "kind", wake.Kind, "run-exit")
		eq(t, "request id", wake.RequestID, "req-wake-a")
		eq(t, "result is the resolved record", string(wake.Result), string(h.resolves[0].Record))
		eq(t, "draft", wake.Review.Drafts[0].Title, "Restart should reconnect")
		eq(t, "answers", len(wake.Review.Answers), 6)
		eq(t, "threshold", wake.Review.Threshold, 0.5)
		if strings.Contains(string(h.resolves[0].Record), "review") {
			t.Fatal("the review must not be in the result record")
		}
	})

	t.Run("no notify, no wake", func(t *testing.T) {
		h := newHarness(t)
		r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
		sum, _ := r.Run(context.Background(), input(plainText))
		if summary(t, sum).Notified != nil {
			t.Fatal("notified should be null")
		}
		if strings.Contains(h.sequence(), "troupe") {
			t.Fatal("no troupe call expected")
		}
	})

	t.Run("a failed DM never fails the run", func(t *testing.T) {
		h := newHarness(t)
		h.troupe = WakeResponse{ExitCode: 1, Stderr: "no route"}
		r, log := h.runner(graphtest.New(t, graphtest.Options{}))
		sum, exit := r.Run(context.Background(), in)
		s := summary(t, sum)
		eq(t, "exit", exit, 0)
		eq(t, "notified", *s.Notified, false)
		if !strings.Contains(log.String(), "the exit wake to b0cc-key@flic.xmpp.example.invalid was not sent") {
			t.Fatalf("log %s", log)
		}
	})

	t.Run("a run that failed before the judge has no review", func(t *testing.T) {
		h := newHarness(t)
		h.spawn = SpawnResponse{ExitCode: 2}
		r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
		r.Run(context.Background(), in)
		_, body, _ := strings.Cut(h.wakeReq.Message, "\n\n")
		if strings.Contains(body, `"review"`) {
			t.Fatalf("body %s", body)
		}
	})

	t.Run("a rejected run's wake carries the rejection", func(t *testing.T) {
		h := newHarness(t)
		h.decide = func(DecideRequest) DecideResponse {
			return answers(map[string]string{"dup": `{"choice":"problem","confidence":0.8}`})
		}
		r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
		r.Run(context.Background(), in)
		_, body, _ := strings.Cut(h.wakeReq.Message, "\n\n")
		if !strings.Contains(body, `"state":"rejected"`) || !strings.Contains(body, `"failed_checks":["dup"]`) {
			t.Fatalf("body %s", body)
		}
	})

	t.Run("no wake account, no wake", func(t *testing.T) {
		h := newHarness(t)
		r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
		r.Config.WakeUser = ""
		r.Run(context.Background(), in)
		if strings.Contains(h.sequence(), "troupe") {
			t.Fatal("no troupe call expected")
		}
	})
}

// scenario r5: an oversized draft body is cut and flagged.
func TestWakeReviewBoundsBodies(t *testing.T) {
	h := newHarness(t)
	big := strings.Repeat("x", 5000)
	h.drafts = []string{`{"call":"create_issue","draft":{"repo":"linenisgreat/moxy","title":"t","body":"` + big + `"},"repo_listing":"linenisgreat/moxy"}`}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	r.Run(context.Background(), []byte(`{"text":"x","notify":"b0cc-key@flic.xmpp.example.invalid"}`))
	_, body, _ := strings.Cut(h.wakeReq.Message, "\n\n")
	var wake struct {
		Review struct {
			Drafts []struct {
				Body      string `json:"body"`
				Truncated bool   `json:"truncated"`
			} `json:"drafts"`
		} `json:"review"`
	}
	if err := json.Unmarshal([]byte(body), &wake); err != nil {
		t.Fatal(err)
	}
	eq(t, "body length", len([]rune(wake.Review.Drafts[0].Body)), 4000)
	eq(t, "truncated", wake.Review.Drafts[0].Truncated, true)
	if len(body) >= 32768 {
		t.Fatalf("wake body is %d bytes", len(body))
	}
}

func TestWakeBodyDropsBodiesThenDrafts(t *testing.T) {
	var drafts []map[string]json.RawMessage
	for range 12 {
		drafts = append(drafts, map[string]json.RawMessage{
			"repo": rawJSON("a/b"), "title": rawJSON("t"), "repo_listing": rawJSON("a/b"), "body": rawJSON(strings.Repeat("y", 4000)),
		})
	}
	st := JevState{Drafts: drafts}
	rv := BuildReview(st, json.RawMessage(`{}`), 0.5)
	b, err := WakeBody("k", "", json.RawMessage(`{"state":"filed"}`), &rv)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 32768 {
		t.Fatalf("%d bytes", len(b))
	}
	if !rv.BodiesDropped {
		t.Fatal("bodies should be dropped first")
	}
	if !strings.Contains(string(b), `"request_id":null`) {
		t.Fatalf("request_id: %s", b)
	}
}

// scenarios o1 and o2: origin picks the labels the commit gets.
func TestOriginLabels(t *testing.T) {
	h := newHarness(t)
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	r.Run(context.Background(), []byte(`{"text":"x","origin":"pebble"}`))
	eq(t, "labels", h.commitSpec.Env[1], `ISSUES_LABELS=["zz-from_pebble"]`)
}

// scenarios a2 and a3: the repo hint goes to the task only when well-formed.
func TestRepoHintInTheTask(t *testing.T) {
	h := newHarness(t)
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	r.Run(context.Background(), []byte(`{"text":"file it","repo_hint":"linenisgreat/moxy"}`))
	eq(t, "task", h.taskText, "file it\n\nPreferred repository: linenisgreat/moxy")

	h = newHarness(t)
	r, log := h.runner(graphtest.New(t, graphtest.Options{}))
	r.Run(context.Background(), []byte(`{"text":"file it","repo_hint":"moxy; rm -rf /"}`))
	eq(t, "task", h.taskText, "file it")
	if !strings.Contains(log.String(), "repo_hint is not an <owner>/<name>") {
		t.Fatalf("log %s", log)
	}
}

// scenarios g, f3, w5, o3, r4, m2: refusals call nothing.
func TestRefusals(t *testing.T) {
	for name, in := range map[string]string{
		"no text":         `{"request_id":"x"}`,
		"blank text":      `{"text":"   "}`,
		"bad request id":  `{"text":"t","request_id":"bad id/with slash"}`,
		"bad notify":      `{"text":"t","notify":"not a jid"}`,
		"bad origin":      `{"text":"t","origin":"Pebble Ring!"}`,
		"bad recorded_at": `{"text":"t","recorded_at":"2026-10-13T00:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
			sum, exit := r.Run(context.Background(), []byte(in))
			eq(t, "exit", exit, 2)
			ref, ok := sum.(Refused)
			if !ok || ref.Outcome != rr.OutcomeRefused {
				t.Fatalf("summary %#v", sum)
			}
			eq(t, "calls", len(h.calls), 0)
		})
	}
}

func TestGraphRefusalCallsNothing(t *testing.T) {
	h := newHarness(t)
	r, _ := h.runner(nil)
	r.GraphErr = os.ErrInvalid
	sum, exit := r.Run(context.Background(), input(plainText))
	eq(t, "exit", exit, 2)
	if _, ok := sum.(Refused); !ok {
		t.Fatalf("summary %#v", sum)
	}
	eq(t, "calls", len(h.calls), 0)
}

func TestNoScratchDirIsRefused(t *testing.T) {
	h := newHarness(t)
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	r.Config.TmpDirs = []string{"/nonexistent/for/sure"}
	_, exit := r.Run(context.Background(), input(plainText))
	eq(t, "exit", exit, 2)
}

// scenario r1 to r3: recorded_at and request_id name the run key.
func TestRunKeys(t *testing.T) {
	h := newHarness(t)
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ := r.Run(context.Background(), []byte(`{"text":"t","recorded_at":"1760313600123"}`))
	if !strings.HasPrefix(summary(t, sum).RunKey, "issue-filer-1760313600123-") {
		t.Fatal("recorded_at should name the key")
	}
	h = newHarness(t)
	r, _ = h.runner(graphtest.New(t, graphtest.Options{}))
	sum, _ = r.Run(context.Background(), []byte(`{"text":"t","request_id":"req-2026-10-12-a"}`))
	if k := summary(t, sum).RunKey; len(k) != len("issue-filer-")+16 {
		t.Fatalf("key %s", k)
	}
}

// scenario l: hostile text reaches the judge, the task and the run's input byte for byte.
func TestHostileText(t *testing.T) {
	text := "-rm -rf \"$(touch pwned)\" `id` 'single' \\back\nsecond line\n\ttab $HOME\n"
	h := newHarness(t)
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	in, _ := json.Marshal(map[string]string{"text": text})
	sum, _ := r.Run(context.Background(), in)
	eq(t, "outcome", summary(t, sum).Outcome, rr.OutcomeSuccess)
	eq(t, "task", h.taskText, text)
	var payload struct {
		State struct {
			Request string `json:"request"`
		} `json:"state"`
	}
	if err := json.Unmarshal(h.decideReq.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	eq(t, "request", payload.State.Request, text)
}

// scenario s1: stopped while the drafter runs.
func TestSignalDuringTheDrafter(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	h.onSpawn = func(context.Context) { cancel(SignalCause("TERM")) }
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(ctx, input(plainText))
	eq(t, "exit", exit, 143)
	s, ok := sum.(Interrupted)
	if !ok {
		t.Fatalf("summary %#v", sum)
	}
	eq(t, "outcome", s.Outcome, rr.OutcomeInterrupted)
	eq(t, "signal", s.Signal, "TERM")
	eq(t, "state", *s.State, rr.StateFailed)
	eq(t, "resolved", s.Resolved, true)
	eq(t, "calls", h.sequence(), "juggler new-run,list_repos,juggler spawn-agent,juggler resolve")
	rec := recordOf(t, h.resolves[0].Record)
	eq(t, "reason", string(rec["reason"]), `"interrupted by SIGTERM"`)
	eq(t, "state", string(rec["state"]), `"failed"`)
	eq(t, "artifacts", string(h.resolves[0].Artifacts), "[]")
}

// scenario s3: stopped while the new run is answered as EXISTING.
func TestSignalOnAnExistingRunResolvesNothing(t *testing.T) {
	h := newHarness(t)
	h.newRun.Existing = true
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(SignalCause("INT"))
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	sum, exit := r.Run(ctx, input(plainText))
	eq(t, "exit", exit, 130)
	s := sum.(Interrupted)
	eq(t, "resolved", s.Resolved, false)
	eq(t, "calls", h.sequence(), "juggler new-run")
}

// scenario s2: stopped after the commit; the real record and state are resolved.
func TestSignalAfterTheCommitResolvesTheRealRecord(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	h.resolve = []int{1, 0}
	r, _ := h.runner(graphtest.New(t, graphtest.Options{}))
	r.Sleep = func(time.Duration) { cancel(SignalCause("HUP")) }
	sum, exit := r.Run(ctx, input(plainText))
	eq(t, "exit", exit, 129)
	s := sum.(Interrupted)
	eq(t, "state", *s.State, rr.StateFiled)
	eq(t, "resolved", s.Resolved, true)
	for _, rq := range h.resolves {
		eq(t, "every resolve is the real one", rq.State, "succeeded")
		if strings.Contains(string(rq.Record), "interrupted") {
			t.Fatalf("record %s", rq.Record)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":              "''",
		"plain":         "'plain'",
		"it's":          `'it'\''s'`,
		"a\nb":          `$'a\nb'`,
		"tab\there":     `$'tab\there'`,
		"back\\slash\n": `$'back\\slash\n'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Fatalf("%q: got %s want %s", in, got, want)
		}
	}
}

func TestFillBrief(t *testing.T) {
	tmpl := []byte("# header mentions @@REPO_LIST@@ and @@TRAFFIC_FILE@@\n@@REPO_LIST@@\npath = \"@@TRAFFIC_FILE@@\"\n")
	out, err := FillBrief(tmpl, []string{"a/b", "c/d (private)"}, "/tmp/x/traffic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "brief", string(out), "# header mentions @@REPO_LIST@@ and /tmp/x/traffic.jsonl\na/b\nc/d (private)\npath = \"/tmp/x/traffic.jsonl\"\n")
	if _, err := FillBrief([]byte("x = \"@@UNKNOWN@@\"\n"), nil, "/t"); err == nil {
		t.Fatal("an unknown marker should be refused")
	}
	// A value that looks like a pattern is pasted literally.
	out, err = FillBrief([]byte("p = \"@@TRAFFIC_FILE@@\"\n"), nil, `/tmp/a&b\1$x`)
	if err != nil || string(out) != "p = \"/tmp/a&b\\1$x\"\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestRepoLinesHoldTheRepoAlphabet(t *testing.T) {
	got := RepoLines([]Repo{{Name: "a/b"}, {Name: "bad name/x"}, {Name: "m/n", Mirror: true, TrackedAt: "evil; x"}})
	eq(t, "lines", got, []string{"a/b", "m/n (mirror, do not file here)"})
}

func TestBuildJevState(t *testing.T) {
	traffic := listingLine + "\n" + searchLine + "\n" + draftLine + "\n[1,2]\n"
	st, err := BuildJevState("req", []byte(traffic))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "repos", st.Checks.ReposAvailable, []string{
		"amarbel-llc/moxy (mirror, do not file here; use linenisgreat/moxy)", "friedenberg/dotfiles (private)", "linenisgreat/moxy",
	})
	eq(t, "listings", st.RepoListings(), []string{"linenisgreat/moxy"})
	eq(t, "searches", len(st.Checks.DuplicateSearches), 1)
	eq(t, "validation", string(st.Validation["pii_scan"]), `"clean"`)
	if _, err := BuildJevState("req", []byte("not json\n")); err == nil {
		t.Fatal("a broken traffic line should fail")
	}
	// A draft without a listing reads "not listed".
	st, _ = BuildJevState("req", []byte(`{"call":"create_issue","draft":{"title":"t"}}`+"\n"))
	eq(t, "default listing", st.RepoListings(), []string{"not listed"})
}
