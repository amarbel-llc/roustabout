package runrecord

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/roustabout/internal/0/graph"
)

var questionNames = []string{"clean", "complete", "dup", "faithful", "repo", "request"}

func allEnforce() map[string]string {
	m := map[string]string{}
	for _, q := range questionNames {
		m[q] = graph.ModeEnforce
	}
	return m
}

// answersJSON makes decide's answers: every question ok at 0.9 unless over
// says otherwise (a nil raw drops the answer).
func answersJSON(t *testing.T, over map[string]string) map[string]Answer {
	t.Helper()
	obj := map[string]json.RawMessage{}
	for _, q := range questionNames {
		obj[q] = json.RawMessage(`{"choice":"ok","confidence":0.9}`)
	}
	for q, v := range over {
		if v == "" {
			delete(obj, q)
		} else {
			obj[q] = json.RawMessage(v)
		}
	}
	raw, _ := json.Marshal(obj)
	a, ok := ParseAnswers(raw)
	if !ok {
		t.Fatal("answers did not parse")
	}
	return a
}

func TestJudgeAllOk(t *testing.T) {
	v := Judge(questionNames, answersJSON(t, nil), 0.5, graph.PassConfidentOK, allEnforce(), true)
	if len(v.Failed) != 0 || len(v.ShadowFailed) != 0 {
		t.Fatalf("%+v", v)
	}
}

// scenario c: Jev finds a duplicate.
func TestJudgeDuplicateRejects(t *testing.T) {
	a := answersJSON(t, map[string]string{"dup": `{"choice":"problem","confidence":0.8}`})
	v := Judge(questionNames, a, 0.5, graph.PassConfidentOK, allEnforce(), true)
	if !reflect.DeepEqual(v.Failed, []string{"dup"}) {
		t.Fatalf("%+v", v)
	}
	if got := RejectionReason(v.Failed, a, 0.5, graph.PassConfidentOK); !strings.Contains(got, "dup: problem at 0.8") || !strings.Contains(got, "threshold 0.5") {
		t.Fatalf("reason %q", got)
	}
}

// scenario d: ok on every question, but request is below the threshold.
func TestJudgeBelowThreshold(t *testing.T) {
	a := answersJSON(t, map[string]string{"request": `{"choice":"ok","confidence":0.4}`})
	v := Judge(questionNames, a, 0.5, graph.PassConfidentOK, allEnforce(), true)
	if !reflect.DeepEqual(v.Failed, []string{"request"}) {
		t.Fatalf("%+v", v)
	}
	if got := RejectionReason(v.Failed, a, 0.5, graph.PassConfidentOK); !strings.Contains(got, "request") || !strings.Contains(got, "0.4") {
		t.Fatalf("reason %q", got)
	}
}

func TestJudgeConfidenceEqualToThresholdPasses(t *testing.T) {
	a := answersJSON(t, map[string]string{"request": `{"choice":"ok","confidence":0.5}`})
	if v := Judge(questionNames, a, 0.5, graph.PassConfidentOK, allEnforce(), true); len(v.Failed) != 0 {
		t.Fatalf("%+v", v)
	}
}

// scenario d3: five of six answered.
func TestJudgeMissingAnswerFails(t *testing.T) {
	a := answersJSON(t, map[string]string{"dup": ""})
	v := Judge(questionNames, a, 0.5, graph.PassConfidentOK, allEnforce(), true)
	if !reflect.DeepEqual(v.Failed, []string{"dup"}) {
		t.Fatalf("%+v", v)
	}
	if got := RejectionReason(v.Failed, a, 0.5, graph.PassConfidentOK); !strings.Contains(got, "dup: no answer at no confidence") {
		t.Fatalf("reason %q", got)
	}
}

func TestJudgeMissingConfidenceCountsAsZero(t *testing.T) {
	a := answersJSON(t, map[string]string{"repo": `{"choice":"ok"}`})
	if v := Judge(questionNames, a, 0.5, graph.PassConfidentOK, allEnforce(), true); !reflect.DeepEqual(v.Failed, []string{"repo"}) {
		t.Fatalf("%+v", v)
	}
	if v := Judge(questionNames, a, 0, graph.PassConfidentOK, allEnforce(), true); len(v.Failed) != 0 {
		t.Fatalf("threshold 0 should pass: %+v", v)
	}
}

