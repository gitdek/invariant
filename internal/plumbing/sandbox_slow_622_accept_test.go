package plumbing

import (
	"context"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rerun the existing fixtures, including all their assertions, with a slow
// test-process stand-in. The Go wrapper adds a TestMain only to their temporary
// fixture package; it never rewrites repository tests or the sandbox runner.
// Compilation and discovery remain real and are not delayed. Each selected
// quick test then spends three seconds starting, before its test body runs.
func slow622ExistingFixture(t *testing.T, selector, pkg, testPrefix string) {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	realGo, err = filepath.Abs(realGo)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, observations := t.TempDir(), t.TempDir()
	source := fmt.Sprintf(`package %s
import ("fmt"; "os"; "path/filepath"; "strings"; "testing"; "time")
func TestMain(m *testing.M) {
  if strings.Contains(strings.Join(os.Args[1:], " "), %q) {
    name := filepath.Join(%q, fmt.Sprint(os.Getpid()))
    if err := os.WriteFile(name+".started", []byte("three-second startup delay"), 0600); err != nil { panic(err) }
    time.Sleep(3*time.Second)
    if err := os.WriteFile(name+".completed", []byte("three-second startup delay"), 0600); err != nil { panic(err) }
  }
  os.Exit(m.Run())
}
`, pkg, testPrefix, observations)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "delay.txt"), formatted, 0600); err != nil {
		t.Fatal(err)
	}
	const wrapper = `#!/usr/bin/env python3
import os, pathlib, sys
root = pathlib.Path.cwd()
mod = root / "go.mod"
if mod.is_file() and "module example.com/shards397" in mod.read_text():
    package = root / os.environ["SLOW622_PACKAGE"]
    if package.is_dir():
        source = pathlib.Path(os.environ["SLOW622_SOURCE"]).read_bytes()
        target = package / "slow_cpu622_test.go"
        if not target.exists(): target.write_bytes(source)
real = os.environ["SLOW622_REAL_GO"]
os.execv(real, [real, *sys.argv[1:]])
`
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLOW622_REAL_GO", realGo)
	t.Setenv("SLOW622_PACKAGE", pkg)
	t.Setenv("SLOW622_SOURCE", filepath.Join(bin, "delay.txt"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// This checks fixture reliability without requiring Docker or a CPU quota.
	// A watchdog is only a guard against a broken fixture, never passing evidence.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	parts := strings.Split(selector, "/")
	pattern := "^" + strings.Join(parts, "$/^") + "$"
	cmd := exec.CommandContext(ctx, exe, "-test.run="+pattern, "-test.v", "-test.count=1", "-test.timeout=3m")
	cmd.WaitDelay = 5 * time.Second
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("fixture subprocess did not finish: %v\n%s", ctx.Err(), out)
	}
	if runErr != nil {
		t.Errorf("%s must keep all its assertions with three-second quick-test startup delays: %v\n%s", selector, runErr, out)
	}
	if !strings.Contains(string(out), "--- PASS: "+selector+" (") || strings.Contains(string(out), "--- SKIP:") {
		t.Errorf("the exact existing fixture must run and pass without skipping: %s\n%s", selector, out)
	}
	entries, err := os.ReadDir(observations)
	if err != nil {
		t.Fatal(err)
	}
	started, completed := 0, 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".started") {
			started++
		}
		if strings.HasSuffix(entry.Name(), ".completed") {
			completed++
		}
	}
	if started == 0 || completed == 0 {
		t.Errorf("the slow-machine stand-in must start and finish a three-second delay in a real quick test process; started=%d completed=%d\n%s", started, completed, out)
	}
}

func TestSandboxShardTimeoutWithSlowHealthyTest622(t *testing.T) {
	slow622ExistingFixture(t, "TestSandboxNamesFailuresFromEveryShard/shard_timeout", "good", "TestHealthy")
}

func TestSandboxSharedBudgetWithSlowQuickTests622(t *testing.T) {
	slow622ExistingFixture(t, "TestSandboxQueueBudgetDoesNotRestartPerTest", "alpha", "TestWork")
}
