package formalize

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// saved857 is a preview as a draft saved it before a bug carried a
// classification or a replay: a model, and its receipt.
func saved857(withBug bool) (Fork, Option) {
	r := &verify.Report{Project: "candidate", ModelOnly: true, Passed: true, Bounds: map[string]string{"Cap": "2"}}
	st := []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "s", SHA256: "sha256:a"}, {Name: "Safe", Kind: project.Invariant, Says: "i", SHA256: "sha256:b"}}
	if withBug {
		r.Bugs = []verify.Bug{{Name: "Old", Label: "old", Says: "b", Expect: "Safe", Caught: true, Outcome: "violated", Violated: "Safe", Steps: 2}}
		st = append(st, project.Statement{Name: "Old", Kind: project.Bug, Says: "b", Expect: "Safe", SHA256: "sha256:c"})
	}
	r.Fingerprint = verify.Fingerprint(*r)
	m := &Proposal{Draft: Draft{Name: "candidate", Slug: "candidate", Module: "M", Package: "m", Bounds: map[string]string{"Cap": "2"}, Statements: st}, ModuleText: "---- MODULE M ----\n====\n", View: "{}"}
	m.Hash = project.ProposalHash(m.Bounds, m.Statements)
	return Fork{ID: "F1", Question: "q?"}, Option{ID: "A", Says: "does a", Preview: &Preview{Fork: "F1", Option: "A", Model: m, Receipt: r}}
}

// A preview saved before #857 keeps the fingerprint it was saved with. The
// receipt has since gained a view of each bug's replay status, and a view
// isn't part of what was saved: without this, every fork drafted before the
// change would read as stale and lose its previews until it was drafted
// again. The two fingerprints are what the code before #857 gave these
// previews.
func TestASavedPreviewKeepsItsFingerprint857(t *testing.T) {
	for _, c := range []struct {
		withBug bool
		want    string
	}{
		{false, "sha256:b3c583d82ec3050d417fb7135e341537e1b076db2115df160f2cca110bf7d909"},
		{true, "sha256:383f1fe571a3c421a2429630459bcc59279afc202236ca2ac6e8899b6bd81a1e"},
	} {
		f, o := saved857(c.withBug)
		got, err := previewFingerprint(f, o)
		if err != nil || got != c.want {
			t.Errorf("a preview saved before #857, with a bug: %v, has fingerprint %s, %v; it was saved with %s", c.withBug, got, err, c.want)
		}
		// It's the same after the trip a saved preview makes: published as
		// JSON, with every view the receipt has today, and read back.
		b, err := json.Marshal(o.Preview)
		if err != nil {
			t.Fatal(err)
		}
		var back Preview
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		o.Preview = &back
		if got, err := previewFingerprint(f, o); err != nil || got != c.want {
			t.Errorf("read back, with a bug: %v, it has fingerprint %s, %v; it was saved with %s", c.withBug, got, err, c.want)
		}
	}
}

// The fingerprint still covers the receipt's evidence, a bug's
// classification and replay among it, and everything else in the preview.
func TestAPreviewsFingerprintCoversItsEvidence857(t *testing.T) {
	f, o := saved857(true)
	base, err := previewFingerprint(f, o)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Preview){
		"a bug's outcome":        func(v *Preview) { v.Receipt.Bugs[0].Outcome = "not violated" },
		"a bug's classification": func(v *Preview) { v.Receipt.Bugs[0].CodeBehavior = "current" },
		"a bug's replay": func(v *Preview) {
			v.Receipt.Bugs[0].Replay = &verify.BugReplayEvidence{Required: true, Status: "replay-unavailable"}
		},
		"the receipt's verdict": func(v *Preview) { v.Receipt.Passed = false },
		"no receipt":            func(v *Preview) { v.Receipt = nil },
		"a problem":             func(v *Preview) { v.Problem = "TLC stopped" },
		"a choice":              func(v *Preview) { v.Choices = map[string]string{"F0": "B"} },
		"the model's words":     func(v *Preview) { v.Model.Statements[1].Says = "another rule" },
		"the option":            func(v *Preview) { v.Option = "B" },
		"the fork":              func(v *Preview) { v.Fork = "F2" },
	} {
		f, o := saved857(true)
		change(o.Preview)
		if got, err := previewFingerprint(f, o); err != nil || got == base {
			t.Errorf("changing %s left the preview's fingerprint as it was: %s, %v", name, got, err)
		}
	}
	// The stored fingerprint is the one field that isn't part of it.
	o.Preview.Fingerprint = "sha256:anything"
	if got, err := previewFingerprint(f, o); err != nil || got != base {
		t.Errorf("the stored fingerprint changed the fingerprint: %s, %v", got, err)
	}
}

// previewIdentity stands in for Preview where the fingerprint is made, so
// it has to hold each of Preview's fields, by the same name in JSON and in
// the same order. A field added to Preview and not to it would be left out
// of every fingerprint.
func TestAPreviewsIdentityHoldsEveryField857(t *testing.T) {
	type field struct{ name, tag string }
	fields := func(x any, skip string) []field {
		var out []field
		for i, v := 0, reflect.TypeOf(x); i < v.NumField(); i++ {
			if v.Field(i).Name != skip {
				out = append(out, field{v.Field(i).Name, v.Field(i).Tag.Get("json")})
			}
		}
		return out
	}
	want, got := fields(Preview{}, "Fingerprint"), fields(previewIdentity{}, "")
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("a preview's identity holds %v; Preview holds %v", got, want)
	}
}
