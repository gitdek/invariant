package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/statechart"
)

// Presentation wording never replaces a retained limitation or its identity.
func readerNumber(n int) string {
	return strings.TrimSuffix(strings.TrimSuffix(counted(int64(n), "unit"), " units"), " unit")
}

func readerLimitCause(l *statechart.Limitation) string {
	actual, limit := readerNumber(l.Actual), readerNumber(l.Limit)
	switch l.Kind {
	case "":
		return fmt.Sprintf("%s concrete states exceed the earlier %s-state ceiling; view not evaluated, fresh check needed for projected states", actual, limit)
	case "projected_states":
		return fmt.Sprintf("%s concrete states yield %s projected states, above the %s-state ceiling; view checked", readerNumber(l.ConcreteStates), actual, limit)
	case "concrete_states":
		return fmt.Sprintf("%s concrete states exceed the %s-state projection ceiling; view not evaluated", actual, limit)
	default:
		view := "view not evaluated"
		if l.ViewChecked != nil && *l.ViewChecked {
			view = "view checked"
		}
		representation := strings.ReplaceAll(l.Representation, "_", " ")
		return fmt.Sprintf("%s evidence has %s %s, above the %s %s ceiling; %s", representation, actual, l.Unit, limit, l.Unit, view)
	}
}

func readerLimitMessage(l *statechart.Limitation) string {
	return "Chart unavailable: " + readerLimitCause(l) + "."
}

// A comparison's text panels are evidence, not a drawable comparison. Both
// complete sides and the combined representation must fit before showing it.
func comparisonUnavailable(c *statechart.Comparison) string {
	if c == nil || c.Unavailable == nil {
		return ""
	}
	if l := c.Volume(); l != nil {
		return "Comparison unavailable: combined " + readerLimitCause(l) + "."
	}
	var causes []string
	for _, side := range []struct {
		name       string
		limitation *statechart.Limitation
	}{{"baseline", c.Baseline.Limitation}, {"candidate", c.Candidate.Limitation}} {
		if side.limitation != nil {
			causes = append(causes, side.name+" "+readerLimitCause(side.limitation))
		}
	}
	return "Comparison unavailable: " + strings.Join(causes, "; ") + "."
}

// JSON remains complete, but source text reads literally inside escaped HTML.
// In particular, a name containing < or & must not appear as a JSON escape.
func readerRecord(v any) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

type readerDetails struct {
	Notes  []string
	Record string
}

func (d readerDetails) fingerprint() string {
	return readerHash(readerJSON(struct {
		Notes        []string
		RecordSHA256 string
	}{d.Notes, readerHash([]byte(d.Record))}))
}

type readerComparisonPresentation struct {
	Message string
	Details readerDetails
}

func proposalDetails(e comparisonEvidence) readerDetails {
	d := readerDetails{Record: readerRecord(e.Proposal)}
	if p := e.Proposal; p != nil && p.Reader != nil && p.Reader.Comparison != nil {
		c := p.Reader.Comparison
		d.Notes = append(comparisonSources(c), c.Qualifications()...)
	}
	return d
}

func comparisonSources(c *statechart.Comparison) []string {
	notes := []string{comparisonPageSummary(c),
		"Baseline source identity: " + readerRecord(c.BaselineIdentity),
		"Candidate source identity: " + readerRecord(c.CandidateIdentity),
		"Baseline checking scope: " + readerRecord(c.Baseline.Lock.Bounds),
		"Candidate checking scope: " + readerRecord(c.Candidate.Lock.Bounds)}
	for _, side := range []struct {
		name     string
		snapshot statechart.ComparisonSnapshot
	}{{"Baseline", c.Baseline}, {"Candidate", c.Candidate}} {
		if chart := side.snapshot.Chart; chart != nil {
			notes = append(notes, side.name+" source, configuration and tools: "+readerRecord(chart.Report.Source),
				side.name+" counts: "+readerRecord(chart.Report.Counts))
		} else if limitation := side.snapshot.Limitation; limitation != nil {
			notes = append(notes, side.name+" retained limitation: "+readerRecord(limitation))
		}
	}
	return notes
}
