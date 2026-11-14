package factory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
)

// A proposal with an unsupported replay can't be built yet. The command
// adapters cover ordinary TypeScript/Python and existing TypeScript; existing
// Python still needs its authenticated adapter. A person
// ratifies from the proposal's comment, so it says so, by name. One that
// calls no bug current, or whose code Go's own checks cover, says nothing.
func TestAProposalSaysItsCurrentBugsCantBeReplayedYet857(t *testing.T) {
	const note = "**This can't be built yet.** It calls `PutWhenFull` current, so the gate has to replay it against the code, and this route cannot execute the required replay. " +
		"A build would stop there, at `replay-unavailable`. Existing Python applications need an authenticated replay adapter."
	for _, tc := range []struct {
		name     string
		behavior string
		shape    func(*formalize.Proposal)
		want     []string
	}{
		{"a current bug in TypeScript", "current", func(p *formalize.Proposal) { p.Language = "typescript" }, nil},
		{"a current bug in Python", "current", func(p *formalize.Proposal) { p.Language = "python" }, nil},
		{"a current bug in code checked as it is", "current", func(p *formalize.Proposal) { p.Existing = []string{"hooks/use-buffer.ts"} }, nil},
		{"existing Python", "current", func(p *formalize.Proposal) {
			p.Target = &formalize.Target{Manifest: project.Manifest{Language: "python", Existing: []string{"app.py"}}}
		}, []string{"PutWhenFull"}},
		{"a current bug in Go", "current", func(*formalize.Proposal) {}, nil},
		{"a hypothetical bug", "hypothetical", func(p *formalize.Proposal) { p.Language = "typescript" }, nil},
		{"a bug an older lock left unclassified", "", func(p *formalize.Proposal) { p.Language = "typescript" }, nil},
	} {
		p := bufferProposal(t)
		classify857(t, p, tc.behavior)
		tc.shape(p)
		if got := Unreplayed(p); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: the gate can't yet replay %v; want %v", tc.name, got, tc.want)
		}
		body := proposalComment(p, nil, Marker{Kind: KindProposal, Proposal: p})
		if said := strings.Contains(body, "\n"+note+"\n"); said != (tc.want != nil) || strings.Contains(body, "can't be built yet") != (tc.want != nil) {
			t.Errorf("%s: the proposal's comment says it can't be built: %v; want %v\n%s", tc.name, said, tc.want != nil, body)
		}
	}

	// An amendment says it too, of the project as it's built today, and
	// counts in its own words when there's more than one.
	p := bufferProposal(t)
	classify857(t, p, "current")
	second := p.Statements[len(p.Statements)-1]
	second.Name = "DropWhenFull"
	p.Statements = append(p.Statements, second)
	c := &formalize.Current{ModuleText: p.ModuleText, Includes: p.Includes, Lock: project.Lock{Bounds: p.Bounds, Statements: append([]project.Statement(nil), p.Statements...)}}
	p.Target = &formalize.Target{Dir: "examples/buffer", Previous: "#1", Amends: p.Hash, Manifest: project.Manifest{Language: "python", Existing: []string{"app.py"}}}
	body := amendmentComment(p, nil, formalize.Diff(c, p), Marker{Kind: KindProposal, Proposal: p})
	const both = "**This can't be built yet.** It calls `PutWhenFull` and `DropWhenFull` current, so the gate has to replay each against the code, and this route cannot execute the required replay. " +
		"A build would stop there, at `replay-unavailable`. Existing Python applications need an authenticated replay adapter."
	if !strings.Contains(body, both+"\n\n") {
		t.Errorf("an amendment with two current bugs doesn't say it can't be built:\n%s", body)
	}
	if strings.Contains(body, "stops calling") {
		t.Errorf("an amendment that lowers no bug says it does:\n%s", body)
	}
}

