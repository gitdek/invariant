package plumbing_test

import (
	"context"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The command check always runs, including inside the factory's Docker-free
// test environment. The merger can also exercise the real pinned image with:
//
//	INVARIANT_PLUMBING_INIT_REAL=1 go test ./internal/plumbing -run '^TestSandboxReapsOrphanedProcesses$' -count=1 -timeout 20m
//
// Sandbox.Run does not forward that opt-in variable into its container.
func TestSandboxReapsOrphanedProcesses(t *testing.T) {
	t.Run("container_command", s426ContainerCommand)
	t.Run("real_image", func(t *testing.T) {
		if os.Getenv("INVARIANT_PLUMBING_INIT_REAL") != "1" {
			// Return normally so default acceptance runs still report a pass
			// once the mandatory launch assertion succeeds.
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
		defer cancel()
		image, err := toolchain.PlumbingImage(ctx)
		if err != nil {
			t.Fatalf("acquire the pinned plumbing image: %v", err)
		}
		root, plan := s426Fixture(t)
		result, err := (plumbing.Sandbox{
			Image: image, ModCache: t.TempDir(), Cache: t.TempDir(),
		}).Run(ctx, root, plan)
		if err != nil || !result.Passed() {
			t.Fatalf("real sandbox did not reap the orphan: %v\n%s\n%s", err, result.Summary(), result.Output)
		}
		if !result.Accepted["orphan_accept_test.go:TestOrphanDisappears"] {
			t.Fatal("the sandbox did not pass the pinned orphan test")
		}
		// A skipped or undiscovered fixture is not evidence of reaping.
		for _, phase := range []string{"test", "accept"} {
			found := false
			for _, test := range result.TestResults {
				if test.Name == "TestOrphanDisappears" && test.Phase == phase && test.Status == "passed" {
					found = true
				}
			}
			if !found {
				t.Errorf("no passing orphan test in phase %q: %+v", phase, result.TestResults)
			}
		}
	})
}

func s426ContainerCommand(t *testing.T) {
	root, plan := s426Fixture(t)
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "docker-args")
	s426Write(t, filepath.Join(bin, "docker"), `#!/bin/sh
set -eu
[ "$1" = run ]
printf '%s\000' "$@" > "$S426_DOCKER_ARGS"
printf '%s\n' '@@invariant vet=0' '@@invariant test=0'
`, 0o755)
	t.Setenv("S426_DOCKER_ARGS", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mod, cache := t.TempDir(), t.TempDir()
	const image = "invariant-init-command-fixture"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := (plumbing.Sandbox{Image: image, ModCache: mod, Cache: cache}).Run(ctx, root, plan); err != nil {
		t.Fatalf("capture sandbox launch: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	imageAt := slices.Index(args, image)
	if len(args) == 0 || args[0] != "run" || imageAt < 1 {
		t.Fatalf("not a Docker run of the selected image: %q", args)
	}
	options := args[1:imageAt]
	for _, want := range [][]string{
		{"--init"}, {"--rm"}, {"--network", "none"}, {"--hostname", "localhost"},
		{"--memory", "4g"}, {"--pids-limit", "1024"}, {"-w", "/src"},
		{"-v", mod + ":/gomod:ro"}, {"-v", cache + ":/gocache"},
		{"-e", "GOTOOLCHAIN=local"}, {"-e", "GOFLAGS=-mod=readonly -p=1"},
		{"-e", "GOMAXPROCS=2"}, {"-e", "GOPROXY=off"}, {"-e", "HOME=/tmp"},
		{"-e", "CGO_ENABLED=0"}, {"-e", "GOMODCACHE=/gomod"}, {"-e", "GOCACHE=/gocache"},
	} {
		if !s426HasArgs(options, want) {
			t.Errorf("sandbox launch lacks %q before the image: %q", want, args)
		}
	}
	if uid, gid := os.Getuid(), os.Getgid(); uid >= 0 && gid >= 0 {
		if !s426HasArgs(options, []string{"--user", fmt.Sprintf("%d:%d", uid, gid)}) {
			t.Errorf("sandbox no longer runs as its caller: %q", options)
		}
	}
	sourceMount := false
	for i := 0; i+1 < len(options); i++ {
		if options[i] == "-v" && strings.HasSuffix(options[i+1], ":/src") {
			sourceMount = true
		}
	}
	if !sourceMount {
		t.Errorf("sandbox lost its checkout mount: %q", options)
	}
	if imageAt+3 >= len(args) || args[imageAt+1] != "sh" || args[imageAt+2] != "-c" {
		t.Errorf("sandbox lost its shell entry point: %q", args[imageAt:])
	}
}

func s426HasArgs(args, want []string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		if slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func s426Write(t *testing.T, name, source string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte(source), mode); err != nil {
		t.Fatal(err)
	}
}

func s426Fixture(t *testing.T) (string, *plumbing.Plan) {
	t.Helper()
	root := t.TempDir()
	s426Write(t, filepath.Join(root, "go.mod"), "module example.com/orphan426\n\ngo 1.27.1\n", 0o644)
	s426Write(t, filepath.Join(root, "orphan.go"), "package orphan\n", 0o644)
	source, err := format.Source([]byte(s426OrphanTest))
	if err != nil {
		t.Fatal(err)
	}
	s426Write(t, filepath.Join(root, "orphan_accept_test.go"), string(source), 0o644)
	plan := &plumbing.Plan{
		Name: "Reap a killed orphan", Summary: "A killed orphan disappears inside the plumbing sandbox.",
		Files:   []string{"orphan.go"},
		Tests:   []plumbing.Test{{Name: "TestOrphanDisappears", File: "orphan_accept_test.go", Says: "A killed orphan is reaped."}},
		Sources: map[string]string{"orphan_accept_test.go": string(source)},
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	return root, plan
}

// This source is compiled and executed by Sandbox.Run, never on the host.
// Waiting for the shell to exit before killing its child establishes the
// orphan case; only ESRCH counts as gone, not an arbitrary signal error.
const s426OrphanTest = `package orphan

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOrphanDisappears(t *testing.T) {
	parent := exec.Command("sh", "-c", "sleep 60 </dev/null >/dev/null 2>&1 & echo $!")
	out, err := parent.Output() // Output also waits for the parent to exit.
	if err != nil {
		t.Fatalf("start orphan and wait for its parent: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("invalid orphan pid %q: %v", out, err)
	}
	killed := false
	defer func() {
		if !killed {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil || !strings.Contains(string(status), "\nPPid:\t1\n") {
		t.Fatalf("child was not adopted by container PID 1 after its parent exited: %v\n%s", err, status)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("orphan must still exist before being killed: %v", err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill the orphan: %v", err)
	}
	killed = true
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		if err != nil {
			t.Fatalf("checking orphan %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("killed orphan %d still exists; container PID 1 did not reap it", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
`
