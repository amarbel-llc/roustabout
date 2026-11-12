package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	"code.linenisgreat.com/roustabout/internal/0/request"
	rr "code.linenisgreat.com/roustabout/internal/alfa/runrecord"
)

// The attest/1 record's states.
const (
	AttestPassed = "passed"
	AttestFailed = "failed"
)

// AttestRunner interprets an attestation graph (docs/attestation-graph.md): the
// agents in their sequence, then the check and the commit, the sink on a fail
// edge, and a resolve with the attest/1 record. It shares the issue-filer
// runner's configuration, ports, clock and log, and none of its pipeline.
type AttestRunner struct {
	// Base carries the configuration, ports, clock and log. Its Graph is unused.
	Base     Runner
	Graph    *graph.AttestGraph
	GraphErr error
	// Version and Commit name the roustabout build, for the summary.
	Version, Commit string
}

// AttestRecord is the attest/1 result record: the verdict, the graph digest
// and the run-level ledger, in the record a gate digests.
type AttestRecord struct {
	State       string `json:"state"`
	RunKey      string `json:"run_key"`
	GraphDigest string `json:"graph_digest"`
	SessionKey  string `json:"session_key"`
	MergeBase   string `json:"merge_base"`
	Head        string `json:"head"`
	// Findings counts what the agents reported, by severity.
	Findings map[string]int   `json:"findings"`
	Ledger   []rr.LedgerEntry `json:"ledger"`
	// Reason is left out of a passed record.
	Reason string `json:"reason,omitempty"`
}

// AttestSummary is the one JSON object an attestation run prints.
type AttestSummary struct {
	Outcome           rr.Outcome       `json:"outcome"`
	RunKey            string           `json:"run_key"`
	RunJob            string           `json:"run_job"`
	Room              string           `json:"room"`
	GraphDigest       string           `json:"graph_digest"`
	RoustaboutVersion string           `json:"roustabout_version,omitempty"`
	RoustaboutCommit  string           `json:"roustabout_commit,omitempty"`
	Existing          bool             `json:"existing"`
	State             *string          `json:"state"`
	Resolved          bool             `json:"resolved"`
	TornDown          *bool            `json:"torn_down"`
	SinkRC            *int             `json:"sink_rc"`
	Reason            *string          `json:"reason"`
	Ledger            []rr.LedgerEntry `json:"ledger,omitempty"`
}

// AttestRunKey is the run's key: from the request_id when there is one, else
// from the attested diff, so the same diff of the same session is one run.
func AttestRunKey(a *request.Attest) string {
	seed := a.RequestID
	if seed == "" {
		seed = a.SessionKey + "\n" + a.MergeBase + "\n" + a.Head
	}
	sum := sha256.Sum256([]byte(seed))
	return "attest-" + hex.EncodeToString(sum[:])[:16]
}

// Run handles one attestation request and returns the summary to print and the
// exit status.
func (r *AttestRunner) Run(ctx context.Context, input []byte) (summary any, exit int) {
	req, err := request.ParseAttest(input)
	if err != nil {
		var ref *request.Refusal
		if errors.As(err, &ref) {
			return r.Base.refused(ref.Reason)
		}
		return r.Base.refused(err.Error())
	}
	if r.GraphErr != nil {
		return r.Base.refused(r.GraphErr.Error())
	}
	work, err := r.Base.scratch()
	if err != nil {
		return r.Base.refused(err.Error())
	}
	defer os.RemoveAll(work)
	e := &attestExecution{r: r, req: req, g: r.Graph, work: work, runKey: AttestRunKey(req)}
	return e.run(ctx)
}

type attestExecution struct {
	r    *AttestRunner
	req  *request.Attest
	g    *graph.AttestGraph
	work string

	runKey, runJob, room string
	root                 Identity
	existing             bool
	existingResolved     *bool

	ledger   []rr.LedgerEntry
	findings []Finding
	state    string // "" until a result exists
	reason   string
	result   []byte
	resolved bool
	tornDown *bool
	sinkRC   *int
}

func (e *attestExecution) cfg() Config { return e.r.Base.Config }

func (e *attestExecution) log(format string, args ...any) { e.r.Base.logf(format, args...) }

func (e *attestExecution) path(name string) string { return e.work + "/" + name }

func (e *attestExecution) record(id, job, verdict, edge string) {
	n := e.g.Nodes[id]
	e.ledger = append(e.ledger, rr.LedgerEntry{Node: id, Label: n.Label, Kind: n.Kind, Job: job, Verdict: verdict, Edge: edge})
}

// stopped reports the signal that stopped the run, or "".
func (e *attestExecution) stopped(ctx context.Context) string {
	if ctx.Err() == nil {
		return ""
	}
	var s SignalCause
	if errors.As(context.Cause(ctx), &s) {
		return string(s)
	}
	return "TERM"
}

