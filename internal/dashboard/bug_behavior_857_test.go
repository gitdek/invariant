package dashboard

import (
	"reflect"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

func bug857(name, behavior string) project.Statement {
	return project.Statement{Name: name, Kind: project.Bug, Says: name + " happens.", Expect: "Safe", CodeBehavior: behavior}
}

// amendment857 is a proposal that amends a lock, and that lock's statements.
// Between them is every way a bug's classification can go: kept, raised to
// current, lowered from it, classified for the first time, left
// unclassified, added, and removed.
func amendment857() (*formalize.Proposal, []project.Statement) {
	rules := []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "It follows the model."}, {Name: "Safe", Kind: project.Invariant, Says: "It's safe."}}
	old := append(slices857(rules), bug857("Kept", "current"), bug857("Raised", "hypothetical"), bug857("Lowered", "current"),
		bug857("Classified", ""), bug857("Legacy", ""), bug857("Gone", "current"), bug857("GoneToo", "hypothetical"), bug857("Unclassified", "current"))
	now := append(slices857(rules), bug857("Kept", "current"), bug857("Raised", "current"), bug857("Lowered", "hypothetical"),
		bug857("Classified", "current"), bug857("Legacy", ""), bug857("Added", "hypothetical"), bug857("Unclassified", ""))
	p := &formalize.Proposal{Draft: formalize.Draft{Name: "queue", Statements: now}, ModuleText: "---- MODULE M ----\n====\n", Hash: "sha256:0123456789abcdef"}
	return p, old
}

func slices857(s []project.Statement) []project.Statement {
	return append([]project.Statement(nil), s...)
}

// The card a person ratifies from says what each bug's statement says of
// the code, what the lock being amended said when that's something else,
// and which bugs the amendment stops calling current (#857). The gate has
// to replay a current bug against the code, so an amendment that lowers or
// drops one changes what the gate checks without changing a word a person
// would otherwise read on the card.
func TestTheRatifyCardSaysWhatEachBugSaysOfTheCode857(t *testing.T) {
	want := []Said{
		{Name: "Spec", Kind: "spec", Says: "It follows the model."},
		{Name: "Safe", Kind: "invariant", Says: "It's safe."},
		{Name: "Kept", Kind: "bug", Says: "Kept happens.", Behavior: "current"},
		{Name: "Raised", Kind: "bug", Says: "Raised happens.", Behavior: "current", Was: "hypothetical"},
		{Name: "Lowered", Kind: "bug", Says: "Lowered happens.", Behavior: "hypothetical", Was: "current"},
		{Name: "Classified", Kind: "bug", Says: "Classified happens.", Behavior: "current", Was: "not classified"},
		{Name: "Legacy", Kind: "bug", Says: "Legacy happens.", Behavior: "not classified"},
		{Name: "Added", Kind: "bug", Says: "Added happens.", Behavior: "hypothetical"},
		{Name: "Unclassified", Kind: "bug", Says: "Unclassified happens.", Behavior: "not classified", Was: "current"},
	}
	relaxed := []string{"Lowered", "Gone", "Unclassified"}
	for name, carry := range map[string]func(*formalize.Proposal, []project.Statement){
		"with its target": func(p *formalize.Proposal, old []project.Statement) {
			p.Target = &formalize.Target{Previous: "#12", Amends: "sha256:old", Baseline: &formalize.Current{Lock: project.Lock{Statements: old}}}
		},
		"with its chart's comparison": func(p *formalize.Proposal, old []project.Statement) {
			p.Target = &formalize.Target{Previous: "#12", Amends: "sha256:old"}
			p.Reader = &formalize.Reader{Version: 1, Comparison: &statechart.Comparison{Baseline: statechart.ComparisonSnapshot{Lock: project.Lock{Statements: old}}}}
		},
	} {
		p, old := amendment857()
		carry(p, old)
		w := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: p}, time.Now())
		if w == nil || !reflect.DeepEqual(w.Statements, want) {
			t.Errorf("an amendment that carries the lock it amends %s says:\n%+v\nwant:\n%+v", name, w.Statements, want)
		}
		if !reflect.DeepEqual(w.Relaxed, relaxed) {
			t.Errorf("an amendment that carries the lock it amends %s stops calling %v current; want %v", name, w.Relaxed, relaxed)
		}
	}
	// A new proposal amends nothing: each bug says what it says, and nothing
	// was anything else.
	p, _ := amendment857()
	w := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: p}, time.Now())
	for i, s := range w.Statements {
		if s.Behavior != want[i].Behavior || s.Was != "" {
			t.Errorf("a new proposal's %s says %q of the code, and that it was %q; want %q and nothing", s.Name, s.Behavior, s.Was, want[i].Behavior)
		}
	}
	if len(w.Relaxed) != 0 {
		t.Errorf("a new proposal stops calling %v current", w.Relaxed)
	}
	// The card names the bugs the gate would have to replay and can't yet:
	// those it calls current, in a project whose code a driver checks.
	if len(w.Unreplayed) != 0 {
		t.Errorf("a proposal written in Go can't be built for %v", w.Unreplayed)
	}
	p.Language = "typescript"
	if w := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: p}, time.Now()); !reflect.DeepEqual(w.Unreplayed, []string{"Kept", "Raised", "Classified"}) {
		t.Errorf("a proposal written in TypeScript can't be built for %v; want its three current bugs", w.Unreplayed)
	}
	p.Language = ""
	// An amendment that carries no lock says nothing of what was.
	p.Target = &formalize.Target{Previous: "#12", Amends: "sha256:old"}
	if w := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: p}, time.Now()); len(w.Relaxed) != 0 || w.Statements[3].Was != "" {
		t.Errorf("an amendment with no lock to compare says %q was %q, and stops calling %v current", w.Statements[3].Name, w.Statements[3].Was, w.Relaxed)
	}
}
