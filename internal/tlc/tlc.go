package tlc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Runner runs TLC inside a pinned Java image, from a jar whose checksum the
// caller has already verified.
type Runner struct {
	Image  string  // Java runtime image, pinned by digest
	Jar    string  // local path to tla2tools.jar
	Budget *Budget // shared admission and memory limits; nil uses a 1 GiB container
	// SilenceTimeout bounds uninterrupted silence, including before the first
	// output. Nonpositive values use fifteen minutes.
	SilenceTimeout time.Duration
}

// Config is what TLC checks. Invariant writes the config file itself, from
// ratified bounds and statements, so nothing the factory writes can change
// what gets checked.
type Config struct {
	Specification string
	Constants     map[string]string
	Invariants    []string
	Properties    []string // temporal properties, checked under the specification's fairness
}

func (c Config) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "SPECIFICATION %s\n", c.Specification)
	names := make([]string, 0, len(c.Constants))
	for name := range c.Constants {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "CONSTANT %s = %s\n", name, c.Constants[name])
	}
	for _, inv := range c.Invariants {
		fmt.Fprintf(&b, "INVARIANT %s\n", inv)
	}
	for _, p := range c.Properties {
		fmt.Fprintf(&b, "PROPERTY %s\n", p)
	}
	return b.String()
}

// Check model-checks module (a file in dir) against cfg, with no network.
// Deadlock checking is always on: Check never passes -deadlock.
func (r Runner) Check(ctx context.Context, dir, module string, cfg Config) (Result, error) {
	return r.check(ctx, dir, module, cfg, 0)
}

// CheckWithin gives an admitted check its own execution allowance. Queueing
// does not consume it; both attempts still share the caller's overall deadline.
func (r Runner) CheckWithin(ctx context.Context, dir, module string, cfg Config, timeout time.Duration) (Result, error) {
	return r.check(ctx, dir, module, cfg, timeout)
}

