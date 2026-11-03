package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// Run the actual command in a child test process. No agent CLI or Docker
// daemon is involved; the Builder still constructs its own MCP command.
func check458Child(marker string) []string {
	for i, arg := range os.Args {
		if arg == marker {
			return os.Args[i+1:]
		}
	}
	return nil
}

func TestWatchCheckParallelFlag(t *testing.T) {
	if args := check458Child("check458-watch"); args != nil {
		os.Exit(watchCmd(context.Background(), args))
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, append([]string{"-test.run=^TestWatchCheckParallelFlag$", "--", "check458-watch"}, args...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("-h")
	if err != nil {
		t.Fatalf("watch help: %v\n%s", err, out)
	}
	if !regexp.MustCompile(`(?m)-check-parallel int\n[^\n]*\(default 1\)`).MatchString(out) {
		t.Fatalf("watch does not advertise a separate check limit defaulting to one:\n%s", out)
	}
	// Invalid limits must be rejected before contacting GitHub or tools.
	bin, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	for _, tool := range []string{"gh", "git", "docker", "go", "codex", "claude"} {
		check458Write(t, bin, tool, "#!/bin/sh\necho called >> "+check458Quote(calls)+"\nexit 1\n", 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, limit := range []string{"0", "-1"} {
		out, err = run("-repo", "owner/repo", "-check-parallel", limit)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 || strings.Contains(out, "flag provided but not defined") {
			t.Errorf("invalid limit %s was not validated: %v\n%s", limit, err, out)
		}
	}
	if data, err := os.ReadFile(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid check limit ran an external tool: %q, %v", data, err)
	}
}

// This test pins only the queue's public wiring: an owned queue on Builder,
// and status through WithCheckStatus. Its transport is deliberately opaque.
func TestWatcherBuildChecksShareTheirMCPQueue(t *testing.T) {
	if args := check458Child("check458-mcp"); args != nil {
		if len(args) == 0 || args[0] != "mcp" {
			os.Exit(2)
		}
		os.Exit(mcpCmd(context.Background(), args[1:]))
	}
	for _, toolFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("toolFirst=%v", toolFirst), func(t *testing.T) {
			f := newCheck458(t)
			q, err := plumbing.NewCheckQueue(1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = q.Close() })
			first, _ := f.start("one", toolFirst, q, 30*time.Second, nil)
			f.waitFile("one.1.entered")
			waiting := make(chan bool, 8)
			second, agent := f.start("two", !toolFirst, q, 30*time.Second, waiting)
			select {
			case job := <-agent.started:
				if job.ToolTimeout < 30*time.Second {
					t.Errorf("queued build's tool allowance %v is below its build allowance", job.ToolTimeout)
				}
			case <-f.ctx.Done():
				t.Fatal("the second agent could not start while the first checked")
			}
			f.waitStatus(waiting)
			f.absent("two.1.entered")
			select {
			case got := <-second:
				t.Fatalf("queued build returned before the occupied check ended: %+v", got)
			default:
			}
			f.release("one", 1)
			f.release("one", 2)
			f.waitFile("two.1.entered")
			f.release("two", 1)
			f.release("two", 2)
			for _, done := range []<-chan check458Result{first, second} {
				got := f.result(done)
				if got.err != nil || got.result == nil || !got.result.Built() {
					t.Fatalf("serialized check failed: %+v, %v", got.result, got.err)
				}
			}
			// Each tool call passed and was recorded by the real MCP server;
			// the builder also ran its own fresh check afterwards.
			toolID := "two"
			if toolFirst {
				toolID = "one"
			}
			f.waitFile(toolID + ".2.entered")
			data, err := os.ReadFile(filepath.Join(f.out[toolID], "test-runs.jsonl"))
			var run synth.GateRun
			if err != nil || json.Unmarshal(bytes.TrimSpace(data), &run) != nil || !run.Passed || run.Run != 1 {
				t.Fatalf("real MCP check did not record one passing run: %s, %v", data, err)
			}
		})
	}
}

func TestMCPCheckWaitReportsBuildTimeout(t *testing.T) {
	f := newCheck458(t)
	q, err := plumbing.NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	first, _ := f.start("holder", false, q, 30*time.Second, nil)
	f.waitFile("holder.1.entered")
	waiting := make(chan bool, 8)
	second, _ := f.start("expired", true, q, 2*time.Second, waiting)
	f.waitStatus(waiting)
	got := f.result(second)
	if !errors.Is(got.err, context.DeadlineExceeded) || got.result == nil || got.result.Built() || !strings.Contains(strings.ToLower(got.result.Summary), "waiting for checks") {
		t.Fatalf("MCP wait lost the build timeout or its reason: %+v, %v", got.result, got.err)
	}
	f.absent("expired.1.entered")
	// Ending the blocked child must withdraw its request, not leave it to
	// launch a container later or keep the next build from checking.
	f.release("holder", 1)
	if got := f.result(first); got.err != nil || got.result == nil || !got.result.Built() {
		t.Fatalf("holder failed: %+v, %v", got.result, got.err)
	}
	third, _ := f.start("next", false, q, 30*time.Second, nil)
	f.waitFile("next.1.entered")
	f.release("next", 1)
	if got := f.result(third); got.err != nil || got.result == nil || !got.result.Built() {
		t.Fatalf("queue was not reusable: %+v, %v", got.result, got.err)
	}
	f.absent("expired.1.entered")
}

type check458Result struct {
	result *plumbing.BuildResult
	err    error
}

type check458Agent struct {
	id      string
	tool    bool
	started chan synth.Job
}

func (*check458Agent) Name() string { return "fixture" }

func (a *check458Agent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		return synth.Usage{Outcome: "success", Summary: "VERDICT: approve"}, nil
	}
	text := "package page\n\n// fixture-id: " + a.id + "\nfunc Hello() string { return \"hello\" }\n"
	if err := os.WriteFile(filepath.Join(job.Workspace, "page", "page.go"), []byte(text), 0o644); err != nil {
		return synth.Usage{}, err
	}
	a.started <- job
	if a.tool {
		cmd := exec.CommandContext(ctx, job.GateServer[0], job.GateServer[1:]...)
		cmd.WaitDelay = time.Second
		cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"test\",\"arguments\":{}}}\n")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return synth.Usage{}, fmt.Errorf("MCP test: %w: %s", err, stderr.String())
		}
		var response struct {
			Error  json.RawMessage `json:"error"`
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(out), &response); err != nil || len(response.Error) != 0 || response.Result.IsError || len(response.Result.Content) != 1 || !strings.Contains(response.Result.Content[0].Text, "Everything passed") {
			return synth.Usage{}, fmt.Errorf("MCP check did not pass: %s; stderr=%s", out, stderr.String())
		}
	}
	return synth.Usage{Outcome: "success", Summary: "Wrote the page."}, nil
}

