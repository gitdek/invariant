package tlc_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestTLCConformanceRecoveryKeepsAdmittedAllowance(t *testing.T) {
	f := stall470Setup(t, "printf 'Java heap space\\n'\nexit 1\n")
	runner := f.runner(5 * time.Second)
	ctx := stall470Context(t)
	b, err := tlc.NewBudget(8, 16<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	runner.Budget = b
	c := tlc.ConformanceCheck{Batch: "steps-1", Range: tlc.ConformanceRange{Kind: "steps", Count: 2},
		Recovery: &tlc.ConformanceRecovery{Timeout: time.Second}}
	_, err = runner.CheckConformance(ctx, f.dir, "Parent", stall470Config(), c)
	var exhausted *tlc.MemoryExhaustion
	if !errors.As(err, &exhausted) {
		t.Fatalf("parent did not offer recovery: %v", err)
	}
	if attempts := b.Attempts(); len(attempts) != 1 || attempts[0].Split {
		t.Fatalf("offering recovery claimed children already replaced the parent: %+v", attempts)
	}
	// Time between attempts, including regeneration and admission, belongs
	// to the original allowance. A child must not start a fresh timer.
	time.Sleep(1100 * time.Millisecond)
	c.Range.Count, c.ParentAttempt = 1, exhausted.Attempt
	result, err := runner.CheckConformance(ctx, f.dir, "Child", stall470Config(), c)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || result.Outcome == tlc.Passed {
		t.Fatalf("child renewed its allowance: %+v %v; caller=%v", result, err, ctx.Err())
	}
	f.events(t, 1, 0)
}

func TestTLCConformanceSplitKeepsSpentStallRetry(t *testing.T) {
	f := stall470Setup(t, `case "$attempt" in
1|4) printf 'unfinished attempt\n'; exec sleep 30;;
2|3) printf 'Java heap space\n'; exit 1;;
*) cat "$root/pass";;
esac
`)
	b, err := tlc.NewBudget(8, 16<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	runner := f.runner(500 * time.Millisecond)
	runner.Budget = b
	ctx := stall470Context(t)
	c := tlc.ConformanceCheck{Batch: "steps-1", Range: tlc.ConformanceRange{Kind: "steps", Count: 2}, Recovery: &tlc.ConformanceRecovery{}}
	_, err = runner.CheckConformance(ctx, f.dir, "Parent", stall470Config(), c)
	var exhausted *tlc.MemoryExhaustion
	if !errors.As(err, &exhausted) || exhausted.Attempt != 2 {
		t.Fatalf("parent did not exhaust after its stall retry: %v", err)
	}
	exhausted.Split()
	c.Range.Count, c.ParentCheck, c.ParentAttempt = 1, exhausted.Check, exhausted.Attempt
	c.StallRetried = exhausted.StallRetried
	result, err := runner.CheckConformance(ctx, f.dir, "Child", stall470Config(), c)
	if err != nil || result.Outcome != tlc.Failed || !strings.Contains(result.Message, "stopped making progress") {
		t.Fatalf("singleton renewed its parent's stall retry: %+v %v", result, err)
	}
	var containers []string
	seen, removed := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(stall470Read(t, filepath.Join(f.root, "events"))), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "run":
			if fields[1] != strconv.Itoa(len(containers)+1) || seen[fields[2]] {
				t.Fatalf("missing or reused container: %s", line)
			}
			if len(containers) == 1 && !removed[containers[0]] {
				t.Fatal("parent retried before stalled container cleanup")
			}
			containers = append(containers, fields[2])
			seen[fields[2]] = true
		case len(fields) == 2 && fields[0] == "rm":
			if len(containers) != 1 && len(containers) != 4 || fields[1] != containers[len(containers)-1] {
				t.Fatalf("removed an unexpected container: %s", line)
			}
			removed[fields[1]] = true
		default:
			t.Fatalf("unexpected lifecycle event: %s", line)
		}
	}
	if len(containers) != 4 || len(removed) != 2 {
		t.Fatalf("wrong attempt or cleanup counts: runs=%v removed=%v", containers, removed)
	}
	parent, child := 0, 0
	for _, a := range b.Attempts() {
		if a.Module == "Parent" {
			parent++
			if a.MemoryBytes != 2<<30 || a.Split != (a.Attempt == 2) {
				t.Fatalf("wrong parent allocation or split attempt: %+v", a)
			}
		} else {
			child++
			memory := uint64(2 << 30)
			if a.Attempt == 2 {
				memory = 12 << 30
			}
			if a.MemoryBytes != memory || a.ParentCheck != exhausted.Check || a.ParentAttempt != 2 || a.Split {
				t.Fatalf("wrong singleton recovery: %+v", a)
			}
		}
	}
	if parent != 2 || child != 2 {
		t.Fatalf("lost attempts: %+v", b.Attempts())
	}
}

func TestTLCConformanceEmptyWindowDoesNotEscalate(t *testing.T) {
	f := stall470Setup(t, "printf 'Java heap space\\n'\nexit 1\n")
	b, err := tlc.NewBudget(8, 16<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	runner := f.runner(time.Second)
	runner.Budget = b
	result, err := runner.CheckConformance(stall470Context(t), f.dir, "Empty", stall470Config(), tlc.ConformanceCheck{Range: tlc.ConformanceRange{Kind: "init"}})
	if err != nil || result.Outcome != tlc.Failed || len(b.Attempts()) != 1 {
		t.Fatalf("empty window treated as a singleton: %+v %v", result, err)
	}
}

func TestTLCConformanceSiblingStallsAreIndependent(t *testing.T) {
	f := stall470Setup(t, `case "$attempt" in
1) printf 'Java heap space\n'; exit 1;;
2|4) printf 'unfinished attempt\n'; exec sleep 30;;
*) cat "$root/pass";;
esac
`)
	runner := f.runner(500 * time.Millisecond)
	ctx := stall470Context(t)
	c := tlc.ConformanceCheck{Range: tlc.ConformanceRange{Kind: "steps", Count: 2}, Recovery: &tlc.ConformanceRecovery{}}
	_, err := runner.CheckConformance(ctx, f.dir, "Parent", stall470Config(), c)
	var exhausted *tlc.MemoryExhaustion
	if !errors.As(err, &exhausted) || exhausted.StallRetried {
		t.Fatalf("unexpected parent recovery: %v", err)
	}
	c.Range.Count, c.StallRetried = 1, exhausted.StallRetried
	c.ParentCheck, c.ParentAttempt = exhausted.Check, exhausted.Attempt
	// A later logical window finishing first cannot spend the earlier one's
	// identical-check retry. Both still share the parent's elapsed allowance.
	for _, start := range []int{1, 0} {
		c.Range.Start = start
		result, err := runner.CheckConformance(ctx, f.dir, "Child"+strconv.Itoa(start), stall470Config(), c)
		stall470Passed(t, result, err)
	}
}
