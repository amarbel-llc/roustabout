package run

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	rr "code.linenisgreat.com/roustabout/internal/runrecord"
)

// The traffic file is the issues moxin's record of the run: one JSON object
// per line, a `call` naming the tool. Jev judges and the commit files from the
// frozen copy.

type trafficLine map[string]json.RawMessage

func readTraffic(b []byte) ([]trafficLine, error) {
	var lines []trafficLine
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("traffic line is not JSON: %w", err)
		}
		if _, ok := v.(map[string]any); !ok {
			continue // the glue keeps only objects
		}
		var l trafficLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, sc.Err()
}

func (l trafficLine) call() string {
	var s string
	_ = json.Unmarshal(l["call"], &s)
	return s
}

func (l trafficLine) str(key string) (string, bool) {
	var s string
	if err := json.Unmarshal(l[key], &s); err != nil {
		return "", false
	}
	return s, true
}

// object reads a field as an object; absent or null reads as an empty one, a
// non-object is an error (the glue's jq errors there and the state is not
// built).
func (l trafficLine) object(key string) (map[string]json.RawMessage, error) {
	raw, ok := l[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, fmt.Errorf("%s is not an object", key)
	}
	return m, nil
}

// JevState is the state the judge is shown, built from the frozen traffic
// exactly as explore-jev-approve builds it: the request, each recorded draft
// with its repo listing, the repositories as listed, the duplicate searches
// and the drafts' validation merged.
type JevState struct {
	Request string
	Drafts  []map[string]json.RawMessage
	Checks  struct {
		ReposAvailable    []string
		DuplicateSearches []map[string]json.RawMessage
	}
	Validation map[string]json.RawMessage
}

// MarshalJSON writes {request, drafts, checks, validation}.
func (s JevState) MarshalJSON() ([]byte, error) {
	drafts := s.Drafts
	if drafts == nil {
		drafts = []map[string]json.RawMessage{}
	}
	repos := s.Checks.ReposAvailable
	if repos == nil {
		repos = []string{}
	}
	searches := s.Checks.DuplicateSearches
	if searches == nil {
		searches = []map[string]json.RawMessage{}
	}
	val := s.Validation
	if val == nil {
		val = map[string]json.RawMessage{}
	}
	return json.Marshal(map[string]any{
		"request":    s.Request,
		"drafts":     drafts,
		"checks":     map[string]any{"repos_available": repos, "duplicate_searches": searches},
		"validation": val,
	})
}

// RepoListings are the repo_listing of each draft.
func (s JevState) RepoListings() []string {
	out := make([]string, 0, len(s.Drafts))
	for _, d := range s.Drafts {
		var l string
		_ = json.Unmarshal(d["repo_listing"], &l)
		out = append(out, l)
	}
	return out
}

