package tlc

import (
	"context"
	"fmt"
)

const mib = uint64(1 << 20)

// Budget bounds the TLC containers of one gate invocation. Construct it with
// NewBudget and share its pointer between runners; its fields are read-only.
type Budget struct {
	Parallel    int
	MemoryBytes uint64
	HeapBytes   uint64
	slots       chan struct{}
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
	return &Budget{Parallel: parallel, MemoryBytes: memory,
		HeapBytes: memory / mib * 3 / 4 * mib, slots: make(chan struct{}, parallel)}, nil
}

func (b *Budget) acquire(ctx context.Context) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	select {
	case b.slots <- struct{}{}:
		// A cancellation can race admission. It must not start a container.
		if err := context.Cause(ctx); err != nil {
			b.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (b *Budget) release() {
	if b != nil {
		<-b.slots
	}
}

func (r Runner) limits() (memory, heap string) {
	if r.Budget == nil {
		return "1024m", "768m"
	}
	return fmt.Sprintf("%dm", r.Budget.MemoryBytes/mib), fmt.Sprintf("%dm", r.Budget.HeapBytes/mib)
}
