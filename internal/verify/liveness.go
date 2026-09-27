package verify

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// Property shows a ratified temporal property holds: TLC checks it, alone,
// of every behavior the spec allows under the ratified fairness (D-0069).
type Property struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Says     string `json:"says"`
	Holds    bool   `json:"holds"`
	Outcome  string `json:"outcome"`
	Steps    int    `json:"steps,omitempty"`    // the counterexample's length, when there is one
	Loop     int    `json:"loop,omitempty"`     // the state the counterexample returns to, forever
	Stutters bool   `json:"stutters,omitempty"` // or it stays in its last state forever
	Trace    string `json:"trace,omitempty"`    // file under traces/, when written
	Message  string `json:"message,omitempty"`
	// Behavior is the counterexample, for the agent that has to repair it.
	Behavior []tlc.State `json:"behavior,omitempty"`
}

// Fair shows a ratified fairness statement is about a step the model takes:
// with its action added to Next, every step of it is already a Next step.
// Fairness on a step the model can't take could leave no behavior to check,
// and every property would then hold vacuously.
type Fair struct {
	Name    string `json:"name"`
	Says    string `json:"says"`
	Strong  bool   `json:"strong,omitempty"` // SF rather than WF
	Action  string `json:"action,omitempty"`
	InNext  bool   `json:"in_next"`
	Message string `json:"message,omitempty"`
}

// condition is one fairness statement's condition: WF_vars(Action), or
// SF_vars(Action), for each Bound in Set when Bound isn't empty.
type condition struct {
	Strong     bool
	Bound, Set string
	Action     string
}

var fairnessForm = regexp.MustCompile(`^(?:\\A\s*([A-Za-z_]\w*)\s*\\in\s*(.+?)\s*:\s*)?([WS])F_vars\s*\((.+)\)$`)

// fairnessOf reads a fairness statement's condition from its definition.
func fairnessOf(src, name string) (condition, error) {
	def, err := tla.Definition(src, name)
	if err != nil {
		return condition{}, err
	}
	head, body, _ := strings.Cut(tla.Canonical(def), "==")
	if strings.Contains(head, "(") {
		return condition{}, fmt.Errorf("a fairness statement takes no arguments")
	}
	body = strings.TrimSpace(body)
	for strings.HasPrefix(body, "(") && strings.HasSuffix(body, ")") && balanced(body[1:len(body)-1]) {
		body = strings.TrimSpace(body[1 : len(body)-1])
	}
	m := fairnessForm.FindStringSubmatch(body)
	if m == nil || !balanced(m[4]) || !balanced(m[2]) {
		return condition{}, fmt.Errorf(`a fairness statement must be one condition, WF_vars(A) or SF_vars(A), optionally for each x in a set: \A x \in S : WF_vars(A(x))`)
	}
	return condition{Strong: m[3] == "S", Bound: m[1], Set: m[2], Action: strings.TrimSpace(m[4])}, nil
}

