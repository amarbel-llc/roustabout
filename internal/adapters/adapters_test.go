package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/roustabout/internal/run"
)

// The test binary stands in for every program the adapters run: with
// ROUSTABOUT_HELPER set it logs how it was called and prints what the test
// asked for.
func TestMain(m *testing.M) {
	if os.Getenv("ROUSTABOUT_HELPER") != "" {
		helper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type call struct {
	Args  []string          `json:"args"`
	Env   map[string]string `json:"env"`
	Stdin string            `json:"stdin"`
}

var watched = []string{
	"TROUPE_MINT_USER", "TROUPE_MINT_PASSWORD_FILE", "TROUPE_XMPP_PASSWORD", "TROUPE_XMPP_USER", "TROUPE_XMPP_DOMAIN",
	"TROUPE_XMPP_PASSWORD_FILE", "CLOWN_SESSION_ID", "MOXIN_PATH", "ISSUES_MODE", "ISSUES_TRAFFIC_FILE", "ISSUES_COMMIT_TOKEN",
}

func helper() {
	var stdin strings.Builder
	buf := make([]byte, 1<<16)
	for {
		n, err := os.Stdin.Read(buf)
		stdin.Write(buf[:n])
		if err != nil {
			break
		}
	}
	c := call{Args: os.Args[1:], Env: map[string]string{}, Stdin: stdin.String()}
	for _, k := range watched {
		if v, ok := os.LookupEnv(k); ok {
			c.Env[k] = v
		}
	}
	if path := os.Getenv("HELPER_LOG"); path != "" {
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		line, _ := json.Marshal(c)
		f.Write(append(line, '\n'))
		f.Close()
	}
	if s := os.Getenv("HELPER_SLEEP"); s != "" {
		d, _ := time.ParseDuration(s)
		time.Sleep(d)
	}
	os.Stdout.WriteString(os.Getenv("HELPER_STDOUT"))
	os.Stderr.WriteString(os.Getenv("HELPER_STDERR"))
	if rc := os.Getenv("HELPER_RC"); rc != "" {
		var n int
		json.Unmarshal([]byte(rc), &n)
		os.Exit(n)
	}
}

func newExec(t *testing.T, extra ...string) (*Exec, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls.jsonl")
	base := append([]string{"ROUSTABOUT_HELPER=1", "HELPER_LOG=" + log, "PATH=" + os.Getenv("PATH")}, extra...)
	return &Exec{Base: base, KillAfter: time.Second}, log
}

func calls(t *testing.T, log string) []call {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var out []call
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c call
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			t.Fatalf("%q: %v", l, err)
		}
		out = append(out, c)
	}
	return out
}

func has(args []string, want ...string) bool {
	for i := range args {
		if i+len(want) <= len(args) && strings.Join(args[i:i+len(want)], "\x00") == strings.Join(want, "\x00") {
			return true
		}
	}
	return false
}

func TestEnvironSeparatesTheMintersCredentialAndDropsTheRest(t *testing.T) {
	base, mint := Environ([]string{
		"PATH=/bin", "HOME=/h", "TROUPE_MINT_USER=minter", "TROUPE_MINT_PASSWORD_FILE=/fake/minter.pass",
		"TROUPE_XMPP_PASSWORD=secret", "TROUPE_XMPP_RESOURCE=res", "ISSUES_COMMIT_TOKEN=tok", "ISSUES_TOKEN_FILE=/t", "TROUPE_XMPP_DOMAIN=d",
	})
	if strings.Join(base, " ") != "PATH=/bin HOME=/h TROUPE_XMPP_DOMAIN=d" {
		t.Fatalf("base %v", base)
	}
	if strings.Join(mint, " ") != "TROUPE_MINT_USER=minter TROUPE_MINT_PASSWORD_FILE=/fake/minter.pass" {
		t.Fatalf("mint %v", mint)
	}
	if _, mint := Environ([]string{"TROUPE_MINT_USER="}); len(mint) != 0 {
		t.Fatal("an empty mint variable is not a credential")
	}
}

func TestExecTimeoutIsExit124(t *testing.T) {
	x, _ := newExec(t, "HELPER_SLEEP=5s", "HELPER_STDERR=slow\n")
	start := time.Now()
	out := x.Run(context.Background(), 200*time.Millisecond, nil, nil, os.Args[0])
	if out.RC != 124 || !strings.HasPrefix(out.Stderr, "timed out after 200ms") {
		t.Fatalf("%+v", out)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the limit did not stop the child")
	}
}

