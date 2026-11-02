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

// TestSandboxBudgetAndRealCheckout pins #397's public limits: eight global
// shard workers, an eight-minute per-shard ceiling and a ten-minute whole
// Sandbox.Run deadline, including staging, startup, vet and acceptance tests.
// Sandbox.Timeout and Sandbox.ShardTimeout may shorten their defaults, never
// extend them. A separate minute for startup and fifteen seconds for cleanup
// conservatively reserve more time before either backend's unchanged limit.
//
// The merger runs the full checkout, with three cold independent build caches:
//
//	INVARIANT_PLUMBING_REAL=1 go test ./internal/plumbing -run '^TestSandboxBudgetAndRealCheckout$' -count=1 -timeout 20m
//
// Image acquisition is outside the timed runs, as it is when the watcher starts.
// Only this subtest's plan touches internal/verify: this acceptance file belongs
// to plumbing so #397 can itself be built by the old, unsharded sandbox.
// The real-image check also reads kernel cgroup high-water marks after each
// run, before Docker removes its container, including compilation's peaks.
// The sandbox does not forward INVARIANT_PLUMBING_REAL into its container, so
// this same acceptance test runs its default assertions there without nesting
// more Docker runs. The default assertions always run, even without Docker.
func TestSandboxBudgetAndRealCheckout(t *testing.T) {
	for _, limit := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"whole run", plumbing.SandboxTimeout, 10 * time.Minute},
		{"one shard", plumbing.SandboxShardTimeout, 8 * time.Minute},
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
	budget := plumbing.SandboxTimeout + plumbing.SandboxStartupAllowance + plumbing.SandboxCleanupAllowance
	limits := make(map[string]time.Duration)
	for _, name := range []string{"codex", "claude-code"} {
		limit := sandboxBudgetBackendLimit(t, name)
		limits[name] = limit
		if limit != 900*time.Second {
			t.Errorf("%s emitted tool limit %s, want the unchanged 900s limit", name, limit)
		}
		if budget >= limit {
			t.Errorf("sandbox deadline plus startup and cleanup reserves = %s, must be below %s's emitted %s tool limit", budget, name, limit)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	t.Run("real_image", func(t *testing.T) {
		if os.Getenv("INVARIANT_PLUMBING_REAL") != "1" {
			t.Skip("the merger enables the real image check with INVARIANT_PLUMBING_REAL=1")
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
			Summary: "Run the touched plumbing and verify packages, vet and this pinned acceptance test under three concurrent watcher steps.",
			Files:   []string{"internal/plumbing/sandbox.go", "internal/verify/verify.go"},
			Tests: []plumbing.Test{{
				Name: "TestSandboxBudgetAndRealCheckout", File: acceptanceFile,
				Says: "The real checkout's checks finish within the sandbox and both backend tool limits, under three concurrent runs.",
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
		sandboxBudgetObserveResources(t)

		const runs = 3
		var sandboxes [runs]plumbing.Sandbox
		for i := range sandboxes {
			sandboxes[i] = plumbing.Sandbox{
				Image: image, ModCache: strings.TrimSpace(string(mod)),
				Cache: filepath.Join(t.TempDir(), "cold-cache"), Original: root,
			}
		}
		t.Logf("running %d concurrent containers, each with 8 global shard workers, 4 GiB and 1024 PIDs: aggregate caps 12 GiB and 3072 PIDs; sandbox deadline %s, outer budget %s", runs, plumbing.SandboxTimeout, budget)
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		type outcome struct {
			i       int
			result  plumbing.Result
			err     error
			elapsed time.Duration
		}
		ready := make(chan struct{}, runs)
		start := make(chan struct{})
		done := make(chan outcome, runs)
		for i, sb := range sandboxes {
			go func() {
				ready <- struct{}{}
				<-start
				began := time.Now()
				result, err := sb.Run(ctx, root, plan)
				done <- outcome{i: i, result: result, err: err, elapsed: time.Since(began)}
			}()
		}
		for range runs {
			<-ready
		}
		began := time.Now()
		close(start)
		for range runs {
			out := <-done
			t.Logf("real sandbox %d finished in %s", out.i+1, out.elapsed)
			if out.err != nil || !out.result.Passed() {
				t.Errorf("real sandbox %d failed after %s: %v\n%s\n%s", out.i+1, out.elapsed, out.err, out.result.Summary(), out.result.Tail(16000))
			}
			if out.elapsed >= plumbing.SandboxTimeout {
				t.Errorf("real sandbox %d took %s, want less than its %s deadline", out.i+1, out.elapsed, plumbing.SandboxTimeout)
			}
			for name, limit := range limits {
				if out.elapsed >= limit {
					t.Errorf("real sandbox %d took %s, outside %s's %s tool limit", out.i+1, out.elapsed, name, limit)
				}
			}
			if !out.result.Accepted[acceptanceFile+":TestSandboxBudgetAndRealCheckout"] {
				t.Errorf("real sandbox %d did not pass the pinned acceptance test in internal/plumbing", out.i+1)
			}
			sandboxBudgetCheckResources(t, out.i+1, out.result.Output)
		}
		elapsed := time.Since(began)
		t.Logf("all three real sandbox runs completed in %s", elapsed)
		if elapsed >= plumbing.SandboxTimeout {
			t.Errorf("three concurrent sandbox runs took %s, want less than %s", elapsed, plumbing.SandboxTimeout)
		}
	})
}

// Wrap the real Docker CLI only to read kernel-maintained peaks at the end of
// the same container's life. No test commands, limits or mounts are replaced.
// A polling docker-stats sample can miss the short peak during compilation;
// memory.peak and pids.peak include every process since container creation.
// Missing kernel counters fail the opt-in check, rather than claiming a peak
// from sampled usage. The merger uses a cgroup-v2 Docker host with these files.
func sandboxBudgetObserveResources(t *testing.T) {
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
	t.Setenv("S397_REAL_DOCKER", docker)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
import os, shlex, sys
args = sys.argv[1:]
if args and args[0] == "run":
    if args[-3:-1] != ["sh", "-c"]:
        sys.exit("sandbox resource observer expected the sandbox's sh -c entry")
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
    args[-1] = "sh -c " + shlex.quote(args[-1]) + "\ns397_status=$?\npython3 -c " + shlex.quote(probe) + '\nexit "$s397_status"\n'
os.execv(os.environ["S397_REAL_DOCKER"], [os.environ["S397_REAL_DOCKER"], *args])
`

// Exercise each production Backend.Run rather than copying its defaults into
// the assertion. Only the CLI process is replaced; it records the arguments or
// single timeout environment variable the real backend actually supplied.
func sandboxBudgetBackendLimit(t *testing.T, name string) time.Duration {
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
