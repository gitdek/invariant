package statechart

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

// Limitation records a complete exploration too large for a reader chart.
type Limitation struct {
	Code   string `json:"code"`
	Actual int    `json:"actual"`
	Limit  int    `json:"limit"`
	Source Source `json:"source"`
}

// ReadModel shares graph dumping, invariant replay and view evaluation between
// proposals and receipts. dir must hold the snapshot the model check used.
func ReadModel(ctx context.Context, runner GraphDumper, dir, module string, lock project.Lock, view []byte, states int64, temporal []tlc.TraceFile) (*Result, *Limitation, error) {
	if _, err := parseView(ctx, view); err != nil {
		return nil, nil, err
	}
	cfg := tlc.Config{Constants: maps.Clone(lock.Bounds)}
	for _, s := range lock.Statements {
		switch s.Kind {
		case project.Spec:
			cfg.Specification = s.Name
		case project.Invariant:
			cfg.Invariants = append(cfg.Invariants, s.Name)
		}
	}
	graph, err := runner.DumpGraph(ctx, dir, module, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("reader graph: %w", err)
	}
	if int64(graph.StateCount) != states {
		return nil, nil, fmt.Errorf("reader graph: reachable count differs from the model check")
	}
	in := Input{Graph: *graph, Lock: lock, Temporal: temporal}
	if graph.StateCount <= MaxStates {
		in.Invariants, err = EvaluateRules(ctx, runner, dir, *graph, lock)
		if err != nil {
			return nil, nil, fmt.Errorf("reader replay: %w", err)
		}
	}
	chart, err := Compute(ctx, in, view)
	if err != nil {
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != "too_large" {
			return nil, nil, err
		}
		return nil, &Limitation{Code: refusal.Code, Actual: refusal.Actual, Limit: refusal.Limit,
			Source: Source{Module: graph.Module, Config: graph.Config, Tool: graph.Tool, TLC: graph.Run.Version}}, nil
	}
	return chart, nil, nil
}
