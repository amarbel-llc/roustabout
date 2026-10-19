package run

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.linenisgreat.com/roustabout/internal/graph"
	"code.linenisgreat.com/roustabout/internal/request"
	rr "code.linenisgreat.com/roustabout/internal/runrecord"
)

// Config is what a host supplies; the graph supplies the rest.
type Config struct {
	JudgeModel  string
	RoomDomain  string
	OperatorJID string
	// Issuer is the issuer principal for spawn --new-run; empty leaves it to
	// the ambient identity.
	Issuer string
	// IssuesMoxin is the moxin the drafter is spawned with and the commit is
	// given.
	IssuesMoxin string
	// WakeUser is the account the exit wake is sent from; empty sends none.
	WakeUser string

	NewRunTimeout, ListReposTimeout, AgentTimeout, AgentBackstop time.Duration
	MadderTimeout, JudgeTimeout, CommitTimeout, SinkTimeout      time.Duration
	ResolveTimeout, SignalResolveTimeout                         time.Duration
	ResolveAttempts                                              int
	ResolveRetryPause                                            time.Duration

	// TmpDirs are tried in order for the private scratch directory.
	TmpDirs []string
}

// SignalCause is the cause a caller cancels a run's context with when a
// signal stopped it: "HUP", "INT" or "TERM".
type SignalCause string

func (s SignalCause) Error() string { return "stopped by SIG" + string(s) }

// Runner interprets the issue-filer graph.
type Runner struct {
	// Graph is the validated document; GraphErr is why there is none. A
	// request is checked before the graph, as the glue does.
	Graph    *graph.Graph
	GraphErr error
	Config   Config
	Ports    Ports
	Now      func() time.Time
	Sleep    func(time.Duration)
	Log      io.Writer
}

// Refused is the summary of a request or graph that was not run.
type Refused struct {
	Outcome rr.Outcome `json:"outcome"`
	Reason  string     `json:"reason"`
}

