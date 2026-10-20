// Package request parses the issue-filer request and derives what the run
// takes from it: the run key, the run's input document, the drafter's task
// and the labels the commit files with. It is the glue's input handling
// (circus infra/hosts/pkgs/issue-filer-glue) as pure functions.
package request

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	requestIDPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	originPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	notifyPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}@[a-z0-9.-]{1,253}$`)
	recordedAtPattern = regexp.MustCompile(`^[0-9]{1,16}$`)
	repoHintPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

// Request is one issue-filer request. Fields of the wrong JSON type read as
// empty, as the glue's jq does; only a malformed value of a field that is
// present with the right type is refused.
type Request struct {
	Text      string
	RepoHint  string
	RequestID string
	Origin    string
	Notify    string
	// RecordedAtMs is epoch milliseconds as digits, or empty.
	RecordedAtMs string
}

// Refusal is an unusable request (the glue's exit 2).
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return "refused: " + r.Reason }

func refuse(format string, args ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, args...)}
}

// Parse reads and checks a request.
func Parse(raw []byte) (*Request, error) {
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return nil, refuse("input is not a JSON object with a non-empty text")
	}
	r := &Request{}
	text, isString := stringField(fields, "text")
	if !isString || strings.TrimSpace(text) == "" {
		return nil, refuse("input is not a JSON object with a non-empty text")
	}
	r.Text = text
	r.RepoHint, _ = stringField(fields, "repo_hint")
	r.RequestID, _ = stringField(fields, "request_id")
	if r.RequestID != "" && !requestIDPattern.MatchString(r.RequestID) {
		return nil, refuse("request_id must be 1..64 characters of [A-Za-z0-9._-]")
	}
	r.Origin, _ = stringField(fields, "origin")
	if r.Origin != "" && !originPattern.MatchString(r.Origin) {
		return nil, refuse("origin must be a short lower-case name (pebble, or a session's)")
	}
	r.Notify, _ = stringField(fields, "notify")
	if r.Notify != "" && !notifyPattern.MatchString(r.Notify) {
		return nil, refuse("notify must be a bare JID (local@domain)")
	}
	r.RecordedAtMs = recordedAtDigits(fields["recorded_at"])
	if r.RecordedAtMs != "" && !recordedAtPattern.MatchString(r.RecordedAtMs) {
		return nil, refuse("recorded_at must be epoch milliseconds (1..16 digits)")
	}
	return r, nil
}

func stringField(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := fields[name]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// recordedAtDigits is the glue's reading of recorded_at: a number is floored
// and printed without an exponent; a string is taken as it stands; anything
// else is empty.
func recordedAtDigits(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return ""
	}
	return strconv.FormatFloat(math.Floor(f), 'f', -1, 64)
}

// RunKey is CONTRACT.md's run key: from the request_id when there is one, else
// from the moment the request was received (RecordedAtMs, or nowMs) and the
// first eight hex digits of the text's sha256.
func (r *Request) RunKey(nowMs int64) string {
	if r.RequestID != "" {
		return "issue-filer-" + sha256Hex(r.RequestID)[:16]
	}
	ms := r.RecordedAtMs
	if ms == "" {
		ms = strconv.FormatInt(nowMs, 10)
	}
	return "issue-filer-" + ms + "-" + sha256Hex(r.Text)[:8]
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// RecordedAt is the moment the request was received, in whole seconds, as
// RFC 3339 UTC; now stands in when the request carries none.
func (r *Request) RecordedAt(now time.Time) string {
	if r.RecordedAtMs == "" {
		return now.UTC().Format("2006-01-02T15:04:05Z")
	}
	ms, err := strconv.ParseInt(r.RecordedAtMs, 10, 64)
	if err != nil {
		return now.UTC().Format("2006-01-02T15:04:05Z")
	}
	return time.Unix(ms/1000, 0).UTC().Format("2006-01-02T15:04:05Z")
}

// RunInput is the document the run's room is started with: the text and
// recorded_at, plus the repo hint when it is a non-empty string.
func (r *Request) RunInput(now time.Time) ([]byte, error) {
	doc := struct {
		Text       string `json:"text"`
		RecordedAt string `json:"recorded_at"`
		RepoHint   string `json:"repo_hint,omitempty"`
	}{r.Text, r.RecordedAt(now), r.RepoHint}
	return json.Marshal(doc)
}

// Task is what the drafter is asked: the text, then, when the hint is an
// <owner>/<name>, a preferred-repository line. A hint of any other shape is
// left out (it would reach a prompt as text); leftOut reports that.
func (r *Request) Task() (task string, hintLeftOut bool) {
	switch {
	case repoHintPattern.MatchString(r.RepoHint):
		return r.Text + "\n\nPreferred repository: " + r.RepoHint, false
	case r.RepoHint != "":
		return r.Text, true
	default:
		return r.Text, false
	}
}

// Labels are the labels the commit files with (circus#322): only a Pebble
// request is labelled as one.
func (r *Request) Labels() []string {
	if r.Origin == "pebble" {
		return []string{"zz-from_pebble"}
	}
	return []string{}
}
