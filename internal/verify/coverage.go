package verify

import (
	"encoding/json"
	"maps"
	"math/big"
	"strconv"
)

// Coverage says how much of a model's reachable states one conformance
// check visited (#802): an ordinary result, a Python invocation or an
// attributed component check, each on its own. It is a view derived from
// the checked evidence every time it's asked for. A report's JSON carries
// it for readers, and nothing reads it back: a serialized claim is ignored,
// and no fingerprint covers it.
type Coverage struct {
	// Source is "conformance", "python_application" or "component".
	Source     string `json:"source"`
	Target     string `json:"target,omitempty"`
	Check      string `json:"check,omitempty"`
	Invocation string `json:"invocation,omitempty"`
	// Mode is "sampled" or "bounded-exhaustive", and empty for a component
	// check that doesn't say.
	Mode string `json:"mode"`
	// States and ModelStates are the counts as recorded, invalid ones too.
	States      int64 `json:"states"`
	ModelStates int64 `json:"model_states"`
	// Checked says the conformance evidence itself is successful, whatever
	// the gate's verdict and the check on attempts say.
	Checked bool `json:"checked"`
	// Sampled says a successful check visited part of the model's states,
	// or an unknown share of them.
	Sampled bool `json:"sampled"`
	// Status is "partial", "full" or "unknown".
	Status string `json:"status"`
	// Fraction and Percent are exact text, since a JavaScript number can't
	// hold every count. Both are empty where the share is unknown.
	Fraction string `json:"fraction"`
	Percent  string `json:"percent"`

	// every says the check also earned "tested in every state": an
	// exhaustive exploration that reached every state, with its attempts
	// passing where it has them. Full coverage alone doesn't.
	every bool
}

// Qualification is what the check's evidence earns: "tested in every
// state", "tested (sampled)", "tested", or "unchecked".
func (c Coverage) Qualification() string {
	switch {
	case !c.Checked:
		return "unchecked"
	case c.every:
		return "tested in every state"
	case c.Sampled:
		return "tested (sampled)"
	}
	return "tested"
}

// Detail says the share in words.
func (c Coverage) Detail() string {
	if c.Status != "partial" && c.Status != "full" {
		return "coverage unknown"
	}
	return "visited " + c.Fraction + " of the model's states (" + c.Percent + ")"
}

// share fills in what the counts say. known is false where something else
// already rules a share out: a failed check, other bounds, or a model
// exploration that didn't finish cleanly.
func (c Coverage) share(known bool) Coverage {
	c.Status, c.Fraction, c.Percent = "unknown", "", ""
	if known && c.Checked && c.States > 0 && c.ModelStates > 0 && c.States <= c.ModelStates {
		c.Status = "partial"
		if c.States == c.ModelStates {
			c.Status = "full"
		}
		c.Fraction = strconv.FormatInt(c.States, 10) + "/" + strconv.FormatInt(c.ModelStates, 10)
		c.Percent = percent(c.States, c.ModelStates)
	}
	c.Sampled = c.Checked && c.Status != "full"
	c.every = c.every && c.Checked && c.Status == "full"
	return c
}

// percent is states out of total as a percentage cut to two decimal places,
// never rounded up, with no trailing zeroes: 2 of 5 is "40%", 3 of 11 is
// "27.27%", and one short of a million is "99.99%".
func percent(states, total int64) string {
	hundredths := new(big.Int).Mul(big.NewInt(states), big.NewInt(10000))
	hundredths.Quo(hundredths, big.NewInt(total))
	whole, part := new(big.Int).QuoRem(hundredths, big.NewInt(100), new(big.Int))
	out := whole.String()
	switch n := part.Int64(); {
	case n == 0:
	case n%10 == 0:
		out += "." + strconv.FormatInt(n/10, 10)
	default:
		out += "." + strconv.FormatInt(n/10, 10) + strconv.FormatInt(n%10, 10)
	}
	return out + "%"
}

// modelCounted says whether the design check can supply the denominator a
// check's share needs: its count agrees with total where it has one, and
// the exploration it came from finished cleanly. A report from before
// outcomes were recorded has none, and its per-check counts stand.
func (r *Report) modelCounted(total int64) bool {
	if r.Design.DistinctStates > 0 && r.Design.DistinctStates != total {
		return false
	}
	return r.Design.Outcome == "" || (r.Design.Outcome == "passed" && r.Design.Passed)
}

