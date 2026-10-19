package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"code.linenisgreat.com/roustabout/internal/run"
)

// Juggler runs the four juggler calls the glue makes. clown's verbs are still
// moving (juggler is a clown verb until clown#258): a flag change is a
// one-line edit in this file. The calls were last checked against what the
// circus glue passes (clown 814d90a0, needing 93de0c9 or later); this adapter
// has not been run against a live juggler.
type Juggler struct {
	X    *Exec
	Bin  string
	Mint []string
	// UserManager runs the drafter under `systemd-run --user`.
	UserManager bool
}

func secs(d time.Duration) string { return fmt.Sprintf("%ds", int(d.Seconds())) }

// rootEnv is the run root's identity: every call made as the root.
func rootEnv(id run.Identity) []string {
	user, domain, _ := strings.Cut(id.JID, "@")
	return []string{
		"CLOWN_SESSION_ID=" + id.Principal,
		"TROUPE_XMPP_USER=" + user,
		"TROUPE_XMPP_DOMAIN=" + domain,
		"TROUPE_XMPP_PASSWORD_FILE=" + id.CredentialRef,
	}
}

// object reads a JSON object from a child's stdout; nil when it printed none.
func object(stdout []byte) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(stdout), &m) != nil {
		return nil
	}
	return m
}

func str(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func boolean(m map[string]json.RawMessage, key string) *bool {
	var b bool
	if json.Unmarshal(m[key], &b) != nil {
		return nil
	}
	return &b
}

func (j *Juggler) NewRun(ctx context.Context, r run.NewRunRequest) run.NewRunResponse {
	args := []string{
		"spawn", "--new-run", "--run-key", r.RunKey, "--input", r.InputFile,
		"--room-domain", r.RoomDomain, "--operator-jid", r.OperatorJID,
	}
	if r.Issuer != "" {
		args = append(args, "--issuer", r.Issuer)
	}
	out := j.X.Run(ctx, r.Limit, j.Mint, nil, j.Bin, args...)
	o := object(out.Stdout)
	resp := run.NewRunResponse{ExitCode: out.RC, Stderr: out.Stderr}
	if o == nil {
		return resp
	}
	if p := boolean(o, "pending"); p != nil {
		resp.Pending = *p
	}
	if e := boolean(o, "existing"); e != nil {
		resp.Existing = *e
	}
	resp.ExistingResolved = boolean(o, "resolved")
	resp.RunJob, resp.Room, resp.RootStanza = str(o, "run_job"), str(o, "room"), str(o, "root_stanza")
	resp.Root = run.Identity{Principal: str(o, "root_principal"), JID: str(o, "root_jid"), CredentialRef: str(o, "root_credential_ref")}
	return resp
}

func (j *Juggler) SpawnAgent(ctx context.Context, r run.SpawnRequest) run.SpawnResponse {
	args := []string{"spawn", "--brief", r.BriefFile, "--task", r.TaskFile, "--run-key", r.RunKey, "--wait", "--timeout", secs(r.AgentLimit)}
	if j.UserManager {
		args = append(args, "--user")
	}
	// MOXIN_PATH is set here, on the spawner, and is only the issues moxin
	// (clown c4aa51c: the agent's unit gets the spawner's MOXIN_PATH).
	env := append(append(rootEnv(r.AsRoot), j.Mint...), "MOXIN_PATH="+r.MoxinPath)
	out := j.X.Run(ctx, r.Backstop, env, nil, j.Bin, args...)
	resp := run.SpawnResponse{ExitCode: out.RC, Stderr: out.Stderr}
	if o := object(out.Stdout); o != nil {
		resp.State, resp.Message = str(o, "state"), str(o, "message")
		if cc := object(o["cannot_complete"]); cc != nil {
			resp.CannotCompleteIsObject = true
			resp.CannotCompleteReason = str(cc, "reason")
		}
	}
	return resp
}

// Decide asks the judge. The threshold is 0 here on purpose: the interpreter
// applies it per question. --question names the gating question juggler
// insists on for a multi-question payload; at threshold 0 the gate passes
// whichever is named.
func (j *Juggler) Decide(ctx context.Context, r run.DecideRequest) run.DecideResponse {
	args := []string{
		"decide", "--run-key", r.RunKey, "--model", r.Model, "--room", r.Room, "--parent", r.Parent,
		"--min-confidence", "0", "--question", "request",
	}
	out := j.X.Run(ctx, r.Limit, rootEnv(r.AsRoot), r.Payload, j.Bin, args...)
	resp := run.DecideResponse{ExitCode: out.RC, Stderr: out.Stderr}
	if o := object(out.Stdout); o != nil {
		resp.Reason = str(o, "reason")
		var answers map[string]json.RawMessage
		if json.Unmarshal(o["answers"], &answers) == nil && answers != nil {
			resp.Answers, resp.HasAnswersObject = o["answers"], true
		}
	}
	return resp
}

// Resolve is tried by the interpreter up to the attempt limit; juggler's
// resolve is idempotent (a second call finds already_resolved). The `=` form
// keeps a value that happens to start with a dash from being read as a flag.
func (j *Juggler) Resolve(ctx context.Context, r run.ResolveRequest) run.ResolveResponse {
	out := j.X.Run(ctx, r.Limit, j.Mint, nil, j.Bin, "resolve", r.RunJob,
		"--state="+r.State, "--reason="+string(r.Record), "--fallback-artifacts="+string(r.Artifacts))
	resp := run.ResolveResponse{ExitCode: out.RC, Stderr: out.Stderr}
	if o := object(out.Stdout); o != nil {
		resp.TornDown = boolean(o, "torn_down")
		var teardown []map[string]json.RawMessage
		if json.Unmarshal(o["teardown"], &teardown) == nil {
			var bad []map[string]json.RawMessage
			for _, t := range teardown {
				if ok := boolean(t, "ok"); ok == nil || !*ok {
					bad = append(bad, map[string]json.RawMessage{"jid": t["jid"], "reason": t["reason"]})
				}
			}
			if b, err := json.Marshal(bad); err == nil {
				resp.Unclean = trim(string(b), 800)
			}
		}
	}
	return resp
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
