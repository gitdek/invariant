package factory

import (
	"context"
	"encoding/json"
	"html"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// #911's build got no second look, and the review that followed found what
// these tests hold. A compact comment is what a person ratifies or answers
// from, so it keeps the TLA+ of what an amendment changes and says where a
// proposal's or a question's is; no sentence in one sends its reader below to
// something that isn't there; a record saved without its publication is
// refused, never retried without end; a name in a code span reads as itself;
// and the dashboard's address names a host. The rest hold what a compact
// comment already did and no test held: the limit's last byte, a question
// that is refused rather than clipped, what a bug must break, earlier
// decisions, what #857 says of a bug, the choices an alternative was checked
// under, an answered question that stays by reference, the run a question's
// draft is saved under, and a plan of issues and a chart-free question that
// a dashboard leaves as they were.

// review911Evidence stands in for the line compactDraft links a saved
// record with, for a comment composed here without a saved record.
const review911Evidence = "\n\n[Complete saved draft and evidence](https://dashboard.example/draft/o/r/911/c/d). The exact saved record retains full sources, occurrence tables, full comparison evidence, hidden steps and provenance.\n"

// review911Reference marks a comment as one published by reference, as
// compactDraft's marker does.
var review911Reference = json.RawMessage(`{"version":1}`)

var review911Changes = regexp.MustCompile(`(?s)<details>\n<summary>What changes, in TLA\+</summary>.*?</details>`)

// review911Amendment amends the buffer's ratified statements as edit leaves
// them, and returns the project it amends as it stands.
func review911Amendment(t *testing.T, edit func(*formalize.Proposal)) (*formalize.Proposal, *formalize.Current) {
	t.Helper()
	old := bufferProposal(t)
	current := &formalize.Current{ModuleText: old.ModuleText, Includes: old.Includes,
		Lock: project.Lock{Bounds: old.Bounds, Statements: slices.Clone(old.Statements)}}
	p := bufferProposal(t)
	edit(p)
	if err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	p.Target = &formalize.Target{Dir: "examples/buffer", Previous: "#1", Amends: old.Hash, Manifest: project.Manifest{Language: "go"}}
	return p, current
}

// A compact amendment keeps the old and new TLA+ of what it changes. Each
// warning above its table sends the reader below to compare the two texts,
// and an invariant loosened in TLA+ alone, under wording that stays as it
// was, left a compact comment nothing to compare: the warning, and the same
// sentence twice. That TLA+ is what a person approves, so it counts toward
// the size a compact comment must fit and is never cut to make it fit. A
// statement the section has nothing to show for is compared in the complete
// record, and its warning says so.
func TestCompact911ReviewAmendmentKeepsWhatChangesInTLA(t *testing.T) {
	const was, now = "WithinCap == Len(buf) <= Cap", "WithinCap == Len(buf) <= Cap + 100"
	p, current := review911Amendment(t, func(p *formalize.Proposal) {
		p.ModuleText = strings.Replace(p.ModuleText, was, now, 1)
	})
	m := Marker{Kind: KindProposal, Proposal: p, ProposalRef: review911Reference}
	compact := reader603Visible(amendmentCommentWithEvidence(p, nil, formalize.Diff(current, p), m, review911Evidence))
	full := reader603Visible(amendmentComment(p, nil, formalize.Diff(current, p), Marker{Kind: KindProposal, Proposal: p}))
	const warning = "**This changes the invariant `WithinCap`.** A changed invariant can promise less than before, so compare its old and new text below."
	section := review911Changes.FindString(compact)
	if at := strings.Index(compact, warning); at < 0 || section == "" || strings.Index(compact, section) < at {
		t.Fatalf("a compact amendment's warning sends its reader below, to no TLA+:\n%s", compact)
	}
	for _, text := range []string{"before:\n\n```tla\n" + was + "\n```", "after:\n\n```tla\n" + now + "\n```"} {
		if !strings.Contains(section, text) {
			t.Errorf("what changes, in TLA+, lacks %q:\n%s", text, section)
		}
	}
	if section != review911Changes.FindString(full) {
		t.Errorf("a compact amendment shows other TLA+ than the complete comment:\n%s", section)
	}
	if strings.Contains(compact, "not in this comment") {
		t.Errorf("an amendment that shows what changes in TLA+ says it doesn't:\n%s", compact)
	}

	// The text a person approves is never cut, so one too long for a comment
	// leaves it for its caller to refuse.
	long := was + strings.Repeat(" + 0", maxComment/4)
	p, current = review911Amendment(t, func(p *formalize.Proposal) { p.ModuleText = strings.Replace(p.ModuleText, was, long, 1) })
	m = Marker{Kind: KindProposal, Proposal: p, ProposalRef: review911Reference}
	body := amendmentCommentWithEvidence(p, nil, formalize.Diff(current, p), m, review911Evidence)
	if completeComment(body, m) || !strings.Contains(body, long) || strings.Contains(body, "Some detail is left out") {
		t.Errorf("a compact amendment of %d bytes fits by leaving out or cutting the TLA+ it changes", len(body))
	}

	// A pin that changed while neither the statement's own text nor a
	// definition under it did leaves the section nothing to show.
	p, current = review911Amendment(t, func(*formalize.Proposal) {})
	current.Lock.Statements[1].SHA256 = "sha256:" + strings.Repeat("0", 64)
	m = Marker{Kind: KindProposal, Proposal: p, ProposalRef: review911Reference}
	compact = reader603Visible(amendmentCommentWithEvidence(p, nil, formalize.Diff(current, p), m, review911Evidence))
	if !strings.Contains(compact, "so compare its old and new text in the complete record.") || strings.Contains(compact, "below") || review911Changes.MatchString(compact) {
		t.Errorf("a compact amendment with no TLA+ to show still sends its reader below:\n%s", compact)
	}
}

// The amendment a watcher publishes compactly carries that section: every
// removed statement's text, and each changed one's before and after, below
// the warnings that point at them, beside the link to the complete record.
func TestCompact911ReviewPublishedAmendmentShowsWhatChanges(t *testing.T) {
	old := compact911Buffer(t, formalize.Request{}, false).Proposal
	r := compact911Rig(t)
	dir, work := "examples/buffer", t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 571, By: "gitdek", Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed every statement kind")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
		return compact911Buffer(t, q, true), nil
	}}
	r.gh.open(911, "gitdek", "Show what an amendment changes", "Project: "+dir+"\n/invariant solve")
	r.poll()
	post := r.expect(911, KindProposal, LabelProposal)
	_, _, ref, _ := compact911Hydrate(t, r, 911, post.Comment.Body)
	compact911Link(t, post.Comment.Body, compact911DashboardURL, ref)
	body := reader603Visible(post.Comment.Body)
	section := review911Changes.FindString(body)
	for _, text := range []string{
		"**`RemovedSafe`**, removed:\n\n```tla\nRemovedSafe == Len(buf) <= Cap\n```",
		"**`RemovedLive`**, removed:\n\n```tla\nRemovedLive == []<>(Len(buf) <= Cap)\n```",
		"**`RemovedFair`**, removed:\n\n```tla\nRemovedFair == WF_vars(Take)\n```",
		"**`WithinCap`**, before:\n\n```tla\nWithinCap == Len(buf) <= Cap\n```",
		"**`Live`**, before:\n\n```tla\nLive == []<>(Len(buf) = 0)\n```",
		"**`FairTake`**, before:\n\n```tla\nFairTake == WF_vars(Take)\n```",
	} {
		if !strings.Contains(section, text) {
			t.Errorf("the published amendment's TLA+ lacks %q:\n%s", text, section)
		}
	}
	for _, warning := range []string{"This changes the invariant `WithinCap`", "This changes the property `Live`"} {
		at := strings.Index(body, warning)
		if at < 0 || !strings.Contains(body[at:], "so compare its old and new text below.") || strings.Index(body, section) < at {
			t.Errorf("%q doesn't point below at the TLA+ it changes:\n%s", warning, body)
		}
	}
	if strings.Contains(body, "not in this comment") {
		t.Errorf("an amendment that shows what changes in TLA+ says it doesn't:\n%s", body)
	}
}

