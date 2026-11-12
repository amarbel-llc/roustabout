package request

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

var (
	sessionKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	principalPattern  = regexp.MustCompile(`^[A-Za-z0-9._@:-]{1,128}$`)
)

// Attest is an attestation request (docs/attestation-graph.md): the session
// whose worktree is attested, the diff to attest as a merge base and a head,
// and the principal that asked, whose grant chain the run root is minted
// under. A grammar whose request type is attest/1 takes this and not Request.
type Attest struct {
	SessionKey string
	// Worktree is the absolute path the nodes read, read-only.
	Worktree  string
	MergeBase string
	Head      string
	Principal string
	RequestID string
}

// ParseAttest reads and checks an attestation request. Every field but
// request_id is required; a value of the wrong type reads as missing, as it
// does for Parse.
func ParseAttest(raw []byte) (*Attest, error) {
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return nil, refuse("input is not a JSON object")
	}
	var a Attest
	for _, f := range []struct {
		name string
		dst  *string
		ok   func(string) bool
		why  string
	}{
		{"session_key", &a.SessionKey, sessionKeyPattern.MatchString, "session_key must be <repo>/<session>"},
		{"worktree", &a.Worktree, absoluteClean, "worktree must be a clean absolute path"},
		{"merge_base", &a.MergeBase, commitPattern.MatchString, "merge_base must be a commit id (7..64 lower-case hex digits)"},
		{"head", &a.Head, commitPattern.MatchString, "head must be a commit id (7..64 lower-case hex digits)"},
		{"principal", &a.Principal, principalPattern.MatchString, "principal must be 1..128 characters of [A-Za-z0-9._@:-]"},
	} {
		v, _ := stringField(fields, f.name)
		if v == "" {
			return nil, refuse("%s is required", f.name)
		}
		if !f.ok(v) {
			return nil, refuse("%s", f.why)
		}
		*f.dst = v
	}
	a.RequestID, _ = stringField(fields, "request_id")
	if a.RequestID != "" && !requestIDPattern.MatchString(a.RequestID) {
		return nil, refuse("request_id must be 1..64 characters of [A-Za-z0-9._-]")
	}
	return &a, nil
}

// absoluteClean is an absolute path that names itself: no "..", no doubled or
// trailing separator, no NUL.
func absoluteClean(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.ContainsRune(p, 0) && path.Clean(p) == p
}
