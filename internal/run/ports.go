// Package run is roustabout's interpreter for the issue-filer graph: the glue's
// step sequence (circus infra/hosts/pkgs/issue-filer-glue) over seams for
// everything that leaves the process. Juggler, madder, troupe and the script
// runner are interfaces here, so the whole sequence is tested against fakes;
// the exec adapters are in internal/adapters.
package run

import (
	"context"
	"encoding/json"
	"time"
)

// Identity is a run root's identity as juggler spawn --new-run reports it.
type Identity struct {
	Principal     string
	JID           string
	CredentialRef string
}

// NewRunRequest starts a run (juggler spawn --new-run). The call needs the
// minter's credential.
type NewRunRequest struct {
	RunKey      string
	InputFile   string
	RoomDomain  string
	OperatorJID string
	Issuer      string
	Limit       time.Duration
}

// NewRunResponse is what juggler spawn --new-run returned. ExitCode is the
// process status (124 for a timeout); the other fields are read from its JSON
// output and are zero when it printed none.
type NewRunResponse struct {
	ExitCode         int
	Stderr           string
	Pending          bool
	Existing         bool
	ExistingResolved *bool
	RunJob           string
	Room             string
	Root             Identity
	RootStanza       string
}

// SpawnRequest runs the drafter (juggler spawn --brief --wait) as the run
// root. The call needs the minter's credential.
type SpawnRequest struct {
	BriefFile  string
	TaskFile   string
	RunKey     string
	AsRoot     Identity
	MoxinPath  string
	AgentLimit time.Duration
	Backstop   time.Duration
}

// SpawnResponse is the drafter's end.
type SpawnResponse struct {
	ExitCode               int
	Stderr                 string
	State                  string
	Message                string
	CannotCompleteReason   string
	CannotCompleteIsObject bool
}

// DecideRequest asks the judge (juggler decide) as the run root. The payload
// is {state, questions}; the threshold is applied by the interpreter, per
// question.
type DecideRequest struct {
	RunKey  string
	Model   string
	Room    string
	Parent  string
	AsRoot  Identity
	Payload []byte
	Limit   time.Duration
	// Question is one of the graph's question names: the gating question
	// juggler insists on naming for a multi-question payload.
	Question string
}

// DecideResponse is the judge's answer. Answers is the `answers` value as
// printed, or nil when there was none.
type DecideResponse struct {
	ExitCode int
	Stderr   string
	Reason   string
	Answers  json.RawMessage
	// HasAnswersObject is whether the output held an answers object.
	HasAnswersObject bool
}

// ResolveRequest resolves the run (juggler resolve) with the result record as
// its reason. The call needs the minter's credential.
type ResolveRequest struct {
	RunJob    string
	State     string
	Record    []byte
	Artifacts []byte
	Limit     time.Duration
}

// ResolveResponse is the resolve's end.
type ResolveResponse struct {
	ExitCode int
	Stderr   string
	TornDown *bool
	// Unclean is the teardown entries that did not end ok, for the log.
	Unclean string
}

// Juggler is the four juggler calls the glue makes. clown's verbs are still
// moving; an adapter is the one place a flag change lands.
type Juggler interface {
	NewRun(ctx context.Context, r NewRunRequest) NewRunResponse
	SpawnAgent(ctx context.Context, r SpawnRequest) SpawnResponse
	Decide(ctx context.Context, r DecideRequest) DecideResponse
	Resolve(ctx context.Context, r ResolveRequest) ResolveResponse
}

// Repo is one entry of the issues moxin's list_repos.
type Repo struct {
	Name      string
	Private   bool
	Mirror    bool
	TrackedAt string
}

// ListReposRequest runs the issues moxin's list_repos in record mode against
// the traffic file, so the listing is the traffic's first line.
type ListReposRequest struct {
	TrafficFile string
	Limit       time.Duration
}

// ListReposResponse is the tool's answer.
type ListReposResponse struct {
	ExitCode int
	Stderr   string
	// OK is isError == false with a repos array.
	OK bool
	// Detail is the tool's own text, for the reason when it did not answer.
	Detail   string
	Repos    []Repo
	Complete bool
	Unlisted []string
}

// ReposLister lists the repositories the drafter may choose from.
type ReposLister interface {
	ListRepos(ctx context.Context, r ListReposRequest) ListReposResponse
}

// MadderResponse is madder write's end. Blob is "madder://blobs/<id>" or
// empty when it named none.
type MadderResponse struct {
	ExitCode int
	Stderr   string
	Output   string
	Blob     string
}

// Madder writes the frozen traffic file to the blob store.
type Madder interface {
	WriteFile(ctx context.Context, path string, limit time.Duration) MadderResponse
}

// WakeRequest is the exit wake: one troupe DM from the wake account.
type WakeRequest struct {
	From    string
	To      string
	ToVHost string
	Message string
}

// WakeResponse is troupe dm's end.
type WakeResponse struct {
	ExitCode int
	Stderr   string
}

// Troupe sends the exit wake.
type Troupe interface {
	DM(ctx context.Context, r WakeRequest) WakeResponse
}

// ScriptSpec is a script node run as a ringmaster job (FDR-0043 decision 10).
type ScriptSpec struct {
	// NodeID and Label name the graph node the job is the run of.
	NodeID  string
	Label   string
	Program string
	Args    []string
	// Env is added to a minimal environment; secrets are never inherited.
	Env   []string
	Stdin []byte
	Limit time.Duration
}

// ScriptResult is the job's end.
type ScriptResult struct {
	// JobID is the ringmaster job the node ran as.
	JobID    string
	ExitCode int
	Stdout   []byte
	Stderr   string
}

// Scripts runs script nodes: the commit and the sink.
type Scripts interface {
	RunScript(ctx context.Context, s ScriptSpec) ScriptResult
}

// Ports are the seams a run uses.
type Ports struct {
	Juggler Juggler
	Repos   ReposLister
	Madder  Madder
	Troupe  Troupe
	Scripts Scripts
}