// review911Question checks a question's alternatives as authority has them,
// one of them carrying an include no marker can hold.
func review911Question(t *testing.T, tc toolchain.Toolchain, authority formalize.CheckContext) *formalize.Proposal {
	t.Helper()
	ws := t.TempDir()
	d := preview639Draft(t, 2, nil)
	preview639Input(d, 0, 0)["model"].(map[string]any)["includes"] = map[string]string{"Helper603.tla": strings.Replace(reader603Include, "====", "\\* "+compact911Noise()+"\n====", 1)}
	preview639Write(t, ws, d)
	p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil || p == nil || !p.HasPreviews() {
		t.Fatalf("checked question: %v", err)
	}
	return p
}

// A compact proposal or question leaves its statements' TLA+ to the complete
// record, and says so in plain words beside the link, so what a person
// ratifies or chooses isn't left out unsaid. Neither sends its reader below.
func TestCompact911ReviewProposalAndQuestionSayWhereTheirTLAIs(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, c := range []struct {
		name, kind, label, says string
		form                    func() Formalizer
	}{
		{"proposal", KindProposal, LabelProposal, " The statements' TLA+ is in that record, not in this comment.", func() Formalizer {
			return &markerCapacityFormalizer{result: compact911Check(t, tc, "incompressible", formalize.CheckContext{})}
		}},
		{"question", KindForks, LabelAsking, " Each alternative's statements are in that record in TLA+, not in this comment.", func() Formalizer {
			return reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
				return &formalize.Result{Proposal: review911Question(t, tc, q.CheckContext())}, nil
			}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := compact911Rig(t)
			r.f.Formalizer = c.form()
			r.gh.open(911, "gitdek", "Say where the TLA+ is", "/invariant solve")
			r.poll()
			post := r.expect(911, c.kind, c.label)
			_, _, ref, _ := compact911Hydrate(t, r, 911, post.Comment.Body)
			link := compact911Link(t, post.Comment.Body, compact911DashboardURL, ref)
			body := reader603Visible(post.Comment.Body)
			if !strings.Contains(body, "[Complete saved draft and evidence]("+link+")."+c.says+" The exact saved record retains") {
				t.Errorf("a compact %s doesn't say beside its link where its statements' TLA+ is:\n%s", c.name, body)
			}
			if strings.Contains(body, "```tla") || strings.Contains(body, "below") {
				t.Errorf("a compact %s shows TLA+ it says it leaves out, or sends its reader below:\n%s", c.name, body)
			}
		})
	}
}