// An amendment that stops calling a bug current says so above its table. The
// gate replays only a current bug against the code, so the amendment ends
// that for the bug while every word of the statement stays as it was. In
// Go the gate replays nothing, and the amendment says only what it stops
// saying of the code.
func TestAnAmendmentSaysWhichBugsItStopsCallingCurrent857(t *testing.T) {
	old := bufferProposal(t)
	classify857(t, old, "current")
	second := old.Statements[len(old.Statements)-1]
	second.Name = "DropWhenFull"
	old.Statements = append(old.Statements, second)
	c := &formalize.Current{ModuleText: old.ModuleText, Includes: old.Includes, Lock: project.Lock{Bounds: old.Bounds, Statements: append([]project.Statement(nil), old.Statements...)}}
	language := "python"
	amend := func(behavior map[string]string) string {
		p := bufferProposal(t)
		p.Statements = append([]project.Statement(nil), old.Statements...)
		for i := range p.Statements {
			if b, ok := behavior[p.Statements[i].Name]; ok {
				p.Statements[i].CodeBehavior = b
			}
		}
		p.Hash = project.ProposalHash(p.Bounds, p.Statements)
		p.Target = &formalize.Target{Dir: "examples/buffer", Previous: "#1", Amends: old.Hash, Manifest: project.Manifest{Language: language}}
		return amendmentComment(p, nil, formalize.Diff(c, p), Marker{Kind: KindProposal, Proposal: p})
	}
	one := "**This stops calling `PutWhenFull` current.** The gate has to replay a current bug against the code, and it would no longer replay that one.\n\n"
	if body := amend(map[string]string{"PutWhenFull": "hypothetical"}); !strings.Contains(body, one) || strings.Index(body, one) > strings.Index(body, "| Statement |") {
		t.Errorf("an amendment that makes a current bug hypothetical doesn't say so above its table:\n%s", body)
	}
	if body := amend(map[string]string{"PutWhenFull": ""}); !strings.Contains(body, one) {
		t.Errorf("an amendment that takes a current bug's classification away doesn't say so:\n%s", body)
	}
	two := "**This stops calling `PutWhenFull` and `DropWhenFull` current.** The gate has to replay a current bug against the code, and it would no longer replay those.\n\n"
	if body := amend(map[string]string{"PutWhenFull": "hypothetical", "DropWhenFull": "hypothetical"}); !strings.Contains(body, two) {
		t.Errorf("an amendment that makes two current bugs hypothetical doesn't name both:\n%s", body)
	}
	if body := amend(nil); strings.Contains(body, "stops calling") {
		t.Errorf("an amendment that keeps its bugs current says it lowers one:\n%s", body)
	}
	// In Go, and in a project that names no language, which is Go, the
	// gate replays no bug, so the amendment promises the end of none.
	for _, language = range []string{"go", ""} {
		body := amend(map[string]string{"PutWhenFull": "hypothetical"})
		if !strings.Contains(body, "**This stops calling `PutWhenFull` current.** Nothing would say any more that the code has that bug today.\n\n") || strings.Contains(body, "replay") {
			t.Errorf("an amendment of a project in %q that makes a current bug hypothetical speaks of a replay, or doesn't say what it stops saying:\n%s", language, body)
		}
		body = amend(map[string]string{"PutWhenFull": "hypothetical", "DropWhenFull": ""})
		if !strings.Contains(body, "**This stops calling `PutWhenFull` and `DropWhenFull` current.** Nothing would say any more that the code has those bugs today.\n\n") {
			t.Errorf("an amendment of a project in %q that lowers two bugs doesn't name both:\n%s", language, body)
		}
	}
	language = "python"
	// A current bug whose words change, and that stays current, isn't lowered.
	reworded := bufferProposal(t)
	reworded.Statements = append([]project.Statement(nil), old.Statements...)
	for i := range reworded.Statements {
		if reworded.Statements[i].Name == "PutWhenFull" {
			reworded.Statements[i].Says = "A producer overfills the buffer."
		}
	}
	reworded.Hash = project.ProposalHash(reworded.Bounds, reworded.Statements)
	reworded.Target = &formalize.Target{Dir: "examples/buffer", Previous: "#1", Amends: old.Hash}
	body := amendmentComment(reworded, nil, formalize.Diff(c, reworded), Marker{Kind: KindProposal, Proposal: reworded})
	if !strings.Contains(body, "| `PutWhenFull` | changed bug |") || strings.Contains(body, "stops calling") {
		t.Errorf("an amendment that rewords a bug it keeps current says it lowers it, or doesn't list the change:\n%s", body)
	}
}

// A bug that must break a property has a counterexample that never ends,
// and no replay can run one. Waiting for replays doesn't make such a
// proposal buildable, so its comment doesn't advise it.
func TestAProposalSaysWhichCurrentBugsCanNeverBeReplayed857(t *testing.T) {
	p := bufferProposal(t)
	classify857(t, p, "current")
	p.Language = "typescript"
	p.Statements = append(p.Statements, project.Statement{Name: "Drains", Kind: project.Property, Says: "The buffer eventually empties."},
		project.Statement{Name: "NeverTakes", Kind: project.Bug, Says: "Nothing is ever taken.", Expect: "Drains", CodeBehavior: "current"})
	body := proposalComment(p, nil, Marker{Kind: KindProposal, Proposal: p})
	const both = "**This can't be built yet.** It calls `NeverTakes` current, so the gate has to replay it against the code, and this route cannot execute the required replay. " +
		"A build would stop there, at `replay-unavailable`. `NeverTakes` must break a property, and a counterexample that never ends isn't one a replay can run, now or once replays run."
	if !strings.Contains(body, "\n"+both+"\n") {
		t.Errorf("a proposal with a bug that breaks an invariant and one that breaks a property:\n%s", body)
	}
	// With only the endless one current, waiting isn't advised at all.
	for i := range p.Statements {
		if p.Statements[i].Name == "PutWhenFull" {
			p.Statements[i].CodeBehavior = "hypothetical"
		}
	}
	body = proposalComment(p, nil, Marker{Kind: KindProposal, Proposal: p})
	const only = "**This can't be built yet.** It calls `NeverTakes` current, so the gate has to replay it against the code, and this route cannot execute the required replay. " +
		"A build would stop there, at `replay-unavailable`. `NeverTakes` must break a property, and a counterexample that never ends isn't one a replay can run, now or once replays run."
	if !strings.Contains(body, "\n"+only+"\n") || strings.Contains(body, "Ratify it once replays run") {
		t.Errorf("a proposal whose only current bug breaks a property:\n%s", body)
	}
}
