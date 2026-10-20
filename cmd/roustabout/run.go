package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"code.linenisgreat.com/purse-first/libs/go-mcp/command"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	rr "code.linenisgreat.com/roustabout/internal/alfa/runrecord"
	"code.linenisgreat.com/roustabout/internal/bravo/run"
	"code.linenisgreat.com/roustabout/internal/charlie/adapters"
)

// runOptions are what a host supplies to `roustabout run`; the graph document
// supplies the rest.
type runOptions struct {
	graphDir string

	juggler, madder, madderStore, troupe, ringmaster string
	issuesMoxin, commitProgram                       string
	roomDomain, operatorJID, issuer                  string
	wakeUser, wakePassFile, judgeModel               string
	noUserManager                                    bool
	grammar, grammarDigest                           string

	newRun, listRepos, agent, agentBackstop, madderLimit, judge time.Duration
	commit, sink, resolve, signalResolve, resolveRetryPause     time.Duration
	resolveAttempts                                             int
}

func parseRunOptions(args []string, stderr io.Writer) (runOptions, error) {
	var o runOptions
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.juggler, "juggler", "juggler", "the juggler program (a clown verb until clown#258)")
	fs.StringVar(&o.madder, "madder", "madder", "the madder program")
	fs.StringVar(&o.madderStore, "madder-store", "", "a blob-store-id for madder write; empty is madder's default store")
	fs.StringVar(&o.troupe, "troupe", "troupe", "the troupe program, for the exit wake")
	fs.StringVar(&o.ringmaster, "ringmaster", "ringmaster", "the ringmaster program; script nodes run as its jobs (empty runs them without one)")
	fs.StringVar(&o.issuesMoxin, "issues-moxin", "", "the issues moxin the drafter is spawned with (required)")
	fs.StringVar(&o.commitProgram, "commit-program", "", "the program the graph's commit node must name; empty skips the check")
	fs.StringVar(&o.roomDomain, "room-domain", "", "the MUC component each run's room is created on (required)")
	fs.StringVar(&o.operatorJID, "operator-jid", "", "the operator's bare address, a second owner of every run's room (required)")
	fs.StringVar(&o.issuer, "issuer", "", "the issuer principal for spawn --new-run; empty leaves it to the ambient identity")
	fs.StringVar(&o.wakeUser, "wake-user", "", "the account the exit wake is sent from; empty sends none")
	fs.StringVar(&o.wakePassFile, "wake-pass-file", "", "the wake account's password file")
	fs.StringVar(&o.judgeModel, "judge-model", "jev", "the model juggler decide asks")
	fs.StringVar(&o.grammar, "grammar", "", "the grammar document the graph must satisfy (needs --grammar-digest); empty checks none")
	fs.StringVar(&o.grammarDigest, "grammar-digest", "", "the sha256:<hex> digest the grammar document is pinned by")
	fs.BoolVar(&o.noUserManager, "no-user-manager", false, "run the drafter without systemd-run --user")
	fs.DurationVar(&o.newRun, "new-run-timeout", 170*time.Second, "limit on spawn --new-run")
	fs.DurationVar(&o.listRepos, "list-repos-timeout", 60*time.Second, "limit on the repository listing")
	fs.DurationVar(&o.agent, "agent-timeout", 150*time.Second, "how long the drafter is waited for")
	fs.DurationVar(&o.agentBackstop, "agent-backstop-timeout", 320*time.Second, "limit on juggler spawn itself")
	fs.DurationVar(&o.madderLimit, "madder-timeout", 60*time.Second, "limit on madder write")
	fs.DurationVar(&o.judge, "judge-timeout", 60*time.Second, "limit on juggler decide")
	fs.DurationVar(&o.commit, "commit-timeout", 120*time.Second, "limit on the commit node")
	fs.DurationVar(&o.sink, "sink-timeout", 60*time.Second, "limit on the sink node")
	fs.DurationVar(&o.resolve, "resolve-timeout", 120*time.Second, "limit on each juggler resolve")
	fs.DurationVar(&o.signalResolve, "signal-resolve-timeout", 30*time.Second, "limit on the one resolve made when a signal stops the run")
	fs.DurationVar(&o.resolveRetryPause, "resolve-retry-pause", 5*time.Second, "pause between resolve attempts")
	fs.IntVar(&o.resolveAttempts, "resolve-attempts", 3, "how many times resolve is tried")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 1 {
		return o, fmt.Errorf("usage: roustabout run [flags] GRAPH_DIR (the request, a JSON object, on stdin)")
	}
	o.graphDir = fs.Arg(0)
	for name, v := range map[string]string{"--issues-moxin": o.issuesMoxin, "--room-domain": o.roomDomain, "--operator-jid": o.operatorJID} {
		if v == "" {
			return o, fmt.Errorf("%s is required", name)
		}
	}
	if (o.grammar == "") != (o.grammarDigest == "") {
		return o, fmt.Errorf("--grammar and --grammar-digest go together: the grammar is pinned by its digest")
	}
	return o, nil
}

