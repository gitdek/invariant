package tlc

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

const mib = uint64(1 << 20)

// Budget bounds the TLC containers of one gate invocation. Construct it with
// NewBudget and share its pointer between runners; its exported limits are read-only.
type Budget struct {
	Parallel        int
	MemoryBytes     uint64
	HeapBytes       uint64
	exclusiveMemory uint64
	mu              sync.Mutex
	used            int
	waiters         []*budgetWaiter
	checks          []checkHistory
	checkCounts     map[string]int
}

type checkHistory struct {
	identity string
	attempts []Attempt
}

type budgetWaiter struct {
	slots int
	ready chan struct{}
}

// Attempt records resource use separately from a check's model evidence.
type Attempt struct {
	Check         string            `json:"check"`
	Module        string            `json:"module"`
	Attempt       int               `json:"attempt"`
	MemoryBytes   uint64            `json:"memory_bytes"`
	HeapBytes     uint64            `json:"heap_bytes"`
	Outcome       Outcome           `json:"outcome"`
	Message       string            `json:"message"`
	ParentCheck   string            `json:"parent_check,omitempty"`
	ParentAttempt int               `json:"parent_attempt,omitempty"`
	Range         *ConformanceRange `json:"range,omitempty"`
	Split         bool              `json:"split,omitempty"`
}

// NewBudget reserves a quarter of the daemon's memory and allows one 2 GiB
// container per CPU, scaling a single container down on smaller machines.
// Unknown capacity uses one 1 GiB container. A request can only lower the limit.
func NewBudget(cpus int, memoryBytes uint64, requestedParallel int) (*Budget, error) {
	if requestedParallel < 0 {
		return nil, fmt.Errorf("TLC parallel limit must not be negative")
	}
	available := (memoryBytes/4*3 + memoryBytes%4*3/4) / mib * mib
	if memoryBytes > 0 && available < 256*mib {
		return nil, fmt.Errorf("TLC needs at least 256 MiB of container memory after reserving a quarter of Docker memory")
	}
	parallel, memory := 1, uint64(1024)*mib
	if cpus > 0 && memoryBytes > 0 {
		memory = 2048 * mib
		if available < memory {
			memory = available
		} else {
			parallel = int(min(uint64(cpus), available/memory))
		}
	}
	if requestedParallel > 0 {
		parallel = min(parallel, requestedParallel)
	}
	b := &Budget{Parallel: parallel, MemoryBytes: memory, HeapBytes: memory / mib * 3 / 4 * mib}
	if cpus > 0 && memoryBytes > 0 && available > memory {
		b.exclusiveMemory = available
	}
	return b, nil
}

func (b *Budget) acquire(ctx context.Context) error {
	return b.acquireSlots(ctx, 1)
}

// Both ordinary work and exclusive retries join the same FIFO. Once an
// exclusive waiter reaches the front, active containers drain without new
// admissions passing it; later ordinary work cannot be starved by retries.
func (b *Budget) acquireSlots(ctx context.Context, slots int) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	w := &budgetWaiter{slots: slots, ready: make(chan struct{})}
	b.mu.Lock()
	b.waiters = append(b.waiters, w)
	b.admit()
	b.mu.Unlock()
	select {
	case <-w.ready:
		// A cancellation can race admission. It must not start a container.
		if err := context.Cause(ctx); err != nil {
			b.releaseSlots(slots)
			return err
		}
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		if i := slices.Index(b.waiters, w); i >= 0 {
			b.waiters = slices.Delete(b.waiters, i, i+1)
		} else {
			b.used -= slots // admission raced cancellation
		}
		b.admit()
		b.mu.Unlock()
		return context.Cause(ctx)
	}
}

func (b *Budget) release() {
	b.releaseSlots(1)
}

func (b *Budget) releaseSlots(slots int) {
	if b != nil {
		b.mu.Lock()
		b.used -= slots
		b.admit()
		b.mu.Unlock()
	}
}

// admit runs with mu held.
func (b *Budget) admit() {
	for len(b.waiters) > 0 {
		w := b.waiters[0]
		if w.slots > b.Parallel-b.used {
			return
		}
		b.used += w.slots
		b.waiters = slices.Delete(b.waiters, 0, 1)
		close(w.ready)
	}
}

func (b *Budget) newCheck(name string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	i := len(b.checks)
	if b.checkCounts == nil {
		b.checkCounts = make(map[string]int)
	}
	b.checkCounts[name]++
	b.checks = append(b.checks, checkHistory{identity: fmt.Sprintf("%s/%d", name, b.checkCounts[name])})
	return i
}

func (b *Budget) record(check int, a Attempt) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a.Check = b.checks[check].identity
	if a.Range != nil {
		window := *a.Range
		a.Range = &window
	}
	b.checks[check].attempts = append(b.checks[check].attempts, a)
}

func (b *Budget) checkIdentity(check int) string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checks[check].identity
}

// Attempts returns a detached history grouped by check, with ordered attempts.
// The identity distinguishes separate batches even when their modules match.
func (b *Budget) Attempts() []Attempt {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Attempt
	for _, check := range b.checks {
		for _, a := range check.attempts {
			if a.Range != nil {
				window := *a.Range
				a.Range = &window
			}
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b Attempt) int {
		if a.Check != b.Check {
			return strings.Compare(a.Check, b.Check)
		}
		return a.Attempt - b.Attempt
	})
	return out
}

func (r Runner) allocation() (memory, heap uint64) {
	if r.Budget == nil {
		return 1024 * mib, 768 * mib
	}
	return r.Budget.MemoryBytes, r.Budget.HeapBytes
}

func (r Runner) limits() (memory, heap string) {
	m, h := r.allocation()
	return fmt.Sprintf("%dm", m/mib), fmt.Sprintf("%dm", h/mib)
}
