package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/purse-first/libs/go-mcp/command"

	"code.linenisgreat.com/roustabout/internal/0/graph"
	rr "code.linenisgreat.com/roustabout/internal/alfa/runrecord"
)

// executeCheck loads a graph document the way `roustabout run` does, against the
// same host expectations and the pinned grammar, and says whether it would be
// run. It starts nothing and writes nothing: no request, no juggler, no scratch
// directory. Exit 0: the document would be run. 2: refused, with the reason.
func executeCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var issuesMoxin, commitProgram, grammar, grammarDigest string
	fs.StringVar(&issuesMoxin, "issues-moxin", "", "the issues moxin the draft node must name; empty skips the check")
	fs.StringVar(&commitProgram, "commit-program", "", "the program the commit node must name; empty skips the check")
	fs.StringVar(&grammar, "grammar", "", "the grammar document the graph must satisfy (needs --grammar-digest)")
	fs.StringVar(&grammarDigest, "grammar-digest", "", "the sha256:<hex> digest the grammar document is pinned by")
	if err := fs.Parse(args); err != nil {
		return rr.ExitRefused
	}
	say := func(v map[string]any, code int) int {
		b, _ := rr.Marshal(v)
		fmt.Fprintln(stdout, string(b))
		return code
	}
	refused := func(format string, a ...any) int {
		return say(map[string]any{"outcome": "refused", "reason": fmt.Sprintf(format, a...)}, rr.ExitRefused)
	}
	if fs.NArg() != 1 {
		return refused("usage: roustabout check [flags] GRAPH_DIR")
	}
	if (grammar == "") != (grammarDigest == "") {
		return refused("--grammar and --grammar-digest go together: the grammar is pinned by its digest")
	}
	expect := graph.Expect{Moxin: issuesMoxin, CommitProgram: commitProgram, GrammarDigest: grammarDigest}
	if grammar != "" {
		b, err := os.ReadFile(grammar)
		if err != nil {
			return refused("the grammar document could not be read: %v", err)
		}
		expect.Grammar = b
	}
	g, err := graph.Load(os.DirFS(fs.Arg(0)), expect)
	if err != nil {
		return refused("%v", err)
	}
	var steps []string
	for _, s := range g.Result {
		steps = append(steps, s.Step)
	}
	return say(map[string]any{
		"outcome": "ok", "graph_digest": g.Digest, "grammar_checked": grammar != "", "result": steps,
	}, rr.ExitOK)
}

func checkCommand() *command.Command {
	return &command.Command{
		Name:            "check",
		PassthroughArgs: true,
		Description: command.Description{
			Short: "say whether a graph document would be run, starting nothing",
			Long: "Loads the graph document in GRAPH_DIR as `roustabout run` does, against the same host expectations " +
				"(--issues-moxin, --commit-program) and the pinned grammar (--grammar with --grammar-digest), and prints one " +
				"JSON object on stdout. It reads files only: no request, no juggler, no scratch directory. Exit 0: the " +
				"document would be run. 2: refused, with the reason.",
		},
		RunCLI: func(_ context.Context, raw json.RawMessage) error {
			var a struct {
				Args []string `json:"args"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return err
			}
			os.Exit(executeCheck(a.Args, os.Stdout, os.Stderr))
			return nil
		},
	}
}
