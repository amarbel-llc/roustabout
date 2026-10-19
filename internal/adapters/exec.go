// Package adapters backs the run package's ports with the real programs:
// juggler, madder, troupe, the issues moxin and ringmaster, all run as child
// processes under a time limit with a scrubbed environment.
package adapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Variables that carry a credential. The minter's pair is handed only to the
// calls that create or delete an account; the rest never reach a child. A
// literal XMPP password or resource would follow the run root's identity into
// every call made as the root, and an ambient commit token is not the run's.
var (
	mintVars   = []string{"TROUPE_MINT_USER", "TROUPE_MINT_PASSWORD_FILE"}
	scrubbed   = []string{"TROUPE_XMPP_PASSWORD", "TROUPE_XMPP_RESOURCE", "ISSUES_COMMIT_TOKEN", "ISSUES_TOKEN_FILE"}
	whitespace = regexp.MustCompile(`\s+`)
)

// Environ splits an environment into the one every child inherits and the
// minter's credential, which the account-creating calls add back.
func Environ(environ []string) (base, mint []string) {
	drop := map[string]bool{}
	for _, v := range append(append([]string{}, mintVars...), scrubbed...) {
		drop[v] = true
	}
	isMint := map[string]bool{}
	for _, v := range mintVars {
		isMint[v] = true
	}
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case isMint[name]:
			if _, v, _ := strings.Cut(kv, "="); v != "" {
				mint = append(mint, kv)
			}
		case drop[name]:
		default:
			base = append(base, kv)
		}
	}
	return base, mint
}

// Exec runs child processes.
type Exec struct {
	// Base is the environment every child starts from.
	Base []string
	// Stderr receives a replay of each child's stderr; nil discards it.
	Stderr io.Writer
	// KillAfter is how long a child gets between SIGTERM at its limit and
	// SIGKILL; default 10s.
	KillAfter time.Duration
}

// Outcome is a child's end.
type Outcome struct {
	// RC is the exit status: 124 when the limit stopped it, 127 when it could
	// not be started, 128+n when a signal n ended it.
	RC     int
	Stdout []byte
	// Stderr is the tail of the child's stderr: the last three lines on one
	// line, at most 600 characters.
	Stderr string
}

const timedOut = 124

// Run runs name with args under limit (0 for none), with stdin, in Base plus
// env.
func (x *Exec) Run(ctx context.Context, limit time.Duration, env []string, stdin []byte, name string, args ...string) Outcome {
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(append([]string{}, x.Base...), env...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	killAfter := x.KillAfter
	if killAfter == 0 {
		killAfter = 10 * time.Second
	}
	// The child leads a process group of its own, and the limit stops the whole
	// group, like timeout(1) does for the glue: TERM, then KILL after killAfter.
	// A child that forks (a commit script that runs a forge client) leaves
	// nothing running after its limit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		time.AfterFunc(killAfter, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	cmd.WaitDelay = killAfter
	err := cmd.Run()
	if x.Stderr != nil {
		_, _ = x.Stderr.Write(stderr.Bytes())
	}
	out := Outcome{Stdout: stdout.Bytes(), Stderr: tail(stderr.String())}
	switch {
	case err == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		out.RC = timedOut
		out.Stderr = "timed out after " + shortDuration(limit) + prefixSep(out.Stderr)
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out.RC = ee.ExitCode()
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				out.RC = 128 + int(ws.Signal())
			}
		} else {
			out.RC = 127
			out.Stderr = err.Error()
		}
	}
	return out
}

// shortDuration writes whole seconds as "60s", as the glue's limits read.
func shortDuration(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return d.String()
}

func prefixSep(s string) string {
	if s == "" {
		return ""
	}
	return "; " + s
}

func tail(stderr string) string {
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	s := strings.TrimSpace(whitespace.ReplaceAllString(strings.Join(lines, " "), " "))
	if r := []rune(s); len(r) > 600 {
		s = string(r[:600])
	}
	return s
}

// executable reports whether path is an executable regular file.
func executable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}