// A completed record saved without its publication, as records were before a
// draft's run kept it, can't be named by a reference: a reference holds a
// record to the check context saved with it, and that record has none. One
// that overflows is refused as it is with no dashboard, in the same words,
// once, and a revision then drafts a record a reference can name. With a
// dashboard it used to fail every poll and post nothing, and no later
// command got through.
func TestCompact911ReviewLegacyRecordIsRefusedAsWithNoDashboard(t *testing.T) {
	tc, _ := reader603Tools(t)
	checked := compact911Check(t, tc, "compressed", formalize.CheckContext{})
	for _, c := range []struct {
		name, kind, label string
		legacy            *formalize.Result
		form              func(*int) Formalizer
	}{
		{"proposal", KindProposal, LabelProposal, &formalize.Result{Proposal: checked.Proposal, Report: checked.Report}, func(calls *int) Formalizer {
			return reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) {
				*calls++
				return &formalize.Result{Proposal: checked.Proposal, Report: checked.Report}, nil
			}}
		}},
		{"question", KindForks, LabelAsking, &formalize.Result{Proposal: review911Question(t, tc, formalize.CheckContext{})}, func(calls *int) Formalizer {
			return reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
				*calls++
				return &formalize.Result{Proposal: review911Question(t, tc, q.CheckContext())}, nil
			}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			refusals := map[string]string{}
			for _, base := range []string{"", compact911DashboardURL} {
				r := newRig(t)
				r.f.DashboardURL = base
				calls := 0
				r.f.Formalizer = c.form(&calls)
				r.gh.open(911, "gitdek", "A draft saved before publication was", "/invariant solve")
				commit, err := r.f.saveResult(t.Context(), c.legacy, "a completed draft, saved without its publication", "")
				if err != nil {
					t.Fatal(err)
				}
				if err := r.f.Repo.Record(t.Context(), 911, "draft-issue", "a draft before the watcher restarted"); err != nil {
					t.Fatal(err)
				}
				if err := r.f.Repo.Finish(t.Context(), 911, "draft-issue", commit); err != nil {
					t.Fatal(err)
				}
				for poll := 1; poll <= 2; poll++ {
					if err := r.f.Poll(t.Context()); err != nil {
						t.Fatalf("dashboard %q, poll %d: a record no reference can name fails the poll instead of being refused: %v", base, poll, err)
					}
				}
				post := r.expect(911, KindStuck, LabelHumanReview)
				if len(r.gh.comments[911]) != 1 || calls != 0 || post.Marker.ProposalRef != nil {
					t.Fatalf("dashboard %q: %d comments and %d drafts for one refusal", base, len(r.gh.comments[911]), calls)
				}
				refusals[base] = reader603Visible(post.Comment.Body)
				if base == "" {
					continue
				}
				// Where no result is in hand, as for a partial answer, the
				// saved bytes say the same.
				if _, _, err := r.f.compactDraft(t.Context(), 911, "draft-issue", Marker{Kind: c.kind, Forks: c.legacy.Proposal.Forks, Proposal: c.legacy.Proposal}); err == nil || !strings.Contains(err.Error(), "saved without its publication") {
					t.Errorf("a record saved without its publication isn't told apart where its bytes are read: %v", err)
				}
				r.gh.say(911, "gitdek", "/invariant revise")
				r.poll()
				revised := r.expect(911, c.kind, c.label)
				if calls != 1 || revised.Marker.ProposalRef == nil {
					t.Fatalf("a revision of the refused record drafted %d times and published by reference: %v", calls, revised.Marker.ProposalRef != nil)
				}
				compact911Hydrate(t, r, 911, revised.Comment.Body)
			}
			if refusals[""] != refusals[compact911DashboardURL] || !strings.Contains(refusals[""], "size limit") {
				t.Errorf("a dashboard changes how a record no reference can name is refused:\n%s\n%s", refusals[""], refusals[compact911DashboardURL])
			}
		})
	}
}

