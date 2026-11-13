package project

import (
	"encoding/json"
	"strings"
	"testing"
)

func statement857(t *testing.T, classification string) Statement {
	t.Helper()
	raw := `{"name":"Overflow","kind":"bug","says":"The existing producer overfills the buffer.","expect":"Safe","sha256":"sha256:pin"`
	if classification != "" {
		raw += `,"code_behavior":"` + classification + `"`
	}
	var s Statement
	if err := json.Unmarshal([]byte(raw+`}`), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBugClassificationIsRatifiedWithoutRehashingLegacy857(t *testing.T) {
	bounds := map[string]string{"Cap": "2"}
	old := statement857(t, "")
	// This is the exact pre-change serialization, independently fixed here.
	want := `{"name":"Overflow","kind":"bug","says":"The existing producer overfills the buffer.","expect":"Safe","sha256":"sha256:pin"}`
	b, err := json.Marshal(old)
	if err != nil || string(b) != want {
		t.Fatalf("legacy statement changed: %s, %v", b, err)
	}
	const legacyHash = "sha256:62006a0448bc4da9e96bc198e320cd41a918bb2ead511405b242d40cf5261e92"
	if got := ProposalHash(bounds, []Statement{old}); got != legacyHash {
		t.Fatalf("legacy proposal hash changed: %s", got)
	}
	seen := map[string]bool{legacyHash: true}
	for _, classification := range []string{"current", "hypothetical"} {
		s := statement857(t, classification)
		if s.CodeBehavior != classification {
			t.Fatalf("classification did not reach the statement API: %+v", s)
		}
		b, _ := json.Marshal(s)
		if !strings.Contains(string(b), `"code_behavior":"`+classification+`"`) {
			t.Errorf("classification lost: %s", b)
		}
		hash := ProposalHash(bounds, []Statement{s})
		if seen[hash] {
			t.Errorf("%s classification does not change ratification identity", classification)
		}
		seen[hash] = true
		var round Statement
		if err := json.Unmarshal(b, &round); err != nil {
			t.Fatal(err)
		}
		if ProposalHash(bounds, []Statement{round}) != hash {
			t.Fatal("classification changed in lock round trip")
		}
	}
}

func TestBugClassificationValidationKeepsLegacyLocks857(t *testing.T) {
	m := Manifest{Module: "M.tla", Code: "src", Language: "typescript", Conformance: "driver.ts"}
	for _, classification := range []string{"", "current", "hypothetical", "Current", "unknown"} {
		l := Lock{Bounds: map[string]string{"Cap": "2"}, Statements: []Statement{{Name: "Spec", Kind: Spec}, {Name: "Safe", Kind: Invariant}, statement857(t, classification)}}
		err := Validate(m, l)
		valid := classification == "" || classification == "current" || classification == "hypothetical"
		if (err == nil) != valid {
			t.Errorf("classification %q: %v", classification, err)
		}
		if !valid && err != nil && !strings.Contains(err.Error(), "code_behavior") {
			t.Errorf("error doesn't identify code_behavior: %v", err)
		}
	}
	for _, kind := range []string{Spec, Invariant, Witness, Fairness, Property} {
		s := statement857(t, "current")
		s.Kind = kind
		s.Name = "Annotated"
		s.Expect = ""
		ss := []Statement{{Name: "Spec", Kind: Spec}, {Name: "Safe", Kind: Invariant}, s}
		if kind == Spec {
			ss = ss[1:]
		}
		if err := Validate(m, Lock{Bounds: map[string]string{"Cap": "2"}, Statements: ss}); err == nil || !strings.Contains(err.Error(), "code_behavior") {
			t.Errorf("non-bug %s accepts classification: %v", kind, err)
		}
	}
}