func TestExecReportsExitStatusAndAStderrTail(t *testing.T) {
	x, _ := newExec(t, "HELPER_RC=3", "HELPER_STDERR=one\ntwo\nthree\nfour\n", "HELPER_STDOUT=out")
	var replay strings.Builder
	x.Stderr = &replay
	out := x.Run(context.Background(), 0, nil, nil, os.Args[0])
	if out.RC != 3 || out.Stderr != "two three four" || string(out.Stdout) != "out" {
		t.Fatalf("%+v", out)
	}
	if replay.String() != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("replay %q", replay.String())
	}
}

func TestExecMissingProgramIs127(t *testing.T) {
	x, _ := newExec(t)
	if out := x.Run(context.Background(), 0, nil, nil, "/nonexistent/program"); out.RC != 127 {
		t.Fatalf("%+v", out)
	}
}

func TestExecEnvAddsToTheBaseAndLastWins(t *testing.T) {
	x, log := newExec(t, "ISSUES_MODE=base")
	x.Run(context.Background(), 0, []string{"ISSUES_MODE=call"}, nil, os.Args[0])
	if got := calls(t, log)[0].Env["ISSUES_MODE"]; got != "call" {
		t.Fatalf("got %s", got)
	}
}

var identity = run.Identity{Principal: "root-uuid-1", JID: "root-uuid-1@xmpp.example.invalid", CredentialRef: "/fake/root.pass"}

func TestJugglerNewRun(t *testing.T) {
	x, log := newExec(t, `HELPER_STDOUT={"run_job":"job-run-1","room":"r@runs.example.invalid","root_principal":"root-uuid-1","root_jid":"root-uuid-1@xmpp.example.invalid","root_credential_ref":"/fake/root.pass","root_stanza":"chat-1","existing":true,"resolved":false}`)
	j := &Juggler{X: x, Bin: os.Args[0], Mint: []string{"TROUPE_MINT_USER=minter", "TROUPE_MINT_PASSWORD_FILE=/fake/minter.pass"}}
	resp := j.NewRun(context.Background(), run.NewRunRequest{RunKey: "issue-filer-1", InputFile: "/w/request.json", RoomDomain: "runs.example.invalid", OperatorJID: "op@xmpp.example.invalid", Issuer: "issue-filer-test"})
	if resp.ExitCode != 0 || resp.RunJob != "job-run-1" || resp.Room != "r@runs.example.invalid" || resp.RootStanza != "chat-1" || !resp.Existing {
		t.Fatalf("%+v", resp)
	}
	if resp.ExistingResolved == nil || *resp.ExistingResolved || resp.Root != identity {
		t.Fatalf("%+v", resp)
	}
	c := calls(t, log)[0]
	want := []string{"spawn", "--new-run", "--run-key", "issue-filer-1", "--input", "/w/request.json", "--room-domain", "runs.example.invalid", "--operator-jid", "op@xmpp.example.invalid", "--issuer", "issue-filer-test"}
	if strings.Join(c.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("args %v", c.Args)
	}
	if c.Env["TROUPE_MINT_USER"] != "minter" || c.Env["CLOWN_SESSION_ID"] != "" {
		t.Fatalf("env %v", c.Env)
	}
}

func TestJugglerPendingAndNoOutput(t *testing.T) {
	x, _ := newExec(t, `HELPER_STDOUT={"pending":true}`)
	j := &Juggler{X: x, Bin: os.Args[0]}
	if resp := j.NewRun(context.Background(), run.NewRunRequest{}); !resp.Pending {
		t.Fatalf("%+v", resp)
	}
	x, _ = newExec(t, "HELPER_RC=1", "HELPER_STDERR=boom")
	j = &Juggler{X: x, Bin: os.Args[0]}
	if resp := j.NewRun(context.Background(), run.NewRunRequest{}); resp.ExitCode != 1 || resp.RunJob != "" || resp.Stderr != "boom" {
		t.Fatalf("%+v", resp)
	}
}