// A statement's name is written in a code span, which shows its text as it
// is: an entity there is read as the name's own characters, so Type_OK read
// Type&#95;OK in a compact amendment's and a compact question's tables.
func TestCompact911ReviewNamesInCodeSpansAreLiteral(t *testing.T) {
	p, current := review911Amendment(t, func(p *formalize.Proposal) {
		p.ModuleText = strings.NewReplacer("WithinCap", "Within_Cap", "PutWhenFull", "Put_When_Full").Replace(p.ModuleText)
		for i, s := range p.Statements {
			s.Name = strings.NewReplacer("WithinCap", "Within_Cap", "PutWhenFull", "Put_When_Full").Replace(s.Name)
			s.Expect = strings.Replace(s.Expect, "WithinCap", "Within_Cap", 1)
			p.Statements[i] = s
		}
	})
	const invariant, bug = "| `Within_Cap` | invariant | The buffer never holds more than its capacity. |\n",
		"| `Put_When_Full` | bug | A producer adds a message to a full buffer. It must break `Within_Cap`. |\n"
	if table := compactStatements(p); !strings.Contains(table, invariant) || !strings.Contains(table, bug) || strings.Contains(table, "&#") {
		t.Errorf("a compact table writes a name's entities in a code span:\n%s", table)
	}
	m := Marker{Kind: KindProposal, Proposal: p, ProposalRef: review911Reference}
	amendment := reader603Visible(amendmentCommentWithEvidence(p, nil, formalize.Diff(current, p), m, review911Evidence))
	if at := strings.Index(amendment, "**Complete candidate statements:**"); at < 0 || !strings.Contains(amendment[at:], invariant) || !strings.Contains(amendment[at:], bug) {
		t.Errorf("a compact amendment's candidate statements don't read as their names:\n%s", amendment)
	}
	question := reader603Visible(compactForksComment(review911Alternatives(p), Marker{Kind: KindForks, ProposalRef: review911Reference}, review911Evidence))
	if strings.Count(question, invariant) != 2 || strings.Count(question, bug) != 2 || strings.Contains(question, "&#95;") {
		t.Errorf("a compact question's alternatives don't read as their names:\n%s", question)
	}
}

// review911Alternatives is one question of two alternatives that share
// model, checked after F1 was answered B, and too large to chart.
func review911Alternatives(model *formalize.Proposal) []formalize.Fork {
	limited := *model
	limited.Reader = &formalize.Reader{Version: 1, Limitation: &statechart.Limitation{Code: "too_large", Actual: 501, Limit: statechart.MaxStates}}
	preview := func(option string) *formalize.Preview {
		return &formalize.Preview{Fork: "F2", Option: option, Choices: map[string]string{"F1": "B"}, Model: &limited,
			Receipt: &verify.Report{Passed: true, ModelOnly: true, Design: verify.Design{Passed: true, DistinctStates: 501}}}
	}
	return []formalize.Fork{{ID: "F2", Question: "How long may the buffer grow?", Options: []formalize.Option{
		{ID: "A", Says: "Up to its capacity.", Preview: preview("A")},
		{ID: "B", Says: "Without limit.", Preview: preview("B")},
	}}}
}

