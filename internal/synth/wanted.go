package synth

import (
	"context"
	"sync/atomic"
	"time"
)

// wantedKey carries the check that the work a context's agent runs are for
// is still wanted (#677).
type wantedKey struct{}

type wanted struct {
	every   time.Duration
	check   func(context.Context) error
	started atomic.Bool
}

// WhileWanted returns ctx carrying check, which says whether the work that
// ctx's agent runs are for is still wanted. A backend that waits for its turn
// calls it every every while it waits, and every backend calls it once more
// just before its agent starts. An error ends the run there, before its agent
// starts, and gives its turn back. Once an agent has started under ctx, its
// later runs, a review or a repair, don't check again (#677).
func WhileWanted(ctx context.Context, every time.Duration, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, wantedKey{}, &wanted{every: every, check: check})
}

// StillWanted calls the check ctx carries, until an agent has started under
// it. Without a check, the work is wanted.
func StillWanted(ctx context.Context) error {
	if w, ok := ctx.Value(wantedKey{}).(*wanted); ok && !w.started.Load() {
		return w.check(ctx)
	}
	return nil
}

// starting checks once more that the work is wanted, then records that an
// agent starts under ctx. A backend calls it just before its agent starts.
func starting(ctx context.Context) error {
	if err := StillWanted(ctx); err != nil {
		return err
	}
	if w, ok := ctx.Value(wantedKey{}).(*wanted); ok {
		w.started.Store(true)
	}
	return nil
}

// wantedTicks is when a wait checks again: a ticker's channel, or nil, which
// never fires, when there's nothing to check. stop stops the ticker.
func wantedTicks(ctx context.Context) (ticks <-chan time.Time, stop func()) {
	w, ok := ctx.Value(wantedKey{}).(*wanted)
	if !ok || w.started.Load() || w.every <= 0 {
		return nil, func() {}
	}
	t := time.NewTicker(w.every)
	return t.C, t.Stop
}
