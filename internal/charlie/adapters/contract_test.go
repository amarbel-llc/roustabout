package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/roustabout/internal/bravo/run"
)

// The contract file says what the juggler adapter runs and reads
// (docs/juggler-contract-format.md). Juggler's own tests read a copy of it and
// run the real binary against it; this test keeps the file equal to the code.
// godyn's test lane supplies no embed configuration, but it runs a package's
// tests with the package directory as cwd, so testdata/ resolves (godyn(7),
// TESTS). The file is read at test time.
var contractJSON = func() []byte {
	b, err := os.ReadFile("testdata/juggler-adapter.contract.json")
	if err != nil {
		panic("the contract file is not in testdata/ of the package directory the test runs in: " + err.Error())
	}
	return b
}()

type contractVerb struct {
	Name         string          `json:"name"`
	Argv         []string        `json:"argv"`
	ArgvOptional [][]string      `json:"argv_optional"`
	Env          []string        `json:"env"`
	EnvAbsent    []string        `json:"env_absent"`
	Question     string          `json:"question"`
	Stdin        json.RawMessage `json:"stdin"`
}

type contractFile struct {
	Schema       int               `json:"schema"`
	Placeholders map[string]string `json:"placeholders"`
	Verbs        []contractVerb    `json:"verbs"`
}

// placeholderValues are the fixed values the argv is rendered with.
var placeholderValues = map[string]string{
	"run_key": "k-1", "input_file": "/w/request.json", "room_domain": "runs.example.invalid",
	"operator_jid": "op@xmpp.example.invalid", "issuer": "issuer-1", "brief_file": "/w/brief.toml",
	"task_file": "/w/task", "timeout": "150s", "model": "jev", "room": "r@runs.example.invalid",
	"parent": "chat-1", "question": "no-sensitive-content", "run_job": "job-run-1", "state": "failed",
	"reason": `{"state":"failed"}`, "artifacts": "[]",
}

var placeholderPattern = regexp.MustCompile(`\{([a-z_]+)\}`)

func loadContract(t *testing.T) contractFile {
	t.Helper()
	var c contractFile
	// The file carries fields this test does not read (stdout, exit, notes, doc,
	// fake_response, adapter_exits); they are for juggler's runner.
	if err := json.Unmarshal(contractJSON, &c); err != nil {
		t.Fatal(err)
	}
	if c.Schema != 1 {
		t.Fatalf("contract schema %d, this test reads 1", c.Schema)
	}
	return c
}

// render fills an argv's placeholders; a placeholder the file does not declare,
// or this test has no value for, fails.
func render(t *testing.T, c contractFile, argv []string) []string {
	t.Helper()
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = placeholderPattern.ReplaceAllStringFunc(a, func(m string) string {
			name := m[1 : len(m)-1]
			if _, ok := c.Placeholders[name]; !ok {
				t.Fatalf("the contract's argv uses {%s}, which it does not declare under placeholders", name)
			}
			v, ok := placeholderValues[name]
			if !ok {
				t.Fatalf("the contract declares {%s}, which this test has no value for", name)
			}
			return v
		})
	}
	return out
}

