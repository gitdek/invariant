package decisions

import (
	"strings"
	"testing"
)

// The fold takes a line only if factory/decision-state, its proved core,
// does (#193), including the lines the fold let through before it ran on
// the core.
func TestTheFoldRefusesWhatTheCoreRefuses(t *testing.T) {
	for name, lines := range map[string][]Event{
		"recorded as superseded": {{Op: OpDecide, Door: "two-way", Status: "superseded", Who: "agent"}},
		"recorded as open":       {{Op: OpDecide, Door: "two-way", Status: "open", Who: "agent"}},
		"ratified by an agent as it's recorded": {
			{Op: OpDecide, Door: "two-way", Status: "ratified", Who: "agent"},
		},
		"an import ratified by an agent": {{Op: OpImport, Door: "two-way", Status: "ratified", Who: "Claude"}},
		"an import of a one-way door decided": {
			{Op: OpImport, Door: "one-way", Status: "decided", Who: "@gitdek"},
		},
		"ratified again": {
			{Op: OpDecide, Door: "two-way", Status: "ratified", Who: "@gitdek"},
			{Op: OpRatify, By: "@gitdek"},
		},
		"ratified once superseded": {
			{Op: OpImport, Door: "two-way", Status: "superseded", Who: "agent"},
			{Op: OpRatify, By: "@gitdek"},
		},
	} {
		for i := range lines {
			lines[i].ID = "D-0001"
		}
		if d, err := fold("demo", lines); err == nil {
			t.Errorf("%s: the fold took it, as %s", name, d.Status)
		}
	}
}

// The fold still takes every kind of line the journals hold, with the state
// the log had: D-0000's door, "—", which isn't one of the model's; an open
// fork a person ratifies; a superseded row; and a link, which the model
// leaves out.
func TestTheFoldTakesWhatTheLogHad(t *testing.T) {
	for name, c := range map[string]struct {
		lines []Event
		want  string
	}{
		"no door": {[]Event{{Op: OpImport, Door: "—", Status: "ratified", Who: "@gitdek"}}, "— ratified @gitdek"},
		"an open fork ratified": {[]Event{
			{Op: OpImport, Door: "one-way", Status: "open", Who: "agent"},
			{Op: OpRatify, By: "@gitdek"},
		}, "one-way ratified @gitdek"},
		"superseded": {[]Event{{Op: OpImport, Door: "two-way", Status: "superseded", Who: "agent"}}, "two-way superseded agent"},
		"proposed, linked, ratified and superseded": {[]Event{
			{Op: OpDecide, Door: "one-way", Status: "proposed", Who: "agent"},
			{Op: OpLink, By: "agent", Edges: []Edge{{Cites, "D-0002"}}},
			{Op: OpRatify, By: "@gitdek"},
			{Op: OpSupersede, By: "agent", With: "D-0003"},
		}, "one-way superseded @gitdek"},
	} {
		for i := range c.lines {
			c.lines[i].ID = "D-0001"
		}
		d, err := fold("demo", c.lines)
		if got := strings.Join([]string{d.Door, d.Status, d.Who}, " "); err != nil || got != c.want {
			t.Errorf("%s: folded to %q, %v; want %q", name, got, err, c.want)
		}
	}
}