func TestJugglerSpawnAgentRunsAsTheRootWithTheMoxinAndTheMinter(t *testing.T) {
	x, log := newExec(t, `HELPER_STDOUT={"state":"succeeded","message":"done"}`)
	j := &Juggler{X: x, Bin: os.Args[0], Mint: []string{"TROUPE_MINT_USER=minter"}, UserManager: true}
	resp := j.SpawnAgent(context.Background(), run.SpawnRequest{
		BriefFile: "/w/brief.toml", TaskFile: "/w/task", RunKey: "k", AsRoot: identity, MoxinPath: "/nix/store/moxin",
		AgentLimit: 150 * time.Second, Backstop: 320 * time.Second,
	})
	if resp.State != "succeeded" || resp.Message != "done" || resp.CannotCompleteIsObject {
		t.Fatalf("%+v", resp)
	}
	c := calls(t, log)[0]
	if !has(c.Args, "--timeout", "150s", "--user") || !has(c.Args, "--wait") {
		t.Fatalf("args %v", c.Args)
	}
	for k, want := range map[string]string{
		"CLOWN_SESSION_ID": "root-uuid-1", "TROUPE_XMPP_USER": "root-uuid-1", "TROUPE_XMPP_DOMAIN": "xmpp.example.invalid",
		"TROUPE_XMPP_PASSWORD_FILE": "/fake/root.pass", "MOXIN_PATH": "/nix/store/moxin", "TROUPE_MINT_USER": "minter",
	} {
		if c.Env[k] != want {
			t.Fatalf("%s = %q, want %q", k, c.Env[k], want)
		}
	}
}

func TestJugglerSpawnAgentCannotComplete(t *testing.T) {
	x, _ := newExec(t, `HELPER_STDOUT={"state":"succeeded","cannot_complete":{"reason":"no match"}}`)
	j := &Juggler{X: x, Bin: os.Args[0]}
	resp := j.SpawnAgent(context.Background(), run.SpawnRequest{AsRoot: identity, AgentLimit: time.Second, Backstop: time.Second})
	if !resp.CannotCompleteIsObject || resp.CannotCompleteReason != "no match" {
		t.Fatalf("%+v", resp)
	}
}

func TestJugglerDecideAsTheRootWithoutTheMinter(t *testing.T) {
	x, log := newExec(t, `HELPER_STDOUT={"answers":{"request":{"choice":"ok","confidence":0.9}},"reason":"fine"}`)
	j := &Juggler{X: x, Bin: os.Args[0], Mint: []string{"TROUPE_MINT_USER=minter"}}
	resp := j.Decide(context.Background(), run.DecideRequest{RunKey: "k", Model: "jev", Room: "r@d", Parent: "chat-1", AsRoot: identity, Payload: []byte(`{"state":{}}`), Limit: time.Second})
	if !resp.HasAnswersObject || !strings.Contains(string(resp.Answers), `"request"`) || resp.Reason != "fine" {
		t.Fatalf("%+v", resp)
	}
	c := calls(t, log)[0]
	if !has(c.Args, "--min-confidence", "0", "--question", "request") || !has(c.Args, "--parent", "chat-1") {
		t.Fatalf("args %v", c.Args)
	}
	if c.Stdin != `{"state":{}}` {
		t.Fatalf("stdin %q", c.Stdin)
	}
	if _, leaked := c.Env["TROUPE_MINT_USER"]; leaked || c.Env["CLOWN_SESSION_ID"] != "root-uuid-1" {
		t.Fatalf("env %v", c.Env)
	}
}

func TestJugglerDecideWithoutAnAnswersObject(t *testing.T) {
	x, _ := newExec(t, `HELPER_STDOUT={"answers":"nope"}`)
	j := &Juggler{X: x, Bin: os.Args[0]}
	if resp := j.Decide(context.Background(), run.DecideRequest{AsRoot: identity}); resp.HasAnswersObject {
		t.Fatalf("%+v", resp)
	}
}

func TestJugglerResolveUsesTheEqualsFormAndTheMinter(t *testing.T) {
	x, log := newExec(t, `HELPER_STDOUT={"torn_down":false,"teardown":[{"jid":"a@d","ok":true},{"jid":"b@d","ok":false,"reason":"gone"}]}`)
	j := &Juggler{X: x, Bin: os.Args[0], Mint: []string{"TROUPE_MINT_USER=minter"}}
	resp := j.Resolve(context.Background(), run.ResolveRequest{RunJob: "job-run-1", State: "failed", Record: []byte(`{"state":"failed"}`), Artifacts: []byte(`[]`), Limit: time.Second})
	if resp.TornDown == nil || *resp.TornDown || !strings.Contains(resp.Unclean, "b@d") || strings.Contains(resp.Unclean, "a@d") {
		t.Fatalf("%+v", resp)
	}
	c := calls(t, log)[0]
	want := `resolve job-run-1 --state=failed --reason={"state":"failed"} --fallback-artifacts=[]`
	if strings.Join(c.Args, " ") != want {
		t.Fatalf("args %v", c.Args)
	}
	if c.Env["TROUPE_MINT_USER"] != "minter" || c.Env["CLOWN_SESSION_ID"] != "" {
		t.Fatalf("env %v", c.Env)
	}
}

