package tlc

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/gitdek/invariant/internal/toolchain"
)

const (
	mebibyte      = uint64(1 << 20)
	defaultMemory = uint64(1 << 30)
	defaultHeap   = uint64(768 << 20)
)

// Budget bounds all TLC containers in one gate invocation. Construct it with
// NewBudget and share its pointer between runners; its fields are read-only.
type Budget struct {
	Parallel    int
	MemoryBytes uint64
	HeapBytes   uint64
	slots       chan struct{}
}

// NewBudget reserves a quarter of Docker's memory and allows at most one
// 2 GiB container per CPU. A smaller machine gets one scaled-down container;
// unavailable discovery gets one 1 GiB container. A request can only lower
// concurrency, and zero selects it automatically.
func NewBudget(cpus int, memoryBytes uint64, requestedParallel int) (*Budget, error) {
	if requestedParallel < 0 {
		return nil, fmt.Errorf("TLC parallel limit must be nonnegative")
	}
	// Divide before multiplying, including for a daemon reporting MaxUint64.
	available := (memoryBytes/4*3 + memoryBytes%4*3/4) / mebibyte * mebibyte
	if memoryBytes > 0 && available < 256*mebibyte {
		return nil, fmt.Errorf("Docker memory budget cannot fit a 256 MiB TLC container after reserving a quarter")
	}
	b := &Budget{Parallel: 1, MemoryBytes: defaultMemory, HeapBytes: defaultHeap}
	if cpus > 0 && memoryBytes > 0 {
		b.MemoryBytes = min(2<<30, available)
		b.Parallel = int(min(uint64(cpus), available/b.MemoryBytes))
		b.HeapBytes = b.MemoryBytes * 3 / 4 / mebibyte * mebibyte
	}
	if requestedParallel > 0 {
		b.Parallel = min(b.Parallel, requestedParallel)
	}
	b.slots = make(chan struct{}, b.Parallel)
	return b, nil
}

func (b *Budget) acquire(ctx context.Context) (func(), error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if b == nil {
		return func() {}, nil
	}
	select {
	case b.slots <- struct{}{}:
		// Cancellation and admission may become ready together.
		if err := context.Cause(ctx); err != nil {
			<-b.slots
			return nil, err
		}
		return func() { <-b.slots }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (r Runner) command(ctx context.Context, dir, module string, extra ...string) *toolchain.DockerCommand {
	memory, heap := defaultMemory, defaultHeap
	if r.Budget != nil {
		memory, heap = r.Budget.MemoryBytes, r.Budget.HeapBytes
	}
	limit := strconv.FormatUint(memory, 10)
	jarDir, jar := filepath.Split(r.Jar)
	args := []string{"run", "--rm", "--network", "none", "--memory", limit, "--memory-swap", limit,
		"-v", dir + ":/work", "-v", filepath.Clean(jarDir) + ":/opt/tla:ro", "-w", "/work",
		r.Image, "java", "-Xmx" + strconv.FormatUint(heap/mebibyte, 10) + "m", "-XX:+UseParallelGC", "-cp", "/opt/tla/" + jar, "tlc2.TLC",
		"-tool", "-workers", "1", "-metadir", "/tmp/states"}
	args = append(args, extra...)
	args = append(args, "-config", "Invariant.cfg", module+".tla")
	return toolchain.Docker(ctx, args...)
}