// ConformanceCoverage is the ordinary conformance result's coverage, or an
// unchecked one where the report has none.
func (r *Report) ConformanceCoverage() Coverage {
	c := Coverage{Source: "conformance", Mode: "sampled"}
	x := r.Conformance
	if x == nil {
		return c.share(false)
	}
	if x.Exhaustive {
		c.Mode = "bounded-exhaustive"
	}
	c.States, c.ModelStates, c.Checked = int64(x.States), x.ModelStates, x.Passed
	c.every = x.Exhaustive && (x.Tried == nil || x.Tried.Passed)
	return c.share(r.modelCounted(x.ModelStates))
}

// PythonCoverage is one Python application invocation's coverage. Its share
// is known only at the report's own bounds.
func (r *Report) PythonCoverage(x PythonApplicationConformance) Coverage {
	c := Coverage{Source: "python_application", Target: x.Key.Target, Check: x.Key.Check, Invocation: x.Key.ID, Mode: x.Mode}
	if c.Mode == "" {
		c.Mode = "sampled"
	}
	c.States, c.ModelStates = int64(x.Result.States), x.Result.ModelStates
	c.Checked = x.Result.Passed && !x.OperationalError
	c.every = x.Mode == "bounded-exhaustive" && x.Result.Exhaustive && x.passed()
	return c.share(maps.Equal(x.Bounds, r.Bounds) && r.modelCounted(x.Result.ModelStates))
}

// ComponentCoverage is one attributed component check's coverage, from the
// row qualified again, so a status edited into the row counts for nothing.
func (r *Report) ComponentCoverage(row ComponentConformanceEvidence) Coverage {
	c := Coverage{Source: "component", Target: row.Target, Check: row.Check, States: row.Result.States, ModelStates: row.Result.ModelStates}
	if row.Result.Mode == "sampled" || row.Result.Mode == "bounded-exhaustive" {
		c.Mode = row.Result.Mode
	}
	if row.Result.Invocation != nil {
		c.Invocation = row.Result.Invocation.ID
	}
	qualified := r.QualifyComponentConformance(row)
	c.Checked = qualified.TransitionsChecked && qualified.Result.Outcome == "passed"
	c.every = qualified.ExhaustiveStates
	return c.share(true)
}

// QualifyComponentConformance qualifies a component conformance row's own
// result again on a detached report, against this report's inventory and
// bounds, so an edited status or flag can't claim what the result doesn't
// earn. A row that doesn't qualify comes back unchecked.
func (r *Report) QualifyComponentConformance(row ComponentConformanceEvidence) ComponentConformanceEvidence {
	known := func(outcome string) string {
		switch outcome {
		case "passed", "failed", "timed_out", "skipped", "empty":
			return outcome
		}
		return ""
	}
	result := row.Result
	result.Outcome = known(result.Outcome)
	if result.Mode != "sampled" && result.Mode != "bounded-exhaustive" {
		result.Mode = ""
	}
	if result.Attempts != nil {
		a := *result.Attempts
		a.Outcome = known(a.Outcome)
		result.Attempts = &a
	}
	if r.Components != nil && row.Target == result.Target && row.Check == result.Check {
		qualified := Report{Bounds: r.Bounds, ModelOnly: r.ModelOnly}
		if err := qualified.AttributeComponentConformance(&r.Components.Inventory, []ComponentConformanceResult{result}); err == nil {
			return qualified.Components.Conformance[0]
		}
	}
	return ComponentConformanceEvidence{Status: "unchecked"}
}

// Coverage is every conformance check's coverage, each on its own: the
// ordinary result, each Python invocation and each attributed component
// check. Nothing is added together: invocations overlap, component checks
// can have other models, and a run one size larger has other bounds.
func (r *Report) Coverage() []Coverage {
	out := []Coverage{}
	if r.Conformance != nil {
		out = append(out, r.ConformanceCoverage())
	}
	if r.Application != nil {
		for _, x := range r.Application.Conformance {
			out = append(out, r.PythonCoverage(x))
		}
	}
	if r.Components != nil {
		for _, row := range r.Components.Conformance {
			out = append(out, r.ComponentCoverage(row))
		}
	}
	return out
}

// Sampled says some successful conformance check visited part of its
// model's states, or an unknown share of them.
func (r *Report) Sampled() bool {
	for _, c := range r.Coverage() {
		if c.Sampled {
			return true
		}
	}
	return false
}

// EvidenceJSON is the report as its evidence is encoded, without the
// derived coverage view. Fingerprints and saved identities are made from
// it, so adding a view changes none of them.
func (r Report) EvidenceJSON() ([]byte, error) {
	type evidence Report
	return json.Marshal(evidence(r))
}

// MarshalJSON adds the coverage view to the report's evidence, derived from
// the evidence as it stands.
func (r Report) MarshalJSON() ([]byte, error) {
	type evidence Report
	return json.Marshal(struct {
		evidence
		Coverage []Coverage `json:"coverage"`
	}{evidence(r), r.Coverage()})
}