func verbOf(t *testing.T, c contractFile, name string) contractVerb {
	t.Helper()
	for _, v := range c.Verbs {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("the contract has no verb %q", name)
	return contractVerb{}
}

var mint = []string{"TROUPE_MINT_USER=minter", "TROUPE_MINT_PASSWORD_FILE=/fake/minter.pass"}

// checkCall compares what the adapter ran with the contract's verb.
func checkCall(t *testing.T, c contractFile, v contractVerb, got call, optional [][]string) {
	t.Helper()
	want := render(t, c, v.Argv)
	for _, g := range optional {
		want = append(want, render(t, c, g)...)
	}
	if !slices.Equal(got.Args, want) {
		t.Fatalf("%s: the adapter ran %q, the contract says %q", v.Name, got.Args, want)
	}
	for _, name := range v.Env {
		if _, ok := got.Env[name]; !ok {
			t.Errorf("%s: the contract lists %s in env; the adapter did not set it", v.Name, name)
		}
	}
	for _, name := range v.EnvAbsent {
		if _, ok := got.Env[name]; ok {
			t.Errorf("%s: the contract says %s is never set; the adapter set it", v.Name, name)
		}
	}
}

func TestContractCoversExactlyTheVerbsTheAdapterCalls(t *testing.T) {
	c := loadContract(t)
	var names []string
	for _, v := range c.Verbs {
		names = append(names, v.Name)
	}
	slices.Sort(names)
	if want := []string{"decide", "resolve", "spawn-new-run", "spawn-wait"}; !slices.Equal(names, want) {
		t.Fatalf("contract verbs %v, adapter verbs %v", names, want)
	}
	for _, v := range c.Verbs {
		for _, g := range v.ArgvOptional {
			render(t, c, g)
		}
		render(t, c, v.Argv)
	}
	for name := range c.Placeholders {
		if _, ok := placeholderValues[name]; !ok {
			t.Errorf("placeholder %q has no value in this test", name)
		}
	}
}

func TestContractSpawnNewRunMatchesTheAdapter(t *testing.T) {
	c := loadContract(t)
	v := verbOf(t, c, "spawn-new-run")
	for _, withIssuer := range []bool{false, true} {
		x, log := newExec(t)
		j := &Juggler{X: x, Bin: os.Args[0], Mint: mint}
		req := run.NewRunRequest{
			RunKey: placeholderValues["run_key"], InputFile: placeholderValues["input_file"],
			RoomDomain: placeholderValues["room_domain"], OperatorJID: placeholderValues["operator_jid"],
		}
		var opt [][]string
		if withIssuer {
			req.Issuer = placeholderValues["issuer"]
			opt = v.ArgvOptional
		}
		j.NewRun(context.Background(), req)
		checkCall(t, c, v, calls(t, log)[0], opt)
	}
}

func TestContractSpawnWaitMatchesTheAdapter(t *testing.T) {
	c := loadContract(t)
	v := verbOf(t, c, "spawn-wait")
	for _, userManager := range []bool{false, true} {
		x, log := newExec(t)
		j := &Juggler{X: x, Bin: os.Args[0], Mint: mint, UserManager: userManager}
		j.SpawnAgent(context.Background(), run.SpawnRequest{
			BriefFile: placeholderValues["brief_file"], TaskFile: placeholderValues["task_file"], RunKey: placeholderValues["run_key"],
			AsRoot: identity, MoxinPath: "/nix/store/moxin", AgentLimit: 150 * time.Second, Backstop: 320 * time.Second,
		})
		var opt [][]string
		if userManager {
			opt = v.ArgvOptional
		}
		checkCall(t, c, v, calls(t, log)[0], opt)
	}
}

func TestContractDecideMatchesTheAdapter(t *testing.T) {
	c := loadContract(t)
	v := verbOf(t, c, "decide")
	if v.Question != placeholderValues["question"] {
		t.Fatalf("the contract's decide question is %q, this test renders %q", v.Question, placeholderValues["question"])
	}
	var payload bytes.Buffer
	if err := json.Compact(&payload, v.Stdin); err != nil {
		t.Fatalf("the contract's decide stdin: %v", err)
	}
	x, log := newExec(t)
	j := &Juggler{X: x, Bin: os.Args[0], Mint: mint}
	j.Decide(context.Background(), run.DecideRequest{
		RunKey: placeholderValues["run_key"], Model: placeholderValues["model"], Room: placeholderValues["room"],
		Parent: placeholderValues["parent"], AsRoot: identity, Payload: payload.Bytes(),
		Limit: time.Second, Question: placeholderValues["question"],
	})
	got := calls(t, log)[0]
	checkCall(t, c, v, got, nil)
	if got.Stdin != payload.String() {
		t.Fatalf("decide stdin %q, the contract's payload %q", got.Stdin, payload.String())
	}
	// The payload is a questions map the answer is keyed by, and the gate is one of them.
	var p struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(v.Stdin, &p); err != nil || p.Questions[v.Question] == nil {
		t.Fatalf("the contract's decide question %q is not one of its stdin questions", v.Question)
	}
}

func TestContractResolveMatchesTheAdapter(t *testing.T) {
	c := loadContract(t)
	v := verbOf(t, c, "resolve")
	x, log := newExec(t)
	j := &Juggler{X: x, Bin: os.Args[0], Mint: mint}
	j.Resolve(context.Background(), run.ResolveRequest{
		RunJob: placeholderValues["run_job"], State: placeholderValues["state"],
		Record: []byte(placeholderValues["reason"]), Artifacts: []byte(placeholderValues["artifacts"]), Limit: time.Second,
	})
	checkCall(t, c, v, calls(t, log)[0], nil)
}

// The file is JSON the adapter's tests and juggler's both read; a stray
// character would break juggler's copy first.
func TestContractIsOneJSONDocument(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contractJSON, &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(contractJSON), "}\n") {
		t.Fatal("the contract file does not end with a closing brace and one newline")
	}
}
