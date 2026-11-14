package factory

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
)

// Since command replays run, a proposal is unbuildable for two reasons, and
// its notice names the status the gate would give each: an existing Python
// application has no replay adapter (replay-unavailable), and a bug that
// must break a property has a counterexample no replay can run
// (replay-unsupported-temporal). Neither waits for replays to run.
func TestTheUnbuildableNoticeNamesTheReplayStatus913(t *testing.T) {
	statements := []project.Statement{
		{Name: "Safe", Kind: project.Invariant, Says: "Nothing is delivered twice."},
		{Name: "Drains", Kind: project.Property, Says: "The queue eventually empties."},
		{Name: "NeverTakes", Kind: project.Bug, Says: "Nothing is ever taken.", Expect: "Drains", CodeBehavior: "current"},
	}
	temporal := &formalize.Proposal{Draft: formalize.Draft{Name: "queue", Language: "typescript", Statements: statements}}
	note := unbuildable(temporal)
	if !strings.Contains(note, "`replay-unsupported-temporal`") || strings.Contains(note, "replay-unavailable") || strings.Contains(note, "once replays run") {
		t.Errorf("a TypeScript proposal whose only current bug breaks a property: %s", note)
	}
	existing := &formalize.Proposal{Draft: formalize.Draft{Name: "queue", Language: "python", Statements: append(append([]project.Statement(nil), statements...),
		project.Statement{Name: "TakesTwice", Kind: project.Bug, Says: "A task is taken twice.", Expect: "Safe", CodeBehavior: "current"})}}
	existing.Target = &formalize.Target{Manifest: project.Manifest{Language: "python", Existing: []string{"queue.py"}}}
	note = unbuildable(existing)
	for _, want := range []string{"`TakesTwice`", "`NeverTakes`", "`replay-unavailable`", "`replay-unsupported-temporal`", "authenticated replay adapter"} {
		if !strings.Contains(note, want) {
			t.Errorf("an existing Python proposal with both kinds omits %q: %s", want, note)
		}
	}
	if strings.Contains(note, "once replays run") {
		t.Errorf("a notice waits for replays that run: %s", note)
	}
}
