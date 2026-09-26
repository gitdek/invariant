package formalize

import (
	"sort"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
)

// Changes is what an amendment does to a project's ratified statements
// and bounds.
type Changes struct {
	Statements []Change      `json:"statements"`
	Bounds     []BoundChange `json:"bounds,omitempty"`
	// Definitions are the helper definitions that changed under some
	// statement, such as vars gaining a variable.
	Definitions []Definition `json:"definitions,omitempty"`
}

// Definition is a helper definition whose text changed.
type Definition struct {
	Name    string `json:"name"`
	OldText string `json:"old_text,omitempty"`
	NewText string `json:"new_text,omitempty"`
}

// Change is one statement's fate: added, changed, removed or unchanged.
// Changed means its meaning, its kind or its pin (its TLA+, or anything it
// depends on) changed.
type Change struct {
	Name    string             `json:"name"`
	How     string             `json:"how"`
	Old     *project.Statement `json:"old,omitempty"`
	New     *project.Statement `json:"new,omitempty"`
	OldText string             `json:"old_text,omitempty"` // the definition's TLA+ before
	NewText string             `json:"new_text,omitempty"` // and after
	// Through names the helper definitions a changed statement depends on
	// that changed too, such as vars.
	Through []string `json:"through,omitempty"`
}

// OwnTextSame says whether a changed statement's own definition, and what
// it says, stayed the same, so it changed only through what it depends on.
func (c Change) OwnTextSame() bool {
	return c.Old != nil && c.New != nil && tla.Canonical(c.OldText) == tla.Canonical(c.NewText) &&
		c.Old.Says == c.New.Says && c.Old.Kind == c.New.Kind && c.Old.Expect == c.New.Expect
}

const (
	Added     = "added"
	Changed   = "changed"
	Removed   = "removed"
	Unchanged = "unchanged"
)

// BoundChange is a constant whose checking bound changed. An empty From
// or To means the constant was added or removed.
type BoundChange struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Diff compares an amendment with the project it amends. Statements keep
// the proposal's order, and removed ones follow.
func Diff(c *Current, p *Proposal) *Changes {
	old := map[string]project.Statement{}
	for _, s := range c.Lock.Statements {
		old[s.Name] = s
	}
	out := &Changes{}
	seen := map[string]bool{}
	for _, s := range p.Statements {
		s := s
		seen[s.Name] = true
		ch := Change{Name: s.Name, New: &s}
		ch.NewText, _ = tla.Definition(p.ModuleText, s.Name)
		if o, ok := old[s.Name]; !ok {
			ch.How = Added
		} else {
			o := o
			ch.Old = &o
			ch.OldText, _ = tla.Definition(c.ModuleText, s.Name)
			ch.How = Unchanged
			if o.SHA256 != s.SHA256 || o.Says != s.Says || o.Kind != s.Kind || o.Expect != s.Expect {
				ch.How = Changed
				ch.Through = changedUnder(c.ModuleText, p.ModuleText, s.Name)
			}
		}
		out.Statements = append(out.Statements, ch)
	}
	helpers := map[string]bool{}
	for _, ch := range out.Statements {
		for _, n := range ch.Through {
			helpers[n] = true
		}
	}
	for _, n := range sortedNames(helpers) {
		before, _ := tla.Definition(c.ModuleText, n)
		after, _ := tla.Definition(p.ModuleText, n)
		out.Definitions = append(out.Definitions, Definition{Name: n, OldText: before, NewText: after})
	}
	for _, o := range c.Lock.Statements {
		if !seen[o.Name] {
			o := o
			text, _ := tla.Definition(c.ModuleText, o.Name)
			out.Statements = append(out.Statements, Change{Name: o.Name, How: Removed, Old: &o, OldText: text})
		}
	}
	names := map[string]bool{}
	for k := range c.Lock.Bounds {
		names[k] = true
	}
	for k := range p.Bounds {
		names[k] = true
	}
	for k := range names {
		if from, to := c.Lock.Bounds[k], p.Bounds[k]; from != to {
			out.Bounds = append(out.Bounds, BoundChange{Name: k, From: from, To: to})
		}
	}
	sort.Slice(out.Bounds, func(i, j int) bool { return out.Bounds[i].Name < out.Bounds[j].Name })
	return out
}

// changedUnder lists the definitions a statement depends on, in either
// version, whose text differs between the versions.
func changedUnder(oldSrc, newSrc, name string) []string {
	names := map[string]bool{}
	for _, src := range []string{oldSrc, newSrc} {
		closure, _ := tla.Closure(src, name, project.Model)
		for _, n := range closure {
			if n != name {
				names[n] = true
			}
		}
	}
	var out []string
	for _, n := range sortedNames(names) {
		before, _ := tla.Definition(oldSrc, n)
		after, _ := tla.Definition(newSrc, n)
		if tla.Canonical(before) != tla.Canonical(after) {
			out = append(out, n)
		}
	}
	return out
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Of lists the changes of one kind.
func (c *Changes) Of(how string) []Change {
	var out []Change
	for _, ch := range c.Statements {
		if ch.How == how {
			out = append(out, ch)
		}
	}
	return out
}
