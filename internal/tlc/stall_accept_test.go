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

// These tests exercise Check through its ordinary docker command boundary.
// No Docker daemon, Java, network or fifteen-minute wait is needed. Each fake
// run remains attached to its client, and the existing Docker wrapper kills
// that process after removing its named container.
const stall470Docker = `#!/bin/sh
set -eu
root="$INVARIANT_TLC470_DIR"
if [ "$1" = rm ]; then
    printf 'rm %s\n' "$3" >> "$root/events"
    exit 0
fi
if [ "$1" != run ]; then exit 94; fi
attempt=1
if [ -f "$root/count" ]; then attempt=$(( $(cat "$root/count") + 1 )); fi
printf '%s\n' "$attempt" > "$root/count"
printf '%s\n' "$@" > "$root/args-$attempt"
shift
name=
work=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --name) shift; name="$1" ;;
        -v) shift; case "$1" in *:/work) work="${1%:/work}" ;; esac ;;
    esac
    shift
done
if [ -z "$name" ] || [ -z "$work" ]; then exit 95; fi
cp "$work/Invariant.cfg" "$root/config-$attempt"
printf 'run %s %s\n' "$attempt" "$name" >> "$root/events"
`

type stall470Fixture struct {
	root string
	dir  string
}

func stall470Frame(code int, body string) string {
	return fmt.Sprintf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
}

func stall470Setup(t *testing.T, body string) stall470Fixture {
	t.Helper()
	f := stall470Fixture{root: t.TempDir(), dir: t.TempDir()}
	pass := stall470Frame(2262, "TLC test version") +
		stall470Frame(2193, "Model checking completed. No error has been found.") +
		stall470Frame(2199, "17 states generated, 9 distinct states found, 0 states left on queue.") +
		stall470Frame(2194, "The depth of the complete state graph search is 4.")
	for name, data := range map[string]string{
		"docker": stall470Docker + body,
		"pass":   pass,
	} {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("INVARIANT_TLC470_DIR", f.root)
	t.Setenv("PATH", f.root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f stall470Fixture) runner(silence time.Duration) tlc.Runner {
	return tlc.Runner{Image: "fake-java@sha256:470", Jar: filepath.Join(f.root, "tla2tools.jar"), SilenceTimeout: silence}
}

func stall470Config() tlc.Config {
	return tlc.Config{
		Specification: "FairSpec", Constants: map[string]string{"N": "3", "Nodes": "{n1, n2}"},
		Invariants: []string{"Safe"}, Properties: []string{"FollowKnownMergesSettled"},
	}
}

func stall470Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func stall470Read(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A stopped attempt must be removed before the next starts. Finished attempts
// need no extra rm, and every attempt must have a different container name.
// Removing a container again is harmless, and toolchain.Docker removes a
// cancelled one both before and after its client exits (#433), so repeated
// removals of one container, one right after another, count once (#488).
func (f stall470Fixture) events(t *testing.T, runs, removed int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "events"))
	if os.IsNotExist(err) && runs == 0 {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(line, "rm ") && len(lines) > 0 && lines[len(lines)-1] == line {
			continue
		}
		lines = append(lines, line)
	}
	var want []string
	seen := map[string]bool{}
	for i := 1; i <= runs; i++ {
		var name string
		for _, line := range lines {
			prefix := fmt.Sprintf("run %d ", i)
			if strings.HasPrefix(line, prefix) {
				name = strings.TrimPrefix(line, prefix)
			}
		}
		if name == "" || seen[name] {
			t.Fatalf("missing or reused container for attempt %d: %s", i, b)
		}
		seen[name] = true
		want = append(want, fmt.Sprintf("run %d %s", i, name))
		if i <= removed {
			want = append(want, "rm "+name)
		}
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("container lifecycle = %q, want %q", lines, want)
	}
}

func stall470Passed(t *testing.T, r tlc.Result, err error) {
	t.Helper()
	if err != nil || r.Outcome != tlc.Passed || r.DistinctStates != 9 || r.StatesGenerated != 17 || r.Depth != 4 {
		t.Fatalf("check = %+v, %v; want successful retry's evidence", r, err)
	}
}

func TestTLCStallRetriesIdenticalCheck(t *testing.T) {
	t.Run("recover after the initial state", func(t *testing.T) {
		f := stall470Setup(t, `if [ "$attempt" = 1 ]; then
    printf 'first-attempt-only: 1 initial state\n'
    exec sleep 30
fi
cat "$root/pass"
`)
		start := time.Now()
		r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Recovery", stall470Config())
		stall470Passed(t, r, err)
		if time.Since(start) < 700*time.Millisecond {
			t.Fatal("the first attempt was retried before its silence limit")
		}
		if strings.Contains(strings.Join(r.Printed, "\n"), "first-attempt-only") {
			t.Fatalf("the successful retry contains the abandoned attempt's evidence: %+v", r)
		}
		f.events(t, 2, 1)
		args := func(attempt int) []string {
			lines := strings.Split(strings.TrimSpace(stall470Read(t, filepath.Join(f.root, fmt.Sprintf("args-%d", attempt)))), "\n")
			for i := 0; i+1 < len(lines); i++ {
				if lines[i] == "--name" {
					lines[i+1] = "<unique-container>"
				}
			}
			return lines
		}
		if !reflect.DeepEqual(args(1), args(2)) {
			t.Fatalf("retry changed the check's command: %q versus %q", args(1), args(2))
		}
		first := stall470Read(t, filepath.Join(f.root, "config-1"))
		second := stall470Read(t, filepath.Join(f.root, "config-2"))
		if first != second || !strings.Contains(second, "PROPERTY FollowKnownMergesSettled\n") || !strings.Contains(second, "INVARIANT Safe\n") {
			t.Fatalf("retry changed or lost the configuration: %q versus %q", first, second)
		}
	})

	// Only silence retries: passing, violated, deadlocked, invalid and crashed
	// checks must preserve the ordinary parser's results after they finish.
	for _, tc := range []struct {
		name string
		out  string
		code int
	}{
		{"passed", stall470Frame(2193, "Model checking completed. No error has been found."), 0},
		{"invariant violated", stall470Frame(2110, "Invariant Safe is violated."), 12},
		{"property violated", stall470Frame(2116, "Temporal properties were violated."), 13},
		{"deadlock", stall470Frame(2114, "Deadlock reached."), 11},
		{"parse failure", stall470Frame(3002, "Parsing or semantic analysis failed."), 150},
		{"crash", "JVM could not finish\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := stall470Setup(t, fmt.Sprintf("cat \"$root/transcript\"\nexit %d\n", tc.code))
			if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(tc.out), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Recovery", stall470Config())
			if want := tlc.Parse(tc.out, tc.code); err != nil || !reflect.DeepEqual(r, want) {
				t.Fatalf("finished check = %+v, %v; want %+v", r, err, want)
			}
			f.events(t, 1, 0)
		})
	}
}