func TestJudgeNonObjectAnswerFails(t *testing.T) {
	a := answersJSON(t, map[string]string{"clean": `"ok"`})
	if v := Judge(questionNames, a, 0.5, graph.PassConfidentOK, allEnforce(), true); !reflect.DeepEqual(v.Failed, []string{"clean"}) {
		t.Fatalf("%+v", v)
	}
}

func TestJudgeFailedChecksAreSorted(t *testing.T) {
	a := answersJSON(t, map[string]string{
		"request": `{"choice":"no","confidence":1}`,
		"clean":   `{"choice":"no","confidence":1}`,
	})
	v := Judge([]string{"request", "clean", "dup"}, a, 0.5, graph.PassConfidentOK, allEnforce(), true)
	if !reflect.DeepEqual(v.Failed, []string{"clean", "request"}) {
		t.Fatalf("%+v", v)
	}
}

// TestJudgePassRules measures one answer to "clean" (enforce) and one to
// "faithful" (shadow) under each pass rule at threshold 0.5. A nil answer
// drops it; everything else is ok at 0.9.
func TestJudgePassRules(t *testing.T) {
	const (
		oldRule = graph.PassConfidentOK
		newRule = graph.PassNoConfidentProblem
	)
	modes := map[string]string{
		"request": "enforce", "complete": "enforce", "repo": "enforce",
		"faithful": "shadow", "clean": "enforce", "dup": "enforce",
	}
	cases := []struct {
		name   string
		rule   string
		q      string
		answer string
		fails  bool
	}{
		{"uncertain problem passes", newRule, "clean", `{"choice":"problem","confidence":0.02}`, false},
		{"problem just below the threshold passes", newRule, "clean", `{"choice":"problem","confidence":0.49}`, false},
		{"problem at the threshold fails", newRule, "clean", `{"choice":"problem","confidence":0.5}`, true},
		{"confident problem fails", newRule, "clean", `{"choice":"problem","confidence":0.9}`, true},
		{"uncertain ok passes", newRule, "clean", `{"choice":"ok","confidence":0.02}`, false},
		{"confident ok passes", newRule, "clean", `{"choice":"ok","confidence":0.9}`, false},
		{"no answer fails", newRule, "clean", "", true},
		{"no choice fails", newRule, "clean", `{"confidence":0.9}`, true},
		{"a choice that is not a string fails", newRule, "clean", `{"choice":1,"confidence":0.9}`, true},
		{"no confidence fails", newRule, "clean", `{"choice":"ok"}`, true},
		{"a confidence that is not a number fails", newRule, "clean", `{"choice":"ok","confidence":"high"}`, true},
		{"another choice fails", newRule, "clean", `{"choice":"maybe","confidence":0.9}`, true},
		{"an answer that is not an object fails", newRule, "clean", `"ok"`, true},
		{"uncertain shadow problem is not a shadow failure", newRule, "faithful", `{"choice":"problem","confidence":0.3}`, false},
		{"confident shadow problem is a shadow failure", newRule, "faithful", `{"choice":"problem","confidence":0.76}`, true},

		{"old: uncertain problem fails", oldRule, "clean", `{"choice":"problem","confidence":0.02}`, true},
		{"old: uncertain ok fails", oldRule, "clean", `{"choice":"ok","confidence":0.02}`, true},
		{"old: confident ok passes", oldRule, "clean", `{"choice":"ok","confidence":0.9}`, false},
		{"old: no confidence fails", oldRule, "clean", `{"choice":"ok"}`, true},
		{"old: uncertain shadow problem is a shadow failure", oldRule, "faithful", `{"choice":"problem","confidence":0.3}`, true},
		{"absent rule is the old rule", "", "clean", `{"choice":"problem","confidence":0.02}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := answersJSON(t, map[string]string{c.q: c.answer})
			v := Judge(questionNames, a, 0.5, c.rule, modes, true)
			want, wantShadow := []string{}, []string{}
			if c.fails && modes[c.q] == "shadow" {
				wantShadow = []string{c.q}
			} else if c.fails {
				want = []string{c.q}
			}
			if !reflect.DeepEqual(v.Failed, want) || !reflect.DeepEqual(v.ShadowFailed, wantShadow) {
				t.Fatalf("got %+v, want failed %v shadow %v", v, want, wantShadow)
			}
		})
	}
}

func TestRejectionReasonNamesANonDefaultRule(t *testing.T) {
	a := answersJSON(t, map[string]string{"clean": `{"choice":"problem","confidence":0.9}`})
	if got := RejectionReason([]string{"clean"}, a, 0.5, graph.PassConfidentOK); got != "Jev did not pass the drafts (threshold 0.5): clean: problem at 0.9" {
		t.Fatalf("default rule: %q", got)
	}
	if got := RejectionReason([]string{"clean"}, a, 0.5, graph.PassNoConfidentProblem); got != "Jev did not pass the drafts (threshold 0.5, pass_rule no-confident-problem): clean: problem at 0.9" {
		t.Fatalf("new rule: %q", got)
	}
}

// d4: no answers object is a failed judge, not a rejection.
func TestParseAnswersWithoutObject(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `3`} {
		if _, ok := ParseAnswers(json.RawMessage(raw)); ok {
			t.Fatalf("%q should not read as answers", raw)
		}
	}
}

func shadowModes() map[string]string {
	return map[string]string{
		"request": "enforce", "complete": "enforce", "repo": "enforce",
		"faithful": "shadow", "clean": "enforce-public", "dup": "shadow",
	}
}

// sh1, sh2: a shadow question fails and is recorded, not rejecting.
func TestShadowQuestionsAreRecordedNotRejecting(t *testing.T) {
	a := answersJSON(t, map[string]string{
		"dup":      `{"choice":"problem","confidence":0.8}`,
		"faithful": `{"choice":"ok","confidence":0.2}`,
	})
	v := Judge(questionNames, a, 0.5, graph.PassConfidentOK, shadowModes(), true)
	if len(v.Failed) != 0 || !reflect.DeepEqual(v.ShadowFailed, []string{"dup", "faithful"}) {
		t.Fatalf("%+v", v)
	}
}

// sh3, sh4: clean is enforce-public.
func TestEnforcePublicDependsOnTheDrafts(t *testing.T) {
	a := answersJSON(t, map[string]string{"clean": `{"choice":"problem","confidence":0.9}`})
	public := Judge(questionNames, a, 0.5, graph.PassConfidentOK, shadowModes(), AnyPublic([]string{"linenisgreat/moxy"}))
	if !reflect.DeepEqual(public.Failed, []string{"clean"}) || len(public.ShadowFailed) != 0 {
		t.Fatalf("public: %+v", public)
	}
	private := Judge(questionNames, a, 0.5, graph.PassConfidentOK, shadowModes(), AnyPublic([]string{"friedenberg/dotfiles (private)"}))
	if len(private.Failed) != 0 || !reflect.DeepEqual(private.ShadowFailed, []string{"clean"}) {
		t.Fatalf("private: %+v", private)
	}
}

func TestAnyPublic(t *testing.T) {
	cases := []struct {
		listings []string
		want     bool
	}{
		{nil, false},
		{[]string{"a/b (private)"}, false},
		{[]string{"a/b (private)", "c/d"}, true},
		{[]string{"not listed"}, true},
	}
	for _, c := range cases {
		if got := AnyPublic(c.listings); got != c.want {
			t.Fatalf("%v: got %v", c.listings, got)
		}
	}
}

func TestEndingState(t *testing.T) {
	if (Ending{Why: WhyJudgeRejected}).State() != StateRejected {
		t.Fatal("judge-rejected is rejected")
	}
	for _, w := range []Why{WhyDrafterFailed, WhyJudgeFailed, WhyCommitPartial, WhyTrafficNotStored, WhyTrafficChanged} {
		if (Ending{Why: w}).State() != StateFailed {
			t.Fatalf("%s should be failed", w)
		}
	}
}

// scenario b: the drafter cannot complete.
func TestDrafterCannotComplete(t *testing.T) {
	e, ok := Drafter(DrafterResult{
		ExitCode: 0, State: "succeeded", CannotCompleteIsObject: true,
		CannotCompleteReason: "no confident repository match",
	}, DrafterTimeouts{})
	if ok || e.Why != WhyDrafterFailed || !strings.Contains(e.Reason, "no confident repository match") {
		t.Fatalf("%+v %v", e, ok)
	}
}

func TestDrafterSuccess(t *testing.T) {
	if _, ok := Drafter(DrafterResult{State: "succeeded"}, DrafterTimeouts{}); !ok {
		t.Fatal("clean success")
	}
	if _, ok := Drafter(DrafterResult{State: "failed"}, DrafterTimeouts{}); ok {
		t.Fatal("state failed is not success")
	}
	if _, ok := Drafter(DrafterResult{ExitCode: 2, State: "succeeded"}, DrafterTimeouts{}); ok {
		t.Fatal("nonzero exit is not success")
	}
}

func TestDrafterReasonsByExit(t *testing.T) {
	timeouts := DrafterTimeouts{Agent: "150s", Backstop: "320s"}
	cases := map[int]string{
		5:   "still running after 150s",
		124: "still running after 320s",
		4:   "interrupted",
		3:   "aborted by a holder",
		2:   "the drafter failed",
		1:   "could not be started: juggler spawn exited 1 (boom)",
		9:   "juggler spawn exited 9, state 'x') (boom)",
	}
	for code, want := range cases {
		e, ok := Drafter(DrafterResult{ExitCode: code, State: "x", Stderr: "boom"}, timeouts)
		if ok || !strings.Contains(e.Reason, want) {
			t.Fatalf("exit %d: %q lacks %q", code, e.Reason, want)
		}
	}
	e, _ := Drafter(DrafterResult{ExitCode: 2, Message: "m"}, timeouts)
	if e.Reason != "the drafter failed: m" {
		t.Fatalf("message: %q", e.Reason)
	}
}

// scenarios e and e2.
func TestCommit(t *testing.T) {
	if _, ok := Commit(CommitResult{ExitCode: 0, State: "filed"}); !ok {
		t.Fatal("filed")
	}
	e, ok := Commit(CommitResult{ExitCode: 1, State: "partial", IssuesCount: 1, NotFiledCount: 1})
	if ok || e.Why != WhyCommitPartial || !strings.Contains(e.Reason, "partial: 1 issue(s) filed, 1 not") {
		t.Fatalf("partial %+v", e)
	}
	e, _ = Commit(CommitResult{ExitCode: 2})
	if e.Why != WhyCommitFailed || e.Reason != "the commit filed nothing" {
		t.Fatalf("none %+v", e)
	}
	e, _ = Commit(CommitResult{ExitCode: 124, TimeoutLimit: "120s"})
	if e.Why != WhyCommitFailed || !strings.Contains(e.Reason, "still running after 120s") {
		t.Fatalf("timeout %+v", e)
	}
	if _, ok := Commit(CommitResult{ExitCode: 0, State: "failed"}); ok {
		t.Fatal("exit 0 with another state is not a commit")
	}
}

// scenario t2.
func TestTrafficChanged(t *testing.T) {
	if _, ok := TrafficChanged("aa", "aa"); !ok {
		t.Fatal("same hash")
	}
	e, ok := TrafficChanged("aa", "bb")
	if ok || e.Why != WhyTrafficChanged || !strings.Contains(e.Reason, "changed between the judge and the commit") {
		t.Fatalf("%+v", e)
	}
	if e, ok := TrafficChanged("aa", ""); ok || !strings.Contains(e.Reason, "unreadable") {
		t.Fatalf("%+v", e)
	}
}

func TestRecordFiledHasNoReasonAndKeepsTheOrder(t *testing.T) {
	b, err := json.Marshal(Record{
		State: StateFiled, RunKey: "issue-filer-1", GraphDigest: "sha256:ab",
		Issues:      json.RawMessage(`[{"number":50}]`),
		TrafficBlob: "madder://blobs/x", Reason: "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"state":"filed","run_key":"issue-filer-1","graph_digest":"sha256:ab","issues":[{"number":50}],"not_filed":[],"failed_checks":[],"shadow_failed_checks":[],"traffic_blob":"madder://blobs/x"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestRecordRejected(t *testing.T) {
	b, _ := json.Marshal(Record{
		State: StateRejected, RunKey: "k", GraphDigest: "d",
		FailedChecks: []string{"dup"}, Reason: "dup",
	})
	want := `{"state":"rejected","run_key":"k","graph_digest":"d","issues":[],"not_filed":[],"failed_checks":["dup"],"shadow_failed_checks":[],"traffic_blob":null,"reason":"dup"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestInterruptedRecord(t *testing.T) {
	r := Interrupted("TERM", "k", "d", json.RawMessage(`[]`), "")
	if r.State != StateFailed || r.Reason != "interrupted by SIGTERM" {
		t.Fatalf("%+v", r)
	}
}

func TestArtifacts(t *testing.T) {
	got := Artifacts(json.RawMessage(`[{"uri":"smith://f/o/r/issues/50"},{"number":2},{"uri":3}]`))
	want := []Artifact{{Tool: "issues_create_issue", Kind: "issue", URIs: []string{"smith://f/o/r/issues/50"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	for _, none := range []string{`[]`, `null`, `{}`, `[{"number":1}]`} {
		got := Artifacts(json.RawMessage(none))
		if got == nil || len(got) != 0 {
			t.Fatalf("%s: want an empty list, got %#v", none, got)
		}
	}
}

func TestTrimNotFiled(t *testing.T) {
	long := strings.Repeat("é", 700)
	in, _ := json.Marshal([]map[string]string{{"repo": "linenisgreat/clown", "reason": long}})
	var out []map[string]string
	if err := json.Unmarshal(TrimNotFiled(in), &out); err != nil {
		t.Fatal(err)
	}
	if len([]rune(out[0]["reason"])) != 600 || out[0]["repo"] != "linenisgreat/clown" {
		t.Fatalf("%v", out)
	}
	if string(TrimNotFiled(json.RawMessage(`{"x":1}`))) != "[]" {
		t.Fatal("a non-array reads as none")
	}
	if string(TrimNotFiled(json.RawMessage(`["plain",{"reason":3}]`))) != `["plain",{"reason":3}]` {
		t.Fatal("entries without a string reason stand")
	}
}

func TestIssuesOf(t *testing.T) {
	if string(IssuesOf(json.RawMessage(`[{"number":1}]`))) != `[{"number":1}]` {
		t.Fatal("array stands")
	}
	for _, bad := range []string{``, `null`, `{}`, `7`} {
		if string(IssuesOf(json.RawMessage(bad))) != "[]" {
			t.Fatalf("%q", bad)
		}
	}
}

func TestOutcomeAndExit(t *testing.T) {
	if OutcomeOf(StateFiled, true) != OutcomeSuccess || OutcomeOf(StateRejected, true) != OutcomeFailure || OutcomeOf(StateFiled, false) != OutcomeUnresolved {
		t.Fatal("outcome")
	}
	cases := []struct {
		o        Outcome
		resolved bool
		want     int
	}{
		{OutcomeSuccess, true, 0},
		{OutcomeFailure, true, 0},
		{OutcomeUnresolved, false, 1},
		{OutcomeExisting, false, 0},
		{OutcomeNotStarted, false, 1},
		{OutcomeRefused, false, 2},
	}
	for _, c := range cases {
		if got := ExitCode(c.o, c.resolved); got != c.want {
			t.Fatalf("%s: %d", c.o, got)
		}
	}
	for name, want := range map[string]int{"HUP": 129, "INT": 130, "TERM": 143} {
		if got, ok := SignalExit(name); !ok || got != want {
			t.Fatalf("%s: %d %v", name, got, ok)
		}
	}
	if _, ok := SignalExit("KILL"); ok {
		t.Fatal("KILL is not handled")
	}
	if ResolveState(StateFiled) != "succeeded" || ResolveState(StateRejected) != "failed" || ResolveState(StateFailed) != "failed" {
		t.Fatal("resolve state")
	}
}

func TestSummaryAlwaysCarriesItsArrays(t *testing.T) {
	s := Summary{Outcome: OutcomeNotStarted}
	s.Normalize()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"failed_checks", "shadow_failed_checks", "issues", "not_filed"} {
		if string(m[k]) != "[]" {
			t.Fatalf("%s = %s", k, m[k])
		}
	}
	for _, k := range []string{"state", "traffic_blob", "sink_rc", "notified", "why"} {
		if string(m[k]) != "null" {
			t.Fatalf("%s = %s", k, m[k])
		}
	}
}
