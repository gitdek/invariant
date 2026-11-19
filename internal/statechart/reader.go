package statechart

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

// ReadError retains the completed graph's provenance when replay or view
// evaluation fails, without returning a partial chart.
type ReadError struct {
	Source Source
	Err    error
}

func (e *ReadError) Error() string { return e.Err.Error() }
func (e *ReadError) Unwrap() error { return e.Err }

// Read computes explanatory evidence from the same source snapshot and lock
// as the model check. A limitation never carries a partial chart.
func Read(ctx context.Context, runner GraphDumper, dir, module string, lock project.Lock, view []byte, states int64) (result *Result, limitation *Limitation, failure error) {
	defer func() {
		if cause := context.Cause(ctx); cause != nil {
			result, limitation, failure = nil, nil, cause
		}
	}()
	if err := context.Cause(ctx); err != nil {
		return nil, nil, err
	}
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
	if graph == nil {
		return nil, nil, fmt.Errorf("reader graph: reachable count differs from the model check")
	}
	source := Source{Module: graph.Module, Config: cloneConfig(graph.Config), Tool: graph.Tool, TLC: graph.Run.Version}
	if int64(graph.StateCount) != states {
		return nil, nil, &ReadError{Source: source, Err: fmt.Errorf("reader graph: reachable count differs from the model check")}
	}
	in := Input{Graph: *graph, Lock: lock}
	if _, err := validateGraph(ctx, *graph); err != nil {
		return nil, nil, &ReadError{Source: source, Err: err}
	}
	invariants, err := validateLock(*graph, lock)
	if err != nil {
		return nil, nil, &ReadError{Source: source, Err: err}
	}
	err = workLimit(in, view, invariants)
	if err == nil {
		in.Invariants, err = EvaluateRules(ctx, runner, dir, *graph, lock)
	}
	var chart *Result
	if err == nil {
		chart, err = Compute(ctx, in, view)
	}
	if err != nil {
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != "too_large" {
			return nil, nil, &ReadError{Source: source, Err: err}
		}
		return nil, refusal.Limitation, nil
	}
	return chart, nil, nil
}