// The dashboard's address names a host: a DNS name or an IP address, with a
// port in range if it gives one. Parsing a URL takes more in a host, and a
// link writes its host as it was given, so https://dash.example) passed and
// ended the Markdown link of every compact comment at the parenthesis. Such
// an address is refused where the others are, before any work. One on this
// machine is still a host.
func TestCompact911ReviewDashboardAddressNamesAHost(t *testing.T) {
	ref := DraftReference{Version: 1, Repository: "owner/saved", Issue: 911, Commit: strings.Repeat("a", 40), SHA256: "sha256:" + strings.Repeat("b", 64)}
	locator := "/draft/owner/saved/911/" + ref.Commit + "/" + strings.Repeat("b", 64)
	for _, base := range []string{
		"https://dash.example)", "https://dash(.example", "https://dash<x>.example", `https://dash"x.example`, "https://dash'x.example",
		"https://dash.example*", "https://dash!.example", "https://dash.exam\u00a0ple", "https://dash_board.example",
		"https://./", "https://dash..example", "https://-dash.example", "https://dash-.example", "https://" + strings.Repeat("a", 64) + ".example",
		"https://dash.example:", "https://dash.example:0", "https://dash.example:65536", "http://[fe80::1%25en0]:8484",
	} {
		if err := ValidateDashboardURL(base); err == nil || !strings.Contains(err.Error(), "dashboard-url") {
			t.Errorf("%q passes as a dashboard's address: %v", base, err)
			continue
		}
		if link, err := ref.URL(base); err == nil {
			t.Errorf("%q makes the link %q", base, link)
		}
		// A watcher with no services would fail at its first use of one.
		f := &Factory{DashboardURL: base}
		if f.Prepare(t.Context()) == nil || f.Poll(t.Context()) == nil {
			t.Errorf("a watcher starts work with the dashboard address %q", base)
		}
	}
	for base, want := range map[string]string{
		"https://dashboard.example":           "https://dashboard.example" + locator,
		"https://Dashboard.Example./install/": "https://Dashboard.Example./install" + locator,
		"https://xn--bcher-kva.example:8443":  "https://xn--bcher-kva.example:8443" + locator,
		"http://localhost:8484":               "http://localhost:8484" + locator,
		"http://127.0.0.1:8484/prefix":        "http://127.0.0.1:8484/prefix" + locator,
		"http://[::1]:8484":                   "http://[::1]:8484" + locator,
		"https://203.0.113.7":                 "https://203.0.113.7" + locator,
	} {
		if err := ValidateDashboardURL(base); err != nil {
			t.Errorf("%q is refused as a dashboard's address: %v", base, err)
		}
		if link, err := ref.URL(base); err != nil || link != want {
			t.Errorf("%q makes the link %q, want %q: %v", base, link, want, err)
		}
	}
}

// A comment of exactly the limit is complete, and one byte more is not. The
// limit counts bytes, so a character of several counts as several.
func TestCompact911ReviewLimitIsItsLastByte(t *testing.T) {
	m := Marker{Kind: KindNote}
	for body, want := range map[string]bool{
		strings.Repeat("x", maxComment):         true,
		strings.Repeat("x", maxComment+1):       false,
		strings.Repeat("€", maxComment/3):       true,
		strings.Repeat("€", maxComment/3) + "x": false,
	} {
		if got := completeComment(body, m); got != want {
			t.Errorf("a comment of %d bytes is complete: %v, want %v", len(body), got, want)
		}
	}
	if completeComment("", Marker{Kind: KindProposal, Proposal: &formalize.Proposal{ModuleText: strings.Repeat("x", maxMarker+1)}}) {
		t.Error("a marker that can't be read back within its limit is complete")
	}
}

// review911StepQuestion asks whether a modeled step of parent #10 fits one
// change, in a question of the caller's length: the one question a modeled
// draft may ask without a chart.
type review911StepQuestion struct{ question string }

func (f review911StepQuestion) Formalize(context.Context, formalize.Request, string) (*formalize.Result, error) {
	fork := formalize.Fork{ID: "F1", Kind: "step_fit", Question: f.question, Options: []formalize.Option{
		{ID: "A", Says: "Build the complete step as one change."},
		{ID: "B", Says: "A writer stops parent #10 with /invariant stop and requests a replacement plan on a new issue with /invariant plan."},
	}}
	return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Forks: []formalize.Fork{fork}}}}, nil
}

