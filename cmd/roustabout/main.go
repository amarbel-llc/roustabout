// roustabout is the graph runtime above juggler and ringmaster (circus
// FDR-0043). This is the scaffold: no graph runtime exists yet, only the
// `version` verb.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"code.linenisgreat.com/purse-first/libs/go-mcp/command"
)

// Injected at link time by the nix build (godyn(7), VERSION EMBEDDING).
var (
	version = "dev"
	commit  = "unknown"
)

func buildApp() *command.App {
	app := command.NewApp("roustabout", "the graph runtime above juggler and ringmaster (circus FDR-0043)")
	app.Version = version + "+" + commit

	app.AddCommand(&command.Command{
		Name:        "version",
		Description: command.Description{Short: "print the build version and commit"},
		RunCLI: func(context.Context, json.RawMessage) error {
			fmt.Println("roustabout " + app.Version)
			return nil
		},
	})

	app.AddCommand(runCommand())

	app.AddCommand(&command.Command{
		Name:            "generate-artifacts",
		Hidden:          true,
		PassthroughArgs: true,
		Description:     command.Description{Short: "write the manpage and shell completions into a directory"},
		RunCLI: func(_ context.Context, raw json.RawMessage) error {
			var a struct {
				Args []string `json:"args"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || len(a.Args) != 1 {
				return fmt.Errorf("usage: roustabout generate-artifacts DIR")
			}
			if err := app.GenerateManpages(a.Args[0]); err != nil {
				return err
			}
			return app.GenerateCompletions(a.Args[0])
		},
	})

	return app
}

func main() {
	app := buildApp()
	if err := app.RunCLI(context.Background(), os.Args[1:], command.StubPrompter{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
