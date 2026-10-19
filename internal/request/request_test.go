package request

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"
)

func parse(t *testing.T, s string) *Request {
	t.Helper()
	r, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return r
}

func TestRunKeyFromRequestID(t *testing.T) {
	r := parse(t, `{"text":"x","request_id":"req-2026-10-12-a"}`)
	sum := sha256.Sum256([]byte("req-2026-10-12-a"))
	want := "issue-filer-" + hex.EncodeToString(sum[:])[:16]
	if got := r.RunKey(1); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestRunKeyFromTextAndClock(t *testing.T) {
	r := parse(t, `{"text":"file an issue"}`)
	sum := sha256.Sum256([]byte("file an issue"))
	want := "issue-filer-1760313600123-" + hex.EncodeToString(sum[:])[:8]
	if got := r.RunKey(1760313600123); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if !regexp.MustCompile(`^issue-filer-[0-9]{12,15}-[0-9a-f]{8}$`).MatchString(want) {
		t.Fatal("key shape")
	}
}

func TestRecordedAtNamesTheKey(t *testing.T) {
	asString := parse(t, `{"text":"t","recorded_at":"1760313600123"}`)
	asNumber := parse(t, `{"text":"t","recorded_at":1760313600123}`)
	if asString.RunKey(5) != asNumber.RunKey(9) {
		t.Fatalf("string and number differ: %s %s", asString.RunKey(5), asNumber.RunKey(9))
	}
	if got := asString.RunKey(5)[:len("issue-filer-1760313600123-")]; got != "issue-filer-1760313600123-" {
		t.Fatalf("time part %s", got)
	}
	if got := asString.RecordedAt(time.Unix(0, 0)); got != "2025-10-13T00:00:00Z" {
		t.Fatalf("recorded_at %s", got)
	}
}

func TestRequestIDWinsOverRecordedAt(t *testing.T) {
	a := parse(t, `{"text":"t","request_id":"req-r3","recorded_at":"1760313600123"}`)
	b := parse(t, `{"text":"t","request_id":"req-r3"}`)
	if a.RunKey(1) != b.RunKey(2) {
		t.Fatal("request_id should decide the key alone")
	}
}

func TestRefusals(t *testing.T) {
	for name, in := range map[string]string{
		"no text":           `{"request_id":"x"}`,
		"blank text":        `{"text":"   "}`,
		"text not a string": `{"text":3}`,
		"not an object":     `[1]`,
		"not json":          `nope`,
		"bad request id":    `{"text":"t","request_id":"bad id/with slash"}`,
		"long request id":   `{"text":"t","request_id":"` + string(make([]byte, 0)) + `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		"bad origin":        `{"text":"t","origin":"Pebble Ring!"}`,
		"bad notify":        `{"text":"t","notify":"not a jid"}`,
		"iso recorded_at":   `{"text":"t","recorded_at":"2026-10-13T00:00:00Z"}`,
		"negative recorded": `{"text":"t","recorded_at":-5}`,
		"17 digit recorded": `{"text":"t","recorded_at":"12345678901234567"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(in))
			var ref *Refusal
			if !errors.As(err, &ref) {
				t.Fatalf("want a refusal, got %v", err)
			}
		})
	}
}

func TestAcceptedOddFields(t *testing.T) {
	for name, in := range map[string]string{
		"hint of wrong type is empty": `{"text":"t","repo_hint":5}`,
		"bare jid":                    `{"text":"t","notify":"b0cc-key@flic.xmpp.example.invalid"}`,
		"recorded_at null":            `{"text":"t","recorded_at":null}`,
	} {
		t.Run(name, func(t *testing.T) { parse(t, in) })
	}
}

func TestTask(t *testing.T) {
	plain := parse(t, `{"text":"file it"}`)
	if task, left := plain.Task(); task != "file it" || left {
		t.Fatalf("plain: %q %v", task, left)
	}
	good := parse(t, `{"text":"file it","repo_hint":"linenisgreat/moxy"}`)
	if task, left := good.Task(); task != "file it\n\nPreferred repository: linenisgreat/moxy" || left {
		t.Fatalf("hint: %q %v", task, left)
	}
	bad := parse(t, `{"text":"file it","repo_hint":"moxy; rm -rf /"}`)
	if task, left := bad.Task(); task != "file it" || !left {
		t.Fatalf("bad hint: %q %v", task, left)
	}
}

func TestHostileTextIsByteExact(t *testing.T) {
	text := "-rm -rf \"$(touch pwned)\" `id` 'single' \\back\nsecond line\n\ttab $HOME\n"
	in, _ := json.Marshal(map[string]string{"text": text})
	r := parse(t, string(in))
	if r.Text != text {
		t.Fatal("text changed")
	}
	if task, _ := r.Task(); task != text {
		t.Fatal("task changed")
	}
}

func TestRunInput(t *testing.T) {
	r := parse(t, `{"text":"t","repo_hint":"linenisgreat/moxy","recorded_at":"1760313600123"}`)
	b, err := r.RunInput(time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "t" || got["recorded_at"] != "2025-10-13T00:00:00Z" || got["repo_hint"] != "linenisgreat/moxy" {
		t.Fatalf("run input %v", got)
	}
	noHint, _ := parse(t, `{"text":"t"}`).RunInput(time.Unix(0, 0))
	if regexp.MustCompile(`repo_hint`).Match(noHint) {
		t.Fatal("no hint should be left out of the run input")
	}
}

func TestLabels(t *testing.T) {
	if l := parse(t, `{"text":"t","origin":"pebble"}`).Labels(); len(l) != 1 || l[0] != "zz-from_pebble" {
		t.Fatalf("pebble labels %v", l)
	}
	if l := parse(t, `{"text":"t"}`).Labels(); l == nil || len(l) != 0 {
		t.Fatalf("no origin should label nothing, got %#v", l)
	}
}