func (o runOptions) config(environ func(string) string) run.Config {
	var tmp []string
	for _, k := range []string{"TMPDIR", "XDG_RUNTIME_DIR"} {
		tmp = append(tmp, environ(k))
	}
	return run.Config{
		JudgeModel: o.judgeModel, RoomDomain: o.roomDomain, OperatorJID: o.operatorJID, Issuer: o.issuer,
		IssuesMoxin: o.issuesMoxin, WakeUser: o.wakeUser,
		NewRunTimeout: o.newRun, ListReposTimeout: o.listRepos, AgentTimeout: o.agent, AgentBackstop: o.agentBackstop,
		MadderTimeout: o.madderLimit, JudgeTimeout: o.judge, CommitTimeout: o.commit, SinkTimeout: o.sink,
		ResolveTimeout: o.resolve, SignalResolveTimeout: o.signalResolve,
		ResolveAttempts: o.resolveAttempts, ResolveRetryPause: o.resolveRetryPause,
		TmpDirs: append(tmp, "/dev/shm", "/tmp"),
	}
}

// realPorts backs the ports with the programs themselves.
func realPorts(o runOptions, environ []string, stderr io.Writer) run.Ports {
	base, mint := adapters.Environ(environ)
	x := &adapters.Exec{Base: base, Stderr: stderr}
	return run.Ports{
		Juggler: &adapters.Juggler{X: x, Bin: o.juggler, Mint: mint, UserManager: !o.noUserManager},
		Repos:   &adapters.Repos{X: x, Moxin: o.issuesMoxin},
		Madder:  &adapters.Madder{X: x, Bin: o.madder, Store: o.madderStore},
		Troupe:  &adapters.Troupe{X: x, Bin: o.troupe, PassFile: o.wakePassFile},
		Scripts: &adapters.Scripts{
			X: x, Ringmaster: o.ringmaster, Source: "roustabout",
			Log: func(format string, args ...any) { fmt.Fprintf(stderr, "issue-filer-run: "+format+"\n", args...) },
		},
	}
}

type env struct {
	environ []string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	// ports builds the seams; tests substitute fakes.
	ports func(o runOptions, environ []string, stderr io.Writer) run.Ports
	// signals delivers the signals that stop a run.
	signals <-chan os.Signal
}

func (e env) lookup(k string) string {
	for _, kv := range e.environ {
		if len(kv) > len(k) && kv[:len(k)] == k && kv[len(k)] == '=' {
			return kv[len(k)+1:]
		}
	}
	return ""
}

// execute handles one request: the summary on stdout, the exit status returned.
func execute(ctx context.Context, e env, args []string) int {
	o, err := parseRunOptions(args, e.stderr)
	if err != nil {
		if err != flag.ErrHelp {
			fmt.Fprintln(e.stderr, "roustabout run:", err)
		}
		return rr.ExitRefused
	}
	input, err := io.ReadAll(e.stdin)
	if err != nil {
		fmt.Fprintln(e.stderr, "roustabout run: reading the request:", err)
		return rr.ExitRefused
	}

	runner := &run.Runner{Config: o.config(e.lookup), Log: e.stderr, Ports: e.ports(o, e.environ, e.stderr)}
	expect := graph.Expect{Moxin: o.issuesMoxin, CommitProgram: o.commitProgram}
	if o.grammar != "" {
		if expect.Grammar, err = os.ReadFile(o.grammar); err != nil {
			runner.GraphErr = fmt.Errorf("the grammar document could not be read: %v", err)
		}
		expect.GrammarDigest = o.grammarDigest
	}
	if runner.GraphErr == nil {
		runner.Graph, runner.GraphErr = graph.Load(os.DirFS(o.graphDir), expect)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if e.signals != nil {
		done := make(chan struct{})
		defer close(done)
		go func() {
			for {
				select {
				case s := <-e.signals:
					name := "TERM"
					switch s {
					case syscall.SIGHUP:
						name = "HUP"
					case syscall.SIGINT:
						name = "INT"
					}
					cancel(run.SignalCause(name))
				case <-done:
					return
				}
			}
		}()
	}

	summary, code := runner.Run(ctx, input)
	b, err := rr.Marshal(summary)
	if err != nil {
		fmt.Fprintln(e.stderr, "roustabout run: the summary could not be built:", err)
		return 1
	}
	fmt.Fprintln(e.stdout, string(b))
	return code
}

func runCommand() *command.Command {
	return &command.Command{
		Name:            "run",
		PassthroughArgs: true,
		Description: command.Description{
			Short: "run the issue-filer graph document on one request",
			Long: "Reads one request, a JSON object with text, on stdin, runs the graph document in GRAPH_DIR " +
				"(graph.json, graph.digest, nodes/, questions.json, drafter.brief.toml) as circus's issue-filer-run does, " +
				"and prints one JSON summary on stdout. Exit 0: the run ended in a recorded result, or an existing run was " +
				"reported. 1: the run was started but not resolved, or could not be started. 2: refused (bad request, bad " +
				"flags, or a graph of another shape). 129, 130, 143: stopped by HUP, INT or TERM.",
		},
		RunCLI: func(ctx context.Context, raw json.RawMessage) error {
			var a struct {
				Args []string `json:"args"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return err
			}
			sigs := make(chan os.Signal, 4)
			signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
			code := execute(ctx, env{environ: os.Environ(), stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, ports: realPorts, signals: sigs}, a.Args)
			signal.Stop(sigs)
			os.Exit(code)
			return nil
		},
	}
}