func repoLabel(repo string, private, mirror bool, trackedAt string) string {
	s := repo
	if private {
		s += " (private)"
	}
	if mirror {
		s += " (mirror, do not file here"
		if trackedAt != "" {
			s += "; use " + trackedAt
		}
		s += ")"
	}
	return s
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// BuildJevState reads the frozen traffic and builds the judge's state.
func BuildJevState(request string, traffic []byte) (JevState, error) {
	lines, err := readTraffic(traffic)
	if err != nil {
		return JevState{}, err
	}
	st := JevState{Request: request, Validation: map[string]json.RawMessage{}}
	seen := map[string]bool{}
	for _, l := range lines {
		switch l.call() {
		case "create_issue":
			draft, err := l.object("draft")
			if err != nil {
				return JevState{}, err
			}
			d := map[string]json.RawMessage{}
			for k, v := range draft {
				d[k] = v
			}
			d["repo_listing"] = rawJSON("not listed")
			if listing, ok := l["repo_listing"]; ok {
				if t := string(bytes.TrimSpace(listing)); t != "null" && t != "false" {
					d["repo_listing"] = listing
				}
			}
			st.Drafts = append(st.Drafts, d)
			val, err := l.object("validation")
			if err != nil {
				return JevState{}, err
			}
			for k, v := range val {
				st.Validation[k] = v
			}
		case "list_repos":
			var res struct {
				Repos []struct {
					Repo      any  `json:"repo"`
					Private   bool `json:"private"`
					Mirror    bool `json:"mirror"`
					TrackedAt any  `json:"tracked_at"`
				} `json:"repos"`
			}
			if r, ok := l["result"]; ok {
				_ = json.Unmarshal(r, &res)
			}
			for _, r := range res.Repos {
				name, ok := r.Repo.(string)
				if !ok {
					continue
				}
				tracked, _ := r.TrackedAt.(string)
				label := repoLabel(name, r.Private, r.Mirror, tracked)
				if !seen[label] {
					seen[label] = true
					st.Checks.ReposAvailable = append(st.Checks.ReposAvailable, label)
				}
			}
		case "list_issues":
			s := map[string]json.RawMessage{}
			for _, k := range []string{"repo", "query", "state", "results"} {
				v, ok := l[k]
				if !ok {
					v = json.RawMessage("null")
				}
				s[k] = v
			}
			st.Checks.DuplicateSearches = append(st.Checks.DuplicateSearches, s)
		}
	}
	sort.Strings(st.Checks.ReposAvailable)
	return st, nil
}

// ReviewDraft is a draft in the exit wake's review.
type ReviewDraft struct {
	Repo        json.RawMessage `json:"repo"`
	Title       json.RawMessage `json:"title"`
	Body        *string         `json:"body,omitempty"`
	RepoListing json.RawMessage `json:"repo_listing"`
	Truncated   bool            `json:"truncated,omitempty"`
	BodyDropped bool            `json:"body_dropped,omitempty"`
}

// Review is what the judge was shown and what it answered, for the caller's
// exit wake only (circus#321): never in the result record nor the summary.
type Review struct {
	Drafts        []ReviewDraft              `json:"drafts"`
	Answers       map[string]json.RawMessage `json:"answers"`
	Validation    map[string]json.RawMessage `json:"validation"`
	Threshold     float64                    `json:"threshold"`
	BodiesDropped bool                       `json:"bodies_dropped,omitempty"`
	DraftsDropped bool                       `json:"drafts_dropped,omitempty"`
}

const (
	reviewBodyMax = 4000
	wakeBodyMax   = 32768
)

// BuildReview cuts draft bodies to 4000 characters.
func BuildReview(st JevState, answers json.RawMessage, threshold float64) Review {
	rv := Review{Drafts: []ReviewDraft{}, Answers: map[string]json.RawMessage{}, Validation: st.Validation, Threshold: threshold}
	if rv.Validation == nil {
		rv.Validation = map[string]json.RawMessage{}
	}
	_ = json.Unmarshal(answers, &rv.Answers)
	if rv.Answers == nil {
		rv.Answers = map[string]json.RawMessage{}
	}
	for _, d := range st.Drafts {
		var body string
		_ = json.Unmarshal(d["body"], &body)
		runes := []rune(body)
		rd := ReviewDraft{Repo: nullIfAbsent(d["repo"]), Title: nullIfAbsent(d["title"]), RepoListing: nullIfAbsent(d["repo_listing"])}
		if len(runes) > reviewBodyMax {
			rd.Truncated = true
			body = string(runes[:reviewBodyMax])
		}
		rd.Body = &body
		rv.Drafts = append(rv.Drafts, rd)
	}
	return rv
}

func nullIfAbsent(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}

// WakeBody is the exit wake's body: a run-exit record carrying the result
// record and, once the judge was asked, the review. It is kept under 32 KiB,
// dropping draft bodies first.
func WakeBody(runKey, requestID string, record json.RawMessage, review *Review) ([]byte, error) {
	doc := map[string]any{"kind": "run-exit", "run_key": runKey, "result": record}
	if requestID == "" {
		doc["request_id"] = nil
	} else {
		doc["request_id"] = requestID
	}
	if review != nil {
		doc["review"] = review
	}
	b, err := rr.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(b) > wakeBodyMax && review != nil {
		for i := range review.Drafts {
			review.Drafts[i].Body = nil
			review.Drafts[i].BodyDropped = true
		}
		review.BodiesDropped = true
		if b, err = rr.Marshal(doc); err != nil {
			return nil, err
		}
	}
	if len(b) > wakeBodyMax && review != nil {
		review.Drafts = []ReviewDraft{}
		review.DraftsDropped = true
		if b, err = rr.Marshal(doc); err != nil {
			return nil, err
		}
	}
	return b, nil
}

var (
	markerPattern  = regexp.MustCompile(`@@[A-Z_]+@@`)
	repoNameFormat = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

// RepoLines are the listing lines pasted into the brief: names held to the
// plain repo alphabet (they go into a TOML literal string), with the tool's own
// private and mirror notes.
func RepoLines(repos []Repo) []string {
	var out []string
	for _, r := range repos {
		if !repoNameFormat.MatchString(r.Name) {
			continue
		}
		tracked := ""
		if repoNameFormat.MatchString(r.TrackedAt) {
			tracked = r.TrackedAt
		}
		out = append(out, repoLabel(r.Name, r.Private, r.Mirror, tracked))
	}
	return out
}

// FillBrief fills the brief template: a line that is exactly @@REPO_LIST@@
// becomes the listing, and @@TRAFFIC_FILE@@ becomes the traffic file's path.
// Replacement is literal. A marker left outside the template's own "# " header
// comments is an error: it would reach the agent as text.
func FillBrief(template []byte, repoLines []string, trafficFile string) ([]byte, error) {
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(string(template), "\n") {
		if line == "" {
			continue
		}
		body := strings.TrimSuffix(line, "\n")
		nl := line[len(body):]
		if body == "@@REPO_LIST@@" {
			for _, r := range repoLines {
				out.WriteString(r + "\n")
			}
			continue
		}
		out.WriteString(strings.ReplaceAll(body, "@@TRAFFIC_FILE@@", trafficFile) + nl)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, "# ") && markerPattern.MatchString(line) {
			return nil, errors.New("the drafter's brief still has an unfilled marker")
		}
	}
	return out.Bytes(), nil
}
