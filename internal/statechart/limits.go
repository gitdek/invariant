package statechart

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

const (
	MaxConcreteStates = 100000
	MaxSteps          = 1000000
	MaxInvariantCells = 1000000
	MaxInputBytes     = 64 << 20
	MaxReplayBytes    = 64 << 20
	MaxEvidenceBytes  = 60 << 20
)

// Optional fields distinguish new dimensions without rewriting historical
// concrete-500 evidence, whose serialized form and fingerprint are immutable.
type Limitation struct {
	Code            string `json:"code"`
	Actual          int    `json:"actual"`
	Limit           int    `json:"limit"`
	Source          Source `json:"source"`
	Kind            string `json:"kind,omitempty"`
	ConcreteStates  int    `json:"concrete_states,omitempty"`
	ProjectedStates *int   `json:"projected_states,omitempty"`
	ViewChecked     *bool  `json:"view_checked,omitempty"`
	Dimension       string `json:"dimension,omitempty"`
	Representation  string `json:"representation,omitempty"`
	Side            string `json:"side,omitempty"`
	Unit            string `json:"unit,omitempty"`
}

func (l *Limitation) Validate() error {
	bad := func() error { return fmt.Errorf("invalid reader size limitation") }
	if l == nil || l.Code != "too_large" || l.Actual <= l.Limit || l.Limit <= 0 {
		return bad()
	}
	if l.Kind == "" {
		if l.Limit != MaxStates || l.ConcreteStates != 0 || l.ProjectedStates != nil || l.ViewChecked != nil || l.Dimension != "" || l.Representation != "" || l.Side != "" || l.Unit != "" {
			return bad()
		}
		return nil
	}
	if !identifier.MatchString(l.Source.Module) || !identifier.MatchString(l.Source.Config.Specification) || strings.TrimSpace(l.Source.Tool.Image) == "" || !hash.MatchString(l.Source.Tool.JarSHA256) || l.Source.TLC == "" {
		return bad()
	}
	if l.ConcreteStates < 1 || l.ViewChecked == nil {
		return bad()
	}
	checked := *l.ViewChecked
	if checked {
		if l.ConcreteStates > MaxConcreteStates || l.ProjectedStates == nil || *l.ProjectedStates < 1 || *l.ProjectedStates > l.ConcreteStates {
			return bad()
		}
	} else if l.ProjectedStates != nil {
		return bad()
	}
	switch l.Kind {
	case "projected_states":
		if !checked || l.Limit != MaxStates || l.Actual != *l.ProjectedStates {
			return bad()
		}
	case "concrete_states":
		if checked || l.Limit != MaxConcreteStates || l.Actual != l.ConcreteStates {
			return bad()
		}
	case "evidence_volume":
		if l.ConcreteStates > MaxConcreteStates || l.Representation == "" || l.Side == "" {
			return bad()
		}
		if !slices.Contains([]string{"projection", "baseline", "candidate", "comparison", "comparison_and_final", "proposal", "preview", "final"}, l.Side) {
			return bad()
		}
		limit, unit := 0, "bytes"
		switch l.Dimension {
		case "steps":
			limit, unit = MaxSteps, "occurrences"
		case "invariant_cells":
			limit, unit = MaxInvariantCells, "cells"
		case "input_bytes":
			limit = MaxInputBytes
		case "replay_bytes":
			limit = MaxReplayBytes
		case "output_bytes":
			limit = MaxEvidenceBytes
		default:
			return bad()
		}
		if l.Limit != limit || l.Unit != unit || checked != (l.Dimension == "output_bytes") {
			return bad()
		}
		representations := map[string][]string{
			"steps": {"graph.steps"}, "invariant_cells": {"invariants"}, "input_bytes": {"compact_json"},
			"replay_bytes": {"replay_modules_and_configurations"},
			"output_bytes": {"compact_json", "indented_json", "markdown", "svg", "animated_poster", "still_poster", "result.json", "formalization.json", "preview.json"},
		}
		if !slices.Contains(representations[l.Dimension], l.Representation) {
			return bad()
		}
		if checked && *l.ProjectedStates > MaxStates {
			return bad()
		}
		return nil
	default:
		return bad()
	}
	if l.Dimension != "" || l.Representation != "" || l.Side != "" || l.Unit != "" {
		return bad()
	}
	return nil
}

func (l *Limitation) ConcreteCount() int {
	if l.Kind == "" {
		return l.Actual
	}
	return l.ConcreteStates
}

func (l *Limitation) ValidateSource(module string, lock project.Lock, concrete int64) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if l.Kind == "" {
		return nil
	}
	if l.Source.Module != module || concrete > 0 && int64(l.ConcreteCount()) != concrete {
		return fmt.Errorf("limitation source or concrete count differs")
	}
	_, err := validateLock(tlc.GraphResult{Config: l.Source.Config}, lock)
	return err
}

func (l *Limitation) Description() string {
	if l.Kind == "" {
		return fmt.Sprintf("The chart is not drawn (too_large): TLC reached %d states, more than the %d states charts are drawn for. View mappings and hiding were not evaluated.", l.Actual, l.Limit)
	}
	check := "View mappings and hiding were not evaluated."
	if l.ViewChecked != nil && *l.ViewChecked {
		check = "Complete view mappings and hiding were checked."
	}
	if l.Kind == "projected_states" {
		return fmt.Sprintf("The chart is not drawn (too_large): %d projected states from %d TLC concrete states exceed the %d projected-state limit. %s", l.Actual, l.ConcreteStates, l.Limit, check)
	}
	if l.Kind == "concrete_states" {
		return fmt.Sprintf("The chart is not drawn (too_large): %d concrete states exceed the %d replay limit. %s", l.Actual, l.Limit, check)
	}
	return fmt.Sprintf("The chart is not drawn (too_large): %s %s has %d %s, exceeding %d; %d concrete states. %s", l.Side, l.Representation, l.Actual, l.Unit, l.Limit, l.ConcreteStates, check)
}