// A chart-free question meets a configured dashboard as any modeled draft
// does. One that fits is posted as it is without one, byte for byte. One
// whose marker can't hold it is published by reference with every word,
// where it was refused before. And one too long for a comment even so is
// refused: a comment published by reference is never clipped into a
// question a person could answer without having read it all.
func TestCompact911ReviewChartFreeQuestionWithADashboard(t *testing.T) {
	const asks = "Storage and display may not fit one reviewable pull request; are they one step of parent #10? "
	ask := func(base string, noise int) (*rig, int, Post) {
		r, _, _, child := stepRig560(t, true)
		r.f.DashboardURL = base
		r.f.Formalizer = review911StepQuestion{question: asks + compact911Noise()[:noise]}
		r.poll()
		return r, child, r.gh.last(child)
	}
	_, _, without := ask("", 0)
	_, _, with := ask(compact911DashboardURL, 0)
	if without.Marker.Kind != KindForks || with.Marker.ProposalRef != nil || with.Comment.Body != without.Comment.Body {
		t.Errorf("a dashboard changes a chart-free question that fits:\n%s", with.Comment.Body)
	}

	// Two copies of the question, too far apart to share, outgrow a marker
	// while one copy still fits a comment.
	const fits = 50000
	if _, _, refused := ask("", fits); refused.Marker.Kind != KindStuck || !strings.Contains(refused.Comment.Body, "exceeds the comment or expanded marker size limit") {
		t.Fatalf("the question's marker should outgrow a comment with no dashboard:\n%s", reader603Visible(refused.Comment.Body))
	}
	r, child, compact := ask(compact911DashboardURL, fits)
	if compact.Marker.Kind != KindForks || !slices.Contains(r.gh.labelsOf(child), LabelAsking) {
		t.Fatalf("a chart-free question that fits by reference is refused:\n%s", reader603Visible(compact.Comment.Body))
	}
	_, _, ref, _ := compact911Hydrate(t, r, child, compact.Comment.Body)
	compact911Link(t, compact.Comment.Body, compact911DashboardURL, ref)
	body := html.UnescapeString(reader603Visible(compact.Comment.Body))
	for _, want := range []string{asks + compact911Noise()[:fits], "Build the complete step as one change.", "A writer stops parent #10 with /invariant stop and requests a replacement plan on a new issue with /invariant plan.", "`/invariant choose F1 A`"} {
		if !strings.Contains(body, want) {
			t.Errorf("the compact question lacks %q", want[:min(len(want), 120)])
		}
	}
	if strings.Contains(body, "Some detail is left out") || strings.Contains(body, "TLA+") {
		t.Errorf("a compact question with no statements is clipped, or says where their TLA+ is:\n%s", body[len(body)-600:])
	}

	_, _, long := ask(compact911DashboardURL, maxComment+10000)
	visible := reader603Visible(long.Comment.Body)
	if long.Marker.Kind != KindStuck || long.Marker.ProposalRef != nil || strings.Contains(visible, "/invariant choose") || strings.Contains(visible, "Some detail is left out") || !strings.Contains(visible, "still exceed the comment or expanded marker size limit") {
		t.Errorf("a question too long for a comment is published clipped instead of refused:\n%s", visible[:min(len(visible), 900)])
	}
}

// A plan of issues, and the question asked before it, never go by reference:
// neither is modeled evidence. With a dashboard configured each is posted as
// it is without one, byte for byte, whether it fits a comment or is clipped
// to fit as it always was.
func TestCompact911ReviewPlanOfIssuesWithADashboard(t *testing.T) {
	for _, long := range []bool{false, true} {
		posts := func(base string) []string {
			r := newRig(t)
			r.f.DashboardURL = base
			plan := graphPlan()
			forks := []formalize.Fork{{ID: "F1", Question: "Does the graph show superseded decisions?",
				Options: []formalize.Option{{ID: "A", Says: "Yes, greyed out."}, {ID: "B", Says: "No, only the ones in force."}}}}
			if long {
				plan.Issues[0].Body = strings.Repeat("The dashboard serves every decision and its edges at /api/graph.json. ", 1200) + "\n\nKind: plumbing"
				forks[0].Options[0].Says = strings.Repeat("Yes, greyed out, with the decision that superseded each. ", 1400)
			}
			r.f.Formalizer = &prdPlanner{t: t, plans: []*formalize.IssuePlan{plan}, forks: forks}
			r.gh.open(40, "gitdek", "Draw the decision graph", prdBody)
			r.gh.say(40, "gitdek", "/invariant plan")
			r.poll()
			r.expect(40, KindForks, LabelAsking)
			r.gh.say(40, "gitdek", "/invariant choose F1 B")
			r.poll()
			if p := r.expect(40, KindProposal, LabelProposal).Marker.Proposal; p == nil || p.IssuePlan == nil {
				t.Fatalf("dashboard %q: the answered question should propose the plan of issues", base)
			}
			var bodies []string
			for _, p := range r.gh.posts(40) {
				if p.Marker.ProposalRef != nil || strings.Contains(p.Comment.Body, "dashboard.example") {
					t.Errorf("dashboard %q: a %s of a plan of issues goes by reference:\n%s", base, p.Marker.Kind, reader603Visible(p.Comment.Body))
				}
				if clipped := strings.Contains(p.Comment.Body, "Some detail is left out"); clipped != long {
					t.Errorf("dashboard %q: the %s is clipped: %v, want %v", base, p.Marker.Kind, clipped, long)
				}
				bodies = append(bodies, p.Comment.Body)
			}
			return bodies
		}
		if without, with := posts(""), posts(compact911DashboardURL); len(without) != 2 || !slices.Equal(without, with) {
			t.Errorf("long %v: a dashboard changes a plan of issues' %d posts", long, len(with))
		}
	}
}

