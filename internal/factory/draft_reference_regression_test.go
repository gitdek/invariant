package factory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/verify"
)

func TestDraftReferenceConflictingEnvelopeFields(t *testing.T) {
	ref := saved790Reference{1, "o/r", 790, "draft-issue", strings.Repeat("a", 40), "sha256:" + strings.Repeat("b", 64), ""}
	raw := string(reader603JSON(t, ref))
	for _, body := range []string{
		`{"kind":"forks","proposal_ref":` + strings.Replace(raw, `"version":1`, `"version":2,"version":1`, 1) + `}`,
		`{"kind":"forks","proposal_ref":null,"proposal_ref":` + raw + `}`,
		`{"kind":"forks","proposal_ref":` + strings.Replace(raw, `,"proposal_hash":""`, "", 1) + `}`,
		`{"kind":17,"proposal_ref":` + raw + `}`,
		`{"kind":"unknown","proposal_ref":` + raw + `}`,
	} {
		m, ok := DecodeMarker("<!-- invariant:" + base64.StdEncoding.EncodeToString([]byte(body)) + " -->")
		if !ok {
			t.Fatal("malformed reference disappeared instead of blocking fallback")
		}
		hydrated, result, err := m.ResolveDraft(t.Context(), "o/r", 790,
			func(context.Context, int, string) (RunState, string, error) {
				t.Fatal("malformed envelope read a run")
				return RunNone, "", nil
			}, nil)
		if err == nil || hydrated.Proposal != nil || result != nil {
			t.Fatalf("malformed reference returned usable state: %v", err)
		}
	}
	// Inline readers need neither a repository nor saved-run callbacks.
	f := Factory{Self: "factory"}
	if _, ok, err := f.draftPost(t.Context(), 790, github.Comment{User: github.User{Login: "factory"}, Body: (Marker{Kind: KindNote}).encode()}); err != nil || !ok {
		t.Fatalf("inline marker acquired a storage dependency: %v", err)
	}
}

func TestDraftReferenceReceiptCannotBeRebound(t *testing.T) {
	good := saved790Result(t, false)
	for name, mutate := range map[string]func(*formalize.Result){
		"source":         func(r *formalize.Result) { r.Report.Reader.Inputs.Model = "sha256:" + strings.Repeat("0", 64) },
		"pin":            func(r *formalize.Result) { r.Report.Pins[0].Match = false },
		"bounds":         func(r *formalize.Result) { r.Report.Bounds["Limit"] = "1" },
		"model outcome":  func(r *formalize.Result) { r.Report.Design.Passed = false },
		"counts":         func(r *formalize.Result) { r.Report.Design.DistinctStates++ },
		"tool":           func(r *formalize.Result) { r.Report.Toolchain.JavaImage = "another-image" },
		"reader receipt": func(r *formalize.Result) { r.Report.Reader = nil },
	} {
		t.Run(name, func(t *testing.T) {
			var broken formalize.Result
			if err := json.Unmarshal(reader603JSON(t, good), &broken); err != nil {
				t.Fatal(err)
			}
			mutate(&broken)
			// Even a self-consistent report fingerprint and a correct transport
			// digest cannot replace the checked proposal's pins and evidence.
			broken.Report.Fingerprint = verify.Fingerprint(*broken.Report)
			payload := reader603JSON(t, &broken)
			ref := saved790Reference{1, "o/r", 790, "draft-issue", strings.Repeat("a", 40), saved790Digest(payload), good.Proposal.Hash}
			m, ok := DecodeMarker(saved790Wire(t, saved790Envelope(ref, good.Proposal)))
			if !ok {
				t.Fatal("valid reference envelope did not decode")
			}
			hydrated, result, err := m.ResolveDraft(t.Context(), "o/r", 790,
				func(context.Context, int, string) (RunState, string, error) { return RunDone, ref.Commit, nil },
				func(context.Context, string, string) ([]byte, error) { return payload, nil })
			if err == nil || hydrated.Proposal != nil || result != nil {
				t.Fatal("substituted receipt became proposal authority")
			}
		})
	}
}