// balanced says whether s's parentheses, brackets and braces pair up, so a
// match can't span two conditions.
func balanced(s string) bool {
	depth := 0
	for _, c := range s {
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// steps is the condition's action as one action: A, or \E x \in S : A.
func (c condition) steps() string {
	if c.Bound == "" {
		return "(" + c.Action + ")"
	}
	return fmt.Sprintf(`(\E %s \in %s : %s)`, c.Bound, c.Set, c.Action)
}

// inNext is the action property that every step of the condition's action
// is a Next step.
func (c condition) inNext() string {
	if c.Bound == "" {
		return fmt.Sprintf("[][(%s) => Next]_vars", c.Action)
	}
	return fmt.Sprintf(`[][\A %s \in %s : ((%s) => Next)]_vars`, c.Bound, c.Set, c.Action)
}

// specFairness says whether the ratified spec states fairness itself. The
// gate's property and bug checks build their specs from Init, Next and the
// fairness statements, so fairness stated anywhere else would be missing
// from some checks and unchecked by all of them.
func specFairness(src string, p *project.Project) bool {
	names, err := tla.Closure(src, p.SpecName(), project.Model)
	if err != nil {
		return false
	}
	for _, n := range names {
		if def, err := tla.Definition(src, n); err == nil && fairnessWord.MatchString(tla.Canonical(def)) {
			return true
		}
	}
	return false
}

var fairnessWord = regexp.MustCompile(`\b[WS]F_`)

// fairSpec is the wrapper's definition of the spec with every fairness
// statement conjoined, and name is what it defines.
func fairSpec(name, base string, fairness []string) string {
	parts := append([]string{base}, fairness...)
	return name + " == " + strings.Join(parts, " /\\ ")
}

// propertyResult reads TLC's run of one property.
func propertyResult(p project.Statement, res tlc.Result) Property {
	out := Property{Name: p.Name, Label: kebab(p.Name), Says: p.Says, Outcome: string(res.Outcome)}
	switch res.Outcome {
	case tlc.Passed:
		out.Holds = true
	case tlc.PropertyViolated:
		out.Steps, out.Loop, out.Stutters, out.Behavior = len(res.Trace)-1, res.Loop, res.Stutters, res.Trace
		switch {
		case res.Stutters:
			out.Message = fmt.Sprintf("TLC found a behavior that breaks it: after %s, it stops, and nothing it's waiting for happens", steps(out.Steps))
		default:
			out.Message = fmt.Sprintf("TLC found a behavior that breaks it: after %s, it returns to state %d and repeats forever", steps(out.Steps), res.Loop)
		}
	default:
		out.Message = describe(res)
	}
	return out
}

func steps(n int) string {
	if n == 1 {
		return "1 step"
	}
	return fmt.Sprintf("%d steps", n)
}

// fairResult reads TLC's check that a fairness statement's action is a step
// the model takes.
func fairResult(f project.Statement, c condition, res tlc.Result) Fair {
	out := Fair{Name: f.Name, Says: f.Says, Strong: c.Strong, Action: c.Action}
	switch {
	case res.Outcome == tlc.Passed:
		out.InNext = true
	case res.Outcome == tlc.PropertyViolated:
		out.Message = fmt.Sprintf("some step of %s isn't a Next step, so this fairness could leave no behavior to check, and the properties would hold vacuously. Keep the action a part of Next", c.Action)
	default:
		out.Message = describe(res)
	}
	return out
}

// checkPropertyBug runs a known bug that a property must catch: with the
// bug's action added to Next, under the same fairness, TLC must find a
// behavior that breaks the property. Every behavior that never takes the
// bug's action is one the property already holds of, so the one TLC finds
// takes it.
func checkPropertyBug(ctx context.Context, out *Bug, b project.Statement, work, outDir string, p *project.Project, src string, runner tlc.Runner, cfg tlc.Config, unfairSpec string) error {
	if unfairSpec != "" {
		out.Message = unfairSpec
		return nil
	}
	name := "Bug_" + b.Name
	base := fmt.Sprintf("Init /\\ [][Next \\/ %s]_vars", b.Name)
	wrapper := fmt.Sprintf("---- MODULE %s ----\nEXTENDS %s\n%s\n====\n", name, p.ModuleName(), fairSpec("Invariant_BuggySpec", base, p.FairnessNames()))
	d, err := stage(work, name, p, src, wrapper)
	if err != nil {
		return err
	}
	res, err := runner.Check(ctx, d, name, tlc.Config{Specification: "Invariant_BuggySpec", Constants: cfg.Constants, Properties: []string{b.Expect}})
	if err != nil {
		return err
	}
	out.Outcome = string(res.Outcome)
	if res.Outcome == tlc.PropertyViolated {
		out.Caught, out.Violated, out.Steps = true, b.Expect, len(res.Trace)-1
	} else {
		out.Message = "expected a behavior that breaks " + b.Expect + "; " + describe(res)
	}
	if outDir != "" && len(res.Trace) > 0 {
		out.Trace = out.Label + ".json"
		return writeTrace(filepath.Join(outDir, "traces", out.Trace), res, p.ModuleName(), "known bug "+out.Label)
	}
	return nil
}