func TestMadderNamesTheBlob(t *testing.T) {
	traffic := filepath.Join(t.TempDir(), "traffic.frozen.jsonl")
	os.WriteFile(traffic, []byte("{\"call\":\"list_repos\"}\n"), 0o400)
	x, log := newExec(t, "HELPER_STDOUT={\"error\":\"x\",\"id\":\"bad\"}\n{\"id\":\"blake2b256-abc\",\"size\":22,\"source\":\"-\"}\n")
	m := &Madder{X: x, Bin: os.Args[0], Store: "store-1"}
	resp := m.WriteFile(context.Background(), traffic, time.Second)
	if resp.Blob != "madder://blobs/blake2b256-abc" || resp.ExitCode != 0 {
		t.Fatalf("%+v", resp)
	}
	c := calls(t, log)[0]
	if strings.Join(c.Args, " ") != "write store-1 -" || c.Stdin != "{\"call\":\"list_repos\"}\n" {
		t.Fatalf("%+v", c)
	}
	// No blob named: the response says so, and the interpreter fails closed.
	x, _ = newExec(t, "HELPER_STDOUT=nothing useful\n")
	m = &Madder{X: x, Bin: os.Args[0]}
	if resp := m.WriteFile(context.Background(), traffic, time.Second); resp.Blob != "" {
		t.Fatalf("%+v", resp)
	}
	if resp := m.WriteFile(context.Background(), "/nonexistent", time.Second); resp.ExitCode == 0 {
		t.Fatal("an unreadable file is a failed write")
	}
}

func TestTroupeSendsAsTheWakeAccountOnly(t *testing.T) {
	x, log := newExec(t)
	tr := &Troupe{X: x, Bin: os.Args[0], PassFile: "/fake/wake.pass"}
	resp := tr.DM(context.Background(), run.WakeRequest{From: "issue-filer-wake", To: "b0cc-key", ToVHost: "flic.xmpp.example.invalid", Message: "line\n\nbody"})
	if resp.ExitCode != 0 {
		t.Fatalf("%+v", resp)
	}
	c := calls(t, log)[0]
	if strings.Join(c.Args, "|") != "dm|--from|issue-filer-wake|--to|b0cc-key|--to-vhost|flic.xmpp.example.invalid|--message|line\n\nbody" {
		t.Fatalf("args %q", c.Args)
	}
	if c.Env["TROUPE_XMPP_USER"] != "issue-filer-wake" || c.Env["TROUPE_XMPP_PASSWORD_FILE"] != "/fake/wake.pass" || c.Env["TROUPE_MINT_USER"] != "" {
		t.Fatalf("env %v", c.Env)
	}
}

func moxin(t *testing.T, command string) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "issues"), 0o755)
	os.WriteFile(filepath.Join(dir, "issues", "list_repos.toml"), []byte("name = \"list_repos\"\ncommand = \""+command+"\"\n"), 0o644)
	return dir
}

func TestReposListsInRecordMode(t *testing.T) {
	out := `{"isError":false,"content":[{"type":"text","text":"ok"}],"structuredContent":{"repos":[{"repo":"linenisgreat/moxy"},{"repo":"amarbel-llc/moxy","mirror":true,"tracked_at":"linenisgreat/moxy"},{"repo":"f/d","private":true},{"repo":5}],"unlisted":["x"],"complete":false}}`
	x, log := newExec(t, "HELPER_STDOUT="+out)
	r := &Repos{X: x, Moxin: moxin(t, os.Args[0])}
	resp := r.ListRepos(context.Background(), run.ListReposRequest{TrafficFile: "/w/traffic.jsonl", Limit: time.Second})
	if !resp.OK || resp.Complete || len(resp.Unlisted) != 1 || len(resp.Repos) != 3 || !resp.Repos[1].Mirror || resp.Repos[1].TrackedAt != "linenisgreat/moxy" || !resp.Repos[2].Private {
		t.Fatalf("%+v", resp)
	}
	c := calls(t, log)[0]
	if c.Env["ISSUES_MODE"] != "record" || c.Env["ISSUES_TRAFFIC_FILE"] != "/w/traffic.jsonl" {
		t.Fatalf("env %v", c.Env)
	}
}

func TestReposTheToolSaysItFailed(t *testing.T) {
	x, _ := newExec(t, `HELPER_STDOUT={"isError":true,"content":[{"text":"the forge"},{"text":"did not answer"}]}`)
	r := &Repos{X: x, Moxin: moxin(t, os.Args[0])}
	resp := r.ListRepos(context.Background(), run.ListReposRequest{Limit: time.Second})
	if resp.OK || resp.Detail != "the forge did not answer" {
		t.Fatalf("%+v", resp)
	}
}