func TestTLCSecondStallReportsLastOutput(t *testing.T) {
	for _, output := range []bool{true, false} {
		t.Run(fmt.Sprintf("output=%t", output), func(t *testing.T) {
			body := "exec sleep 30\n"
			if output {
				body = `printf '1 initial state on attempt %s\n' "$attempt"
i=0
while [ "$i" -lt 40 ]; do printf 'old detail %s\n' "$i"; i=$((i+1)); done
printf 'last stdout on attempt %s\n' "$attempt"
printf 'last stderr on attempt %s\n' "$attempt" >&2
exec sleep 30
`
			}
			f := stall470Setup(t, body)
			ctx := stall470Context(t)
			start := time.Now()
			r, err := f.runner(750*time.Millisecond).Check(ctx, f.dir, "Recovery", stall470Config())
			if err != nil || r.Outcome != tlc.Failed || !strings.Contains(r.Message, "TLC stopped making progress") {
				t.Fatalf("two stalls = %+v, %v; want failed result naming the stall", r, err)
			}
			if ctx.Err() != nil {
				t.Fatalf("stall detection used up the outer deadline: %v", ctx.Err())
			}
			if time.Since(start) < 1400*time.Millisecond {
				t.Fatal("both attempts must receive their own full silence window")
			}
			if output {
				for _, last := range []string{"last stdout on attempt 2", "last stderr on attempt 2"} {
					if !strings.Contains(r.Message, last) {
						t.Errorf("stall diagnostic lost %q: %s", last, r.Message)
					}
				}
			}
			f.events(t, 2, 2)
		})
	}
}

