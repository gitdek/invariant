package tla

import (
	"fmt"
	"strings"
)

// ReplayActions reads executable names and binders, never TLC's display labels.
// A bug may name an existential alias of parameterized actions. An action body
// that cannot be decomposed is the named operation itself; it must take no
// unbound arguments at the root.
func (m *Model) ReplayActions(bug string) (normal, faulty []Step, err error) {
	normal, err = m.Steps()
	if err != nil {
		return nil, nil, err
	}
	var expand func(string, []Binder, map[string]bool) ([]Step, error)
	expand = func(name string, binders []Binder, seen map[string]bool) ([]Step, error) {
		if seen[name] {
			return nil, fmt.Errorf("recursive replay alias %s", name)
		}
		seen[name] = true
		defer delete(seen, name)
		def, err := m.Definition(name)
		if err != nil {
			return nil, err
		}
		toks := tokenize(def)
		eq := indexTop(toks, "==")
		if eq != 1 {
			return nil, fmt.Errorf("replay alias %s needs bound arguments", name)
		}
		var parts []Step
		if err := disjuncts(toks[eq+1:], binders, &parts); err != nil {
			body := unwrap(toks[eq+1:])
			if len(body) == 0 || body[0].text == `\E` {
				return nil, fmt.Errorf("replay-unsupported-action: %s must name its parameterized operations: %v", name, err)
			}
			return []Step{{Name: name, Binders: binders}}, nil
		}
		var out []Step
		for _, part := range parts {
			if len(part.Args) > 0 {
				out = append(out, part)
				continue
			}
			x, err := expand(part.Name, part.Binders, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, x...)
		}
		return out, nil
	}
	var expanded []Step
	for _, step := range normal {
		if len(step.Args) > 0 {
			expanded = append(expanded, step)
			continue
		}
		x, e := expand(step.Name, step.Binders, map[string]bool{})
		if e != nil {
			return nil, nil, e
		}
		expanded = append(expanded, x...)
	}
	normal = expanded
	faulty, err = expand(bug, nil, map[string]bool{})
	for _, group := range [][]Step{normal, faulty} {
		for _, s := range group {
			seen := map[string]bool{}
			for _, b := range s.Binders {
				if seen[b.Var] {
					return nil, nil, fmt.Errorf("shadowed replay binder %s", b.Var)
				}
				seen[b.Var] = true
			}
		}
	}
	return normal, faulty, err
}

// ReplayCandidates returns the set TLC evaluates at an exact source state.
// ENABLED binds all primed variables to the requested target; the remaining
// expressions and arguments come only from the model the gate just read.
func ReplayCandidates(steps []Step, bug bool, target string) string {
	var parts []string
	flag := "FALSE"
	if bug {
		flag = "TRUE"
	}
	for _, s := range steps {
		call := s.Name
		if len(s.Args) > 0 {
			call += "(" + strings.Join(s.Args, ", ") + ")"
		}
		x := fmt.Sprintf("IF ENABLED ((%s) /\\ (%s)) THEN {<<%q, <<%s>>, %s>>} ELSE {}", call, target, s.Name, strings.Join(s.Args, ", "), flag)
		for i := len(s.Binders) - 1; i >= 0; i-- {
			b := s.Binders[i]
			x = "UNION {(" + x + ") : " + b.Var + " \\in " + b.Domain + "}"
		}
		parts = append(parts, "("+x+")")
	}
	if len(parts) == 0 {
		return "{}"
	}
	return strings.Join(parts, " \\cup ")
}

// ReplayLoose returns the set of operations that take a step to its target
// and, with the same arguments, could have gone somewhere else: the
// operation's body chooses part of its next state, and no argument names
// the choice. A replay names each step's operation and arguments and
// nothing more, so code under replay couldn't be told which way such a step
// went. TLC decides it at the exact source state, whatever the body's text
// looks like: a choice the text hides behind an alias, a disjunction or a
// set is found, and an \E that only guards isn't.
func ReplayLoose(steps []Step, target string) string {
	var parts []string
	for _, s := range steps {
		call := s.Name
		if len(s.Args) > 0 {
			call += "(" + strings.Join(s.Args, ", ") + ")"
		}
		x := fmt.Sprintf("IF ENABLED ((%s) /\\ (%s)) /\\ ENABLED ((%s) /\\ ~(%s)) THEN {%q} ELSE {}", call, target, call, target, s.Name)
		for i := len(s.Binders) - 1; i >= 0; i-- {
			b := s.Binders[i]
			x = "UNION {(" + x + ") : " + b.Var + " \\in " + b.Domain + "}"
		}
		parts = append(parts, "("+x+")")
	}
	if len(parts) == 0 {
		return "{}"
	}
	return strings.Join(parts, " \\cup ")
}

// ReplaySpecification limits finite replay to the safety specification whose
// Init and Next the contract checks. Other temporal forms need another contract.
func (m *Model) ReplaySpecification(name string) error {
	def, err := m.Definition(name)
	if err != nil {
		return err
	}
	if Canonical(def) != Canonical(name+" == Init /\\ [][Next]_vars") {
		return fmt.Errorf("replay-unsupported-temporal: specification is not Init /\\ [][Next]_vars")
	}
	return nil
}