func (e *attestExecution) baseSummary(o rr.Outcome) AttestSummary {
	return AttestSummary{
		Outcome: o, RunKey: e.runKey, RunJob: e.runJob, Room: e.room, GraphDigest: e.g.Digest,
		RoustaboutVersion: e.r.Version, RoustaboutCommit: e.r.Commit,
	}
}

// startRun is juggler spawn --new-run, idempotent on the key. The run's input
// is the request: the session, the diff and the requesting principal.
func (e *attestExecution) startRun(ctx context.Context) (string, bool) {
	cfg := e.cfg()
	input, err := json.Marshal(map[string]string{
		"session_key": e.req.SessionKey, "merge_base": e.req.MergeBase, "head": e.req.Head, "principal": e.req.Principal,
	})
	if err == nil {
		err = os.WriteFile(e.path("request.json"), input, 0o600)
	}
	if err != nil {
		return "the request could not be written for the run's room", false
	}
	resp := e.r.Base.Ports.Juggler.NewRun(ctx, NewRunRequest{
		RunKey: e.runKey, InputFile: e.path("request.json"),
		RoomDomain: cfg.RoomDomain, OperatorJID: cfg.OperatorJID, Issuer: cfg.Issuer, Limit: cfg.NewRunTimeout,
	})
	if resp.ExitCode != 0 {
		return fmt.Sprintf("the run could not be started: juggler spawn --new-run exited %d%s", resp.ExitCode, paren(resp.Stderr)), false
	}
	if resp.Pending {
		return "the run did not finish starting: juggler spawn --new-run reported it as pending", false
	}
	e.existing = resp.Existing
	e.runJob, e.room, e.root, e.existingResolved = resp.RunJob, resp.Room, resp.Root, resp.ExistingResolved
	if e.runJob == "" || e.room == "" {
		e.runJob, e.room = "", ""
		return "juggler spawn --new-run exited 0 but did not report a run job and a room", false
	}
	return "", true
}

