package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Options controls gate execution without changing what the gate checks.
type Options struct {
	// TLCParallel is an optional lowering limit on automatic TLC concurrency.
	// Zero selects automatically from Docker capacity; negatives are refused.
	TLCParallel int
}

func gateOptions(options []Options) (Options, error) {
	if len(options) > 1 {
		return Options{}, fmt.Errorf("verify accepts at most one Options value")
	}
	var o Options
	if len(options) == 1 {
		o = options[0]
	}
	if o.TLCParallel < 0 {
		return Options{}, fmt.Errorf("TLCParallel must not be negative")
	}
	return o, nil
}

func tlcBudget(ctx context.Context, requested int) (*tlc.Budget, error) {
	if requested < 0 {
		return nil, fmt.Errorf("TLCParallel must not be negative")
	}
	// Docker's client selects the same daemon as the workload (including a
	// remote context). Host resources would describe the wrong machine.
	discovery, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := toolchain.Docker(discovery, "info", "--format", "{{json .}}").Output()
	if cause := context.Cause(ctx); cause != nil {
		return nil, fmt.Errorf("discovering TLC capacity: %w", cause)
	}
	var capacity struct {
		NCPU     int
		MemTotal uint64
	}
	if err != nil || json.Unmarshal(out, &capacity) != nil {
		return tlc.NewBudget(0, 0, requested)
	}
	return tlc.NewBudget(capacity.NCPU, capacity.MemTotal, requested)
}
