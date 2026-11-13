package plumbing_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
)

// TestSandboxBudgetAndRealCheckout pins the ordinary ten/twelve-minute limits
// and the exception for internal/verify, #498's, which #883 made fifty-five
// minutes of tests inside an hour, with eight global queue workers. The whole run includes preparation and acceptance.
// Overrides only shorten the selected limits. A separate minute for startup
// and fifteen seconds for cleanup fit within the selected backend tool timer.
//
// The merger runs the full checkout in two waves: two simultaneous cold-cache
// runs must pass, each with at least a tenth of its hour to spare, and say
// how much they had; then three simultaneous fresh-cache runs must return
// results within the budget, with partial results allowed. Both waves measure
// peaks. Each wave may take its whole budget, so the command allows for two:
//
//	INVARIANT_PLUMBING_REAL=1 go test ./internal/plumbing -run '^TestSandboxBudgetAndRealCheckout$' -count=1 -timeout 135m
//
// Image acquisition is outside the timed runs, as it is when the watcher starts.
// Only this subtest's plan touches internal/verify: this acceptance file belongs
// to plumbing so #397 can itself be built by the old, unsharded sandbox.
// The real-image check samples working memory every 100 ms, retaining kernel
// peaks as diagnostics. Host reports survive cancellation of the Docker CLI
// and output pipe; cleanup takes a bounded final observation when possible.
// The sandbox does not forward INVARIANT_PLUMBING_REAL into its container, so
// this same acceptance test runs its default assertions there without nesting
// more Docker runs. The default assertions always run, even without Docker.
func TestSandboxBudgetAndRealCheckout(t *testing.T) {
	for _, limit := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"whole run", plumbing.SandboxTimeout, 12 * time.Minute},
		{"touched-test phase", plumbing.SandboxShardTimeout, 10 * time.Minute},
		{"verify whole run", plumbing.SandboxVerifyTimeout, 60 * time.Minute},
		{"verify touched-test phase", plumbing.SandboxVerifyShardTimeout, 55 * time.Minute},
		{"startup reserve", plumbing.SandboxStartupAllowance, time.Minute},
		{"cleanup reserve", plumbing.SandboxCleanupAllowance, 15 * time.Second},
	} {
		if limit.got != limit.want {
			t.Errorf("%s limit = %s, want %s", limit.name, limit.got, limit.want)
		}
	}
	if plumbing.SandboxShards != 8 {
		t.Errorf("sandbox shard workers = %d, want 8", plumbing.SandboxShards)
	}
	defaultBudget := plumbing.SandboxTimeout + plumbing.SandboxStartupAllowance + plumbing.SandboxCleanupAllowance
	budget := plumbing.SandboxVerifyTimeout + plumbing.SandboxStartupAllowance + plumbing.SandboxCleanupAllowance
	limits := make(map[string]time.Duration)
	for _, name := range []string{"codex", "claude-code"} {
		limit := sandboxBudgetBackendLimit(t, name, 0)
		if limit != 900*time.Second {
			t.Errorf("%s emitted tool limit %s, want the unchanged 900s limit", name, limit)
		}
		if defaultBudget >= limit {
			t.Errorf("sandbox deadline plus startup and cleanup reserves = %s, must be below %s's emitted %s tool limit", defaultBudget, name, limit)
		}
		limits[name] = sandboxBudgetBackendLimit(t, name, budget)
		if limits[name] < budget {
			t.Errorf("%s's verify tool limit = %s, want at least %s including reserves", name, limits[name], budget)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	t.Run("real_image", func(t *testing.T) {
		if os.Getenv("INVARIANT_PLUMBING_REAL") != "1" {
			// A SKIP line for this subtest is misread as a skipped parent by
			// the old sandbox parser that checks this change.
			return
		}
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("cannot locate this checkout's acceptance test")
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
		const acceptanceFile = "internal/plumbing/sandbox_budget_accept_test.go"
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(acceptanceFile)))
		if err != nil {
			t.Fatal(err)
		}
		plan := &plumbing.Plan{
			Name:    "Exercise the whole slow package inside the tool budget",
			Summary: "Run the touched plumbing and verify packages, vet and this pinned acceptance test with two, then three simultaneous cold-cache sandboxes.",
			Files:   []string{"internal/plumbing/sandbox.go", "internal/verify/verify.go"},
			Tests: []plumbing.Test{{
				Name: "TestSandboxBudgetAndRealCheckout", File: acceptanceFile,
				Says: "Two simultaneous checks pass before the sandbox deadline; three simultaneous checks return within the budget, with measured memory and process peaks below their enforced limits.",
			}},
			Sources: map[string]string{acceptanceFile: string(source)},
		}
		if err := plan.Validate(); err != nil {
			t.Fatal(err)
		}
		setupCtx, finishSetup := context.WithTimeout(context.Background(), 8*time.Minute)
		defer finishSetup()
		acquisition := time.Now()
		image, err := toolchain.PlumbingImage(setupCtx)
		if err != nil {
			t.Fatalf("acquire the real pinned sandbox image: %v", err)
		}
		t.Logf("image %s acquired in %s outside timed runs; host %s/%s", image, time.Since(acquisition), runtime.GOOS, runtime.GOARCH)
		for _, args := range [][]string{
			{"git", "rev-parse", "HEAD"}, {"git", "status", "--porcelain"},
			{"docker", "context", "show"}, {"docker", "version", "--format", "{{json .}}"},
			{"docker", "info", "--format", "id={{.ID}} name={{.Name}} os={{.OperatingSystem}} arch={{.Architecture}} kernel={{.KernelVersion}} cpus={{.NCPU}} memory={{.MemTotal}} cgroup={{.CgroupVersion}}"},
			{"docker", "image", "inspect", "--format", "{{.Id}}", image},
		} {
			cmd := exec.CommandContext(setupCtx, args[0], args[1:]...)
			cmd.Dir = root
			data, err := cmd.CombinedOutput()
			t.Logf("measurement identity %v: %s (error: %v)", args, strings.TrimSpace(string(data)), err)
		}
		modCmd := exec.CommandContext(setupCtx, "go", "env", "GOMODCACHE")
		modCmd.Dir = root
		mod, err := modCmd.Output()
		if err != nil || strings.TrimSpace(string(mod)) == "" {
			t.Fatalf("find the real Go module cache: %v; output %q", err, mod)
		}
		finishSetup()
		reports := sandboxBudgetObserveResources(t)
		seenReports := make(map[string]bool)

		type outcome struct {
			i       int
			result  plumbing.Result
			err     error
			elapsed time.Duration
		}
		for _, wave := range []struct {
			runs     int
			mustPass bool
		}{{2, true}, {3, false}} {
			sandboxes := make([]plumbing.Sandbox, wave.runs)
			for i := range sandboxes {
				sandboxes[i] = plumbing.Sandbox{
					Image: image, ModCache: strings.TrimSpace(string(mod)),
					Cache: filepath.Join(t.TempDir(), "cold-cache"), Original: root,
				}
			}
			cpus, err := toolchain.ContainerCPUs()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("running %d concurrent cold-cache containers, each with %s CPUs, 8 global queue workers, 4 GiB and 1024 PIDs: aggregate memory/process caps %d GiB and %d PIDs; sandbox deadline %s, outer budget %s", wave.runs, cpus, wave.runs*4, wave.runs*1024, plumbing.SandboxVerifyTimeout, budget)
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			ready := make(chan struct{}, wave.runs)
			start := make(chan struct{})
			done := make(chan outcome, wave.runs)
			for i, sb := range sandboxes {
				go func() {
					ready <- struct{}{}
					<-start
					began := time.Now()
					result, err := sb.Run(ctx, root, plan)
					done <- outcome{i: i, result: result, err: err, elapsed: time.Since(began)}
				}()
			}
			for range wave.runs {
				<-ready
			}
			began := time.Now()
			close(start)
			for range wave.runs {
				var out outcome
				select {
				case out = <-done:
				case <-ctx.Done():
					cancel()
					t.Fatalf("%d concurrent real sandbox runs did not all return within %s", wave.runs, budget)
				}
				t.Logf("%d-run wave: real sandbox %d finished in %s", wave.runs, out.i+1, out.elapsed)
				for _, line := range strings.Split(out.result.Output, "\n") {
					if strings.HasPrefix(line, "@@s397-container ") {
						t.Logf("%d-run wave: real sandbox %d: %s", wave.runs, out.i+1, line)
					}
				}
				for _, test := range sandboxBudgetSlowTests(out.result) {
					t.Logf("%d-run wave: real sandbox %d slow test: package=%s test=%s phase=%s elapsed_seconds=%.6f", wave.runs, out.i+1, test.Package, test.Name, test.Phase, *test.ElapsedSeconds)
				}
				if out.err != nil {
					t.Errorf("%d-run wave: real sandbox %d did not return a usable result after %s: %v\n%s\n%s", wave.runs, out.i+1, out.elapsed, out.err, out.result.Summary(), out.result.Tail(16000))
				}
				if wave.mustPass {
					if !out.result.Passed() {
						t.Errorf("%d-run wave: real sandbox %d failed after %s:\n%s\n%s", wave.runs, out.i+1, out.elapsed, out.result.Summary(), out.result.Tail(16000))
					}
					if out.elapsed >= plumbing.SandboxVerifyTimeout {
						t.Errorf("%d-run wave: real sandbox %d took %s, want less than its %s deadline", wave.runs, out.i+1, out.elapsed, plumbing.SandboxVerifyTimeout)
					}
					// A run that only just fits fails the next plan that adds a
					// test, so it says what it had left, and that must be a
					// tenth of its hour (#883).
					spare := plumbing.SandboxVerifyTimeout - out.elapsed
					t.Logf("%d-run wave: real sandbox %d had %s to spare of its %s", wave.runs, out.i+1, spare.Round(time.Second), plumbing.SandboxVerifyTimeout)
					if spare < plumbing.SandboxVerifyTimeout/10 {
						t.Errorf("%d-run wave: real sandbox %d had %s to spare, want at least %s", wave.runs, out.i+1, spare.Round(time.Second), plumbing.SandboxVerifyTimeout/10)
					}
					if !out.result.Accepted[acceptanceFile+":TestSandboxBudgetAndRealCheckout"] {
						t.Errorf("%d-run wave: real sandbox %d did not pass the pinned acceptance test in internal/plumbing", wave.runs, out.i+1)
					}
				} else if !out.result.Passed() {
					// Overload may exhaust the deadline, but must return the
					// work already completed and name unfinished or failed work.
					passed, unfinished := false, false
					summary := out.result.Summary()
					for _, result := range out.result.TestResults {
						if result.Status == "passed" {
							passed = true
						}
						if result.Status == "failed" || result.Status == "running" || result.Status == "not_run" {
							unfinished = true
							if result.Package == "" || result.Name == "" || !strings.Contains(summary, result.Package) || !strings.Contains(summary, result.Name) {
								t.Errorf("three-run wave: real sandbox %d did not name partial result %+v in its summary:\n%s", out.i+1, result, summary)
							}
						}
					}
					if !passed || !unfinished {
						t.Errorf("three-run wave: real sandbox %d returned failure without completed and unfinished/failed test results: %+v\n%s\n%s", out.i+1, out.result.TestResults, summary, out.result.Tail(16000))
					}
				}
				if out.elapsed >= budget {
					t.Errorf("%d-run wave: real sandbox %d took %s, outside its %s return budget", wave.runs, out.i+1, out.elapsed, budget)
				}
				for name, limit := range limits {
					if out.elapsed >= limit {
						t.Errorf("%d-run wave: real sandbox %d took %s, outside %s's %s tool limit", wave.runs, out.i+1, out.elapsed, name, limit)
					}
				}
			}
			elapsed := time.Since(began)
			cancel()
			t.Logf("all %d real sandbox runs completed in %s", wave.runs, elapsed)
			if elapsed >= budget || wave.mustPass && elapsed >= plumbing.SandboxVerifyTimeout {
				t.Errorf("%d concurrent sandbox runs took %s, exceeding the required deadline/budget", wave.runs, elapsed)
			}
			entries, err := os.ReadDir(reports)
			if err != nil {
				t.Fatal("read the real-image resource reports: ", err)
			}
			var fresh []string
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") && !seenReports[entry.Name()] {
					seenReports[entry.Name()] = true
					fresh = append(fresh, entry.Name())
				}
			}
			if len(fresh) != wave.runs {
				t.Errorf("%d-run wave produced %d container resource reports, want %d", wave.runs, len(fresh), wave.runs)
			}
			for i, name := range fresh {
				data, err := os.ReadFile(filepath.Join(reports, name))
				if err != nil {
					t.Error("read resource report: ", err)
					continue
				}
				t.Logf("%d-run wave: resource report %d is for container %s", wave.runs, i+1, strings.TrimSuffix(name, ".json"))
				t.Logf("%d-run wave: resource report %s", wave.runs, data)
				sandboxBudgetCheckResources(t, i+1, "@@s397-resources "+string(data))
			}
		}
	})
}

