package tlc_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestTLCBudgetRoundedCapacity(t *testing.T) {
	for _, c := range []struct {
		name   string
		memory uint64
		cap    uint64
		heap   uint64
	}{
		{"minimum", 342 << 20, 256 << 20, 192 << 20},
		{"fractional MiB", (1027 << 20) + 1, 770 << 20, 577 << 20},
		{"no overflow", ^uint64(0), 2 << 30, 1536 << 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := tlc.NewBudget(1, c.memory, 99)
			if err != nil || b.Parallel != 1 || b.MemoryBytes != c.cap || b.HeapBytes != c.heap {
				t.Fatalf("budget = %+v, %v; want one slot with memory %d, heap %d", b, err, c.cap, c.heap)
			}
		})
	}
	for _, cpus := range []int{0, 1} {
		if _, err := tlc.NewBudget(cpus, 341<<20, 0); err == nil {
			t.Fatalf("accepted known insufficient memory with %d CPUs", cpus)
		}
	}
}

func TestTLCMemoryFailureBeforeStallDoesNotRetry(t *testing.T) {
	f := stall470Setup(t, "cat \"$root/pass\"\nprintf 'java.lang.OutOfMemoryError: Java heap space\\n' >&2\nexec sleep 30\n")
	r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Memory", stall470Config())
	if err != nil || r.Outcome != tlc.Failed || !strings.Contains(r.Message, "Memory") || !strings.Contains(r.Message, "memory") {
		t.Fatalf("memory exhaustion followed by silence = %+v, %v", r, err)
	}
	if r.DistinctStates != 0 || r.Depth != 0 || len(r.Trace) != 0 {
		t.Fatalf("unfinished check supplied evidence: %+v", r)
	}
	f.events(t, 1, 1)
}

func TestTLCMemoryWordsKeepModelEvidence(t *testing.T) {
	passed := strings.ReplaceAll(stall470Read(t, "testdata/passed.txt"), "TwoPhase", "OutOfMemoryError")
	violated := strings.ReplaceAll(stall470Read(t, "testdata/violated.txt"), "TwoPhase", "OutOfMemoryError")
	names := strings.ReplaceAll(violated, "TCConsistent", "OutOfMemoryError")
	values := strings.NewReplacer(
		`"working"`, `"java.lang.OutOfMemoryError: Java heap space"`,
		`"prepared"`, `"GC overhead limit exceeded"`,
		`"init"`, `"Java heap space"`,
		`"done"`, `"Exception in thread \"main\" java.lang.OutOfMemoryError"`,
		"tmPrepared", "OutOfMemoryError",
	).Replace(violated)
	for _, c := range []struct {
		name, out string
		exit      int
		outcome   tlc.Outcome
	}{
		{"module", passed, 0, tlc.Passed},
		{"counterexample names", names, 12, tlc.Violated},
		{"counterexample values", values, 12, tlc.Violated},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := stall470Setup(t, fmt.Sprintf("cat \"$root/transcript\"\nexit %d\n", c.exit))
			if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(c.out), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := f.runner(time.Second).Check(stall470Context(t), f.dir, "OutOfMemoryError", stall470Config())
			if err != nil || r.Outcome != c.outcome || !reflect.DeepEqual(r, tlc.Parse(c.out, c.exit)) {
				t.Fatalf("model output changed evidence: %+v, %v", r, err)
			}
			f.events(t, 1, 0)
		})
	}
	t.Run("graph dump", func(t *testing.T) {
		f := stall470Setup(t, "cat \"$root/transcript\"\nprintf 'digraph G {}\\n' > \"$work/states.dot\"\n")
		if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(passed), 0o600); err != nil {
			t.Fatal(err)
		}
		dot, err := f.runner(time.Second).Dump(stall470Context(t), f.dir, "OutOfMemoryError", stall470Config())
		if err != nil || dot != "digraph G {}\n" {
			t.Fatalf("model output prevented graph dump: %q, %v", dot, err)
		}
		f.events(t, 1, 0)
	})
}

