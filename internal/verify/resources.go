package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Options controls execution resources without changing what the gate checks.
type Options struct {
	// TLCParallel is an optional lowering limit on automatic TLC concurrency.
	// Zero selects automatically; negative values are invalid.
	TLCParallel int
}

func tlcBudget(ctx context.Context, requested int) (*tlc.Budget, error) {
	if requested < 0 {
		return nil, fmt.Errorf("TLCParallel must be nonnegative")
	}
	// Query the same Docker client and selected daemon as the checks. Discovery
	// has a short cap of its own; unavailable capacity uses the safe fallback.
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := toolchain.Docker(dctx, "info", "--format", "{{json .}}")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if cause := context.Cause(ctx); cause != nil {
		return nil, fmt.Errorf("discovering TLC capacity: %w", cause)
	}
	var info struct {
		NCPU     int
		MemTotal uint64
	}
	if err != nil || json.Unmarshal(out, &info) != nil {
		return tlc.NewBudget(0, 0, requested)
	}
	return tlc.NewBudget(info.NCPU, info.MemTotal, requested)
}
