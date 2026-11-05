package plumbing_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
)

// TestSandboxBudgetAndRealCheckout pins the ordinary ten/twelve-minute limits
// and #498's twenty/twenty-five-minute exception for internal/verify, with eight
// global queue workers. The whole run includes preparation and acceptance.
// Overrides only shorten the selected limits. A separate minute for startup
// and fifteen seconds for cleanup fit within the selected backend tool timer.
//
// The merger runs the full checkout in two waves: two simultaneous cold-cache
// runs must pass, then three simultaneous fresh-cache runs must return results
// within the budget, with partial results allowed. Both waves measure peaks:
//
//	INVARIANT_PLUMBING_REAL=1 go test ./internal/plumbing -run '^TestSandboxBudgetAndRealCheckout$' -count=1 -timeout 70m
//
// Image acquisition is outside the timed runs, as it is when the watcher starts.
// Only this subtest's plan touches internal/verify: this acceptance file belongs
// to plumbing so #397 can itself be built by the old, unsharded sandbox.
// The real-image check records kernel cgroup high-water marks on normal exit
// or just before forced removal, including compilation's peaks. Host report
// files survive cancellation of the run's Docker CLI and output pipe.
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
		{"verify whole run", plumbing.SandboxVerifyTimeout, 25 * time.Minute},
		{"verify touched-test phase", plumbing.SandboxVerifyShardTimeout, 20 * time.Minute},
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
		image, err := toolchain.PlumbingImage(setupCtx)
		if err != nil {
			t.Fatalf("acquire the real pinned sandbox image: %v", err)
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
			t.Logf("running %d concurrent cold-cache containers, each with 8 global queue workers, 4 GiB and 1024 PIDs: aggregate caps %d GiB and %d PIDs; sandbox deadline %s, outer budget %s", wave.runs, wave.runs*4, wave.runs*1024, plumbing.SandboxVerifyTimeout, budget)
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
				sandboxBudgetCheckResources(t, i+1, "@@s397-resources "+string(data))
			}
		}
	})
}

// Wrap the real Docker CLI to read kernel-maintained peaks on normal exit and
// immediately before forced removal. A cidfile identifies each container; host
// report files survive loss of the run's output pipe on cancellation. No test
// commands, limits or mounts are replaced. The extra inspection/exec calls are
// bounded and never prevent delegation of the original removal command.
// A polling docker-stats sample can miss compilation peaks; memory.peak and
// pids.peak include every process since container creation. Missing counters or
// observer failures fail only the opt-in check. It needs a cgroup-v2 Docker host.
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
		MemoryPeak  int64  `json:"memory_peak"`
		MemoryLimit int64  `json:"memory_limit"`
		PIDsPeak    int64  `json:"pids_peak"`
		PIDsLimit   int64  `json:"pids_limit"`
		OOMs        int64  `json:"oom"`
		OOMKills    int64  `json:"oom_kill"`
		PIDsDenied  int64  `json:"pids_denied"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal([]byte(reports[0]), &usage); err != nil || usage.Error != "" {
		t.Errorf("real sandbox %d could not measure resource peaks: %v; %s", run, err, reports[0])
		return
	}
	t.Logf("real sandbox %d: peak memory %d / %d bytes, peak processes %d / %d, OOM events %d, OOM kills %d, denied processes %d", run, usage.MemoryPeak, usage.MemoryLimit, usage.PIDsPeak, usage.PIDsLimit, usage.OOMs, usage.OOMKills, usage.PIDsDenied)
	if usage.MemoryLimit != 4*1024*1024*1024 || usage.PIDsLimit != 1024 {
		t.Errorf("real sandbox %d did not enforce 4 GiB and 1024 PIDs: %+v", run, usage)
	}
	if usage.MemoryPeak <= 0 || usage.MemoryPeak >= usage.MemoryLimit || usage.PIDsPeak <= 0 || usage.PIDsPeak >= usage.PIDsLimit {
		t.Errorf("real sandbox %d did not stay below its memory/process caps: %+v", run, usage)
	}
	if usage.OOMs != 0 || usage.OOMKills != 0 || usage.PIDsDenied != 0 {
		t.Errorf("real sandbox %d hit a memory/process limit: %+v", run, usage)
	}
}

const sandboxBudgetDocker = `#!/usr/bin/env python3
import fcntl, json, os, pathlib, re, selectors, shlex, subprocess, sys, time, uuid
args = sys.argv[1:]
real = os.environ["S397_REAL_DOCKER"]
root = pathlib.Path(os.environ["S397_RESOURCE_DIR"])
reports = root / "reports"
marker = b"@@s397-resources "
probe = '''import json, pathlib
root = pathlib.Path("/sys/fs/cgroup")
def counter(name):
    return int((root / name).read_text().strip())
def events(name):
    return {key: int(value) for key, value in (line.split() for line in (root / name).read_text().splitlines())}
try:
    memory = events("memory.events")
    pids = events("pids.events")
    report = dict(memory_peak=counter("memory.peak"), memory_limit=counter("memory.max"),
                  pids_peak=counter("pids.peak"), pids_limit=counter("pids.max"),
                  oom=memory["oom"], oom_kill=memory["oom_kill"], pids_denied=pids["max"])
except Exception as error:
    report = dict(error=str(error))
print("\\n@@s397-resources " + json.dumps(report), flush=True)
'''
def atomic(path, value):
    tmp = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
    tmp.write_text(json.dumps(value))
    os.replace(tmp, path)
def save(cid, report):
    if not re.fullmatch("[0-9a-f]{64}", cid):
        unknown = cid
        cid = "observer-error-" + uuid.uuid4().hex
        report = dict(error="cannot identify observed container: " + repr(unknown), detail=report)
    path = reports / (cid + ".json")
    with (reports / (cid + ".lock")).open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if path.exists():
            prior = json.loads(path.read_text())
            if not prior.get("error"):
                if report.get("error"):
                    return # An already recorded natural exit can race rm.
                for key in ("memory_peak", "pids_peak", "oom", "oom_kill", "pids_denied"):
                    report[key] = max(prior[key], report[key])
                if any(prior[key] != report[key] for key in ("memory_limit", "pids_limit")):
                    report["error"] = "container limits changed between observations"
        atomic(path, report)
def read_cid(path):
    try: return pathlib.Path(path).read_text().strip()
    except FileNotFoundError: return ""
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
                if prior.exists() and not json.loads(prior.read_text()).get("error"): continue
                result = subprocess.run([real, "exec", cid, "python3", "-c", probe],
                                        capture_output=True, timeout=3)
                if result.returncode: raise RuntimeError(result.stderr.decode(errors="replace").strip())
                save(cid, extract(result.stdout))
            except Exception as error:
                save(cid, dict(error="before removal of " + ref + ": " + str(error)))
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
    identity = dict(cidfile=cidfile, name=value("--name"))
    atomic(registration, identity)
    args[-1] = "sh -c " + shlex.quote(args[-1]) + "\ns397_status=$?\npython3 -c " + shlex.quote(probe) + '\nexit "$s397_status"\n'
    process = subprocess.Popen([real, *args], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    pending = b""
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ)
    end = time.monotonic() + 1590
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
                    if line.startswith(marker): save(read_cid(cidfile), json.loads(line[len(marker):]))
                sys.stdout.buffer.write(chunk)
                sys.stdout.buffer.flush()
        code = process.wait(timeout=2)
    except Exception as error:
        save(read_cid(cidfile), dict(error=str(error)))
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
