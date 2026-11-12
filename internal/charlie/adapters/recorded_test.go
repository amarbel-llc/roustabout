package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/roustabout/internal/bravo/run"
)

// The adapter's parsing of juggler's output, tested on recorded bytes
// (docs/juggler-contract-format.md). A case is a directory of
// {args, stdout, rc, stderr}, captured from the real juggler binary on its
// no-model lane and written by `just codemod-juggler-testdata`; what the adapter
// must read out of it is written by hand in an expect file. The stdout is
// handed to the adapter through the test binary standing in for juggler, so the
// adapter's own exec and parsing code is what runs.
//
// The recordings are real juggler output over ledgers the juggler test seeds:
// the agent is not run (see testdata/juggler/README.md).

type expectFile struct {
	Schema int                   `json:"schema"`
	Cases  map[string]expectCase `json:"cases"`
}

type expectCase struct {
	// Verb is the contract verb whose adapter call parses the case.
	Verb string `json:"verb"`
	// Want maps a field of the adapter's response to what it must hold: a JSON
	// string (exact, or "@nonempty", "@jid", "@json"), a number, or a bool.
	Want map[string]any `json:"want"`
}

// responseFields reads the adapter's response for the verb into named fields.
func responseFields(t *testing.T, verb, stdout, stderr string, rc int) map[string]any {
	t.Helper()
	x, _ := newExec(t, "HELPER_STDOUT="+stdout, "HELPER_STDERR="+stderr, "HELPER_RC="+strconv.Itoa(rc))
	j := &Juggler{X: x, Bin: os.Args[0], Mint: mint}
	ctx := context.Background()
	switch verb {
	case "spawn-new-run":
		r := j.NewRun(ctx, run.NewRunRequest{RunKey: "k", InputFile: "/w/in", RoomDomain: "d", OperatorJID: "o@d", Limit: time.Minute})
		f := map[string]any{
			"exit_code": r.ExitCode, "run_job": r.RunJob, "room": r.Room, "root_stanza": r.RootStanza,
			"root_jid": r.Root.JID, "root_principal": r.Root.Principal, "pending": r.Pending, "existing": r.Existing,
			"existing_resolved": triState(r.ExistingResolved),
		}
		return f
	case "spawn-wait":
		r := j.SpawnAgent(ctx, run.SpawnRequest{AsRoot: identity, AgentLimit: time.Minute, Backstop: time.Minute})
		return map[string]any{
			"exit_code": r.ExitCode, "state": r.State, "message": r.Message, "has_result": r.HasResult,
			"result": string(r.Result), "result_error": r.ResultError, "cannot_complete": r.CannotCompleteIsObject,
			"cannot_complete_reason": r.CannotCompleteReason,
		}
	case "decide":
		r := j.Decide(ctx, run.DecideRequest{AsRoot: identity, Payload: []byte(`{}`), Limit: time.Minute})
		var keys []string
		var m map[string]json.RawMessage
		if json.Unmarshal(r.Answers, &m) == nil {
			for k := range m {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		return map[string]any{"exit_code": r.ExitCode, "has_answers": r.HasAnswersObject, "answers_keys": strings.Join(keys, ","), "reason": r.Reason}
	case "resolve":
		r := j.Resolve(ctx, run.ResolveRequest{RunJob: "job", State: "failed", Record: []byte(`{}`), Artifacts: []byte(`[]`), Limit: time.Minute})
		return map[string]any{"exit_code": r.ExitCode, "torn_down": triState(r.TornDown), "unclean": r.Unclean}
	}
	t.Fatalf("expect names the verb %q, which the adapter does not have", verb)
	return nil
}

func triState(b *bool) string {
	switch {
	case b == nil:
		return "nil"
	case *b:
		return "true"
	}
	return "false"
}

func matches(got, want any) bool {
	g, w := toJSON(got), toJSON(want)
	if s, ok := want.(string); ok && strings.HasPrefix(s, "@") {
		gs, _ := got.(string)
		switch s {
		case "@nonempty":
			return gs != ""
		case "@jid":
			return strings.Contains(gs, "@")
		case "@json":
			return json.Valid([]byte(gs))
		}
		return false
	}
	return g == w
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// runRecorded runs every case under dir against the expect file and requires
// the two to name the same cases.
func runRecorded(t *testing.T, dir, expectPath string) {
	t.Helper()
	raw, err := os.ReadFile(expectPath)
	if err != nil {
		t.Fatal(err)
	}
	var ex expectFile
	if err := json.Unmarshal(raw, &ex); err != nil || ex.Schema != 1 {
		t.Fatalf("%s: schema 1 expected: %v", expectPath, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	var named []string
	for name := range ex.Cases {
		named = append(named, name)
	}
	sort.Strings(dirs)
	sort.Strings(named)
	if !slices.Equal(dirs, named) {
		t.Fatalf("recorded cases %v, expect file cases %v: they must name the same ones", dirs, named)
	}
	for _, name := range named {
		t.Run(name, func(t *testing.T) {
			c := ex.Cases[name]
			read := func(f string) string {
				b, err := os.ReadFile(filepath.Join(dir, name, f))
				if err != nil {
					t.Fatalf("case %s: %v", name, err)
				}
				return string(b)
			}
			rc, err := strconv.Atoi(strings.TrimSpace(read("rc")))
			if err != nil {
				t.Fatalf("case %s: rc is not a number: %v", name, err)
			}
			read("args")
			got := responseFields(t, c.Verb, read("stdout"), read("stderr"), rc)
			for field, want := range c.Want {
				g, ok := got[field]
				if !ok {
					t.Fatalf("case %s: expect names the field %q, which the %s response does not have", name, field, c.Verb)
				}
				if !matches(g, want) {
					t.Errorf("case %s: %s = %s, want %s", name, field, toJSON(g), toJSON(want))
				}
			}
		})
	}
}

// The runner itself, on cases written by hand: they are not juggler output.
func TestRecordedRunnerOnSyntheticCases(t *testing.T) {
	runRecorded(t, "testdata/selftest", "testdata/selftest-expect.json")
}

// The real recordings, once they are committed.
func TestRecordedJugglerOutput(t *testing.T) {
	commit, err := os.ReadFile("testdata/juggler/COMMIT")
	if err != nil {
		t.Skip("no recorded juggler output is committed yet (docs/juggler-contract-format.md, slice 2)")
	}
	if strings.TrimSpace(string(commit)) == "" {
		t.Fatal("testdata/juggler/COMMIT is empty: it must name the juggler commit the recordings came from")
	}
	runRecorded(t, "testdata/juggler", "testdata/juggler-expect.json")
}
