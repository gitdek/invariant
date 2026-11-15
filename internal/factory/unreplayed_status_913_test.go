package factory

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
)

// Both adapters run safety replays. A bug that must break a property still
// has a counterexample no finite replay can run, and the notice names it.
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
	for _, want := range []string{"`NeverTakes`", "`replay-unsupported-temporal`"} {
		if !strings.Contains(note, want) {
			t.Errorf("an existing Python proposal with both kinds omits %q: %s", want, note)
		}
	}
	if strings.Contains(note, "TakesTwice") || strings.Contains(note, "replay-unavailable") {
		t.Errorf("supported Python safety replay was blocked: %s", note)
	}
	if strings.Contains(note, "once replays run") {
		t.Errorf("a notice waits for replays that run: %s", note)
	}
}
