package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Findings are what an attestation agent reports (docs/attestation-graph.md,
// operator ruling 2026-11-12): its final message is one JSON object,
// {"findings":[{"severity","path","summary"}]}, with an empty array when it
// found nothing. roustabout validates them and writes them to the run's
// findings file, which the check and the commit are handed by path; the check
// fails the run on any P0 to P2 finding. A message that is not exactly that
// fails the agent: an attestation that cannot read what its agents found must
// not pass.
const (
	maxFindings       = 500
	maxFindingPath    = 1000
	maxFindingSummary = 2000
)

var severityPattern = regexp.MustCompile(`^P[0-4]$`)

// Severities are the severities in the order the record lists their counts.
var Severities = []string{"P0", "P1", "P2", "P3", "P4"}

// Finding is one finding. Agent is the label of the node that reported it, set
// by roustabout, never by the agent.
type Finding struct {
	Agent    string `json:"agent"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Summary  string `json:"summary"`
}

// ParseFindings reads an agent's final message.
func ParseFindings(agent, message string) ([]Finding, error) {
	var doc struct {
		Findings *[]struct {
			Severity string `json:"severity"`
			Path     string `json:"path"`
			Summary  string `json:"summary"`
		} `json:"findings"`
	}
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(message)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil || doc.Findings == nil || dec.More() {
		return nil, fmt.Errorf(`the message is not one JSON object {"findings":[...]}`)
	}
	if len(*doc.Findings) > maxFindings {
		return nil, fmt.Errorf("%d findings, more than the %d a run keeps", len(*doc.Findings), maxFindings)
	}
	out := make([]Finding, 0, len(*doc.Findings))
	for i, f := range *doc.Findings {
		switch {
		case !severityPattern.MatchString(f.Severity):
			return nil, fmt.Errorf("finding %d: severity %q is not one of %s", i+1, f.Severity, strings.Join(Severities, ", "))
		case f.Path == "" || len(f.Path) > maxFindingPath || strings.ContainsRune(f.Path, 0):
			return nil, fmt.Errorf("finding %d: path must be 1..%d bytes", i+1, maxFindingPath)
		case strings.TrimSpace(f.Summary) == "" || len(f.Summary) > maxFindingSummary:
			return nil, fmt.Errorf("finding %d: summary must be 1..%d bytes", i+1, maxFindingSummary)
		}
		out = append(out, Finding{Agent: agent, Severity: f.Severity, Path: f.Path, Summary: f.Summary})
	}
	return out, nil
}

// appendFindings writes the findings to the findings file, one JSON object per
// line.
func appendFindings(file string, fs []Finding) error {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var buf bytes.Buffer
	for _, x := range fs {
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	_, err = f.Write(buf.Bytes())
	return err
}

// countBySeverity is the record's findings counts: every severity, so the
// shape does not depend on what was found.
func countBySeverity(fs []Finding) map[string]int {
	counts := map[string]int{}
	for _, s := range Severities {
		counts[s] = 0
	}
	for _, f := range fs {
		counts[f.Severity]++
	}
	return counts
}
