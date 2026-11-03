package tlc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestTLCRetryKeepsCompletedFailure(t *testing.T) {
	f := stall470Setup(t, `if [ "$attempt" = 1 ]; then
    cat "$root/pass"
    printf 'discarded attempt\n'
    exec sleep 30
fi
cat "$root/retry"
exit 12
`)
	out, err := os.ReadFile("testdata/violated.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "retry"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Recovery", stall470Config())
	if want := tlc.Parse(string(out), 12); err != nil || !reflect.DeepEqual(r, want) {
		t.Fatalf("retry = %+v, %v; want completed failure %+v", r, err, want)
	}
	f.events(t, 2, 1)
}

func TestTLCIncompleteSuccessStalls(t *testing.T) {
	f := stall470Setup(t, `cat "$root/pass"
printf 'unfinished attempt %s\n' "$attempt"
exec sleep 30
`)
	r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Recovery", stall470Config())
	if err != nil || r.Outcome != tlc.Failed || !strings.Contains(r.Message, "TLC stopped making progress") {
		t.Fatalf("unfinished check = %+v, %v; want a stall", r, err)
	}
	if r.Version != "" || r.StatesGenerated != 0 || r.DistinctStates != 0 || r.Depth != 0 || len(r.Printed) != 0 || len(r.Trace) != 0 {
		t.Fatalf("unfinished check supplied evidence: %+v", r)
	}
	if !strings.Contains(r.Message, "unfinished attempt 2") || strings.Contains(r.Message, "unfinished attempt 1") {
		t.Fatalf("diagnostic must come from the second attempt: %s", r.Message)
	}
	f.events(t, 2, 2)
}

func TestTLCCancellationDuringStallCleanup(t *testing.T) {
	f := stall470Setup(t, "exec sleep 30\n")
	// Hold removal until the caller cancels, so the context ends after the
	// silence window but before cleanup could allow a retry.
	script := strings.Replace(stall470Docker, "    exit 0\nfi\n", `    while [ ! -f "$root/release-rm" ]; do sleep 0.01; done
    exit 0
fi
`, 1) + "exec sleep 30\n"
	if err := os.WriteFile(filepath.Join(f.root, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("gate stopped by caller")
	ctx, cancel := context.WithCancelCause(stall470Context(t))
	t.Cleanup(func() {
		cancel(cause)
		os.WriteFile(filepath.Join(f.root, "release-rm"), nil, 0o600)
	})
	type result struct {
		r   tlc.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := f.runner(750*time.Millisecond).Check(ctx, f.dir, "Recovery", stall470Config())
		done <- result{r, err}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case got := <-done:
			t.Fatalf("check ended before removal was released: %+v, %v", got.r, got.err)
		case <-ctx.Done():
			t.Fatal("check did not begin stall cleanup before the deadline")
		case <-ticker.C:
			events, _ := os.ReadFile(filepath.Join(f.root, "events"))
			if !strings.Contains(string(events), "\nrm ") {
				continue
			}
			cancel(cause)
			if err := os.WriteFile(filepath.Join(f.root, "release-rm"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				stall470CutOff(t, got.r, got.err, cause)
			case <-time.After(3 * time.Second):
				t.Fatal("check did not finish after cleanup was released")
			}
			f.events(t, 1, 1)
			return
		}
	}
}