func TestTLCMemoryDiagnosticLinesOverrideEvidence(t *testing.T) {
	for _, diagnostic := range []string{
		"java.lang.OutOfMemoryError: Java heap space",
		`Exception in thread "main" java.lang.OutOfMemoryError: Java heap space`,
		"\tCaused by: java.lang.OutOfMemoryError: GC overhead limit exceeded",
		"\tSuppressed: java.lang.OutOfMemoryError: Java heap space",
		"The exception was a java.lang.OutOfMemoryError",
		"Error: Java heap space",
		"OutOfMemoryError",
		"Java heap space",
		"GC overhead limit exceeded",
	} {
		t.Run(diagnostic, func(t *testing.T) {
			f := stall470Setup(t, "cat \"$root/pass\"\ncat \"$root/diagnostic\" >&2\nexit 1\n")
			if err := os.WriteFile(filepath.Join(f.root, "diagnostic"), []byte(diagnostic+"\r\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := f.runner(time.Second).Check(stall470Context(t), f.dir, "Memory", stall470Config())
			if err != nil || r.Outcome != tlc.Failed || r.ExitCode != 1 || !strings.Contains(r.Message, "TLC check Memory exhausted Java memory") {
				t.Fatalf("memory diagnostic = %+v, %v", r, err)
			}
			if r.Invariant != "" || r.Property != "" || len(r.Trace) != 0 || r.DistinctStates != 0 || r.StatesGenerated != 0 || r.Depth != 0 {
				t.Fatalf("memory failure supplied evidence: %+v", r)
			}
			if strings.Contains(diagnostic, "GC overhead limit exceeded") && !strings.Contains(r.Message, "GC overhead limit exceeded") {
				t.Fatalf("memory failure lost GC diagnostic: %+v", r)
			}
			f.events(t, 1, 0)
		})
	}
}

func TestTLCStallRemovalExit137StillRetries(t *testing.T) {
	body := `if [ "$attempt" = 1 ]; then
    printf '%s\n' "$$" > "$root/pid"
    trap 'exit 137' TERM
    while :; do sleep 0.01; done
fi
cat "$root/pass"
`
	f := stall470Setup(t, body)
	// Real Docker may exit 137 when rm kills the container, before the
	// wrapper stops its client. Make that order deterministic here.
	script := strings.Replace(stall470Docker, "    exit 0\nfi\n", `    if [ -f "$root/pid" ]; then
        pid=$(cat "$root/pid")
        rm "$root/pid"
        kill -TERM "$pid"
        while kill -0 "$pid" 2>/dev/null; do sleep 0.01; done
    fi
    exit 0
fi
`, 1) + body
	if err := os.WriteFile(filepath.Join(f.root, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Removal", stall470Config())
	stall470Passed(t, r, err)
	f.events(t, 2, 1)
}

func TestTLCCheckWithinKeepsCallerDeadline(t *testing.T) {
	f := stall470Setup(t, "printf 'running\\n'\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(stall470Context(t), 500*time.Millisecond)
	defer cancel()
	r, err := f.runner(5*time.Second).CheckWithin(ctx, f.dir, "Deadline", stall470Config(), 5*time.Second)
	stall470CutOff(t, r, err, context.DeadlineExceeded)
	f.events(t, 1, 1)
}

func TestTLCCheckWithinRetryKeepsExecutionAllowance(t *testing.T) {
	f := stall470Setup(t, `printf 'started\n'
if [ "$attempt" = 1 ]; then exec sleep 30; fi
while :; do printf 'progress\n'; sleep 0.05; done
`)
	ctx := stall470Context(t)
	r, err := f.runner(500*time.Millisecond).CheckWithin(ctx, f.dir, "Retry", stall470Config(), 1500*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("execution allowance = %+v, %v; caller = %v", r, err, ctx.Err())
	}
	f.events(t, 2, 2)
}
