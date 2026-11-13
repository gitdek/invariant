package factory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestDraftReferenceFailedAnswerKeepsOversizedCarrier(t *testing.T) {
	for _, amend := range []bool{false, true} {
		t.Run(fmt.Sprintf("amend=%v", amend), func(t *testing.T) {
			tc, _ := reader603Tools(t)
			ws := t.TempDir()
			reader603Workspace(t, ws)
			reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", maxMarker+1024)+"\n====", 1))
			old, _ := reader603Checked(t, ws, tc)
			r := newRig(t)
			saved790Open(t, r)
			req := formalize.Request{Previous: old}
			dir, work := "examples/saved-carrier", ""
			if amend {
				work = t.TempDir()
				git(t, work, "clone", "--quiet", r.origin, ".")
				if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 600, By: "gitdek", Proposal: old.Hash}); err != nil {
					t.Fatal(err)
				}
				git(t, work, "add", "-A")
				git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed baseline")
				git(t, work, "push", "--quiet", "origin", "HEAD:main")
				var err error
				req.Current, err = r.f.current(t.Context(), dir)
				if err != nil || req.Current == nil {
					t.Fatalf("capture amendment: %v", err)
				}
				r.gh.issues[790].Body = "Project: " + dir + "\n/invariant solve"
			}
			authority := req.CheckContext()
			preview639Write(t, ws, preview639Draft(t, 1, nil))
			p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
			if err != nil {
				t.Fatal(err)
			}
			if amend {
				if err := p.Amend(req.Current); err != nil {
					t.Fatal(err)
				}
			}
			carrier := p.Carrier()
			if len(reader603JSON(t, carrier)) <= maxMarker {
				t.Fatal("carrier must exceed the expanded marker limit")
			}
			ref := saved790Save(t, r, &formalize.Result{Proposal: p, CheckContext: &authority}, "draft-issue")
			saved790Post(t, r, ref, p)
			var received []formalize.Request
			r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
				received = append(received, q)
				return &formalize.Result{Problem: "fixture draft failed"}, nil
			}}
			choice := r.gh.say(790, "gitdek", "/invariant choose F1 B")
			r.poll()
			failed := r.expect(790, KindStuck, LabelHumanReview)
			m := saved790Compact(t, failed.Comment.Body, ref)
			if !m.ProposalCarrier || len(m.Answers) != 1 || m.Answers[0].Comment != choice.URL || m.Answers[0].Option != "B" {
				t.Fatal("failed final answer lost its carrier or human answer")
			}
			saved790Fresh(t, r)
			saved790Read(t, r, carrier)
			posts := len(r.gh.posts(790))
			r.poll()
			if len(received) != 1 || len(r.gh.posts(790)) != posts {
				t.Fatal("restart repeated the completed final-answer draft")
			}
			if amend {
				reader603Write(t, filepath.Join(work, dir, ".invariant/view.json"), strings.ReplaceAll(reader603View, "Working", "Moved branch"))
				reader603Write(t, filepath.Join(work, dir, old.Manifest().Module), old.ModuleText+"\\* later implementation\n")
				git(t, work, "add", "-A")
				git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "move branch after capture")
				git(t, work, "push", "--quiet", "origin", "HEAD:main")
			}
			r.gh.say(790, "gitdek", "/invariant revise")
			r.poll()
			if len(received) != 2 || len(r.build.built) != 0 {
				t.Fatal("revision must draft exactly once without building")
			}
			for _, q := range received {
				if !draftEqual(q.Previous, carrier) || !draftEqual(q.CheckContext().Baseline, authority.Baseline) ||
					len(q.Answers) != 1 || q.Answers[0].Comment != choice.URL || q.Answers[0].By != "gitdek" {
					t.Fatal("answer or revision replaced the baseline carrier or human answer")
				}
				if amend {
					a, b := q.CapturedCurrent(), req.CapturedCurrent()
					a.Reader, b.Reader = nil, nil
					if !draftEqual(a, b) {
						t.Fatal("revision recaptured the amendment from the moving branch")
					}
				}
			}
			saved790Compact(t, r.expect(790, KindStuck, LabelHumanReview).Comment.Body, ref)
			saved790Fresh(t, r)
			saved790Read(t, r, carrier)
		})
	}
}