// Wrap the real Docker CLI with an in-container sampler, from preparation to
// exit. A cidfile identifies the one host report, updated during work and at
// removal. Inspection and final observations are bounded and never prevent
// cleanup. Missing counters or observer failures fail only the opt-in check.
// Sampled working memory is not a kernel high-water mark.
func sandboxBudgetObserveResources(t *testing.T) string {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	docker, err = filepath.Abs(docker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("the real-image resource observer needs python3 on the host: ", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(sandboxBudgetDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	reports := filepath.Join(dir, "reports")
	if err := os.MkdirAll(reports, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("S397_REAL_DOCKER", docker)
	t.Setenv("S397_RESOURCE_DIR", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return reports
}

func sandboxBudgetCheckResources(t *testing.T, run int, output string) {
	t.Helper()
	const marker = "@@s397-resources "
	var reports []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, marker) {
			reports = append(reports, strings.TrimPrefix(line, marker))
		}
	}
	if len(reports) != 1 {
		t.Errorf("real sandbox %d has %d resource reports, want one measured report", run, len(reports))
		return
	}
	var usage struct {
		MemoryPeak  int64             `json:"memory_peak"`
		MemoryLimit int64             `json:"memory_limit"`
		PIDsPeak    int64             `json:"pids_peak"`
		PIDsLimit   int64             `json:"pids_limit"`
		OOMs        int64             `json:"oom"`
		OOMKills    int64             `json:"oom_kill"`
		PIDsDenied  int64             `json:"pids_denied"`
		Error       string            `json:"error"`
		CPUMax      string            `json:"cpu_max"`
		CPUStat     map[string]uint64 `json:"cpu_stat"`
		Elapsed     float64           `json:"elapsed_seconds"`
		WorkingPeak int64             `json:"working_memory_peak"`
		Samples     int64             `json:"memory_samples"`
		Interval    float64           `json:"sample_interval_seconds"`
		MaxEvents   int64             `json:"memory_max_events"`
		Complete    bool              `json:"complete"`

		Phases map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal([]byte(reports[0]), &usage); err != nil || usage.Error != "" {
		t.Errorf("real sandbox %d could not measure resource peaks: %v; %s", run, err, reports[0])
		return
	}
	fields := sandboxBudgetRequired(t, run, []byte(reports[0]), "memory_peak", "memory_limit", "working_memory_peak", "memory_samples", "sample_interval_seconds", "memory_max_events", "oom", "oom_kill", "pids_peak", "pids_limit", "pids_denied", "cpu_max", "cpu_stat", "elapsed_seconds", "complete", "phases")
	t.Logf("real sandbox %d: sampled working memory peak %d / %d bytes (%d samples at %.3fs), kernel peak %d bytes, max events %d, peak processes %d / %d, OOM events %d, OOM kills %d, denied processes %d", run, usage.WorkingPeak, usage.MemoryLimit, usage.Samples, usage.Interval, usage.MemoryPeak, usage.MaxEvents, usage.PIDsPeak, usage.PIDsLimit, usage.OOMs, usage.OOMKills, usage.PIDsDenied)
	t.Logf("real sandbox %d: elapsed %.3fs, cpu.max %q, cpu.stat %v (usage and throttling times in microseconds)", run, usage.Elapsed, usage.CPUMax, usage.CPUStat)
	cpus, err := toolchain.ContainerCPUs()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := strconv.ParseFloat(cpus, 64)
	cpuMax := strings.Fields(usage.CPUMax)
	if len(cpuMax) != 2 {
		t.Errorf("real sandbox %d has malformed cpu.max: %q", run, usage.CPUMax)
	} else {
		quota, quotaErr := strconv.ParseInt(cpuMax[0], 10, 64)
		period, periodErr := strconv.ParseInt(cpuMax[1], 10, 64)
		if quotaErr != nil || periodErr != nil || quota <= 0 || period <= 0 || math.Abs(float64(quota)-want*float64(period)) > 1 {
			t.Errorf("real sandbox %d cpu.max = %q, want %s CPUs", run, usage.CPUMax, cpus)
		}
	}
	sandboxBudgetCPU(t, run, fields["cpu_stat"])
	if usage.Elapsed <= 0 || usage.CPUStat["usage_usec"] == 0 {
		t.Errorf("real sandbox %d has no measured elapsed time or CPU usage: %+v", run, usage)
	}
	if usage.MemoryLimit != 4*1024*1024*1024 || usage.PIDsLimit != 1024 {
		t.Errorf("real sandbox %d did not enforce 4 GiB and 1024 PIDs: %+v", run, usage)
	}
	if usage.MemoryPeak < 0 || usage.WorkingPeak < 0 || usage.WorkingPeak >= usage.MemoryLimit || usage.PIDsPeak <= 0 || usage.PIDsPeak >= usage.PIDsLimit {
		t.Errorf("real sandbox %d did not stay below its memory/process caps: %+v", run, usage)
	}
	if usage.OOMs != 0 || usage.OOMKills != 0 || usage.PIDsDenied != 0 {
		t.Errorf("real sandbox %d hit a memory/process limit: %+v", run, usage)
	}
	if !usage.Complete || usage.Samples <= 0 || usage.Interval != 0.1 || usage.MaxEvents < 0 {
		t.Errorf("real sandbox %d lacks complete 100 ms sampling evidence: %+v", run, usage)
	}
	if _, ok := usage.Phases["prepare"]; !ok {
		t.Errorf("real sandbox %d lacks preparation measurements", run)
	}
	var samples int64
	var elapsed float64
	for name, data := range usage.Phases {
		if name != "prepare" && name != "build" && name != "test" && name != "accept" {
			t.Errorf("real sandbox %d has unknown phase %q", run, name)
		}
		var phase struct {
			Peak    int64             `json:"working_memory_peak"`
			Samples int64             `json:"memory_samples"`
			Elapsed float64           `json:"elapsed_seconds"`
			CPU     map[string]uint64 `json:"cpu_stat"`
		}
		if err := json.Unmarshal(data, &phase); err != nil {
			t.Errorf("real sandbox %d phase %s: %v", run, name, err)
			continue
		}
		fields := sandboxBudgetRequired(t, run, data, "working_memory_peak", "memory_samples", "elapsed_seconds", "cpu_stat")
		sandboxBudgetCPU(t, run, fields["cpu_stat"])
		t.Logf("real sandbox %d phase %s: sampled working peak %d bytes, %d samples, elapsed %.6fs, cpu.stat delta %v", run, name, phase.Peak, phase.Samples, phase.Elapsed, phase.CPU)
		if phase.Peak < 0 || phase.Peak > usage.WorkingPeak || phase.Samples <= 0 || phase.Elapsed < 0 {
			t.Errorf("real sandbox %d has invalid %s measurements: %s", run, name, data)
		}
		for key, value := range phase.CPU {
			if value > usage.CPUStat[key] {
				t.Errorf("real sandbox %d phase %s CPU delta %s exceeds total", run, name, key)
			}
		}
		samples += phase.Samples
		elapsed += phase.Elapsed
	}
	if samples > usage.Samples || elapsed > usage.Elapsed+0.001 {
		t.Errorf("real sandbox %d phase measurements exceed container totals", run)
	}
}

func sandboxBudgetRequired(t *testing.T, run int, data []byte, required ...string) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Errorf("real sandbox %d malformed measurements: %v", run, err)
	}
	for _, key := range required {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			t.Errorf("real sandbox %d missing measurement %s", run, key)
		}
	}
	return fields
}

func sandboxBudgetCPU(t *testing.T, run int, data []byte) {
	t.Helper()
	fields := sandboxBudgetRequired(t, run, data, "usage_usec", "user_usec", "system_usec", "nr_periods", "nr_throttled", "throttled_usec")
	for key, raw := range fields {
		var value uint64
		if err := json.Unmarshal(raw, &value); err != nil || string(raw) == "null" {
			t.Errorf("real sandbox %d malformed CPU counter %s: %s", run, key, raw)
		}
	}
}

func sandboxBudgetSlowTests(result plumbing.Result) []plumbing.TestResult {
	var tests []plumbing.TestResult
	for _, test := range result.TestResults {
		if strings.Contains(test.Name, "/") || test.ElapsedSeconds == nil || math.IsNaN(*test.ElapsedSeconds) || math.IsInf(*test.ElapsedSeconds, 0) || *test.ElapsedSeconds < 0 {
			continue
		}
		if test.Status == "passed" || test.Status == "failed" || test.Status == "skipped" {
			tests = append(tests, test)
		}
	}
	sort.Slice(tests, func(i, j int) bool {
		if *tests[i].ElapsedSeconds != *tests[j].ElapsedSeconds {
			return *tests[i].ElapsedSeconds > *tests[j].ElapsedSeconds
		}
		return tests[i].Package+"\x00"+tests[i].Name+"\x00"+tests[i].Phase < tests[j].Package+"\x00"+tests[j].Name+"\x00"+tests[j].Phase
	})
	return tests[:min(20, len(tests))]
}

const sandboxBudgetDocker = `#!/usr/bin/env python3
import fcntl, json, os, pathlib, re, selectors, shlex, subprocess, sys, time, uuid
args = sys.argv[1:]
real = os.environ["S397_REAL_DOCKER"]
root = pathlib.Path(os.environ["S397_RESOURCE_DIR"])
reports = root / "reports"
marker = b"@@s397-resources "
# Shared by the in-container sampler, final exec and host report merger.
common = r'''
import contextlib, fcntl, json, os, pathlib, re, time, uuid
interval = 0.1
cpu_keys = ("usage_usec", "user_usec", "system_usec", "nr_periods", "nr_throttled", "throttled_usec")
peaks = ("memory_peak", "working_memory_peak", "memory_samples", "memory_max_events",
         "pids_peak", "oom", "oom_kill", "pids_denied", "elapsed_seconds")
def atomic(path, value):
    tmp = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
    try:
        tmp.write_text(json.dumps(value, allow_nan=False))
        os.replace(tmp, path)
    finally:
        tmp.unlink(missing_ok=True)
@contextlib.contextmanager
def locked(path):
    with path.open("a") as lock:
        end = time.monotonic() + 0.5
        while True:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= end: raise TimeoutError("resource report lock unavailable")
                time.sleep(0.01)
        try: yield
        finally: fcntl.flock(lock, fcntl.LOCK_UN)
def merge(prior, report):
    result = dict(prior, **report)
    for key in peaks:
        if key in prior:
            result[key] = max(prior[key], report.get(key, prior[key]))
    for key in ("memory_limit", "pids_limit", "cpu_max", "sample_interval_seconds"):
        if key in prior and key in report and prior[key] != report[key]:
            result["error"] = "container limits changed between observations"
    result["cpu_stat"] = dict(prior.get("cpu_stat", {}))
    for key, value in report.get("cpu_stat", {}).items():
        result["cpu_stat"][key] = max(value, result["cpu_stat"].get(key, 0))
    result["phases"] = dict(prior.get("phases", {}))
    for name, phase in report.get("phases", {}).items():
        result["phases"][name] = merge(prior.get("phases", {}).get(name, {}), phase)
        # Phase rows contain deltas, not independent container reports.
        result["phases"][name].pop("phases", None)
        result["phases"][name].pop("complete", None)
    result["complete"] = prior.get("complete", False) or report.get("complete", False)
    if prior.get("error"): result["error"] = prior["error"]
    return result
def public(report):
    return {key: value for key, value in report.items() if not key.startswith("_")}
def completed(report):
    required = (*peaks, "memory_limit", "pids_limit", "cpu_max", "cpu_stat",
                "sample_interval_seconds", "phases")
    return (report.get("complete") is True and not report.get("error")
            and all(key in report for key in required)
            and report["memory_samples"] > 0 and report["sample_interval_seconds"] == interval
            and "prepare" in report["phases"]
            and all(key in report["cpu_stat"] for key in cpu_keys))
def read_sample():
    cg = pathlib.Path("/sys/fs/cgroup")
    sample, errors = {}, []
    def number(value):
        if not re.fullmatch(r"[0-9]+", value): raise ValueError("invalid counter " + repr(value))
        return int(value)
    def counter(name):
        return number((cg / name).read_text().strip())
    def table(name, required):
        result = {}
        for line in (cg / name).read_text().splitlines():
            key, value = line.split()
            if key in result: raise ValueError("duplicate counter " + key)
            result[key] = number(value)
        for key in required:
            if key not in result: raise ValueError(name + " lacks " + key)
        return result
    def read(name, get):
        try: sample.update(get())
        except Exception as error: errors.append(name + ": " + str(error))
    def memory():
        stat = table("memory.stat", ("anon", "shmem"))
        return dict(working_memory_peak=stat["anon"] + stat["shmem"])
    def memory_events():
        events = table("memory.events", ("max", "oom", "oom_kill"))
        return dict(memory_max_events=events["max"], oom=events["oom"], oom_kill=events["oom_kill"])
    def cpu_max():
        value = (cg / "cpu.max").read_text().strip()
        quota, period = value.split()
        if number(quota) <= 0 or number(period) <= 0: raise ValueError("nonpositive CPU limit")
        return dict(cpu_max=value)
    read("memory.stat", memory)
    read("memory.events", memory_events)
    for key, name in (("memory_peak", "memory.peak"), ("memory_limit", "memory.max"),
                      ("pids_peak", "pids.peak"), ("pids_limit", "pids.max")):
        read(name, lambda: {key: counter(name)})
    read("pids.events", lambda: dict(pids_denied=table("pids.events", ("max",))["max"]))
    read("cpu.max", cpu_max)
    read("cpu.stat", lambda: dict(cpu_stat=table("cpu.stat", cpu_keys)))
    return sample, errors
def observe(state, phase=None, final=False):
    if state.get("complete"): return state
    now = time.monotonic()
    sample, errors = read_sample()
    if not state:
        state = dict(_started=now, _phase="prepare", _phase_started=now,
                     _phase_cpu=sample.get("cpu_stat", {}), memory_samples=0,
                     sample_interval_seconds=interval, phases={}, complete=False)
    current = state["_phase"]
    phases = state["phases"]
    def update_phase():
        row = phases.setdefault(current, dict(working_memory_peak=0, memory_samples=0, cpu_stat={}))
        row["elapsed_seconds"] = max(row.get("elapsed_seconds", 0), now-state["_phase_started"])
        for key, value in sample.get("cpu_stat", {}).items():
            baseline = state["_phase_cpu"].get(key, value)
            if value < baseline: errors.append("CPU counter decreased: " + key)
            row["cpu_stat"][key] = max(row["cpu_stat"].get(key, 0), value-baseline)
        return row
    update_phase()
    if phase is not None and phase != current:
        if phase not in ("build", "test", "accept") or phase in phases:
            errors.append("invalid phase transition: " + phase)
        else:
            state.update(_phase=phase, _phase_started=now, _phase_cpu=sample.get("cpu_stat", {}))
            current = phase
    row = update_phase()
    if "working_memory_peak" in sample:
        state["memory_samples"] += 1
        row["memory_samples"] += 1
        row["working_memory_peak"] = max(row["working_memory_peak"], sample["working_memory_peak"])
    sample.update(elapsed_seconds=now-state["_started"], complete=final and not errors)
    state = merge(state, sample)
    if errors and not state.get("error"): state["error"] = "; ".join(errors)
    return state
def observation(path, phase=None, final=False, initial=False):
    with locked(path.with_suffix(".lock")):
        if path.exists():
            state = json.loads(path.read_text())
        elif initial:
            state = {}
        else:
            raise RuntimeError("sampler state unavailable")
        state = observe(state, phase, final)
        atomic(path, state)
        return public(state)
'''
exec(common)
probe = common + r'''
import sys
try:
    report = observation(pathlib.Path(sys.argv[1]), final=True)
except Exception as error:
    report = dict(error="final observation: " + str(error), complete=False)
print("@@s397-resources " + json.dumps(report), flush=True)
'''
sampler = common + r'''
import selectors, subprocess, sys
path, command = pathlib.Path(sys.argv[1]), sys.argv[2]
output_open = True
def output(data):
    global output_open
    if output_open:
        try:
            sys.stdout.buffer.write(data)
            sys.stdout.buffer.flush()
        except BrokenPipeError:
            output_open = False
            # Cancellation loses the host pipe, but local sampling continues.
            os.dup2(os.open(os.devnull, os.O_WRONLY), sys.stdout.fileno())
def snapshot(**options):
    report = observation(path, **options)
    output(b"@@s397-resources " + json.dumps(report).encode() + b"\n")
def line(data):
    if data.startswith(b"@@result "):
        event = json.loads(data[len(b"@@result "):])
        if event.get("kind") == "phase_start": snapshot(phase=event["phase"])
    output(data)
process = None
selector = selectors.DefaultSelector()
pending = b""
try:
    snapshot(initial=True)
    process = subprocess.Popen(["sh", "-c", command], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    selector.register(process.stdout, selectors.EVENT_READ)
    end, next_sample = time.monotonic()+1590, time.monotonic()+interval
    while selector.get_map() or process.poll() is None:
        now = time.monotonic()
        if now >= end: raise TimeoutError("in-container observer exceeded 1590 seconds")
        if now >= next_sample:
            snapshot()
            next_sample = time.monotonic()+interval
        for key, _ in selector.select(max(0, min(next_sample, end)-time.monotonic())):
            chunk = os.read(key.fd, 65536)
            if not chunk:
                selector.unregister(key.fileobj)
                continue
            pending += chunk
            while b"\n" in pending:
                complete, pending = pending.split(b"\n", 1)
                line(complete+b"\n")
            if len(pending) > 1024*1024:
                output(pending)
                pending = b""
    if pending: line(pending+b"\n")
    code = process.wait(timeout=1)
    snapshot(final=True)
except Exception as error:
    try:
        with locked(path.with_suffix(".lock")):
            state = json.loads(path.read_text()) if path.exists() else {}
            state = merge(state, dict(error=str(error)))
            atomic(path, state)
        output(b"@@s397-resources " + json.dumps(public(state)).encode() + b"\n")
    finally:
        if process is not None:
            process.kill()
            try: process.wait(timeout=1)
            except subprocess.TimeoutExpired: pass
    code = 1
finally:
    selector.close()
sys.exit(code if code >= 0 else 128-code)
'''
def read_cid(path):
    try: return pathlib.Path(path).read_text().strip()
    except FileNotFoundError: return ""
def registration_for(cid):
    for registration in root.glob("run-*.json"):
        run = json.loads(registration.read_text())
        if cid == (run.get("cid") or read_cid(run["cidfile"])): return run
    return {}
def save(cid, report, removal_error=False):
    if not re.fullmatch("[0-9a-f]{64}", cid):
        raise RuntimeError("cannot identify observed container: " + repr(cid))
    run = registration_for(cid)
    report = dict(report, container_id=cid, container_name=run.get("name", ""))
    path = reports / (cid + ".json")
    with locked(reports / (cid + ".lock")):
        if path.exists():
            prior = json.loads(path.read_text())
            # The workload can finish while cleanup's final probe is failing.
            if removal_error and completed(prior): return prior
            report = merge(prior, report)
        atomic(path, report)
    return report
def value(flag):
    for i, arg in enumerate(args):
        if arg == flag and i+1 < len(args): return args[i+1]
        if arg.startswith(flag + "="): return arg[len(flag)+1:]
    return ""
def resolve(ref):
    if re.fullmatch("[0-9a-f]{64}", ref): return ref
    for path in root.glob("run-*.json"):
        run = json.loads(path.read_text())
        cid = run.get("cid") or read_cid(run["cidfile"])
        if cid and (ref == run["name"] or cid.startswith(ref)): return cid
    result = subprocess.run([real, "inspect", "--format", "{{.Id}}", ref],
                            capture_output=True, text=True, timeout=2)
    if result.returncode: raise RuntimeError(result.stderr.strip())
    return result.stdout.strip()
def extract(data):
    for line in data.splitlines():
        if line.startswith(marker): return json.loads(line[len(marker):])
    raise RuntimeError("container did not emit a cgroup resource report")
if args and args[0] == "rm":
    try:
        for ref in (arg for arg in args[1:] if not arg.startswith("-")):
            cid = ""
            try:
                cid = resolve(ref)
                prior = reports / (cid + ".json")
                # Only completed, healthy evidence tolerates a late removal.
                if prior.exists() and completed(json.loads(prior.read_text())): continue
                run = registration_for(cid)
                result = subprocess.run([real, "exec", cid, "python3", "-c", probe, run["state"]],
                                        capture_output=True, timeout=3)
                if result.returncode: raise RuntimeError(result.stderr.decode(errors="replace").strip())
                save(cid, extract(result.stdout))
            except Exception as error:
                try: save(cid, dict(error="before removal of " + ref + ": " + str(error)), removal_error=True)
                except Exception as save_error: print(str(save_error), file=sys.stderr)
    finally:
        os.execv(real, [real, *args])
if args and args[0] == "run":
    if args[-3:-1] != ["sh", "-c"]:
        sys.exit("sandbox resource observer expected the sandbox's sh -c entry")
    cidfile = value("--cidfile")
    if not cidfile:
        cidfile = str(root / ("cid-" + uuid.uuid4().hex))
        args[1:1] = ["--cidfile", cidfile]
    registration = root / ("run-" + uuid.uuid4().hex + ".json")
    identity = dict(cidfile=cidfile, name=value("--name"),
                    state="/tmp/invariant-resource-" + uuid.uuid4().hex + ".json")
    atomic(registration, identity)
    args[-1] = "exec python3 -c " + shlex.quote(sampler) + " " + shlex.quote(identity["state"]) + " " + shlex.quote(args[-1])
    process = subprocess.Popen([real, *args], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    pending = b""
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ)
    end = time.monotonic() + 1590
    identified = False
    def receive(line):
        global identified
        if line.startswith(marker):
            report = save(read_cid(cidfile), json.loads(line[len(marker):]))
            if not identified:
                identity = {key: report[key] for key in ("container_id", "container_name")}
                sys.stdout.buffer.write(b"@@s397-container " + json.dumps(identity).encode() + b"\n")
                identified = True
            # Periodic reports live on the host; only the final one expands the log.
            if report.get("complete"):
                sys.stdout.buffer.write(marker + json.dumps(report).encode() + b"\n")
        else:
            sys.stdout.buffer.write(line+b"\n")
        sys.stdout.buffer.flush()
    try:
        while selector.get_map():
            if time.monotonic() >= end: raise TimeoutError("resource observer's Docker run exceeded 1590 seconds")
            if not identity.get("cid") and read_cid(cidfile):
                identity["cid"] = read_cid(cidfile)
                atomic(registration, identity)
            for key, _ in selector.select(timeout=max(0, min(1, end-time.monotonic()))):
                chunk = os.read(key.fd, 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                pending += chunk
                while b"\n" in pending:
                    line, pending = pending.split(b"\n", 1)
                    receive(line)
                if len(pending) > 1024*1024:
                    sys.stdout.buffer.write(pending)
                    sys.stdout.buffer.flush()
                    pending = b""
        if pending: receive(pending)
        code = process.wait(timeout=2)
        cid = read_cid(cidfile)
        path = reports / (cid + ".json")
        if not path.exists() or not json.loads(path.read_text()).get("complete"):
            save(cid, dict(error="container exited without a successful final observation"))
    except Exception as error:
        try: save(read_cid(cidfile), dict(error=str(error)))
        finally:
            process.kill()
            try: process.wait(timeout=2)
            except subprocess.TimeoutExpired: pass
        code = 1
    finally:
        selector.close()
    sys.exit(code if code >= 0 else 128-code)
os.execv(real, [real, *args])
`

// Exercise each production Backend.Run rather than copying its defaults into
// the assertion. Only the CLI process is replaced; it records the arguments or
// single timeout environment variable the real backend actually supplied.
func sandboxBudgetBackendLimit(t *testing.T, name string, timeout time.Duration) time.Duration {
	t.Helper()
	dir := t.TempDir()
	const standIn = `#!/bin/sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
printf '%s\n' "$@" > "$here/args"
printf '%s\n' "${MCP_TOOL_TIMEOUT-}" > "$here/tool-timeout"
cat "$here/events"
`
	events := "{\"type\":\"thread.started\",\"thread_id\":\"sandbox-budget\"}\n" +
		"{\"type\":\"turn.started\"}\n" +
		"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\n"
	if name == "claude-code" {
		events = "{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"Done.\",\"total_cost_usd\":0,\"num_turns\":1}\n"
	}
	for file, data := range map[string]string{"agent": standIn, "events": events} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var backend synth.Backend
	switch name {
	case "codex":
		backend = synth.Codex{Binary: filepath.Join(dir, "agent"), Home: filepath.Join(dir, "home"), MaxTurns: 5}
	case "claude-code":
		backend = synth.ClaudeCode{Binary: filepath.Join(dir, "agent"), MaxTurns: 5, BudgetUSD: 1}
	default:
		t.Fatalf("unhandled agent backend %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	usage, err := backend.Run(ctx, synth.Job{
		Workspace: t.TempDir(), Prompt: "Record the test tool's configured limit.",
		GateServer: []string{"invariant", "mcp", "-plumbing"}, Tools: []string{"test"}, Transcript: io.Discard,
		ToolTimeout: timeout,
	})
	if err != nil || usage.Outcome != "success" {
		t.Fatalf("%s's stand-in backend did not complete: %v, usage %+v", name, err, usage)
	}
	filename, unit := "tool-timeout", time.Millisecond
	if name == "codex" {
		filename, unit = "args", time.Second
	}
	data, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	value := strings.TrimSpace(string(data))
	if name == "codex" {
		matches := regexp.MustCompile(`(?:^|[ ,{])tool_timeout_sec=([0-9]+)(?:[, }]|$)`).FindAllStringSubmatch(value, -1)
		if len(matches) != 1 {
			t.Fatalf("expected one actual Codex MCP tool limit, found %d in %q", len(matches), value)
		}
		value = matches[0][1]
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		t.Fatalf("%s emitted an invalid tool limit %q: %v", name, value, err)
	}
	return time.Duration(n) * unit
}