func TestTLCOutputRenewsSilenceWindow(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			redirect := ""
			if stream == "stderr" {
				redirect = " >&2"
			}
			// No newlines: activity means output bytes, not parsed progress frames
			// or complete lines. Each attempt prints for over two silence windows.
			body := "i=0\nwhile [ \"$i\" -lt 40 ]; do\n    printf 'progress '%s\n    sleep 0.05\n    i=$((i+1))\ndone\n"
			body = fmt.Sprintf(body, redirect) + `printf '\n'
if [ "$attempt" = 1 ]; then exec sleep 30; fi
cat "$root/pass"
`
			f := stall470Setup(t, body)
			start := time.Now()
			r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Recovery", stall470Config())
			stall470Passed(t, r, err)
			if time.Since(start) < 4*time.Second {
				t.Fatal("TLC was stopped while it was still producing output")
			}
			f.events(t, 2, 1)
		})
	}
}

func stall470CutOff(t *testing.T, r tlc.Result, err, cause error) {
	t.Helper()
	if r.Outcome == tlc.Passed || !errors.Is(err, cause) {
		t.Fatalf("cutoff = %+v, %v; want error wrapping %v", r, err, cause)
	}
	text := strings.ToLower(err.Error() + "\n" + r.Message)
	if !strings.Contains(text, "cut off") || strings.Contains(text, "stopped making progress") || strings.Contains(text, "stall") {
		t.Fatalf("context cutoff was not distinguished from a stall: %s", text)
	}
}

func TestTLCGateDeadlineCutsOff(t *testing.T) {
	for _, silence := range []time.Duration{0, -time.Second, 5 * time.Second} {
		t.Run(silence.String(), func(t *testing.T) {
			f := stall470Setup(t, "printf '1 initial state before deadline\n'\nexec sleep 30\n")
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			start := time.Now()
			r, err := f.runner(silence).Check(ctx, f.dir, "Recovery", stall470Config())
			stall470CutOff(t, r, err, context.DeadlineExceeded)
			if time.Since(start) > 2*time.Second {
				t.Fatal("TLC kept running after the gate's deadline")
			}
			f.events(t, 1, 1)
		})
	}
	t.Run("cancel a running check", func(t *testing.T) {
		f := stall470Setup(t, "printf '1 initial state before cancellation\n'\nexec sleep 30\n")
		ctx, cancel := context.WithCancel(stall470Context(t))
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, err := os.Stat(filepath.Join(f.root, "events")); err == nil {
						cancel()
						return
					}
				}
			}
		}()
		r, err := f.runner(5*time.Second).Check(ctx, f.dir, "Recovery", stall470Config())
		cancel()
		<-done
		stall470CutOff(t, r, err, context.Canceled)
		f.events(t, 1, 1)
	})
	t.Run("already ended", func(t *testing.T) {
		f := stall470Setup(t, "cat \"$root/pass\"\n")
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		r, err := f.runner(750*time.Millisecond).Check(ctx, f.dir, "Recovery", stall470Config())
		stall470CutOff(t, r, err, context.DeadlineExceeded)
		f.events(t, 0, 0)
	})
}

func TestTLCRetrySharesGateDeadline(t *testing.T) {
	f := stall470Setup(t, `printf '1 initial state on attempt %s\n' "$attempt"
if [ "$attempt" = 1 ]; then exec sleep 30; fi
i=0
while [ "$i" -lt 100 ]; do
    printf 'progress on retry\n'
    sleep 0.05
    i=$((i+1))
done
cat "$root/pass"
`)
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, err := f.runner(time.Second).Check(ctx, f.dir, "Recovery", stall470Config())
	stall470CutOff(t, r, err, context.DeadlineExceeded)
	if ctx.Err() != context.DeadlineExceeded || time.Since(start) > 3250*time.Millisecond {
		t.Fatalf("retry renewed or ignored the gate's deadline: elapsed %s, context %v", time.Since(start), ctx.Err())
	}
	f.events(t, 2, 2)
}