func TestDraftReferenceRejectsFieldAliases(t *testing.T) {
	good := saved790Result(t, false)
	payload := reader603JSON(t, good)
	ref := saved790Reference{1, "o/r", 790, "draft-issue", strings.Repeat("a", 40), saved790Digest(payload), good.Proposal.Hash}
	raw := string(reader603JSON(t, ref))
	for name, body := range map[string]string{
		"version alias last":       `{"kind":"proposal","proposal_ref":` + strings.Replace(raw, `"version":1`, `"version":2,"Version":1`, 1) + `}`,
		"version alias first":      `{"kind":"proposal","proposal_ref":` + strings.Replace(raw, `"version":1`, `"Version":2,"version":1`, 1) + `}`,
		"version alias alone":      `{"kind":"proposal","proposal_ref":` + strings.Replace(raw, `"version":1`, `"Version":1`, 1) + `}`,
		"unicode version alias":    `{"kind":"proposal","proposal_ref":` + strings.Replace(raw, `"version":1`, `"version":2,"verſion":1`, 1) + `}`,
		"reference alias last":     `{"kind":"proposal","proposal_ref":null,"Proposal_Ref":` + raw + `}`,
		"reference alias first":    `{"kind":"proposal","Proposal_Ref":null,"proposal_ref":` + raw + `}`,
		"reference alias alone":    `{"kind":"proposal","Proposal_Ref":` + raw + `}`,
		"malformed alias envelope": `{"kind":17,"Proposal_Ref":` + raw + `}`,
		"kind alias":               `{"kind":"forks","Kind":"proposal","proposal_ref":` + raw + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			m, ok := DecodeMarker("<!-- invariant:" + base64.StdEncoding.EncodeToString([]byte(body)) + " -->")
			if !ok {
				t.Fatal("malformed reference disappeared and allowed fallback")
			}
			reads := 0
			hydrated, result, err := m.ResolveDraft(t.Context(), "o/r", 790,
				func(context.Context, int, string) (RunState, string, error) { reads++; return RunDone, ref.Commit, nil },
				func(context.Context, string, string) ([]byte, error) { reads++; return payload, nil })
			if err == nil || hydrated.Proposal != nil || result != nil || reads != 0 {
				t.Fatalf("noncanonical fields supplied authority or caused storage reads: %v", err)
			}
		})
	}
}

// Extend the existing checking fixture only for the required witness and bug
// outcomes. The real checker still constructs every report, pin and reader.
// The stand-in for Docker is this test binary, run for this one test with
// Docker's arguments after "--". A test file a build adds may not run code
// as the binary starts (#869), so nothing here does: without the variable
// the stand-in sets, this test does nothing.
func TestDraftReceiptDockerStandIn(t *testing.T) {
	if os.Getenv("INVARIANT_DRAFT_RECEIPT_DOCKER") != "1" {
		return
	}
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Exit(draftReceiptDocker(args))
}

func draftReceiptDocker(args []string) int {
	if len(args) == 0 || args[0] != "run" {
		return reader603Docker(args)
	}
	module := strings.TrimSuffix(args[len(args)-1], ".tla")
	violated, temporal := "", false
	switch {
	case strings.HasPrefix(module, "Witness_"):
		violated = "Invariant_Never_" + strings.TrimPrefix(module, "Witness_")
	case module == "Bug_Overflow":
		violated = "Safe"
	case module == "Bug_Starve":
		temporal = true
	default:
		return reader603Docker(args)
	}
	frame := func(code int, body string) {
		fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
	}
	frame(2262, "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)")
	if temporal {
		frame(2116, "Temporal properties were violated.")
		frame(2217, "1: <Initial predicate>\n/\\ n = 0")
		frame(2217, "2: <Advance>\n/\\ n = 1")
		frame(2122, "3: Back to state 1: <Starve>")
		return 13
	}
	frame(2110, "Invariant "+violated+" is violated.")
	frame(2217, "1: <Initial predicate>\n/\\ n = 0")
	frame(2217, "2: <Next>\n/\\ n = 502")
	return 12
}

