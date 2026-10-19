// Package runrecord holds the issue-filer run's decisions as pure functions: how
// the judge's answers become failed and shadow checks, how a step's outcome
// becomes a state and a reason, the result record (circus CONTRACT.md) and the
// run summary, and the exit codes. It is the glue's logic
// (circus infra/hosts/pkgs/issue-filer-glue) without the processes.
package runrecord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"code.linenisgreat.com/roustabout/internal/graph"
)

// Marshal is json.Marshal without HTML escaping. The glue builds its JSON with
// jq, which writes <, > and & as themselves; encoding/json writes them as
// < and so on, which reads back the same but is longer, and the exit
// wake's body is bounded in bytes.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// State is CONTRACT.md's vocabulary.
type State string

const (
	StateFiled    State = "filed"
	StateRejected State = "rejected"
	StateFailed   State = "failed"
)

// Why names the step that took a fail edge.
type Why string

const (
	WhyRunNotStarted      Why = "run-not-started"
	WhyRunPending         Why = "run-pending"
	WhyRepoListFailed     Why = "repo-list-failed"
	WhyRepoListIncomplete Why = "repo-list-incomplete"
	WhyBriefNotPrepared   Why = "brief-not-prepared"
	WhyDrafterFailed      Why = "drafter-failed"
	WhyTrafficNotFrozen   Why = "traffic-not-frozen"
	WhyTrafficNotStored   Why = "traffic-not-stored"
	WhyTrafficChanged     Why = "traffic-changed"
	WhyJudgeFailed        Why = "judge-failed"
	WhyJudgeRejected      Why = "judge-rejected"
	WhyCommitPartial      Why = "commit-partial"
	WhyCommitFailed       Why = "commit-failed"
)

// Ending is why a run did not reach a filed commit: the reason that goes in the
// record and the step that took the fail edge.
type Ending struct {
	Why    Why
	Reason string
}

// State is rejected when the judge rejected, failed for every other fail edge.
func (e Ending) State() State {
	if e.Why == WhyJudgeRejected {
		return StateRejected
	}
	return StateFailed
}

// Outcome is the summary's outcome word.
type Outcome string

const (
	OutcomeSuccess     Outcome = "success"
	OutcomeFailure     Outcome = "failure"
	OutcomeUnresolved  Outcome = "unresolved"
	OutcomeExisting    Outcome = "existing"
	OutcomeRefused     Outcome = "refused"
	OutcomeNotStarted  Outcome = "not-started"
	OutcomeInterrupted Outcome = "interrupted"
)

// OutcomeOf is the outcome of a run that reached a result: unresolved when
// every resolve attempt failed, else success for a filed run and failure for
// the rest.
func OutcomeOf(state State, resolved bool) Outcome {
	switch {
	case !resolved:
		return OutcomeUnresolved
	case state == StateFiled:
		return OutcomeSuccess
	default:
		return OutcomeFailure
	}
}

// Exit codes (the glue's header).
const (
	ExitOK          = 0
	ExitUnresolved  = 1
	ExitRefused     = 2
	ExitSigHUP      = 129
	ExitSigINT      = 130
	ExitSigTERM     = 143
	maxReasonLength = 600
)

// ExitCode is the process exit status for a summary's outcome. resolved only
// matters to a run that reached a result.
func ExitCode(o Outcome, resolved bool) int {
	switch o {
	case OutcomeRefused:
		return ExitRefused
	case OutcomeNotStarted:
		return ExitUnresolved
	case OutcomeExisting:
		return ExitOK
	default:
		if resolved {
			return ExitOK
		}
		return ExitUnresolved
	}
}

// SignalExit is the status for a stop by HUP, INT or TERM; 0 and false for
// any other name.
func SignalExit(name string) (int, bool) {
	switch name {
	case "HUP":
		return ExitSigHUP, true
	case "INT":
		return ExitSigINT, true
	case "TERM":
		return ExitSigTERM, true
	}
	return 0, false
}

// ResolveState is the state juggler resolve is given.
func ResolveState(s State) string {
	if s == StateFiled {
		return "succeeded"
	}
	return "failed"
}