func (r Runner) check(ctx context.Context, dir, module string, cfg Config, timeout time.Duration) (Result, error) {
	if err := r.Budget.acquire(ctx); err != nil {
		return Result{}, fmt.Errorf("TLC check %s cut off: %w", module, err)
	}
	// Docker Run joins cancellation cleanup before returning. Retain the slot
	// through that cleanup and the identical retry, if one is needed.
	defer r.Budget.release()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := context.Cause(ctx); err != nil {
		return Result{}, fmt.Errorf("TLC check %s cut off: %w", module, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Invariant.cfg"), []byte(cfg.render()), 0o644); err != nil {
		return Result{}, err
	}
	silence := r.SilenceTimeout
	if silence <= 0 {
		silence = 15 * time.Minute
	}
	for attempt := 0; ; attempt++ {
		if err := context.Cause(ctx); err != nil {
			return Result{}, fmt.Errorf("TLC check %s cut off: %w", module, err)
		}
		out, stalled, err := r.run(ctx, dir, module, silence)
		// Cancellation takes priority even when it arrives during stall cleanup.
		if cause := context.Cause(ctx); cause != nil {
			return Result{}, fmt.Errorf("TLC check %s cut off: %w", module, cause)
		}
		var exit *exec.ExitError
		exitCode := 0
		if errors.As(err, &exit) {
			exitCode = exit.ExitCode()
		}
		// Removing a stalled container may itself make Docker exit 137.
		// Only a memory diagnostic can establish memory failure in that case.
		failureCode := exitCode
		if stalled {
			failureCode = 0
		}
		if failure, ok := resourceFailure(module, out, failureCode); ok {
			failure.ExitCode = exitCode
			return failure, nil
		}
		if stalled {
			if attempt == 0 {
				continue
			}
			msg := fmt.Sprintf("TLC stopped making progress: no output for %s on the second attempt", silence)
			if last := tail(out, 20); last != "" {
				msg += "\n" + last
			}
			return Result{Outcome: Failed, Message: msg}, nil
		}
		switch {
		case err == nil:
			return Parse(out, 0), nil
		case errors.As(err, &exit):
			return Parse(out, exit.ExitCode()), nil
		default:
			return Result{}, fmt.Errorf("running TLC: %w", err)
		}
	}
}

func (r Runner) run(ctx context.Context, dir, module string, silence time.Duration) (string, bool, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jarDir, jar := filepath.Split(r.Jar)
	memory, heap := r.limits()
	cmd := toolchain.Docker(attemptCtx, "run", "--rm", "--network", "none",
		"--memory", memory, "--memory-swap", memory,
		"-v", dir+":/work", "-v", filepath.Clean(jarDir)+":/opt/tla:ro", "-w", "/work",
		r.Image, "java", "-Xmx"+heap, "-XX:+UseParallelGC", "-cp", "/opt/tla/"+jar, "tlc2.TLC",
		"-tool", "-workers", "1", "-metadir", "/tmp/states", "-config", "Invariant.cfg", module+".tla")
	out := checkOutput{last: time.Now()}
	cmd.Stdout, cmd.Stderr = &out, &out
	timer := time.NewTimer(silence)
	defer timer.Stop()
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	for {
		select {
		case err := <-done:
			return out.buf.String(), false, err
		case <-ctx.Done():
			cancel()
			err := <-done
			return out.buf.String(), false, err
		case <-timer.C:
			// A completed check keeps its ordinary outcome, even if the timer
			// became ready before this goroutine was scheduled.
			select {
			case err := <-done:
				return out.buf.String(), false, err
			default:
			}
			out.mu.Lock()
			remaining := silence - time.Since(out.last)
			if remaining > 0 {
				out.mu.Unlock()
				timer.Reset(remaining)
				continue
			}
			cancel()
			out.mu.Unlock()
			// Wait for Docker's removal and the output copies before retrying
			// or reading the attempt's diagnostics.
			err := <-done
			return out.buf.String(), true, err
		}
	}
}

// Match diagnostic lines, not names in SANY progress or values in a trace.
var memoryDiagnostic = regexp.MustCompile(`(?im)^[\t ]*(?:(?:Exception in thread "[^"\r\n]+"|Caused by:|Suppressed:|Error:|The exception was a)[\t ]+)?(?:(?:java\.lang\.)?OutOfMemoryError(?::[^\r\n]*)?|Java heap space|GC overhead limit exceeded)[\t ]*\r?$`)

// Resource failures override partial success and counterexamples alike. None
// of an unfinished check's output can establish model evidence or earn a retry.
func resourceFailure(module, out string, exitCode int) (Result, bool) {
	msg := ""
	diagnostics := memoryDiagnostic.FindAllString(out, -1)
	for _, m := range message.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		// tlc2.output.EC: SYSTEM_OUT_OF_MEMORY, SYSTEM_OUT_OF_MEMORY_TOO_MANY_INIT,
		// and SYSTEM_OUT_OF_MEMORY_LIVENESS. TLC may catch the Java exception
		// and emit only its own diagnostic, without an exception line.
		case "1001", "1002", "1003":
			diagnostics = append(diagnostics, m[2])
		}
	}
	switch {
	case len(diagnostics) > 0:
		msg = fmt.Sprintf("TLC check %s exhausted Java memory", module)
		for _, diagnostic := range diagnostics {
			if strings.Contains(strings.ToLower(diagnostic), "gc overhead limit exceeded") {
				msg += " (GC overhead limit exceeded)"
				break
			}
		}
	case exitCode == 137:
		msg = fmt.Sprintf("TLC check %s was killed (exit 137); possible memory exhaustion", module)
	default:
		return Result{}, false
	}
	return Result{Outcome: Failed, Message: msg, ExitCode: exitCode}, true
}

type checkOutput struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	last time.Time
}

func (o *checkOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n, err := o.buf.Write(p)
	if n > 0 {
		o.last = time.Now()
	}
	return n, err
}
