package decisions

import (
	"strings"

	"github.com/gitdek/invariant/factory/decision-state/state"
)

// stateCore is a decision's fold as factory/decision-state, its proved core,
// sees it (#193). Each line is one of the core's steps, and the fold takes
// a line only if the core does. A link line records an edge, which the
// model leaves out, since it doesn't change the decision's state. An import
// carries a row of decisions/log.md over as the core's decide: a door the
// model doesn't have, such as D-0000's "—", is two-way, the door with no
// rule of its own; an open fork is proposed; and a superseded row is
// proposed, then superseded.
type stateCore struct {
	j       *state.Journal
	writers map[string]int
}

// newStateCore is the core for a decision with this many lines. An import
// can take two of the core's steps.
func newStateCore(lines int) *stateCore {
	return &stateCore{j: state.New(lines + 1), writers: map[string]int{}}
}

// writer is a person, named as @gitdek is, or an agent, in the core's terms.
// A person's name starts with @ and an agent's doesn't, so one count numbers
// both.
func (c *stateCore) writer(name string) state.Writer {
	id, ok := c.writers[name]
	if !ok {
		id = len(c.writers) + 1
		c.writers[name] = id
	}
	if strings.HasPrefix(name, "@") {
		return state.Writer{Kind: state.Person, ID: id}
	}
	return state.Writer{Kind: state.Agent, ID: id}
}

// coreDoor is a door in the core's terms, or no door, which the core's
// decide refuses.
func coreDoor(door string) state.Door {
	switch door {
	case "one-way":
		return state.OneWay
	case "two-way":
		return state.TwoWay
	}
	return state.NoDoor
}

// importedDoor is an imported row's door in the core's terms: one the model
// doesn't have is two-way.
func importedDoor(door string) state.Door {
	if d := coreDoor(door); d != state.NoDoor {
		return d
	}
	return state.TwoWay
}

// coreStatus is a status in the core's terms, or none, which the core's
// decide refuses.
func coreStatus(status string) state.Status {
	switch status {
	case "proposed":
		return state.Proposed
	case "decided":
		return state.Decided
	case "ratified":
		return state.Ratified
	case "superseded":
		return state.Superseded
	}
	return state.None
}

// importedStatus is an imported row's status in the core's terms: an open
// fork is proposed.
func importedStatus(status string) state.Status {
	if status == "open" {
		return state.Proposed
	}
	return coreStatus(status)
}

// take takes a line's step, and says whether the core took it.
func (c *stateCore) take(e Event) bool {
	switch e.Op {
	case OpDecide:
		return c.j.Decide(c.writer(e.Who), coreDoor(e.Door), coreStatus(e.Status))
	case OpImport:
		if e.Status == "superseded" {
			return c.j.Decide(c.writer(e.Who), importedDoor(e.Door), state.Proposed) && c.j.Supersede(c.writer(e.Who))
		}
		return c.j.Decide(c.writer(e.Who), importedDoor(e.Door), importedStatus(e.Status))
	case OpRatify:
		return c.j.Ratify(c.writer(e.By))
	case OpSupersede:
		return c.j.Supersede(c.writer(e.By))
	case OpLink:
		return true
	}
	return false
}

// agrees says whether a decision, as the fold has it, is in the core's state.
// Only an import gives the fold a door or status the model doesn't have.
func (c *stateCore) agrees(d Decision) bool {
	return c.j.Current() == state.View{Status: importedStatus(d.Status), Door: importedDoor(d.Door), Who: c.writer(d.Who)}
}
