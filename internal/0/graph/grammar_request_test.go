package graph

import (
	"strings"
	"testing"
)

func TestGrammarRequestTypeDefaultsToIssueFiler(t *testing.T) {
	gr, err := ParseGrammar(issueFilerSeq(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := gr.RequestType(); got != RequestIssueFiler {
		t.Fatalf("request type %q, want %q", got, RequestIssueFiler)
	}
}

func TestGrammarNamesItsRequestType(t *testing.T) {
	gr, err := ParseGrammar(issueFilerSeq(t, func(d map[string]any) { d["request"] = RequestAttest }))
	if err != nil {
		t.Fatal(err)
	}
	if got := gr.RequestType(); got != RequestAttest {
		t.Fatalf("request type %q, want %q", got, RequestAttest)
	}
}

func TestGrammarRefusesARequestTypeThisBuildDoesNotShip(t *testing.T) {
	_, err := ParseGrammar(issueFilerSeq(t, func(d map[string]any) { d["request"] = "attest/2" }))
	if err == nil || !strings.Contains(err.Error(), "request type") {
		t.Fatalf("err %v", err)
	}
}

// A grammar written before the request field must keep its digest: the field
// is left out of the document, not written as the default.
func TestGrammarWithoutRequestFieldIsUnchanged(t *testing.T) {
	if strings.Contains(string(issueFilerSeq(t, nil)), `"request"`) {
		t.Fatal("the fixture grammar carries a request field")
	}
}