func TestReposCompleteDefaultsToTrueAndAMissingCommandIsReported(t *testing.T) {
	x, _ := newExec(t, `HELPER_STDOUT={"isError":false,"structuredContent":{"repos":[]}}`)
	r := &Repos{X: x, Moxin: moxin(t, os.Args[0])}
	if resp := r.ListRepos(context.Background(), run.ListReposRequest{Limit: time.Second}); !resp.OK || !resp.Complete {
		t.Fatalf("%+v", resp)
	}
	r = &Repos{X: x, Moxin: moxin(t, "/nonexistent/list_repos")}
	resp := r.ListRepos(context.Background(), run.ListReposRequest{})
	if resp.OK || !strings.Contains(resp.Detail, "no runnable list_repos command") {
		t.Fatalf("%+v", resp)
	}
}

func TestScriptsRunAsRingmasterJobs(t *testing.T) {
	// The same stand-in answers ringmaster start (a job id) and the program.
	x, log := newExec(t, "HELPER_STDOUT=job-commit-1\n")
	s := &Scripts{X: x, Ringmaster: os.Args[0], Source: "roustabout"}
	res := s.RunScript(context.Background(), run.ScriptSpec{Label: "commit", Program: os.Args[0], Args: []string{"/w/frozen", "/w/token"}, Env: []string{"ISSUES_MODE=x"}, Limit: time.Second})
	if res.JobID != "job-commit-1" || res.ExitCode != 0 {
		t.Fatalf("%+v", res)
	}
	cs := calls(t, log)
	if len(cs) != 3 {
		t.Fatalf("calls %+v", cs)
	}
	if strings.Join(cs[0].Args, " ") != "start --label commit --source roustabout" {
		t.Fatalf("start %v", cs[0].Args)
	}
	if strings.Join(cs[1].Args, " ") != "/w/frozen /w/token" || cs[1].Env["ISSUES_MODE"] != "x" {
		t.Fatalf("program %+v", cs[1])
	}
	if strings.Join(cs[2].Args, " ") != "done job-commit-1 --state succeeded --message commit exited 0" {
		t.Fatalf("done %v", cs[2].Args)
	}
	// start and done share one session key, or done cannot find the journal
	// (ringmaster derives its directory from the key and, absent one, makes a
	// new UUID per invocation).
	if key := cs[0].Env["CLOWN_SESSION_ID"]; key == "" || key != cs[2].Env["CLOWN_SESSION_ID"] {
		t.Fatalf("session keys: start %q, done %q", key, cs[2].Env["CLOWN_SESSION_ID"])
	}
}

func TestScriptsRecordAFailureAndPassStdin(t *testing.T) {
	x, log := newExec(t, "HELPER_STDOUT=job-sink-1\n", "HELPER_RC=3")
	s := &Scripts{X: x, Ringmaster: "", Source: "roustabout"}
	res := s.RunScript(context.Background(), run.ScriptSpec{Label: "sink", Program: os.Args[0], Stdin: []byte(`{"state":"failed"}`), Limit: time.Second})
	if res.ExitCode != 3 || res.JobID != "" {
		t.Fatalf("%+v", res)
	}
	if c := calls(t, log); len(c) != 1 || c[0].Stdin != `{"state":"failed"}` {
		t.Fatalf("calls %+v", c)
	}
}

func TestScriptsRunWithoutAJobWhenRingmasterCannotStart(t *testing.T) {
	x, _ := newExec(t, "HELPER_STDOUT=out")
	var logged []string
	s := &Scripts{X: x, Ringmaster: "/nonexistent/ringmaster", Source: "roustabout", Log: func(f string, a ...any) { logged = append(logged, f) }}
	res := s.RunScript(context.Background(), run.ScriptSpec{Label: "commit", Program: os.Args[0], Limit: time.Second})
	if res.JobID != "" || res.ExitCode != 0 || string(res.Stdout) != "out" || len(logged) == 0 {
		t.Fatalf("%+v %v", res, logged)
	}
}

func TestTail(t *testing.T) {
	if got := tail(strings.Repeat("x", 700)); len(got) != 600 {
		t.Fatalf("%d", len(got))
	}
	if got := tail("  a \t b \n\n"); got != "a b" {
		t.Fatalf("%q", got)
	}
	if got := tail(""); got != "" {
		t.Fatalf("%q", got)
	}
}