type check458Fixture struct {
	t       *testing.T
	ctx     context.Context
	control string
	binary  string
	out     map[string]string
}

func newCheck458(t *testing.T) *check458Fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	f := &check458Fixture{t: t, ctx: ctx, control: t.TempDir(), out: map[string]string{}}
	bin := t.TempDir()
	check458Write(t, bin, "docker", check458Docker, 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHECK458_CONTROL", f.control)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.binary = filepath.Join(bin, "invariant")
	check458Write(t, bin, "invariant", "#!/bin/sh\nexec "+check458Quote(exe)+" '-test.run=^TestWatcherBuildChecksShareTheirMCPQueue$' -- check458-mcp \"$@\"\n", 0o755)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(f.control, "release-all"), nil, 0o644) })
	return f
}

func (f *check458Fixture) start(id string, tool bool, q *plumbing.CheckQueue, budget time.Duration, status chan bool) (<-chan check458Result, *check458Agent) {
	f.t.Helper()
	root := f.t.TempDir()
	const source = "package page\n\nimport \"testing\"\n\nfunc TestPageSaysHello(t *testing.T) { if Hello() != \"hello\" { t.Fatal(\"wrong greeting\") } }\n"
	p := plumbing.Plan{Name: "hello", Summary: "Say hello.", Files: []string{"page/page.go"}, Tests: []plumbing.Test{{Name: "TestPageSaysHello", File: "page/page_accept_test.go", Says: "Say hello."}}, Sources: map[string]string{"page/page_accept_test.go": source}}
	lock := plumbing.Lock{Ratified: plumbing.Ratification{Issue: 458, By: "gitdek", Comment: "https://github.com/o/r/issues/458#issuecomment-1", Proposal: p.Hash()}, Plan: p}
	if err := lock.Write(root); err != nil {
		f.t.Fatal(err)
	}
	check458Write(f.t, root, "go.mod", "module fixture\n\ngo 1.27.1\n", 0o644)
	check458Write(f.t, root, "page/page.go", "package page\n", 0o644)
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "-A"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "fixture"}} {
		cmd := exec.CommandContext(f.ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			f.t.Fatalf("git fixture: %v\n%s", err, out)
		}
	}
	a := &check458Agent{id: id, tool: tool, started: make(chan synth.Job, 1)}
	b := plumbing.Builder{Backend: a, Reviewer: a, Binary: f.binary, TestRuns: 2, Timeout: budget, Checks: q, Sandbox: plumbing.Sandbox{Image: "fixture", ModCache: f.t.TempDir()}}
	f.out[id] = f.t.TempDir()
	out := f.out[id]
	ctx, cancel := context.WithCancel(f.ctx)
	if status != nil {
		ctx = plumbing.WithCheckStatus(ctx, func(waiting bool) { status <- waiting })
	}
	done := make(chan check458Result, 1)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		r, err := b.Build(ctx, root, 458, out)
		done <- check458Result{r, err}
	}()
	f.t.Cleanup(func() {
		cancel()
		_ = os.WriteFile(filepath.Join(f.control, "release-all"), nil, 0o644)
		select {
		case <-ended:
		case <-time.After(7 * time.Second):
			f.t.Errorf("build %s did not stop during cleanup", id)
		}
	})
	return done, a
}

