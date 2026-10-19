package adapters

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"code.linenisgreat.com/roustabout/internal/run"
)

// Madder writes the frozen traffic file to a blob store (madder-write(1): '-'
// reads stdin, a blob-store-id first switches the store, and the record on a
// piped stdout is NDJSON {id, size, source}).
type Madder struct {
	X     *Exec
	Bin   string
	Store string
}

func (m *Madder) WriteFile(ctx context.Context, path string, limit time.Duration) run.MadderResponse {
	data, err := os.ReadFile(path)
	if err != nil {
		return run.MadderResponse{ExitCode: 1, Stderr: err.Error()}
	}
	args := []string{"write"}
	if m.Store != "" {
		args = append(args, m.Store)
	}
	args = append(args, "-")
	out := m.X.Run(ctx, limit, nil, data, m.Bin, args...)
	resp := run.MadderResponse{ExitCode: out.RC, Stderr: out.Stderr, Output: string(out.Stdout)}
	// One record per line; the first with an id and no error names the blob.
	for _, line := range bytes.Split(out.Stdout, []byte("\n")) {
		var rec struct {
			ID    string          `json:"id"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.ID == "" {
			continue
		}
		if e := strings.TrimSpace(string(rec.Error)); e != "" && e != "null" && e != "false" && e != `""` {
			continue
		}
		resp.Blob = "madder://blobs/" + rec.ID
		break
	}
	return resp
}

// Troupe sends the exit wake from the wake account: only its own credential
// goes to troupe; the minter's was never in the base environment.
type Troupe struct {
	X        *Exec
	Bin      string
	PassFile string
}

const wakeLimit = 30 * time.Second

func (t *Troupe) DM(ctx context.Context, r run.WakeRequest) run.WakeResponse {
	out := t.X.Run(ctx, wakeLimit,
		[]string{"TROUPE_XMPP_USER=" + r.From, "TROUPE_XMPP_PASSWORD_FILE=" + t.PassFile}, nil,
		t.Bin, "dm", "--from", r.From, "--to", r.To, "--to-vhost", r.ToVHost, "--message", r.Message)
	return run.WakeResponse{ExitCode: out.RC, Stderr: out.Stderr}
}

// Repos runs the issues moxin's own list_repos, the way moxy would run it, in
// record mode against the run's traffic file: the listing is then the
// traffic's first line (design P6).
type Repos struct {
	X     *Exec
	Moxin string
}

var commandLine = regexp.MustCompile(`(?m)^command = "(.*)"$`)

func (r *Repos) ListRepos(ctx context.Context, req run.ListReposRequest) run.ListReposResponse {
	toml, _ := os.ReadFile(filepath.Join(r.Moxin, "issues", "list_repos.toml"))
	m := commandLine.FindSubmatch(toml)
	if m == nil || !executable(string(m[1])) {
		return run.ListReposResponse{Detail: "the issues moxin has no runnable list_repos command"}
	}
	out := r.X.Run(ctx, req.Limit,
		[]string{"PEBBLE_TARGET_MODE=real", "ISSUES_MODE=record", "ISSUES_TRAFFIC_FILE=" + req.TrafficFile}, nil, string(m[1]))
	resp := run.ListReposResponse{ExitCode: out.RC, Stderr: out.Stderr}

	var tool struct {
		IsError *bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Structured struct {
			Repos    json.RawMessage `json:"repos"`
			Unlisted []any           `json:"unlisted"`
			Complete *bool           `json:"complete"`
		} `json:"structuredContent"`
	}
	if json.Unmarshal(bytes.TrimSpace(out.Stdout), &tool) != nil {
		return resp
	}
	var texts []string
	for _, c := range tool.Content {
		texts = append(texts, c.Text)
	}
	resp.Detail = strings.Join(texts, " ")
	var repos []struct {
		Repo      any    `json:"repo"`
		Private   bool   `json:"private"`
		Mirror    bool   `json:"mirror"`
		TrackedAt string `json:"tracked_at"`
	}
	isArray := json.Unmarshal(tool.Structured.Repos, &repos) == nil && repos != nil
	resp.OK = tool.IsError != nil && !*tool.IsError && isArray
	resp.Complete = tool.Structured.Complete == nil || *tool.Structured.Complete
	for _, u := range tool.Structured.Unlisted {
		if s, ok := u.(string); ok {
			resp.Unlisted = append(resp.Unlisted, s)
		}
	}
	for _, rp := range repos {
		if name, ok := rp.Repo.(string); ok {
			resp.Repos = append(resp.Repos, run.Repo{Name: name, Private: rp.Private, Mirror: rp.Mirror, TrackedAt: rp.TrackedAt})
		}
	}
	return resp
}

// Scripts runs script nodes as ringmaster jobs (FDR-0043 decision 10): each
// node gets a job id from `ringmaster start`, runs under its time limit, and
// ends with `ringmaster done`, so it is observable like any other job. When
// ringmaster cannot start a job the node still runs, without one.
type Scripts struct {
	X          *Exec
	Ringmaster string
	// Source labels the emitting plugin in every record of the job.
	Source string
	Log    func(format string, args ...any)

	keyOnce sync.Once
	keyEnv  []string
}

// sessionEnv pins one ringmaster session key for every call this run makes.
// ringmaster derives a job's journal directory from the session key and, when
// neither CLOWN_SESSION_ID nor CLAUDE_SESSION_ID is set, generates a fresh
// UUIDv4 per invocation (ringmaster(1) SESSION KEY): `start` and `done` would
// then land in different channels and `done` could not find the journal.
// `done` has no --target, so the key has to come from the environment.
func (s *Scripts) sessionEnv() []string {
	s.keyOnce.Do(func() {
		for _, kv := range s.X.Base {
			name, v, _ := strings.Cut(kv, "=")
			if (name == "CLOWN_SESSION_ID" || name == "CLAUDE_SESSION_ID") && v != "" {
				return
			}
		}
		var b [16]byte
		_, _ = rand.Read(b[:])
		b[6] = b[6]&0x0f | 0x40
		b[8] = b[8]&0x3f | 0x80
		s.keyEnv = []string{fmt.Sprintf("CLOWN_SESSION_ID=%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])}
	})
	return s.keyEnv
}

func (s *Scripts) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

func (s *Scripts) RunScript(ctx context.Context, spec run.ScriptSpec) run.ScriptResult {
	var jobID string
	if s.Ringmaster != "" {
		start := s.X.Run(ctx, 30*time.Second, s.sessionEnv(), nil, s.Ringmaster, "start", "--label", spec.Label, "--source", s.Source)
		if start.RC == 0 {
			jobID = strings.TrimSpace(string(start.Stdout))
		} else {
			s.logf("ringmaster start for the %s node exited %d%s; it runs without a job", spec.Label, start.RC, paren(start.Stderr))
		}
	}
	out := s.X.Run(ctx, spec.Limit, spec.Env, spec.Stdin, spec.Program, spec.Args...)
	if jobID != "" {
		state, message := "succeeded", spec.Label+" exited 0"
		if out.RC != 0 {
			state, message = "failed", spec.Label+" exited "+strconv.Itoa(out.RC)
		}
		// A ctx stopped by a signal must still get its end recorded.
		done := s.X.Run(context.WithoutCancel(ctx), 30*time.Second, s.sessionEnv(), nil, s.Ringmaster, "done", jobID, "--state", state, "--message", message)
		if done.RC != 0 {
			s.logf("ringmaster done %s exited %d%s", jobID, done.RC, paren(done.Stderr))
		}
	}
	return run.ScriptResult{JobID: jobID, ExitCode: out.RC, Stdout: out.Stdout, Stderr: out.Stderr}
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}