func (e *attestExecution) run(ctx context.Context) (any, int) {
	cfg := e.cfg()
	if why, ok := e.startRun(ctx); !ok {
		if sig := e.stopped(ctx); sig != "" {
			return e.onSignal(sig)
		}
		e.log("%s", why)
		s := e.baseSummary(rr.OutcomeNotStarted)
		s.RunJob, s.Room = "", ""
		s.Reason = &why
		return s, rr.ExitCode(rr.OutcomeNotStarted, false)
	}
	if sig := e.stopped(ctx); sig != "" {
		return e.onSignal(sig)
	}
	if e.existing {
		e.log("%s already exists (resolved: %s); nothing was done", e.runKey, boolOrNull(e.existingResolved))
		s := e.baseSummary(rr.OutcomeExisting)
		s.Existing = true
		reason := "a run with this key already exists; its result is the terminal record of its run job"
		s.Reason = &reason
		if e.existingResolved != nil {
			s.Resolved = *e.existingResolved
		}
		return s, rr.ExitOK
	}

	passed, atSink, sig := e.walk(ctx)
	if sig != "" {
		return e.onSignal(sig)
	}
	if sig := e.stopped(ctx); sig != "" {
		return e.onSignal(sig)
	}
	resolveState := "failed"
	e.state = AttestFailed
	if passed {
		e.state, resolveState = AttestPassed, "succeeded"
	}
	e.buildRecord()
	if atSink {
		e.runSink(ctx)
	}
	if sig := e.stopped(ctx); sig != "" {
		return e.onSignal(sig)
	}

	attempts := max(cfg.ResolveAttempts, 1)
	var last ResolveResponse
	for attempt := 1; ; attempt++ {
		last = e.r.Base.Ports.Juggler.Resolve(ctx, ResolveRequest{
			RunJob: e.runJob, State: resolveState, Record: e.result, Artifacts: []byte("[]"), Limit: cfg.ResolveTimeout,
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
		e.r.Base.sleep(cfg.ResolveRetryPause)
	}
	e.resolved = last.ExitCode == 0
	if e.resolved {
		e.tornDown = last.TornDown
	} else {
		e.runSink(ctx)
		e.log("the run is NOT resolved after %d attempt(s); its job has no terminal record. %s",
			attempts, recoveryLine(e.runJob, resolveState, string(e.result), "[]"))
	}

	outcome := rr.OutcomeUnresolved
	switch {
	case e.resolved && passed:
		outcome = rr.OutcomeSuccess
	case e.resolved:
		outcome = rr.OutcomeFailure
	}
	s := e.baseSummary(outcome)
	st := e.state
	s.State, s.Resolved, s.TornDown, s.SinkRC, s.Ledger = &st, e.resolved, e.tornDown, e.sinkRC, e.ledger
	if e.reason != "" {
		s.Reason = &e.reason
	}
	return s, rr.ExitCode(outcome, e.resolved)
}

func (e *attestExecution) buildRecord() {
	rec := AttestRecord{
		State: e.state, RunKey: e.runKey, GraphDigest: e.g.Digest,
		SessionKey: e.req.SessionKey, MergeBase: e.req.MergeBase, Head: e.req.Head,
		Findings: countBySeverity(e.findings), Ledger: e.ledger, Reason: e.reason,
	}
	if rec.Ledger == nil {
		rec.Ledger = []rr.LedgerEntry{}
	}
	body, err := rr.Marshal(rec)
	if err != nil {
		e.log("the result record could not be built: %v", err)
		body = []byte(`{"state":"failed"}`)
	}
	e.result = body
}

// walk runs each node by its kind from the entry and takes the edge its outcome
// names, until a node has no edge for its outcome (the walk ended there: passed
// when that outcome was ok) or the next node is the sink.
func (e *attestExecution) walk(ctx context.Context) (passed, atSink bool, signal string) {
	type key struct{ from, cond string }
	next := map[key]string{}
	for _, edge := range e.g.Edges {
		next[key{edge.From(), edge.Condition()}] = edge.To()
	}
	cur := e.g.EntryID
	for range e.g.Nodes {
		var cond, sig string
		switch e.g.Nodes[cur].Kind {
		case graph.KindAgent:
			cond, sig = e.runAgent(ctx, cur)
		case graph.KindCheck, graph.KindCommit:
			cond, sig = e.runScript(ctx, cur)
		}
		if sig != "" {
			return false, false, sig
		}
		to, ok := next[key{cur, cond}]
		if !ok {
			return cond == graph.EdgeOK, false, ""
		}
		if e.g.Nodes[to].Kind == graph.KindSink {
			return false, true, ""
		}
		cur = to
	}
	return false, false, ""
}

// task is what each agent is asked: the diff to attest, and where to read it.
func (e *attestExecution) task() string {
	return fmt.Sprintf("Attest the change in the worktree %s: the diff from %s to %s, session %s. Read the worktree; never write to it.",
		e.req.Worktree, e.req.MergeBase, e.req.Head, e.req.SessionKey)
}

func (e *attestExecution) runAgent(ctx context.Context, id string) (cond, signal string) {
	cfg, n := e.cfg(), e.g.Nodes[id]
	fail := func(reason string) (string, string) {
		e.reason = reason
		e.record(id, "", "failed", graph.EdgeFail)
		return graph.EdgeFail, ""
	}
	brief, task := e.path("brief-"+id[len("sha256:"):len("sha256:")+16]+".toml"), e.path("task")
	if err := os.WriteFile(brief, e.g.Briefs[n.Brief], 0o600); err != nil {
		return fail("the agent's brief could not be written")
	}
	if err := os.WriteFile(task, []byte(e.task()), 0o600); err != nil {
		return fail("the agent's task could not be written")
	}
	resp := e.r.Base.Ports.Juggler.SpawnAgent(ctx, SpawnRequest{
		BriefFile: brief, TaskFile: task, RunKey: e.runKey, AsRoot: e.root,
		MoxinPath: n.Moxin, AgentLimit: cfg.AgentTimeout, Backstop: cfg.AgentBackstop,
	})
	if sig := e.stopped(ctx); sig != "" {
		return "", sig
	}
	end, ok := rr.Drafter(rr.DrafterResult{
		ExitCode: resp.ExitCode, State: resp.State, Message: resp.Message,
		CannotCompleteReason: resp.CannotCompleteReason, CannotCompleteIsObject: resp.CannotCompleteIsObject,
		Stderr: resp.Stderr,
	}, rr.DrafterTimeouts{Agent: limit(cfg.AgentTimeout), Backstop: limit(cfg.AgentBackstop)})
	if !ok {
		return fail(fmt.Sprintf("the %s agent did not complete: %s", n.Label, end.Reason))
	}
	found, err := ParseFindings(n.Label, resp.Message)
	if err == nil {
		err = appendFindings(e.path("findings.jsonl"), found)
	}
	if err != nil {
		return fail(fmt.Sprintf("the %s agent's findings could not be read: %v", n.Label, err))
	}
	e.findings = append(e.findings, found...)
	e.record(id, "", fmt.Sprintf("succeeded (%d findings)", len(found)), graph.EdgeOK)
	return graph.EdgeOK, ""
}

// runScript runs a check or the commit as a ringmaster job, given the request
// file and the findings file (one JSON object per line: agent, severity, path,
// summary) by path. The commit alone is conferred a write token, in a 0600
// file, as the last argument.
func (e *attestExecution) runScript(ctx context.Context, id string) (cond, signal string) {
	cfg, n := e.cfg(), e.g.Nodes[id]
	if err := appendFindings(e.path("findings.jsonl"), nil); err != nil {
		e.reason = "the findings file could not be made"
		e.record(id, "", "failed", graph.EdgeFail)
		return graph.EdgeFail, ""
	}
	args := []string{e.path("request.json"), e.path("findings.jsonl")}
	if graph.MayWrite(n) {
		token, err := mintToken()
		if err == nil {
			err = os.WriteFile(e.path("commit.token"), []byte(token), 0o600)
		}
		if err != nil {
			e.log("the %s token could not be minted: %v", n.Label, err)
			e.reason = "the commit token could not be minted"
			e.record(id, "", "failed", graph.EdgeFail)
			return graph.EdgeFail, ""
		}
		args = append(args, e.path("commit.token"))
	}
	res := e.r.Base.Ports.Scripts.RunScript(ctx, ScriptSpec{
		NodeID: id, Label: n.Label, Program: n.Program, Args: args,
		Env: []string{
			"ATTEST_RUN_KEY=" + e.runKey, "ATTEST_GRAPH_DIGEST=" + e.g.Digest, "ATTEST_SESSION_KEY=" + e.req.SessionKey,
			"ATTEST_MERGE_BASE=" + e.req.MergeBase, "ATTEST_HEAD=" + e.req.Head,
		},
		Limit: cfg.CommitTimeout,
	})
	if graph.MayWrite(n) {
		_ = os.Remove(e.path("commit.token"))
	}
	if sig := e.stopped(ctx); sig != "" {
		return "", sig
	}
	if res.ExitCode != 0 {
		e.reason = fmt.Sprintf("the %s node exited %d%s", n.Label, res.ExitCode, paren(res.Stderr))
		e.record(id, res.JobID, fmt.Sprintf("exit %d", res.ExitCode), graph.EdgeFail)
		return graph.EdgeFail, ""
	}
	e.record(id, res.JobID, "ok", graph.EdgeOK)
	return graph.EdgeOK, ""
}

// runSink gives the sink program the result record on stdin. Its exit is
// recorded and never changes the outcome. At most once.
func (e *attestExecution) runSink(ctx context.Context) {
	sink := e.g.Nodes[e.g.SinkID]
	if sink.Program == graph.NoSinkProgram || e.sinkRC != nil {
		return
	}
	res := e.r.Base.Ports.Scripts.RunScript(ctx, ScriptSpec{
		NodeID: e.g.SinkID, Label: sink.Label, Program: sink.Program,
		Stdin: append(append([]byte{}, e.result...), '\n'), Limit: e.cfg().SinkTimeout,
	})
	rc := res.ExitCode
	e.sinkRC = &rc
	e.record(e.g.SinkID, res.JobID, fmt.Sprintf("exit %d", rc), "")
	if rc != 0 {
		e.log("the sink %s exited %d; the outcome is unchanged", sink.Program, rc)
	}
}

// onSignal is the run stopped from outside: one best-effort resolve so the run
// does not stay open, then the signal's exit status. Never for an existing run.
func (e *attestExecution) onSignal(sig string) (any, int) {
	e.log("stopped by SIG%s", sig)
	if e.runJob != "" && !e.existing && !e.resolved {
		rec := AttestRecord{
			State: AttestFailed, RunKey: e.runKey, GraphDigest: e.g.Digest,
			SessionKey: e.req.SessionKey, MergeBase: e.req.MergeBase, Head: e.req.Head,
			Findings: countBySeverity(e.findings), Ledger: append([]rr.LedgerEntry{}, e.ledger...), Reason: "interrupted by SIG" + sig,
		}
		record := mustJSON(rec)
		ctx, cancel := context.WithTimeout(context.Background(), e.cfg().SignalResolveTimeout)
		resp := e.r.Base.Ports.Juggler.Resolve(ctx, ResolveRequest{
			RunJob: e.runJob, State: "failed", Record: record, Artifacts: []byte("[]"), Limit: e.cfg().SignalResolveTimeout,
		})
		cancel()
		if resp.ExitCode == 0 {
			e.resolved = true
		} else {
			e.log("juggler resolve %s exited %d on the way out%s; the run may be unresolved. %s",
				e.runJob, resp.ExitCode, paren(resp.Stderr), recoveryLine(e.runJob, "failed", string(record), "[]"))
		}
	}
	s := Interrupted{Outcome: rr.OutcomeInterrupted, Signal: sig, RunKey: e.runKey, RunJob: e.runJob, Existing: e.existing, Resolved: e.resolved}
	if e.runJob != "" && !e.existing {
		st := rr.StateFailed
		s.State = &st
	}
	code, _ := rr.SignalExit(sig)
	return s, code
}
