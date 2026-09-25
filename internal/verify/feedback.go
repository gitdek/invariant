package verify

import (
	"fmt"
	"strings"
)

// Feedback explains a report to the factory: what failed, and enough detail
// to repair it. It's what the gate tool returns during synthesis.
func Feedback(r *Report) string {
	var b strings.Builder
	var failed []string
	section := func(title string, lines ...string) {
		failed = append(failed, title)
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", title, strings.Join(lines, "\n"))
	}
	if !r.RatificationMatches() {
		section("Ratification", fmt.Sprintf("The lock's statements or bounds no longer match the proposal @%s ratified. Restore the lock.", r.Ratified.By))
	}
	for _, p := range r.Pins {
		switch {
		case p.Error != "":
			section("Pinned statement "+p.Name, "It can't be read: "+p.Error+". It's ratified: restore it exactly.")
		case !p.Match:
			section("Pinned statement "+p.Name, "Its text, or the text of a definition it depends on, changed. "+
				"It's ratified, so you may not edit it: restore it exactly as it was.")
		}
	}
	d := r.Design
	switch d.Outcome {
	case "passed":
	case "violated":
		section("Design: TLC", fmt.Sprintf("Invariant %s is violated. The shortest behavior that violates it:", d.Violated), traceText(d))
	case "deadlock":
		section("Design: TLC", "A reachable state has no successor, so the system can deadlock. The behavior that reaches it:", traceText(d))
	default:
		section("Design: TLC", "TLC couldn't check the model:", d.Message)
	}
	for _, w := range r.Witnesses {
		if !w.Reached {
			section("Witness "+w.Name, fmt.Sprintf("It says: %s. %s. The model must be able to reach it.", w.Says, w.Message))
		}
	}
	for _, bug := range r.Bugs {
		if !bug.Caught {
			section("Known bug "+bug.Name, fmt.Sprintf("It says: %s. With %s added to Next, TLC must find %s violated. Instead: %s.",
				bug.Says, bug.Name, bug.Expect, bug.Message))
		}
	}
	if a := r.Agreement; a != nil && !a.Passed {
		section("Agreement", "Exploring the Go code from Init with Successors must reach exactly the states TLC reaches in the model. "+
			a.Message+".")
	}
	if c := r.Conformance; c != nil && !c.Passed {
		lines := []string{c.Message + "."}
		if c.BadStart != "" {
			lines = append(lines, "The first state of a run: "+c.BadStart)
		}
		if c.BadStep != nil {
			lines = append(lines, "From: "+c.BadStep.From, "To:   "+c.BadStep.To, "No action of the model makes that step.")
		}
		section("Conformance", lines...)
	}
	if c := r.Code; c != nil && !c.Passed {
		section("Code: "+c.Verifier, c.Errors...)
	}
	if !r.Build.Passed && !r.ModelOnly {
		section("Build: go vet and go test", "```", lastLines(r.Build.Output, 60), "```")
	}
	if len(failed) == 0 && r.ModelOnly {
		return fmt.Sprintf("The model checks out. TLC explored %d states (depth %d) and found no invariant violated and no deadlock, "+
			"every witness was reached, and every known bug was caught.", d.DistinctStates, d.Depth)
	}
	if len(failed) == 0 {
		return fmt.Sprintf("The gate passed. Every check passed: TLC explored %d states (depth %d), every witness was reached, "+
			"every known bug was caught, the code-level evidence holds (%s), and the build is green.",
			d.DistinctStates, d.Depth, r.Assurance)
	}
	return fmt.Sprintf("The gate failed. What to fix: %s.\n%s", strings.Join(failed, "; "), b.String())
}

// Failed names the checks a report failed.
func Failed(r *Report) []string {
	var out []string
	if !r.RatificationMatches() {
		out = append(out, "ratification")
	}
	for _, p := range r.Pins {
		if !p.Match {
			out = append(out, "pin "+p.Name)
		}
	}
	if !r.Design.Passed {
		out = append(out, "design")
	}
	for _, w := range r.Witnesses {
		if !w.Reached {
			out = append(out, "witness "+w.Name)
		}
	}
	for _, b := range r.Bugs {
		if !b.Caught {
			out = append(out, "bug "+b.Name)
		}
	}
	if r.Agreement != nil && !r.Agreement.Passed {
		out = append(out, "agreement")
	}
	if r.Conformance != nil && !r.Conformance.Passed {
		out = append(out, "conformance")
	}
	if r.Code != nil && !r.Code.Passed {
		out = append(out, "code")
	}
	if !r.Build.Passed && !r.ModelOnly {
		out = append(out, "build")
	}
	return out
}

func traceText(d Design) string {
	var b strings.Builder
	for _, s := range d.Trace {
		fmt.Fprintf(&b, "%d. %s\n", s.Index, s.Action)
		for _, v := range s.Vars {
			fmt.Fprintf(&b, "     %s = %s\n", v.Name, v.Value)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