func sizeRefusal(source Source, concrete int, projected *int, kind, dimension, representation, side, unit string, actual, limit int) *Refusal {
	checked := projected != nil
	l := &Limitation{Code: "too_large", Actual: actual, Limit: limit, Source: source, Kind: kind, ConcreteStates: concrete, ProjectedStates: projected, ViewChecked: &checked, Dimension: dimension, Representation: representation, Side: side, Unit: unit}
	return &Refusal{Code: l.Code, Message: l.Description(), Actual: actual, Limit: limit, Limitation: l}
}

func graphSource(g tlc.GraphResult) Source {
	return Source{Module: g.Module, Config: cloneConfig(g.Config), Tool: g.Tool, TLC: g.Run.Version}
}

// The false matrix is a byte reservation, not an allocation of one million
// cells. JSON object ordering cannot affect its byte count.
func projectionInputBytes(in Input, view []byte, invariants []string) (int, error) {
	b, err := json.Marshal(struct {
		Graph      tlc.GraphResult `json:"graph"`
		Lock       project.Lock    `json:"lock"`
		View       string          `json:"view"`
		Temporal   []tlc.TraceFile `json:"temporal"`
		Invariants json.RawMessage `json:"invariants"`
	}{in.Graph, in.Lock, string(view), in.Temporal, json.RawMessage(`{}`)})
	if err != nil {
		return 0, err
	}
	row := 2
	for i, name := range invariants {
		key, _ := json.Marshal(name)
		row += len(key) + len(":false")
		if i > 0 {
			row++
		}
	}
	n := len(b)
	for i, s := range in.Graph.Graph.States {
		key, _ := json.Marshal(s.ID)
		n += len(key) + 1 + row
		if i > 0 {
			n++
		}
	}
	return n, nil
}

func workLimit(in Input, view []byte, invariants []string) error {
	g := in.Graph
	source, n := graphSource(g), len(g.Graph.States)
	if n > MaxConcreteStates {
		return sizeRefusal(source, n, nil, "concrete_states", "", "", "", "", n, MaxConcreteStates)
	}
	for _, d := range []struct {
		dimension, representation, unit string
		actual, limit                   int
	}{
		{"steps", "graph.steps", "occurrences", len(g.Graph.Steps), MaxSteps},
		{"invariant_cells", "invariants", "cells", n * len(invariants), MaxInvariantCells},
	} {
		if d.actual > d.limit {
			return sizeRefusal(source, n, nil, "evidence_volume", d.dimension, d.representation, "projection", d.unit, d.actual, d.limit)
		}
	}
	bytes, err := projectionInputBytes(in, view, invariants)
	if err != nil {
		return err
	}
	if bytes > MaxInputBytes {
		return sizeRefusal(source, n, nil, "evidence_volume", "input_bytes", "compact_json", "projection", "bytes", bytes, MaxInputBytes)
	}
	return nil
}

func outputLimit(source Source, concrete, projected int, representation, side string, actual int) *Limitation {
	if actual <= MaxEvidenceBytes {
		return nil
	}
	return sizeRefusal(source, concrete, &projected, "evidence_volume", "output_bytes", representation, side, "bytes", actual, MaxEvidenceBytes).Limitation
}

func (c *Result) OutputLimit(representation, side string, actual int) *Limitation {
	return outputLimit(c.Report.Source, int(c.Report.Counts["tlc_states"]), len(c.States), representation, side, actual)
}

type RenderError struct{ Err error }

func (e *RenderError) Error() string { return e.Err.Error() }
func (e *RenderError) Unwrap() error { return e.Err }

// EvidenceLimit measures complete encodings in policy order. Render errors
// retain their diagnostic and are never turned into successful size evidence.
func EvidenceLimit(value any, source Source, concrete, projected int, side string, markdown func() string, svg func() (string, error), posters ...func() (string, error)) (*Limitation, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if l := outputLimit(source, concrete, projected, "compact_json", side, len(b)); l != nil {
		return l, nil
	}
	b, err = json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	if l := outputLimit(source, concrete, projected, "indented_json", side, len(b)+1); l != nil {
		return l, nil
	}
	if markdown != nil {
		if l := outputLimit(source, concrete, projected, "markdown", side, len(markdown())); l != nil {
			return l, nil
		}
	}
	renders := append([]func() (string, error){svg}, posters...)
	for i, render := range renders {
		if render == nil {
			continue
		}
		text, err := render()
		if err != nil {
			return nil, &RenderError{Err: err}
		}
		name := []string{"svg", "animated_poster", "still_poster"}[i]
		if l := outputLimit(source, concrete, projected, name, side, len(text)); l != nil {
			return l, nil
		}
	}
	return nil, nil
}

func (c *Result) EvidenceLimit(title, side string) (*Limitation, error) {
	return EvidenceLimit(c, c.Report.Source, int(c.Report.Counts["tlc_states"]), len(c.States), side, c.Markdown, c.SVG,
		func() (string, error) { return c.Poster(PosterOptions{Title: title, Animated: true}) },
		func() (string, error) { return c.Poster(PosterOptions{Title: title}) })
}