// A compact comment keeps what the complete one says of its statements: what
// a known bug must break, what each bug says of the code and what the gate
// can't yet do about it (#857), and the decisions made on the issue before
// the draft.
func TestCompact911ReviewCompactCommentKeepsWhatItsStatementsSay(t *testing.T) {
	p := bufferProposal(t)
	classify857(t, p, "current")
	p.Language = "typescript"
	m := Marker{Kind: KindProposal, Proposal: p, ProposalRef: review911Reference, Answers: []formalize.Answer{
		{Fork: "F1", Question: "When the buffer is full, what should a producer do?", Option: "B", Says: "Get an <error> right away.", By: "gitdek"}}}
	proposal := reader603Visible(proposalCommentWithEvidence(p, nil, m, review911Evidence))
	for _, want := range []string{
		"| `PutWhenFull` | bug | A producer adds a message to a full buffer. It must break `WithinCap`. Code behavior: current. |\n",
		"\n**This can't be built yet.** It calls `PutWhenFull` current, so the gate has to replay it against the code",
		"\n**Decided on this issue:**\n\n- F1. When the buffer is full, what should a producer do? **B.** Get an &#60;error&#62; right away. (@gitdek)\n",
	} {
		if !strings.Contains(proposal, want) {
			t.Errorf("a compact proposal lacks %q:\n%s", want, proposal)
		}
	}

	// An amendment that stops calling a bug current says so, and lists what
	// the bug must break among its candidate statements.
	lowered, current := review911Amendment(t, func(p *formalize.Proposal) { p.Language = "python" })
	lowered.Target.Manifest.Language = "python"
	classify857(t, lowered, "hypothetical")
	for i := range current.Lock.Statements {
		if current.Lock.Statements[i].Kind == project.Bug {
			current.Lock.Statements[i].CodeBehavior = "current"
		}
	}
	m = Marker{Kind: KindProposal, Proposal: lowered, ProposalRef: review911Reference}
	amendment := reader603Visible(amendmentCommentWithEvidence(lowered, nil, formalize.Diff(current, lowered), m, review911Evidence))
	for _, want := range []string{
		"**This stops calling `PutWhenFull` current.** The gate has to replay a current bug against the code, and it would no longer replay that one.\n",
		"Code behavior: hypothetical. *(was: Code behavior: current.)*",
		"**Complete candidate statements:**\n\n| Statement | Kind | Says |\n",
		"| `PutWhenFull` | bug | A producer adds a message to a full buffer. It must break `WithinCap`. |\n",
	} {
		if !strings.Contains(amendment, want) {
			t.Errorf("a compact amendment lacks %q:\n%s", want, amendment)
		}
	}
}

// An alternative too large to chart was checked under the choices made
// before it, and a compact question says which, as one with a chart does.
func TestCompact911ReviewLimitedAlternativeKeepsItsChoices(t *testing.T) {
	forks := review911Alternatives(bufferProposal(t))
	question := reader603Visible(compactForksComment(forks, Marker{Kind: KindForks, ProposalRef: review911Reference}, review911Evidence))
	if strings.Count(question, "\nPreviously authorized choice: F1 = B.\n") != 2 || strings.Count(question, "The chart is not drawn: TLC reached 501 states") != 2 {
		t.Errorf("a compact question drops the choices its uncharted alternatives were checked under:\n%s", question)
	}
}