func (f *check458Fixture) result(done <-chan check458Result) check458Result {
	f.t.Helper()
	select {
	case got := <-done:
		return got
	case <-f.ctx.Done():
		f.t.Fatal("build did not finish")
		return check458Result{}
	}
}

func (f *check458Fixture) waitStatus(ch <-chan bool) {
	f.t.Helper()
	for {
		select {
		case waiting := <-ch:
			if waiting {
				return
			}
		case <-f.ctx.Done():
			f.t.Fatal("no waiting-for-checks status while another check held the queue")
		}
	}
}

func (f *check458Fixture) waitFile(name string) {
	f.t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(f.control, name)); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-f.ctx.Done():
			f.t.Fatalf("sandbox never reported %s", name)
		}
	}
}

func (f *check458Fixture) absent(name string) {
	f.t.Helper()
	if _, err := os.Stat(filepath.Join(f.control, name)); !errors.Is(err, os.ErrNotExist) {
		f.t.Fatalf("sandbox %s started without admission, or couldn't be observed: %v", name, err)
	}
}

func (f *check458Fixture) release(id string, n int) {
	f.t.Helper()
	check458Write(f.t, f.control, fmt.Sprintf("%s.%d.release", id, n), "", 0o644)
}

func check458Write(t *testing.T, root, name, text string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func check458Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

const check458Docker = `#!/bin/sh
[ "$1" = run ] || exit 0
src=
for a in "$@"; do
  case "$a" in *:/src) src=${a%:/src} ;; esac
done
id=$(sed -n 's|^// fixture-id: ||p' "$src/page/page.go")
[ -n "$id" ] || exit 1
n=1
while [ -e "$CHECK458_CONTROL/$id.$n.entered" ]; do n=$((n+1)); done
: > "$CHECK458_CONTROL/$id.$n.entered"
while [ ! -e "$CHECK458_CONTROL/$id.$n.release" ] && [ ! -e "$CHECK458_CONTROL/release-all" ]; do sleep 0.01; done
echo '@@invariant vet=0'
echo '@@invariant test=0'
echo '--- PASS: TestPageSaysHello (0.00s)'
echo '@@invariant accept=0'
`