func TestDraftReferenceRequiresAllModelOutcomes(t *testing.T) {
	tc, _ := reader603Tools(t)
	bin := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport INVARIANT_DRAFT_RECEIPT_DOCKER=1\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' -test.run='^TestDraftReceiptDockerStandIn$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ws := t.TempDir()
	reader603Workspace(t, ws)
	model := strings.Replace(reader603Model, "====", `Seen == n = 1
Done == n = Limit
Finish == <>Done
Repeat == []<>(n = 0)
FairAdvance == WF_vars(Advance)
FairReset == SF_vars(Reset)
Overflow == n' = Limit + 1
Starve == /\ n > 0 /\ n' = n - 1
====`, 1)
	reader603Write(t, filepath.Join(ws, "Reader603.tla"), model)
	var draft formalize.Draft
	if err := json.Unmarshal([]byte(reader603Draft), &draft); err != nil {
		t.Fatal(err)
	}
	// This exercises a checked reader limitation, so the original fixture's
	// invariant-only chart replay does not stand in for the additional rules.
	draft.Bounds["Limit"] = "501"
	draft.Statements = append(draft.Statements,
		project.Statement{Name: "Seen", Kind: project.Witness, Says: "An advance is reachable."},
		project.Statement{Name: "Done", Kind: project.Witness, Says: "Completion is reachable."},
		project.Statement{Name: "Overflow", Kind: project.Bug, Says: "An overflow breaks safety.", Expect: "Safe", CodeBehavior: "hypothetical"},
		project.Statement{Name: "Starve", Kind: project.Bug, Says: "Going backwards can prevent completion.", Expect: "Finish", CodeBehavior: "hypothetical"},
		project.Statement{Name: "Finish", Kind: project.Property, Says: "Eventually finish."},
		project.Statement{Name: "Repeat", Kind: project.Property, Says: "Always return to the start."},
		project.Statement{Name: "FairAdvance", Kind: project.Fairness, Says: "Advances are weakly fair."},
		project.Statement{Name: "FairReset", Kind: project.Fairness, Says: "Resets are strongly fair."})
	preview639Write(t, ws, draft)
	p, report := reader603Checked(t, ws, tc)
	if p.Reader.Limitation == nil {
		t.Fatal("receipt fixture must exercise a checked too_large limitation")
	}
	good := &formalize.Result{Proposal: p, Report: report, CheckContext: &formalize.CheckContext{}}
	options := preview639Draft(t, 1, nil)
	for i := range 2 {
		preview639Input(options, 0, i)["model"] = p
	}
	preview639Write(t, ws, options)
	question := preview639Checked(t, ws, tc)
	previews := &formalize.Result{Proposal: question, CheckContext: &formalize.CheckContext{}}
	mutations := map[string]func(*verify.Report){
		"tool provenance":               func(r *verify.Report) { r.Toolchain.JavaImage = "another-image" },
		"limitation state count":        func(r *verify.Report) { r.Design.DistinctStates++ },
		"missing witness":               func(r *verify.Report) { r.Witnesses = nil },
		"failed witness":                func(r *verify.Report) { r.Witnesses[0].Reached = false },
		"wrong witness":                 func(r *verify.Report) { r.Witnesses[0].Name = "other" },
		"changed witness words":         func(r *verify.Report) { r.Witnesses[0].Says = "other" },
		"duplicate witness":             func(r *verify.Report) { r.Witnesses[1] = r.Witnesses[0] },
		"extra witness":                 func(r *verify.Report) { r.Witnesses = append(r.Witnesses, r.Witnesses[0]) },
		"missing bug":                   func(r *verify.Report) { r.Bugs = nil },
		"uncaught bug":                  func(r *verify.Report) { r.Bugs[0].Caught = false },
		"wrong bug":                     func(r *verify.Report) { r.Bugs[0].Name = "other" },
		"wrong bug expectation":         func(r *verify.Report) { r.Bugs[0].Expect = "Repeat" },
		"wrong violation":               func(r *verify.Report) { r.Bugs[0].Violated = "Repeat" },
		"unsuccessful bug outcome":      func(r *verify.Report) { r.Bugs[0].Outcome = "failed" },
		"wrong temporal bug outcome":    func(r *verify.Report) { r.Bugs[1].Outcome = r.Bugs[0].Outcome },
		"duplicate bug":                 func(r *verify.Report) { r.Bugs[1] = r.Bugs[0] },
		"extra bug":                     func(r *verify.Report) { r.Bugs = append(r.Bugs, r.Bugs[0]) },
		"missing property":              func(r *verify.Report) { r.Properties = nil },
		"failed property":               func(r *verify.Report) { r.Properties[0].Holds = false },
		"wrong property":                func(r *verify.Report) { r.Properties[0].Name = "other" },
		"unsuccessful property outcome": func(r *verify.Report) { r.Properties[0].Outcome = "failed" },
		"duplicate property":            func(r *verify.Report) { r.Properties[1] = r.Properties[0] },
		"extra property":                func(r *verify.Report) { r.Properties = append(r.Properties, r.Properties[0]) },
		"missing fairness":              func(r *verify.Report) { r.Fairness = nil },
		"failed fairness":               func(r *verify.Report) { r.Fairness[0].InNext = false },
		"wrong fairness":                func(r *verify.Report) { r.Fairness[0].Name = "other" },
		"missing fairness action":       func(r *verify.Report) { r.Fairness[0].Action = "" },
		"duplicate fairness":            func(r *verify.Report) { r.Fairness[1] = r.Fairness[0] },
		"extra fairness":                func(r *verify.Report) { r.Fairness = append(r.Fairness, r.Fairness[0]) },
	}
	for name, base := range map[string]*formalize.Result{"selected": good, "preview": previews} {
		t.Run(name, func(t *testing.T) {
			resolve := func(r *formalize.Result) error {
				payload := reader603JSON(t, r)
				ref := saved790Reference{1, "o/r", 790, "draft-issue", strings.Repeat("a", 40), saved790Digest(payload), r.Proposal.Hash}
				m, ok := DecodeMarker(saved790Wire(t, saved790Envelope(ref, r.Proposal)))
				if !ok {
					t.Fatal("valid reference did not decode")
				}
				hydrated, result, err := m.ResolveDraft(t.Context(), "o/r", 790,
					func(context.Context, int, string) (RunState, string, error) { return RunDone, ref.Commit, nil },
					func(context.Context, string, string) ([]byte, error) { return payload, nil })
				if err != nil && (hydrated.Proposal != nil || result != nil) {
					t.Fatal("failed resolution returned usable state")
				}
				return err
			}
			if err := resolve(base); err != nil {
				t.Fatalf("complete checked outcomes refused: %v", err)
			}
			for name, mutate := range mutations {
				t.Run(name, func(t *testing.T) {
					var broken formalize.Result
					if err := json.Unmarshal(reader603JSON(t, base), &broken); err != nil {
						t.Fatal(err)
					}
					r := broken.Report
					if len(broken.Proposal.Forks) > 0 {
						r = broken.Proposal.Forks[0].Options[0].Preview.Receipt
					}
					mutate(r)
					r.Fingerprint = verify.Fingerprint(*r)
					if len(broken.Proposal.Forks) > 0 {
						f := broken.Proposal.Forks[0]
						o := f.Options[0]
						var err error
						if o.Preview.Fingerprint, err = f.PreviewFingerprint(o); err != nil {
							t.Fatal(err)
						}
						if err := broken.Proposal.ValidatePreviewContext(*broken.CheckContext); err != nil {
							t.Fatalf("fixture changed unrelated preview evidence: %v", err)
						}
					}
					if err := resolve(&broken); err == nil {
						t.Fatal("missing, mismatched or unsuccessful receipt evidence became authority")
					}
				})
			}
		})
	}
}
