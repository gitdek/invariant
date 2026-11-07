package statechart_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func comparison717Clone(t *testing.T, c *statechart.Comparison) *statechart.Comparison {
	t.Helper()
	var out statechart.Comparison
	if err := json.Unmarshal(comparison648JSON(t, c), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestComparison717ValidatedSnapshotsRejectMutations(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", strings.ReplaceAll(comparison648View, "Done", "Done λ <&>"))
	b.Lock.Bounds = map[string]string{"Limit": "3"}
	b.Chart.Report.Source.Config.Constants = map[string]string{"Limit": "3"}
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	before := comparison648JSON(t, c)
	validated, err := c.Validated()
	if err != nil || validated == nil {
		t.Fatalf("canonical validation: %v", err)
	}
	if !bytes.Equal(before, comparison648JSON(t, validated)) || !bytes.Equal(before, comparison648JSON(t, c)) {
		t.Fatal("validation changed comparison bytes or fingerprint")
	}
	validated.Baseline.Includes["Extra.tla"] = "changed"
	validated.Candidate.Chart.States[0].Label = "changed"
	if !bytes.Equal(before, comparison648JSON(t, c)) {
		t.Fatal("validated snapshots alias retained evidence")
	}
	if _, err := validated.Validated(); err == nil {
		t.Fatal("a mutated validation result retained authority")
	}
	if err := validated.Validate(); err == nil {
		t.Fatal("standalone Validate skipped mutation")
	}

	// Pin the old canonical wire and hash algorithm independently for equal
	// snapshots, including empty maps and nil deltas (no schema migration).
	equal, err := statechart.Compare(a, a)
	if err != nil {
		t.Fatal(err)
	}
	want := &statechart.Comparison{Version: 1, Baseline: a, Candidate: a, BaselineIdentity: a.Identity(), CandidateIdentity: a.Identity()}
	want.Fingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(comparison648JSON(t, want)))
	if !bytes.Equal(comparison648JSON(t, equal), comparison648JSON(t, want)) {
		t.Fatal("canonical bytes or saved comparison fingerprint changed")
	}

	for i := 0; i < reflect.TypeOf(*c).NumField(); i++ {
		field := reflect.TypeOf(*c).Field(i).Name
		t.Run(field, func(t *testing.T) {
			q := comparison717Clone(t, c)
			if _, err := q.Validated(); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "Version":
				q.Version++
			case "Fingerprint":
				q.Fingerprint = "sha256:" + strings.Repeat("0", 64)
			case "Baseline":
				q.Baseline.Lock.Ratified = nil
				q.Baseline.Name += " changed"
			case "Candidate":
				q.Candidate.Model += "\\* changed\n"
			case "BaselineIdentity":
				q.BaselineIdentity.LockSHA256 += "changed"
			case "CandidateIdentity":
				q.CandidateIdentity.Includes["Extra.tla"] = "changed"
			case "Visible":
				q.Visible.AddedStates = append(q.Visible.AddedStates, statechart.State{ID: "invented", Label: "invented"})
			case "BeforeHiding":
				q.BeforeHiding.RemovedArrows = nil
			case "ViewChanged":
				q.ViewChanged = !q.ViewChanged
			case "ModelChanged":
				q.ModelChanged = !q.ModelChanged
			case "BoundsChanged":
				q.BoundsChanged = !q.BoundsChanged
			case "Scope":
				q.Scope += " changed"
			case "Unavailable":
				q.Unavailable = &statechart.ComparisonUnavailable{Code: "too_large", Message: "invented"}
			default:
				t.Fatalf("comparison field %s needs mutation coverage", field)
			}
			if bytes.Equal(before, comparison648JSON(t, q)) {
				t.Fatalf("ineffective mutation of %s", field)
			}
			if _, err := q.Validated(); err == nil {
				t.Error("damaged field accepted after prior validation")
			}
			if err := q.Validate(); err == nil {
				t.Error("Validate no longer checks the full comparison")
			}
		})
	}
	for name, mutate := range map[string]func(*statechart.ComparisonSnapshot){
		"pin":               func(s *statechart.ComparisonSnapshot) { s.Lock.Statements[0].SHA256 = "forged" },
		"module":            func(s *statechart.ComparisonSnapshot) { s.Module = "Other" },
		"view":              func(s *statechart.ComparisonSnapshot) { s.View = `{"version":99}` },
		"include path":      func(s *statechart.ComparisonSnapshot) { s.Includes["../Helper.tla"] = "source" },
		"include collision": func(s *statechart.ComparisonSnapshot) { s.Includes[s.Module+".tla"] = s.Model },
		"lock bounds":       func(s *statechart.ComparisonSnapshot) { s.Lock.Bounds = map[string]string{"Limit": "100"} },
		"chart bounds": func(s *statechart.ComparisonSnapshot) {
			s.Chart.Report.Source.Config.Constants = map[string]string{"Limit": "100"}
		},
		"chart rules":   func(s *statechart.ComparisonSnapshot) { s.Chart.Report.Rules = nil },
		"chart version": func(s *statechart.ComparisonSnapshot) { s.Chart.Version = 99 },
		"duplicate occurrence count": func(s *statechart.ComparisonSnapshot) {
			s.Chart.Arrows[0].Members = append(s.Chart.Arrows[0].Members, s.Chart.Arrows[0].Members[0])
		},
		"both outcomes": func(s *statechart.ComparisonSnapshot) {
			s.Limitation = &statechart.Limitation{Code: "too_large", Actual: 501, Limit: 500, Source: s.Chart.Report.Source}
		},
		"no outcome": func(s *statechart.ComparisonSnapshot) { s.Chart = nil },
	} {
		t.Run(name, func(t *testing.T) {
			q := comparison717Clone(t, c)
			mutate(&q.Candidate)
			if _, err := statechart.Compare(q.Baseline, q.Candidate); err == nil {
				t.Fatal("invalid source admitted to a fresh comparison")
			}
			if _, err := q.Validated(); err == nil {
				t.Fatal("invalid retained source admitted")
			}
		})
	}
	large := a
	large.Limitation = &statechart.Limitation{Code: "too_large", Actual: 501, Limit: 500, Source: a.Chart.Report.Source}
	large.Chart = nil
	limited, err := statechart.Compare(large, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limited.Validated(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"code", "actual", "limit", "source", "unavailable"} {
		q := comparison717Clone(t, limited)
		switch field {
		case "code":
			q.Baseline.Limitation.Code = "other"
		case "actual":
			q.Baseline.Limitation.Actual = 500
		case "limit":
			q.Baseline.Limitation.Limit = 499
		case "source":
			q.Baseline.Limitation.Source.Module = "Other"
		case "unavailable":
			q.Unavailable.Message = "different"
		}
		if _, err := q.Validated(); err == nil {
			t.Errorf("damaged limitation %s accepted", field)
		}
	}
	var missing *statechart.Comparison
	if _, err := missing.Validated(); err == nil {
		t.Fatal("missing comparison accepted")
	}
}