// A question published by reference goes on by reference as it's answered:
// the post that records a partial answer links the same complete record,
// even when the alternatives still open would fit a comment whole. Here the
// first question's alternatives hide every poll, and their complete charts
// list each one; the second's hide nothing, and fit.
func TestCompact911ReviewPartialAnswerStaysByReference(t *testing.T) {
	tc, _ := reader603Tools(t)
	compact911TopologyTools(t)
	r := compact911Rig(t)
	r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
		ws := t.TempDir()
		d := preview639Draft(t, 2, nil)
		for option := range 2 {
			model := preview639Input(d, 1, option)["model"].(map[string]any)
			model["view"] = strings.Replace(model["view"].(string), `,"hide":true`, "", 1)
		}
		preview639Write(t, ws, d)
		p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, q.CheckContext())
		if err != nil {
			t.Fatal(err)
		}
		return &formalize.Result{Proposal: p}, nil
	}}
	r.gh.open(911, "gitdek", "Answer one question at a time", "/invariant solve")
	r.poll()
	question := r.expect(911, KindForks, LabelAsking)
	_, _, ref, _ := compact911Hydrate(t, r, 911, question.Comment.Body)
	r.gh.say(911, "gitdek", "/invariant choose F1 A")
	r.poll()
	partial := r.expect(911, KindForks, LabelAsking)
	open, _, again, _ := compact911Hydrate(t, r, 911, partial.Comment.Body)
	if again != ref || len(open.Forks) != 1 || open.Forks[0].ID != "F2" {
		t.Fatalf("the partial answer names another record or other questions: %+v", open.Forks)
	}
	whole := open
	whole.ProposalRef = nil
	if body := forksComment(whole.Forks, whole); !completeComment(body, whole) {
		t.Fatalf("the alternatives still open should fit a comment whole, and take %d bytes", len(body))
	}
	compact911Link(t, partial.Comment.Body, compact911DashboardURL, ref)
	if body := reader603Visible(partial.Comment.Body); strings.Contains(body, "Unavailable model actions") || !strings.Contains(body, "F1. F1 How many advances? **A.** F1A completes after 1 advances. (@gitdek)") {
		t.Errorf("a partial answer to a question published by reference isn't compact, or loses the answer:\n%s", body)
	}
}

// A question's draft is saved under the command that asked for it, the last
// its first post answers: the issue itself, or a comment. Posts that record
// answers name the answers, not a draft, so a partial answer that has to go
// by reference finds the question's run through the post that asked every
// question.
func TestCompact911ReviewQuestionDraftStep(t *testing.T) {
	fork := func(id string) formalize.Fork {
		return formalize.Fork{ID: id, Question: id + "?", Options: []formalize.Option{{ID: "A", Says: "Yes."}, {ID: "B", Says: "No."}}}
	}
	question := func(ids ...string) *formalize.Proposal {
		p := &formalize.Proposal{}
		for _, id := range ids {
			p.Forks = append(p.Forks, fork(id))
		}
		return p
	}
	asked := func(p *formalize.Proposal, remaining int, reply ...int64) Post {
		return Post{Marker: Marker{Kind: KindForks, Forks: p.Forks[len(p.Forks)-remaining:], Proposal: p, ReplyTo: reply}}
	}
	first, second := question("F1", "F2"), question("F3", "F4")
	for _, c := range []struct {
		name  string
		posts []Post
		of    *formalize.Proposal
		want  string
	}{
		{"asked by the issue", []Post{asked(first, 2, 0)}, first, "draft-issue"},
		{"asked by a comment, after answers were recorded", []Post{asked(first, 2, 1001), asked(first, 1, 1002), asked(first, 1, 1003)}, first, "draft-1001"},
		{"asked by the last of the commands it answers", []Post{asked(first, 2, 0), asked(second, 2, 1004, 1005)}, second, "draft-1005"},
		{"an earlier question, asked again since", []Post{asked(first, 2, 0), asked(second, 2, 1005), asked(first, 2, 1009)}, first, "draft-1009"},
		{"a question no post asked whole", []Post{asked(first, 1, 1002), {Marker: Marker{Kind: KindProposal, Proposal: first, ReplyTo: []int64{0}}}}, first, ""},
	} {
		if got := questionDraftStep(Thread{Posts: c.posts}, c.of); got != c.want {
			t.Errorf("%s: the question's draft is saved under %q, want %q", c.name, got, c.want)
		}
	}
}
