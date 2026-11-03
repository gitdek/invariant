package tlc_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestTLCBudgetRoundingAndBoundaries(t *testing.T) {
	for _, c := range []struct {
		name            string
		cpus            int
		memory          uint64
		parallel        int
		container, heap uint64
	}{
		{"fractional MiB", 8, (1027 << 20) + 3, 1, 770 << 20, 577 << 20},
		{"minimum container", 8, 342 << 20, 1, 256 << 20, 192 << 20},
		{"maximum discovery", 8, ^uint64(0), 8, 2 << 30, 1536 << 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := tlc.NewBudget(c.cpus, c.memory, 0)
			if err != nil {
				t.Fatal(err)
			}
			if b.Parallel != c.parallel || b.MemoryBytes != c.container || b.HeapBytes != c.heap {
				t.Fatalf("budget = %+v", b)
			}
		})
	}
	for _, cpus := range []int{0, 8} {
		if _, err := tlc.NewBudget(cpus, 341<<20, 0); err == nil {
			t.Fatalf("accepted known memory below the minimum with %d CPUs", cpus)
		}
	}
}

func TestTLCMemoryDiagnosticBeforeStallNeverRetries(t *testing.T) {
	f := stall470Setup(t, "printf 'java.lang.OutOfMemoryError: Java heap space\\n'\nexec sleep 30\n")
	r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Memory", stall470Config())
	if err != nil || r.Outcome != tlc.Failed || !strings.Contains(r.Message, "Memory") || !strings.Contains(r.Message, "memory") {
		t.Fatalf("memory failure = %+v, %v", r, err)
	}
	if count := strings.TrimSpace(stall470Read(t, filepath.Join(f.root, "count"))); count != "1" {
		t.Fatalf("memory failure attempted %s times", count)
	}
	f.events(t, 1, 1)
}

func TestTLCAdmittedTimeoutDoesNotRenewForRetry(t *testing.T) {
	f := stall470Setup(t, "if [ \"$attempt\" = 1 ]; then exec sleep 30; fi\nfor i in 1 2 3 4 5 6; do printf progress; sleep 0.2; done\ncat \"$root/pass\"\n")
	r, err := f.runner(500*time.Millisecond).CheckWithin(stall470Context(t), f.dir, "Timed", stall470Config(), 1100*time.Millisecond)
	if err == nil || r.Outcome == tlc.Passed || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("retry renewed execution timeout: %+v, %v", r, err)
	}
	f.events(t, 2, 2)
}