// Record is the result record, the reason juggler resolve is given and the
// body of the run's wake. Issues and NotFiled are arrays as the commit printed
// them.
type Record struct {
	State              State
	RunKey             string
	GraphDigest        string
	Issues             json.RawMessage
	NotFiled           json.RawMessage
	FailedChecks       []string
	ShadowFailedChecks []string
	// TrafficBlob is the madder URI of the frozen traffic, or empty for null.
	TrafficBlob string
	// Reason is left out of a filed record.
	Reason string
}

func emptyArray(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MarshalJSON writes the record's fields in CONTRACT.md's order.
func (r Record) MarshalJSON() ([]byte, error) {
	var blob any
	if r.TrafficBlob != "" {
		blob = r.TrafficBlob
	}
	doc := []struct {
		K string
		V any
	}{
		{"state", r.State},
		{"run_key", r.RunKey},
		{"graph_digest", r.GraphDigest},
		{"issues", emptyArray(r.Issues)},
		{"not_filed", emptyArray(r.NotFiled)},
		{"failed_checks", nonNil(r.FailedChecks)},
		{"shadow_failed_checks", nonNil(r.ShadowFailedChecks)},
		{"traffic_blob", blob},
	}
	if r.State != StateFiled {
		doc = append(doc, struct {
			K string
			V any
		}{"reason", r.Reason})
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, kv := range doc {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv.K)
		v, err := json.Marshal(kv.V)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// Interrupted is the record for a run stopped by a signal before it built one.
func Interrupted(signal, runKey, graphDigest string, issues json.RawMessage, trafficBlob string) Record {
	return Record{
		State: StateFailed, RunKey: runKey, GraphDigest: graphDigest,
		Issues: issues, TrafficBlob: trafficBlob, Reason: "interrupted by SIG" + signal,
	}
}

// Artifact is one entry of the run's artifact list.
type Artifact struct {
	Tool string   `json:"tool"`
	Kind string   `json:"kind"`
	URIs []string `json:"uris"`
}

// Artifacts are the filed uris, as one issues_create_issue entry that is
// present only when something was filed. issues is the commit's issues array.
func Artifacts(issues json.RawMessage) []Artifact {
	var list []struct {
		URI any `json:"uri"`
	}
	if json.Unmarshal(issues, &list) != nil {
		return []Artifact{}
	}
	uris := []string{}
	for _, i := range list {
		if s, ok := i.URI.(string); ok {
			uris = append(uris, s)
		}
	}
	if len(uris) == 0 {
		return []Artifact{}
	}
	return []Artifact{{Tool: "issues_create_issue", Kind: "issue", URIs: uris}}
}

// TrimNotFiled bounds each not-filed entry's reason: the whole record goes on a
// command line. Entries that are not objects with a string reason stand as
// they are; anything that is not an array reads as none.
func TrimNotFiled(raw json.RawMessage) json.RawMessage {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return json.RawMessage("[]")
	}
	for i, item := range list {
		var obj map[string]json.RawMessage
		if json.Unmarshal(item, &obj) != nil {
			continue
		}
		var reason string
		if json.Unmarshal(obj["reason"], &reason) != nil {
			continue
		}
		if r := []rune(reason); len(r) > maxReasonLength {
			obj["reason"], _ = json.Marshal(string(r[:maxReasonLength]))
			list[i], _ = json.Marshal(obj)
		}
	}
	out, err := json.Marshal(list)
	if err != nil {
		return json.RawMessage("[]")
	}
	return out
}

// IssuesOf reads the commit's issues array; anything else reads as none.
func IssuesOf(raw json.RawMessage) json.RawMessage {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || list == nil {
		return json.RawMessage("[]")
	}
	return raw
}

// Answer is the judge's answer to one question.
type Answer struct {
	Present    bool
	Choice     string
	Confidence float64
	HasChoice  bool
	HasConf    bool
}

// ParseAnswers reads juggler decide's `answers` object. It reports false when
// there is no answers object (a failed judge, not a rejection). An answer that
// is not an object is absent.
func ParseAnswers(raw json.RawMessage) (map[string]Answer, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}
	out := make(map[string]Answer, len(obj))
	for q, v := range obj {
		var fields map[string]json.RawMessage
		if json.Unmarshal(v, &fields) != nil || fields == nil {
			continue
		}
		a := Answer{Present: true}
		if json.Unmarshal(fields["choice"], &a.Choice) == nil {
			a.HasChoice = true
		}
		if json.Unmarshal(fields["confidence"], &a.Confidence) == nil {
			a.HasConf = true
		}
		out[q] = a
	}
	return out, true
}

// Verdict is the judge's answers measured against the graph.
type Verdict struct {
	// Failed enforced on this run: the run is rejected.
	Failed []string
	// ShadowFailed did not pass but did not enforce on this run.
	ShadowFailed []string
}

// Judge applies the threshold per question. A question passes when it is
// answered `ok` with a confidence at or above the threshold; a missing answer,
// a missing confidence (counted as 0) and a non-ok choice fail it. A failed
// question is enforced or shadow by its mode; enforce-public enforces only
// when anyPublic. Questions are taken in sorted order.
func Judge(questions []string, answers map[string]Answer, threshold float64, modes map[string]string, anyPublic bool) Verdict {
	sorted := append([]string(nil), questions...)
	sort.Strings(sorted)
	v := Verdict{Failed: []string{}, ShadowFailed: []string{}}
	for _, q := range sorted {
		a, ok := answers[q]
		if ok && a.Present && a.HasChoice && a.Choice == "ok" && a.Confidence >= threshold {
			continue
		}
		mode := modes[q]
		if mode == graph.ModeEnforce || (mode == graph.ModeEnforcePublic && anyPublic) {
			v.Failed = append(v.Failed, q)
		} else {
			v.ShadowFailed = append(v.ShadowFailed, q)
		}
	}
	return v
}

// AnyPublic reports whether any draft targets a repository whose listing is
// not marked " (private)". No drafts is false.
func AnyPublic(repoListings []string) bool {
	for _, l := range repoListings {
		if !strings.Contains(l, " (private)") {
			return true
		}
	}
	return false
}

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// RejectionReason is the reason when the judge rejected: the threshold and, per
// failed question, its choice and confidence.
func RejectionReason(failed []string, answers map[string]Answer, threshold float64) string {
	parts := make([]string, 0, len(failed))
	for _, q := range failed {
		a := answers[q]
		choice, conf := "no answer", "no confidence"
		if a.Present && a.HasChoice {
			choice = a.Choice
		}
		if a.Present && a.HasConf {
			conf = formatNumber(a.Confidence)
		}
		parts = append(parts, fmt.Sprintf("%s: %s at %s", q, choice, conf))
	}
	detail := truncate(strings.Join(parts, "; "), 1000)
	return fmt.Sprintf("Jev did not pass the drafts (threshold %s): %s", formatNumber(threshold), detail)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// DrafterResult is what the drafter's spawn returned.
type DrafterResult struct {
	ExitCode int
	// State is juggler's agent state, cut to 100 characters.
	State string
	// Message and CannotComplete are the agent's own text.
	Message                string
	CannotCompleteReason   string
	CannotCompleteIsObject bool
	Stderr                 string
}

// DrafterTimeouts name the limits the reasons quote.
type DrafterTimeouts struct{ Agent, Backstop string }

// Drafter reports whether the drafter succeeded. Success is `succeeded` with a
// zero exit and no cannot_complete; anything else takes the fail edge.
func Drafter(d DrafterResult, t DrafterTimeouts) (Ending, bool) {
	state := truncate(d.State, 100)
	if d.ExitCode == 0 && state == "succeeded" && !d.CannotCompleteIsObject {
		return Ending{}, true
	}
	var reason string
	switch d.ExitCode {
	case 5:
		reason = "the drafter was still running after " + t.Agent + " and was not waited for any longer"
	case 124:
		reason = "juggler spawn itself was still running after " + t.Backstop + " and was stopped"
	case 4:
		reason = "the drafter was interrupted (it crashed or was killed)"
	case 3:
		reason = "the drafter was aborted by a holder"
	case 2:
		reason = "the drafter failed"
	case 1:
		reason = "the drafter could not be started: juggler spawn exited 1" + stderrNote(d.Stderr)
	default:
		reason = fmt.Sprintf("the drafter did not end in a clean success (juggler spawn exited %d, state '%s')%s", d.ExitCode, state, stderrNote(d.Stderr))
	}
	if cc := truncate(d.CannotCompleteReason, 2000); cc != "" {
		reason += ". cannot_complete: " + cc
	} else if msg := truncate(d.Message, 1500); msg != "" {
		reason += ": " + msg
	}
	return Ending{Why: WhyDrafterFailed, Reason: reason}, false
}

func stderrNote(err string) string {
	if err == "" {
		return ""
	}
	return " (" + err + ")"
}

// CommitResult is what the commit script returned.
type CommitResult struct {
	ExitCode int
	// State is the `state` the script printed.
	State         string
	IssuesCount   int
	NotFiledCount int
	Stderr        string
	TimeoutLimit  string
}

// Commit reports whether the commit filed everything: exit 0 and state filed.
// A partial commit (exit 1) keeps what it filed in the record's issues.
func Commit(c CommitResult) (Ending, bool) {
	if c.ExitCode == 0 && c.State == string(StateFiled) {
		return Ending{}, true
	}
	switch c.ExitCode {
	case 1:
		return Ending{WhyCommitPartial, fmt.Sprintf("the commit was partial: %d issue(s) filed, %d not (the forges have no multi-issue transaction)", c.IssuesCount, c.NotFiledCount)}, false
	case 2:
		return Ending{WhyCommitFailed, "the commit filed nothing" + stderrNote(c.Stderr)}, false
	case 124:
		return Ending{WhyCommitFailed, "the commit was still running after " + c.TimeoutLimit + " and was stopped; what it filed before that is listed, the rest is unknown"}, false
	default:
		return Ending{WhyCommitFailed, fmt.Sprintf("the commit did not end cleanly (exit %d, state '%s')%s", c.ExitCode, c.State, stderrNote(c.Stderr))}, false
	}
}

// TrafficChanged is the fail edge when the frozen traffic no longer hashes to
// what was judged: nothing is filed.
func TrafficChanged(judgedSHA, nowSHA string) (Ending, bool) {
	if judgedSHA == nowSHA && nowSHA != "" {
		return Ending{}, true
	}
	if nowSHA == "" {
		nowSHA = "unreadable"
	}
	return Ending{WhyTrafficChanged, fmt.Sprintf("the frozen traffic file changed between the judge and the commit (sha256 %s, then %s); nothing was filed", judgedSHA, nowSHA)}, false
}

// Summary is the one JSON object a run prints.
type Summary struct {
	Outcome            Outcome         `json:"outcome"`
	RunKey             string          `json:"run_key"`
	RunJob             string          `json:"run_job"`
	Room               string          `json:"room"`
	GraphDigest        string          `json:"graph_digest"`
	MinConfidence      *float64        `json:"min_confidence"`
	Existing           bool            `json:"existing"`
	State              *State          `json:"state"`
	FailedChecks       []string        `json:"failed_checks"`
	ShadowFailedChecks []string        `json:"shadow_failed_checks"`
	Issues             json.RawMessage `json:"issues"`
	NotFiled           json.RawMessage `json:"not_filed"`
	TrafficBlob        *string         `json:"traffic_blob"`
	TrafficSHA         *string         `json:"traffic_sha"`
	Resolved           *bool           `json:"resolved"`
	TornDown           *bool           `json:"torn_down"`
	SinkRC             *int            `json:"sink_rc"`
	Notified           *bool           `json:"notified"`
	Why                *Why            `json:"why"`
	Reason             *string         `json:"reason"`
	// Ledger is roustabout's run-level ledger (FDR-0043 decision 1): one entry
	// per node that ran. It is in the summary, not in the result record, so the
	// record stays what CONTRACT.md defines.
	Ledger []LedgerEntry `json:"ledger,omitempty"`
}

// LedgerEntry is one node of a run: the node's digest, the job it was
// executed as, its verdict, and the edge taken out of it.
type LedgerEntry struct {
	Node    string `json:"node"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Job     string `json:"job,omitempty"`
	Verdict string `json:"verdict"`
	Edge    string `json:"edge,omitempty"`
}

// Normalize fills the arrays the summary always carries.
func (s *Summary) Normalize() {
	s.FailedChecks = nonNil(s.FailedChecks)
	s.ShadowFailedChecks = nonNil(s.ShadowFailedChecks)
	s.Issues = emptyArray(s.Issues)
	s.NotFiled = emptyArray(s.NotFiled)
}