// Interrupted is the summary of a run stopped by a signal.
type Interrupted struct {
	Outcome  rr.Outcome `json:"outcome"`
	Signal   string     `json:"signal"`
	RunKey   string     `json:"run_key"`
	RunJob   string     `json:"run_job"`
	Existing bool       `json:"existing"`
	State    *rr.State  `json:"state"`
	Resolved bool       `json:"resolved"`
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		fmt.Fprintf(r.Log, "issue-filer-run: "+format+"\n", args...)
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) sleep(d time.Duration) {
	if r.Sleep != nil {
		r.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Run handles one request and returns the summary to print and the exit status.
func (r *Runner) Run(ctx context.Context, input []byte) (summary any, exit int) {
	req, err := request.Parse(input)
	if err != nil {
		var ref *request.Refusal
		if errors.As(err, &ref) {
			return r.refused(ref.Reason)
		}
		return r.refused(err.Error())
	}
	if r.GraphErr != nil {
		return r.refused(r.GraphErr.Error())
	}
	work, err := r.scratch()
	if err != nil {
		return r.refused(err.Error())
	}
	defer os.RemoveAll(work)
	e := &execution{r: r, req: req, g: r.Graph, work: work}
	return e.run(ctx)
}

func (r *Runner) refused(reason string) (any, int) {
	r.logf("refused: %s", reason)
	return Refused{Outcome: rr.OutcomeRefused, Reason: reason}, rr.ExitRefused
}

func (r *Runner) scratch() (string, error) {
	for _, dir := range r.Config.TmpDirs {
		if dir == "" {
			continue
		}
		if w, err := os.MkdirTemp(dir, "issue-filer-run."); err == nil {
			return w, nil
		}
	}
	return "", errors.New("no private scratch directory could be made (tried TMPDIR, XDG_RUNTIME_DIR, /dev/shm and /tmp)")
}

// execution is one run's state.
type execution struct {
	r    *Runner
	req  *request.Request
	g    *graph.Graph
	work string

	runKey, runJob, room string
	root                 Identity
	rootStanza           string
	existing             bool
	existingResolved     *bool

	state    rr.State // "" until a result exists
	ending   rr.Ending
	failed   []string
	shadow   []string
	issues   json.RawMessage
	notFiled json.RawMessage

	trafficBlob string
	trafficSHA  string
	judgeAsked  bool
	jevState    JevState
	answersRaw  json.RawMessage

	result    []byte
	resolved  bool
	tornDown  *bool
	sinkRC    *int
	notified  *bool
	ledger    []rr.LedgerEntry
	sinkRan   bool
	resolveSt string
}

func (e *execution) log(format string, args ...any) { e.r.logf(format, args...) }

func (e *execution) path(name string) string { return filepath.Join(e.work, name) }

func limit(d time.Duration) string { return fmt.Sprintf("%ds", int(d.Seconds())) }

func (e *execution) record(node, label, kind, job, verdict, edge string) {
	e.ledger = append(e.ledger, rr.LedgerEntry{Node: node, Label: label, Kind: kind, Job: job, Verdict: verdict, Edge: edge})
}

// stopped reports the signal that stopped the run, or "".
func (e *execution) stopped(ctx context.Context) string {
	if ctx.Err() == nil {
		return ""
	}
	var s SignalCause
	if errors.As(context.Cause(ctx), &s) {
		return string(s)
	}
	return "TERM"
}

func (e *execution) run(ctx context.Context) (any, int) {
	g, cfg := e.g, e.r.Config
	e.runKey = e.req.RunKey(e.r.now().UnixMilli())
	e.issues, e.notFiled = json.RawMessage("[]"), json.RawMessage("[]")
	e.resolveSt = "failed"
	threshold := g.Threshold()

	if end, ok := e.startRun(ctx); !ok {
		e.log("%s", end.Reason)
		s := e.baseSummary(rr.OutcomeNotStarted)
		why := end.Why
		s.Why, s.Reason = &why, &end.Reason
		resolved := false
		s.Resolved = &resolved
		s.RunJob, s.Room = "", ""
		return s, rr.ExitCode(rr.OutcomeNotStarted, false)
	}
	if sig := e.stopped(ctx); sig != "" {
		return e.onSignal(sig)
	}
	if e.existing {
		e.log("%s already exists (resolved: %s); nothing was done", e.runKey, boolOrNull(e.existingResolved))
		s := e.baseSummary(rr.OutcomeExisting)
		s.Existing = true
		s.Resolved = e.existingResolved
		reason := "a run with this key already exists; its result is the terminal record of its run job"
		s.Reason = &reason
		return s, rr.ExitOK
	}

	var end rr.Ending
	filed := false
	drafted := false
	if end, drafted = e.draft(ctx); drafted {
		if sig := e.stopped(ctx); sig != "" {
			return e.onSignal(sig)
		}
		if tend, ok := e.freezeAndStore(ctx); !ok {
			end = tend
		} else if jend, ok := e.judge(ctx); !ok {
			end = jend
			e.record(g.JudgeID, "judge", graph.KindDecide, "", string(end.Why), graph.EdgeFail)
		} else if cend, ok := e.commit(ctx); !ok {
			end = cend
		} else {
			filed = true
		}
	} else if e.fileExists("traffic.jsonl") {
		if tend, ok := e.freezeAndStore(ctx); !ok {
			e.log("%s (the run had already failed: %s)", tend.Reason, end.Why)
		}
	}
	if sig := e.stopped(ctx); sig != "" {
		return e.onSignal(sig)
	}

	if filed {
		e.state = rr.StateFiled
		e.resolveSt = "succeeded"
	} else {
		e.state = end.State()
		e.ending = end
	}

	rec := rr.Record{
		State: e.state, RunKey: e.runKey, GraphDigest: g.Digest,
		Issues: e.issues, NotFiled: e.notFiled, FailedChecks: e.failed, ShadowFailedChecks: e.shadow,
		TrafficBlob: e.trafficBlob, Reason: e.ending.Reason,
	}
	body, err := json.Marshal(rec)
	if err != nil {
		e.log("the result record could not be built: %v", err)
		body = []byte(`{"state":"failed"}`)
	}
	e.result = body
	artifacts := mustJSON(rr.Artifacts(e.issues))

	if !filed {
		e.runSink(ctx)
	}
	if sig := e.stopped(ctx); sig != "" {
		return e.onSignal(sig)
	}

	e.wake(ctx, threshold)
	if sig := e.stopped(ctx); sig != "" {
		return e.onSignal(sig)
	}

	attempts := max(cfg.ResolveAttempts, 1)
	var last ResolveResponse
	for attempt := 1; ; attempt++ {
		last = e.r.Ports.Juggler.Resolve(ctx, ResolveRequest{
			RunJob: e.runJob, State: e.resolveSt, Record: e.result, Artifacts: artifacts, Limit: cfg.ResolveTimeout,
		})
		if last.ExitCode == 0 {
			break
		}
		if sig := e.stopped(ctx); sig != "" {
			return e.onSignal(sig)
		}
		e.log("juggler resolve %s exited %d (attempt %d of %d)%s", e.runJob, last.ExitCode, attempt, attempts, paren(last.Stderr))
		if attempt >= attempts {
			break
		}
		e.r.sleep(cfg.ResolveRetryPause)
		if sig := e.stopped(ctx); sig != "" {
			return e.onSignal(sig)
		}
	}
	outcome := rr.OutcomeOf(e.state, last.ExitCode == 0)
	if last.ExitCode == 0 {
		e.resolved = true
		e.tornDown = last.TornDown
		if last.TornDown != nil && !*last.TornDown {
			e.log("juggler resolve %s left some of the run's accounts in place: %s", e.runJob, last.Unclean)
		}
	} else {
		e.runSink(ctx)
		e.log("the run is NOT resolved after %d attempt(s); its accounts may remain and its job has no terminal record. %s",
			attempts, recoveryLine(e.runJob, e.resolveSt, string(e.result), string(artifacts)))
	}

	s := e.baseSummary(outcome)
	st := e.state
	s.State = &st
	s.FailedChecks, s.ShadowFailedChecks = e.failed, e.shadow
	s.Issues, s.NotFiled = e.issues, e.notFiled
	if e.trafficBlob != "" {
		s.TrafficBlob = &e.trafficBlob
	}
	if e.trafficSHA != "" {
		s.TrafficSHA = &e.trafficSHA
	}
	s.Resolved, s.TornDown, s.SinkRC, s.Notified = &e.resolved, e.tornDown, e.sinkRC, e.notified
	if e.ending.Why != "" {
		w := e.ending.Why
		s.Why = &w
	}
	if e.ending.Reason != "" {
		s.Reason = &e.ending.Reason
	}
	s.Ledger = e.ledger
	s.Normalize()
	return s, rr.ExitCode(outcome, e.resolved)
}

func (e *execution) baseSummary(o rr.Outcome) rr.Summary {
	thr := e.g.Threshold()
	resolved := false
	s := rr.Summary{
		Outcome: o, RunKey: e.runKey, RunJob: e.runJob, Room: e.room,
		GraphDigest: e.g.Digest, MinConfidence: &thr, Resolved: &resolved,
	}
	s.Normalize()
	return s
}

func boolOrNull(b *bool) string {
	if b == nil {
		return "null"
	}
	return fmt.Sprint(*b)
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func (e *execution) fileExists(name string) bool {
	_, err := os.Stat(e.path(name))
	return err == nil
}

// startRun is the start step: juggler spawn --new-run, idempotent on the key.
func (e *execution) startRun(ctx context.Context) (rr.Ending, bool) {
	cfg := e.r.Config
	input, err := e.req.RunInput(e.r.now())
	if err == nil {
		err = os.WriteFile(e.path("request.json"), input, 0o600)
	}
	if err != nil {
		return rr.Ending{Why: rr.WhyRunNotStarted, Reason: "the request could not be written for the run's room"}, false
	}
	resp := e.r.Ports.Juggler.NewRun(ctx, NewRunRequest{
		RunKey: e.runKey, InputFile: e.path("request.json"),
		RoomDomain: cfg.RoomDomain, OperatorJID: cfg.OperatorJID, Issuer: cfg.Issuer, Limit: cfg.NewRunTimeout,
	})
	if resp.ExitCode != 0 {
		return rr.Ending{Why: rr.WhyRunNotStarted, Reason: fmt.Sprintf(
			"the run could not be started: juggler spawn --new-run exited %d%s. If the start failed part-way, juggler keeps it and resumes it when the same request is run again",
			resp.ExitCode, paren(resp.Stderr))}, false
	}
	if resp.Pending {
		return rr.Ending{Why: rr.WhyRunPending, Reason: "the run did not finish starting: juggler spawn --new-run reported it as pending"}, false
	}
	e.existing = resp.Existing
	e.runJob, e.room = resp.RunJob, resp.Room
	e.root, e.rootStanza, e.existingResolved = resp.Root, resp.RootStanza, resp.ExistingResolved
	if e.runJob == "" || e.room == "" {
		e.runJob, e.room = "", ""
		return rr.Ending{Why: rr.WhyRunNotStarted, Reason: "juggler spawn --new-run exited 0 but did not report a run job and a room"}, false
	}
	return rr.Ending{}, true
}

// draft is the draft node: the repo listing fills the brief, then the drafter
// runs. It is the draft --ok--> judge edge, or the fail edge to the sink.
func (e *execution) draft(ctx context.Context) (rr.Ending, bool) {
	cfg := e.r.Config
	g := e.g
	fail := func(end rr.Ending) (rr.Ending, bool) {
		e.record(g.DraftID, "draft", graph.KindAgent, "", string(end.Why), graph.EdgeFail)
		return end, false
	}
	if end, ok := e.prepareBrief(ctx); !ok {
		return fail(end)
	}
	resp := e.r.Ports.Juggler.SpawnAgent(ctx, SpawnRequest{
		BriefFile: e.path("brief.toml"), TaskFile: e.path("task"), RunKey: e.runKey,
		AsRoot: e.root, MoxinPath: cfg.IssuesMoxin, AgentLimit: cfg.AgentTimeout, Backstop: cfg.AgentBackstop,
	})
	end, ok := rr.Drafter(rr.DrafterResult{
		ExitCode: resp.ExitCode, State: resp.State, Message: resp.Message,
		CannotCompleteReason: resp.CannotCompleteReason, CannotCompleteIsObject: resp.CannotCompleteIsObject,
		Stderr: resp.Stderr,
	}, rr.DrafterTimeouts{Agent: limit(cfg.AgentTimeout), Backstop: limit(cfg.AgentBackstop)})
	if !ok {
		return fail(end)
	}
	e.record(g.DraftID, "draft", graph.KindAgent, "", "succeeded", graph.EdgeOK)
	return rr.Ending{}, true
}

func (e *execution) prepareBrief(ctx context.Context) (rr.Ending, bool) {
	cfg := e.r.Config
	notPrepared := func(reason string) (rr.Ending, bool) {
		return rr.Ending{Why: rr.WhyBriefNotPrepared, Reason: reason}, false
	}
	// The traffic file exists, 0600, before anything can write it.
	f, err := os.OpenFile(e.path("traffic.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return notPrepared("the traffic file could not be created")
	}
	f.Close()

	lr := e.r.Ports.Repos.ListRepos(ctx, ListReposRequest{TrafficFile: e.path("traffic.jsonl"), Limit: cfg.ListReposTimeout})
	if lr.ExitCode != 0 || !lr.OK {
		detail := trunc(lr.Detail, 1000)
		if detail == "" {
			detail = fmt.Sprintf("list_repos exited %d%s", lr.ExitCode, paren(lr.Stderr))
		}
		return rr.Ending{Why: rr.WhyRepoListFailed, Reason: "the repository list could not be read: " + detail}, false
	}
	// A partial list is worse than none for choosing a repository (a mirror
	// then looks like the only repository of its name).
	if len(lr.Unlisted) > 0 || !lr.Complete {
		detail := trunc(strings.Join(lr.Unlisted, ", "), 500)
		if detail == "" {
			detail = "the tool did not say which roots"
		}
		return rr.Ending{Why: rr.WhyRepoListIncomplete, Reason: "the repository list is incomplete (could not list: " + detail + "), so the drafter was not asked to choose a repository from it"}, false
	}
	lines := RepoLines(lr.Repos)
	if len(lines) == 0 {
		return rr.Ending{Why: rr.WhyRepoListFailed, Reason: "the repository list could not be read: list_repos returned no usable <owner>/<name>"}, false
	}
	brief, err := FillBrief(e.g.BriefTOML, lines, e.path("traffic.jsonl"))
	if err != nil {
		return notPrepared("the drafter's brief still has an unfilled marker")
	}
	if err := os.WriteFile(e.path("brief.toml"), brief, 0o600); err != nil {
		return notPrepared("the drafter's brief could not be filled in")
	}
	task, left := e.req.Task()
	if left {
		e.log("repo_hint is not an <owner>/<name>; left out of the task")
	}
	if err := os.WriteFile(e.path("task"), []byte(task), 0o600); err != nil {
		return notPrepared("the drafter's task could not be written")
	}
	return rr.Ending{}, true
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// freezeAndStore copies the traffic file to a read-only file the moment the
// drafter returns, and stores that copy in madder before anything is judged.
// Fail closed: a run whose record cannot be kept files nothing.
func (e *execution) freezeAndStore(ctx context.Context) (rr.Ending, bool) {
	cfg := e.r.Config
	notFrozen := rr.Ending{Why: rr.WhyTrafficNotFrozen, Reason: "the traffic file could not be frozen for the judge and the commit"}
	data, err := os.ReadFile(e.path("traffic.jsonl"))
	if err != nil {
		return notFrozen, false
	}
	frozen := e.path("traffic.frozen.jsonl")
	_ = os.Remove(frozen)
	if err := os.WriteFile(frozen, data, 0o400); err != nil {
		return notFrozen, false
	}
	sum := sha256.Sum256(data)
	e.trafficSHA = hex.EncodeToString(sum[:])

	resp := e.r.Ports.Madder.WriteFile(ctx, frozen, cfg.MadderTimeout)
	if resp.ExitCode != 0 || resp.Blob == "" {
		e.trafficBlob = ""
		return rr.Ending{Why: rr.WhyTrafficNotStored, Reason: fmt.Sprintf(
			"the traffic file was not stored: madder write exited %d%s; it printed: %s",
			resp.ExitCode, paren(resp.Stderr), trunc(resp.Output, 300))}, false
	}
	e.trafficBlob = resp.Blob
	return rr.Ending{}, true
}

// judge is the judge node: the six questions, the threshold applied per
// question, the modes deciding which failures reject.
func (e *execution) judge(ctx context.Context) (rr.Ending, bool) {
	g, cfg := e.g, e.r.Config
	failedJudge := func(reason string) (rr.Ending, bool) {
		return rr.Ending{Why: rr.WhyJudgeFailed, Reason: reason}, false
	}
	traffic, err := os.ReadFile(e.path("traffic.frozen.jsonl"))
	var st JevState
	if err == nil {
		st, err = BuildJevState(e.req.Text, traffic)
	}
	var payload []byte
	if err == nil {
		payload, err = json.Marshal(map[string]any{"state": st, "questions": json.RawMessage(g.QuestionsJSON)})
	}
	if err != nil {
		return failedJudge("the judge was not asked: Jev's state could not be built from the traffic file")
	}
	e.jevState = st
	e.judgeAsked = true

	resp := e.r.Ports.Juggler.Decide(ctx, DecideRequest{
		RunKey: e.runKey, Model: cfg.JudgeModel, Room: e.room, Parent: e.rootStanza,
		AsRoot: e.root, Payload: payload, Limit: cfg.JudgeTimeout,
	})
	if resp.ExitCode == 0 {
		e.answersRaw = resp.Answers
	}
	if resp.ExitCode != 0 {
		detail := trunc(resp.Reason, 1000)
		return failedJudge(fmt.Sprintf("the judge could not be asked: juggler decide exited %d%s%s", resp.ExitCode, paren(detail), paren(resp.Stderr)))
	}
	answers, ok := rr.ParseAnswers(resp.Answers)
	if !resp.HasAnswersObject || !ok {
		return failedJudge("the judge's answer could not be read: juggler decide exited 0 but printed no answers object")
	}

	threshold := g.Threshold()
	verdict := rr.Judge(graph.QuestionNames, answers, threshold, g.QuestionModes(), rr.AnyPublic(st.RepoListings()))
	e.failed, e.shadow = verdict.Failed, verdict.ShadowFailed
	if len(verdict.ShadowFailed) > 0 {
		e.log("Jev would have rejected on %s (shadow; not enforced)", strings.Join(verdict.ShadowFailed, ", "))
	}
	if len(verdict.Failed) > 0 {
		return rr.Ending{Why: rr.WhyJudgeRejected, Reason: rr.RejectionReason(verdict.Failed, answers, threshold)}, false
	}
	e.record(g.JudgeID, "judge", graph.KindDecide, "", "ok", graph.EdgeOK)
	return rr.Ending{}, true
}

// commit is the commit node, run as a ringmaster job on the frozen traffic with
// a token minted for this run alone, in a 0600 file the script reads; the token
// is in no argument and no environment.
func (e *execution) commit(ctx context.Context) (rr.Ending, bool) {
	g, cfg := e.g, e.r.Config
	frozen := e.path("traffic.frozen.jsonl")
	failedCommit := func(end rr.Ending) (rr.Ending, bool) {
		e.record(g.CommitID, "commit", graph.KindScript, "", string(end.Why), graph.EdgeFail)
		return end, false
	}
	var nowSHA string
	if data, err := os.ReadFile(frozen); err == nil {
		sum := sha256.Sum256(data)
		nowSHA = hex.EncodeToString(sum[:])
	}
	if end, ok := rr.TrafficChanged(e.trafficSHA, nowSHA); !ok {
		return failedCommit(end)
	}
	token, err := mintToken()
	tokenFile := e.path("commit.token")
	if err == nil {
		err = os.WriteFile(tokenFile, []byte(token), 0o600)
	}
	if err != nil {
		return failedCommit(rr.Ending{Why: rr.WhyCommitFailed, Reason: "the commit token could not be minted"})
	}
	res := e.r.Ports.Scripts.RunScript(ctx, ScriptSpec{
		NodeID: g.CommitID, Label: "commit", Program: g.Commit().Program,
		Args: []string{frozen, tokenFile},
		// The run key and the graph digest are the provenance footer's
		// content (circus#314): the committing tool writes it, after the
		// judge, so a draft cannot forge it. Neither is a secret.
		Env: []string{
			"ISSUES_MOXIN=" + cfg.IssuesMoxin, "ISSUES_LABELS=" + string(mustJSON(e.req.Labels())),
			"ISSUES_RUN_KEY=" + e.runKey, "ISSUES_GRAPH_DIGEST=" + g.Digest,
		},
		Limit: cfg.CommitTimeout,
	})
	_ = os.Remove(tokenFile)

	var out struct {
		Issues   json.RawMessage `json:"issues"`
		NotFiled json.RawMessage `json:"not_filed"`
		State    string          `json:"state"`
	}
	_ = json.Unmarshal(res.Stdout, &out)
	e.issues = rr.IssuesOf(out.Issues)
	e.notFiled = rr.TrimNotFiled(out.NotFiled)
	var issueList, notFiledList []json.RawMessage
	_ = json.Unmarshal(e.issues, &issueList)
	_ = json.Unmarshal(e.notFiled, &notFiledList)

	end, ok := rr.Commit(rr.CommitResult{
		ExitCode: res.ExitCode, State: out.State, IssuesCount: len(issueList), NotFiledCount: len(notFiledList),
		Stderr: res.Stderr, TimeoutLimit: limit(cfg.CommitTimeout),
	})
	if !ok {
		e.record(g.CommitID, "commit", graph.KindScript, res.JobID, string(end.Why), graph.EdgeFail)
		return end, false
	}
	e.record(g.CommitID, "commit", graph.KindScript, res.JobID, "filed", graph.EdgeOK)
	return rr.Ending{}, true
}

func mintToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	t := base64.StdEncoding.EncodeToString(b)
	t = strings.NewReplacer("/", "", "+", "", "=", "", "\n", "").Replace(t)
	if t == "" {
		return "", errors.New("empty token")
	}
	return t, nil
}

// runSink is the sink node: the graph's failure-sink program, when it is not
// "none", gets the result record on stdin. Its exit is recorded and never
// changes the outcome. At most once.
func (e *execution) runSink(ctx context.Context) {
	if !e.g.HasSinkProgram() || e.sinkRan {
		return
	}
	e.sinkRan = true
	res := e.r.Ports.Scripts.RunScript(ctx, ScriptSpec{
		NodeID: e.g.SinkID, Label: "sink", Program: e.g.Sink().Program,
		Stdin: append(append([]byte{}, e.result...), '\n'), Limit: e.r.Config.SinkTimeout,
	})
	rc := res.ExitCode
	e.sinkRC = &rc
	e.record(e.g.SinkID, "sink", graph.KindScript, res.JobID, fmt.Sprintf("exit %d", rc), "")
	if rc != 0 {
		e.log("the sink %s exited %d; the outcome is unchanged", e.g.Sink().Program, rc)
	}
}

// wake sends the exit wake: one DM to the caller's notify from the fixed wake
// account, carrying the result record, before resolve. Best effort.
func (e *execution) wake(ctx context.Context, threshold float64) {
	cfg := e.r.Config
	if e.req.Notify == "" || cfg.WakeUser == "" {
		return
	}
	var review *Review
	if e.judgeAsked {
		rv := BuildReview(e.jevState, e.answersRaw, threshold)
		review = &rv
	}
	body, err := WakeBody(e.runKey, e.req.RequestID, e.result, review)
	sent := false
	if err == nil {
		local, vhost, _ := strings.Cut(e.req.Notify, "@")
		resp := e.r.Ports.Troupe.DM(ctx, WakeRequest{
			From: cfg.WakeUser, To: local, ToVHost: vhost,
			Message: fmt.Sprintf("issue-filer %s %s\n\n%s", e.runKey, e.state, body),
		})
		sent = resp.ExitCode == 0
		if !sent {
			e.log("the exit wake to %s was not sent (troupe exited %d%s)", e.req.Notify, resp.ExitCode, strings.TrimSuffix(": "+resp.Stderr, ": "))
		}
	}
	e.notified = &sent
}

// recoveryLine is the command that finishes a run by hand, for the journal.
// Every value is quoted for bash.
func recoveryLine(runJob, state, record, artifacts string) string {
	return fmt.Sprintf("To finish it by hand: juggler resolve %s --state=%s --reason=%s --fallback-artifacts=%s",
		shellQuote(runJob), shellQuote(state), shellQuote(record), shellQuote(artifacts))
}

// shellQuote quotes s as one bash word. Control characters use $'...'.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	control := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			control = true
		}
	}
	if !control {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, c := range []byte(s) {
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\'':
			b.WriteString(`\'`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// onSignal is the run stopped from outside. One best-effort resolve so the run
// does not stay open with its accounts, then the signal's exit status. Never
// for an existing run (another process's live run) and never one already
// resolved.
func (e *execution) onSignal(sig string) (any, int) {
	e.log("stopped by SIG%s", sig)
	if e.runJob != "" && !e.existing && !e.resolved {
		var record []byte
		rstate := "failed"
		issues := e.issues
		if e.result != nil {
			// The signal landed in the sink or between resolve attempts: the
			// record already built is resolved with its real state.
			record, rstate = e.result, e.resolveSt
		} else {
			rec := rr.Interrupted(sig, e.runKey, e.g.Digest, e.issues, e.trafficBlob)
			record = mustJSON(rec)
		}
		artifacts := mustJSON(rr.Artifacts(issues))
		ctx, cancel := context.WithTimeout(context.Background(), e.r.Config.SignalResolveTimeout)
		resp := e.r.Ports.Juggler.Resolve(ctx, ResolveRequest{
			RunJob: e.runJob, State: rstate, Record: record, Artifacts: artifacts, Limit: e.r.Config.SignalResolveTimeout,
		})
		cancel()
		if resp.ExitCode == 0 {
			e.resolved = true
		} else {
			e.log("juggler resolve %s exited %d on the way out%s; the run may be unresolved. %s",
				e.runJob, resp.ExitCode, paren(resp.Stderr), recoveryLine(e.runJob, rstate, string(record), string(artifacts)))
		}
	}
	s := Interrupted{Outcome: rr.OutcomeInterrupted, Signal: sig, RunKey: e.runKey, RunJob: e.runJob, Existing: e.existing, Resolved: e.resolved}
	switch {
	case e.state != "":
		st := e.state
		s.State = &st
	case e.runJob != "" && !e.existing:
		st := rr.StateFailed
		s.State = &st
	}
	code, _ := rr.SignalExit(sig)
	return s, code
}
