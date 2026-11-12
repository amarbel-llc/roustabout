package request

import (
	"encoding/json"
	"strings"
	"testing"
)

func attestDoc(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	doc := map[string]any{
		"session_key": "roustabout/mild-yew",
		"worktree":    "/home/u/repos/roustabout/.worktrees/mild-yew",
		"merge_base":  "5e25791",
		"head":        "abf3213aa94d",
		"principal":   "91cb108c-e14f-4058-9029-2662ca4a631a",
	}
	if mutate != nil {
		mutate(doc)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseAttestReadsTheRequest(t *testing.T) {
	a, err := ParseAttest(attestDoc(t, func(d map[string]any) { d["request_id"] = "merge-1" }))
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionKey != "roustabout/mild-yew" || a.MergeBase != "5e25791" || a.Head != "abf3213aa94d" ||
		a.Principal != "91cb108c-e14f-4058-9029-2662ca4a631a" || a.RequestID != "merge-1" ||
		a.Worktree != "/home/u/repos/roustabout/.worktrees/mild-yew" {
		t.Fatalf("%+v", a)
	}
}

func TestParseAttestRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"missing session":  {func(d map[string]any) { delete(d, "session_key") }, "session_key is required"},
		"bad session":      {func(d map[string]any) { d["session_key"] = "no-slash" }, "session_key must be"},
		"relative tree":    {func(d map[string]any) { d["worktree"] = "wt/x" }, "worktree must be"},
		"dotdot tree":      {func(d map[string]any) { d["worktree"] = "/a/../b" }, "worktree must be"},
		"trailing slash":   {func(d map[string]any) { d["worktree"] = "/a/b/" }, "worktree must be"},
		"ref as base":      {func(d map[string]any) { d["merge_base"] = "origin/master" }, "merge_base must be"},
		"option as head":   {func(d map[string]any) { d["head"] = "--output=x" }, "head must be"},
		"missing":          {func(d map[string]any) { delete(d, "principal") }, "principal is required"},
		"bad principal":    {func(d map[string]any) { d["principal"] = "a b" }, "principal must be"},
		"bad request id":   {func(d map[string]any) { d["request_id"] = "bad id/x" }, "request_id must be"},
		"number as string": {func(d map[string]any) { d["head"] = 5 }, "head is required"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseAttest(attestDoc(t, c.mutate))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to contain %q", err, c.want)
			}
		})
	}
	if _, err := ParseAttest([]byte(`[1]`)); err == nil {
		t.Fatal("an array was accepted")
	}
}
