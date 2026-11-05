package plumbing

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Reuse the existing dependency Docker process double for image inspection,
// staged input round trips and commits. Only the executable boundary is fake.
// The wrapper also records literal argv and handles persistent exec and the
// built-in sandbox, neither of which needs real Docker for these assertions.
func cpu622Boundary(t *testing.T) string {
	t.Helper()
	bin, logs, state := t.TempDir(), t.TempDir(), t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := `#!/bin/sh
set -eu
printf '%s\000' "$@" > "$CPU622_CALLS/$$"
case "$1" in
  run) printf '%s\n' '@@invariant vet=0' '@@invariant test=0'; exit 0 ;;
  exec) printf '%s\n' 'cpu622 check output'; exit 0 ;;
esac
exec ` + quote(exe) + ` -test.run=^TestDependencyDockerProcess$ -- "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CPU622_CALLS", logs)
	t.Setenv("INVARIANT_DEPENDENCY_DOCKER_TEST", state)
	t.Setenv("INVARIANT_DEPENDENCY_DOCKER_MODE", "success")
	return logs
}

func cpu622Calls(t *testing.T, dir string) [][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"))
	}
	return calls
}

func cpu622Flag(args []string, flag string) []string {
	var values []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			values = append(values, args[i+1])
		} else if value, ok := strings.CutPrefix(a, flag+"="); ok {
			values = append(values, value)
		}
	}
	return values
}

func cpu622AssertLaunch(t *testing.T, calls [][]string, image, network string, want float64) []string {
	t.Helper()
	var launch []string
	for _, args := range calls {
		if len(args) > 0 && (args[0] == "run" || args[0] == "create") {
			if launch != nil {
				t.Fatal("more than one container launched for one environment")
			}
			launch = args
		}
	}
	at := slices.Index(launch, image)
	if at < 1 {
		t.Fatalf("missing launch of selected image %s: %q", image, calls)
	}
	opts := launch[:at]
	values := cpu622Flag(opts, "--cpus")
	if len(values) != 1 {
		t.Errorf("container must have exactly one CPU limit before the image: %q", launch)
	} else if got, err := strconv.ParseFloat(values[0], 64); err != nil || got != want {
		t.Errorf("CPU limit = %q, want %g", values[0], want)
	}
	for flag, want := range map[string]string{"--network": network, "--memory": "4g", "--pids-limit": "1024"} {
		if values := cpu622Flag(opts, flag); len(values) != 1 || values[0] != want {
			t.Errorf("changed %s: %q", flag, launch)
		}
	}
	if !slices.Contains(opts, "--init") {
		t.Error("container lost its init process")
	}
	return launch
}

func TestSandboxContainerCPU622(t *testing.T) {
	for _, value := range []string{"", "0.5", "6"} {
		t.Run("cpus="+value, func(t *testing.T) {
			t.Setenv("INVARIANT_CONTAINER_CPUS", value)
			want := 4.0
			if value != "" {
				want, _ = strconv.ParseFloat(value, 64)
			}
			logs := cpu622Boundary(t)
			root := t.TempDir()
			for name, text := range map[string]string{
				"go.mod":               "module example.invalid/cpu622\n\ngo 1.27.1\n",
				"value.go":             "package fixture\n",
				"value_accept_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {}\n",
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			plan := &Plan{Name: "CPU fixture", Summary: "Capture a real sandbox launch.", Files: []string{"value.go"},
				Tests: []Test{{Name: "TestValue", File: "value_accept_test.go", Says: "Fixture."}}}
			const image = "cpu622-sandbox"
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err := (Sandbox{Image: image, ModCache: t.TempDir(), Cache: t.TempDir(), Original: root}).Run(ctx, root, plan)
			if err != nil {
				t.Fatal(err)
			}
			launch := cpu622AssertLaunch(t, cpu622Calls(t, logs), image, "none", want)
			for _, env := range []string{"GOMAXPROCS=2", "GOFLAGS=-mod=readonly -p=1", "GOPROXY=off", "GOTOOLCHAIN=local"} {
				if !slices.Contains(launch, env) {
					t.Errorf("sandbox lost %s: %q", env, launch)
				}
			}
		})
	}
}

func cpu622Request(t *testing.T, mode string) DependencyRequest {
	t.Helper()
	source := t.TempDir()
	const mod = "module example.invalid/app\n\ngo 1.27.1\n"
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte(mod), 0644); err != nil {
		t.Fatal(err)
	}
	q := DependencyRequest{Token: "invariant-deps-regression", Image: baseID468, Platform: "linux/amd64", Directory: ".",
		SourceDir: source, Env: dependencyEnv("go", false), Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024}
	switch mode {
	case "install":
		q.Image, q.Key = image468, hash468("recipe")
		q.Argv, q.Env = []string{"go", "mod", "download"}, dependencyEnv("go", true)
		q.Inputs = map[string]string{"go.mod": hash468(mod)}
		q.Network, q.Pull = true, true
	case "run":
		q.Argv = []string{"go", "test", "./...", "literal;$(no-shell)"}
	}
	return q
}

func cpu622Configured(ctx context.Context, t *testing.T, q DependencyRequest, mode string) error {
	t.Helper()
	r := &DockerDependencyRunner{}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.Cleanup(cleanup, q.Token); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	}()
	switch mode {
	case "install":
		_, err := r.Install(ctx, q)
		return err
	case "run":
		_, err := r.Run(ctx, q)
		return err
	default:
		env, err := r.OpenChecks(ctx, q)
		if err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := env.Close(cleanup); err != nil {
				t.Errorf("close environment: %v", err)
			}
		}()
		out, err := env.Run(ctx, []string{"fixture-check", "literal;$(no-shell)"})
		if err == nil && string(out) != "cpu622 check output\n" {
			t.Errorf("persistent command output changed: %q", out)
		}
		return err
	}
}

func TestConfiguredContainersCPU622(t *testing.T) {
	for _, mode := range []string{"install", "run", "persistent"} {
		for _, value := range []string{"", "0.5", "6"} {
			t.Run(mode+"/cpus="+value, func(t *testing.T) {
				t.Setenv("INVARIANT_CONTAINER_CPUS", value)
				want := 4.0
				if value != "" {
					want, _ = strconv.ParseFloat(value, 64)
				}
				logs := cpu622Boundary(t)
				q := cpu622Request(t, mode)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if err := cpu622Configured(ctx, t, q, mode); err != nil {
					t.Fatalf("%s: %v", mode, err)
				}
				calls := cpu622Calls(t, logs)
				network := "none"
				if mode == "install" {
					network = "bridge"
				}
				launch := cpu622AssertLaunch(t, calls, baseID468, network, want)
				opts := launch[:slices.Index(launch, baseID468)]
				for flag, expected := range map[string]string{"--pull": "never", "--platform": "linux/amd64", "--cap-drop": "ALL", "--security-opt": "no-new-privileges", "--entrypoint": "/usr/bin/env"} {
					if values := cpu622Flag(opts, flag); len(values) != 1 || values[0] != expected {
						t.Errorf("configured container lost %s=%s: %q", flag, expected, launch)
					}
				}
				removed := false
				for _, args := range calls {
					if args[0] == "rm" && slices.Contains(args, q.Token) {
						removed = true
					}
					if args[0] != "create" && len(cpu622Flag(args, "--cpus")) != 0 {
						t.Errorf("CPU option leaked to a non-create operation: %q", args)
					}
				}
				if !removed {
					t.Error("container cleanup was lost")
				}
			})
		}
	}
}

func TestConfiguredContainersRejectInvalidCPU622(t *testing.T) {
	for _, mode := range []string{"install", "run", "persistent"} {
		for _, value := range []string{"0", "-1", "0.001", "NaN", "bad"} {
			t.Run(mode+"/cpus="+value, func(t *testing.T) {
				t.Setenv("INVARIANT_CONTAINER_CPUS", value)
				logs := cpu622Boundary(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				err := cpu622Configured(ctx, t, cpu622Request(t, mode), mode)
				if err == nil || !strings.Contains(err.Error(), "INVARIANT_CONTAINER_CPUS") {
					t.Errorf("invalid CPU value %q was not diagnosed by %s: %v", value, mode, err)
				}
				for _, args := range cpu622Calls(t, logs) {
					if args[0] == "create" || args[0] == "start" || args[0] == "run" {
						t.Errorf("invalid CPU setting launched a container: %q", args)
					}
				}
			})
		}
	}
}
