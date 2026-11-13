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
	// CheckRoot makes check identities relative to the gate's staging directory,
	// distinguishing batches without recording temporary host paths.
	CheckRoot string
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
	// Later are constants written after Constants. TLC orders model values
	// as its configuration introduces them, and CHOOSE follows that order,
	// so a check that declares a model's values itself writes the ratified
	// bounds first, as the run it's checking did.
	Later map[string]string
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
	later := make([]string, 0, len(c.Later))
	for name := range c.Later {
		later = append(later, name)
	}
	sort.Strings(later)
	for _, name := range later {
		fmt.Fprintf(&b, "CONSTANT %s = %s\n", name, c.Later[name])
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
	return r.check(ctx, dir, module, cfg, 0, nil)
}

// CheckWithin gives an admitted check its own execution allowance. Queueing
// for the first attempt does not consume it. Retries and their admission waits
// share that allowance and the caller's overall deadline.
func (r Runner) CheckWithin(ctx context.Context, dir, module string, cfg Config, timeout time.Duration) (Result, error) {
	return r.check(ctx, dir, module, cfg, timeout, nil)
}

func (r Runner) check(ctx context.Context, dir, module string, cfg Config, timeout time.Duration, conformance *ConformanceCheck) (Result, error) {
	if conformance != nil {
		var cancel context.CancelFunc
		ctx, cancel = conformance.Recovery.context(ctx, false)
		defer cancel()
	}
	if err := r.Budget.acquire(ctx); err != nil {
		return Result{}, fmt.Errorf("TLC check %s cut off: %w", module, err)
	}
	// Docker Run joins cleanup before returning. Keep the allocation until
	// then, releasing it before queueing for an exclusive memory retry.
	slots := 1
	defer func() {
		if slots > 0 {
			r.Budget.releaseSlots(slots)
		}
	}()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if conformance != nil {
		var cancel context.CancelFunc
		ctx, cancel = conformance.Recovery.context(ctx, true)
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
	name := module
	if r.CheckRoot != "" {
		if rel, err := filepath.Rel(r.CheckRoot, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			name = filepath.ToSlash(filepath.Join(rel, module))
		}
	}
	check := r.Budget.newCheck(name)
	memory, heap := r.allocation()
	stallRetried, memoryRetried := false, false
	if conformance != nil {
		stallRetried = conformance.StallRetried
	}
	for attempt := 1; ; attempt++ {
		if err := context.Cause(ctx); err != nil {
			return Result{}, fmt.Errorf("TLC check %s cut off: %w", module, err)
		}
		record := func(result Result) {
			a := Attempt{Module: module, Attempt: attempt,
				MemoryBytes: memory, HeapBytes: heap, Outcome: result.Outcome, Message: result.Message}
			if conformance != nil {
				a.ParentCheck, a.ParentAttempt, a.Range = conformance.ParentCheck, conformance.ParentAttempt, &conformance.Range
			}
			r.Budget.record(check, a)
		}
		out, stalled, err := r.run(ctx, dir, module, silence, memory, heap)
		// Cancellation takes priority even when it arrives during stall cleanup.
		if cause := context.Cause(ctx); cause != nil {
			cutoff := fmt.Errorf("TLC check %s cut off: %w", module, cause)
			record(Result{Outcome: Failed, Message: cutoff.Error()})
			return Result{}, cutoff
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
		failure, confirmed := resourceFailure(module, out, 0)
		if confirmed || failureCode == 137 {
			if !confirmed {
				failure, _ = resourceFailure(module, out, failureCode)
			}
			failure.ExitCode = exitCode
			record(failure)
			if confirmed && conformance != nil && conformance.Range.Count > 1 {
				return Result{}, &MemoryExhaustion{Check: r.Budget.checkIdentity(check), Attempt: attempt, Result: failure, StallRetried: stallRetried, budget: r.Budget, check: check}
			}
			if confirmed && (conformance == nil || conformance.Range.Count == 1) && !memoryRetried && r.Budget != nil && r.Budget.exclusiveMemory > memory {
				memoryRetried = true
				r.Budget.releaseSlots(slots)
				slots = 0
				if err := r.Budget.acquireSlots(ctx, r.Budget.Parallel); err != nil {
					return Result{}, fmt.Errorf("TLC check %s cut off waiting for memory retry: %w", module, err)
				}
				slots = r.Budget.Parallel
				memory = r.Budget.exclusiveMemory
				heap = memory / mib * 3 / 4 * mib
				continue
			}
			return failure, nil
		}
		if stalled {
			msg := fmt.Sprintf("TLC stopped making progress: no output for %s", silence)
			if stallRetried {
				if attempt == 2 {
					msg += " on the second attempt"
				} else {
					msg += " after the one stall retry"
				}
			}
			if last := tail(out, 20); last != "" {
				msg += "\n" + last
			}
			failure := Result{Outcome: Failed, Message: msg}
			record(failure)
			if !stallRetried {
				stallRetried = true
				continue
			}
			return failure, nil
		}
		var result Result
		switch {
		case err == nil:
			result = Parse(out, 0)
		case errors.As(err, &exit):
			result = Parse(out, exit.ExitCode())
		default:
			runErr := fmt.Errorf("running TLC: %w", err)
			record(Result{Outcome: Failed, Message: runErr.Error()})
			return Result{}, runErr
		}
		record(result)
		return result, nil
	}
}

func (r Runner) run(ctx context.Context, dir, module string, silence time.Duration, memoryBytes, heapBytes uint64) (string, bool, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jarDir, jar := filepath.Split(r.Jar)
	memory, heap := fmt.Sprintf("%dm", memoryBytes/mib), fmt.Sprintf("%dm", heapBytes/mib)
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

// TLC's general exception frame puts ExecutionException's cause after a
// colon, sometimes on the next line. Require the cause's exception type.
var wrappedMemoryDiagnostic = regexp.MustCompile(`(?im)^[\t ]*The exception was a java\.util\.concurrent\.ExecutionException[\t ]*(?:\r?\n[\t ]*)?:[\t ]+(?:java\.lang\.)?OutOfMemoryError(?::[^\r\n]*)?[\t ]*\r?$`)

// Resource failures override partial success and counterexamples alike. None
// of an unfinished check's output can establish model evidence. Only a memory
// diagnostic, not exit 137 alone, can earn a larger allocation.
func resourceFailure(module, out string, exitCode int) (Result, bool) {
	msg := ""
	diagnostics := memoryDiagnostic.FindAllString(out, -1)
	for _, m := range message.FindAllStringSubmatch(strings.ReplaceAll(out, "\r\n", "\n"), -1) {
		switch m[1] {
		case "1000":
			diagnostics = append(diagnostics, wrappedMemoryDiagnostic.FindAllString(m[2], -1)...)
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
