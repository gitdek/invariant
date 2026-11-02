package verify_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/verify"
)

// These tests observe the public executor's Docker protocol. The Docker double
// records argv and supplies output bytes; it does not simulate Linux's DAC.
// The integration-tagged real gate test must exercise the actual mounts.
func TestPythonApplicationDockerUsesMountOwner(t *testing.T) {
	for _, options := range []string{
		`[]`,
		`["name=seccomp,profile=builtin","name=cgroupns"]`,
		`["name=rootless-other","name=seccomp,profile=rootless"]`,
	} {
		t.Run(options, func(t *testing.T) {
			f := pa399CommandFixture(t, options, "0")
			pa399SuccessfulCommand(t, f, fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid()))
		})
	}
}

func TestPythonApplicationDockerUsesRootlessRoot(t *testing.T) {
	for _, options := range []string{
		`["name=rootless"]`,
		`["name=seccomp,profile=builtin","name=rootless","name=cgroupns"]`,
		`["name=userns","name=rootless"]`,
	} {
		t.Run(options, func(t *testing.T) {
			f := pa399CommandFixture(t, options, "0")
			// In rootless Docker, container root maps to the host daemon user;
			// passing the host's numeric UID would select a subordinate UID.
			pa399SuccessfulCommand(t, f, "0:0")
		})
	}
}

func TestPythonApplicationDockerRefusesUnknownIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, options, status string
	}{
		{"daemon failure", `["name=rootless"]`, "73"},
		{"empty response", "", "0"},
		{"malformed JSON", `["name=rootless"`, "0"},
		{"wrong shape", `{"rootless":true}`, "0"},
		{"null response", `null`, "0"},
		{"non-string option", `["name=rootless",17]`, "0"},
		{"rootful remapped users", `["name=seccomp,profile=builtin","name=userns"]`, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := pa399CommandFixture(t, tc.options, tc.status)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p, err := verify.RunPythonApplicationCommand(ctx, f.command)
			if err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("unknown mount identity must return a diagnostic before execution: %+v, %v", p, err)
			}
			if _, err := os.Stat(f.runLog); !os.IsNotExist(err) {
				t.Fatalf("application launched despite unknown or unsupported mount identity: %v", err)
			}
			pa399PrivateFilesUnchanged(t, f)
		})
	}
}

type pa399Fixture struct {
	command verify.PythonApplicationCommand
	runLog  string
	modes   map[string]os.FileMode
}

func pa399CommandFixture(t *testing.T, securityOptions, status string) pa399Fixture {
	t.Helper()
	base := t.TempDir()
	privateDir := func(name string) string {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	root, runtimeDir, output := privateDir("application source"), privateDir("runtime"), privateDir("output")
	write := func(name, text string, mode os.FileMode) {
		if err := os.WriteFile(name, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "application.py"), "value = 1\n", 0o600)
	write(filepath.Join(runtimeDir, "runner.py"), "# trusted runner\n", 0o600)
	write(filepath.Join(runtimeDir, "request.json"), "{}\n", 0o600)
	write(filepath.Join(output, "authentication.key"), "bootstrap secret\n", 0o600)
	overlay := filepath.Join(privateDir("larger driver"), "driver.py")
	write(overlay, "Limit = 3\n", 0o400)
	bin := privateDir("bin")
	write(filepath.Join(bin, "docker"), `#!/bin/sh
case "$1" in
  info)
    if [ "$#" != 3 ] || [ "$2" != --format ] || [ "$3" != '{{json .SecurityOptions}}' ]; then
      printf '%s\n' 'expected a structured daemon security-options query' >&2
      exit 89
    fi
    printf '%s\n' "$PA399_SECURITY_OPTIONS"
    if [ "$PA399_INFO_STATUS" != 0 ]; then
      printf '%s\n' 'daemon identity unavailable' >&2
    fi
    exit "$PA399_INFO_STATUS"
    ;;
  run)
    printf '%s\n' "$@" > "$PA399_RUN_LOG"
    printf '%s\n' 'event bytes from application' > "$PA399_OUTPUT/events.jsonl"
    printf '%s\n' 'application stdout'
    printf '%s\n' 'application stderr' >&2
    exit 0
    ;;
  rm) exit 0 ;;
  *) printf '%s\n' 'unexpected Docker command' >&2; exit 89 ;;
esac
`, 0o700)
	f := pa399Fixture{
		command: verify.PythonApplicationCommand{
			Root: root, RuntimeDir: runtimeDir, OutputDir: output,
			Image:       "sha256:" + strings.Repeat("a", 64),
			Interpreter: "/usr/local/bin/python", Launcher: "/runtime/runner.py",
			WorkingDir: "/src", Mode: "explored",
			Args:     []string{"-S", "-s", "-P", "-B", "/runtime/runner.py", "/runtime/request.json", "argument with spaces;$(literal)"},
			Env:      map[string]string{"HOME": "/tmp", "PYTHONHASHSEED": "0", "PYTHONDONTWRITEBYTECODE": "1", "PATH": "/usr/local/bin:/usr/bin:/bin"},
			Overlays: []verify.PythonApplicationOverlay{{Host: overlay, Guest: "/src/driver.py"}},
		},
		runLog: filepath.Join(base, "run-argv"),
		modes:  map[string]os.FileMode{},
	}
	for _, name := range []string{root, runtimeDir, output, filepath.Dir(overlay), overlay,
		filepath.Join(root, "application.py"), filepath.Join(runtimeDir, "runner.py"),
		filepath.Join(runtimeDir, "request.json"), filepath.Join(output, "authentication.key")} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		f.modes[name] = info.Mode().Perm()
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PA399_SECURITY_OPTIONS", securityOptions)
	t.Setenv("PA399_INFO_STATUS", status)
	t.Setenv("PA399_RUN_LOG", f.runLog)
	t.Setenv("PA399_OUTPUT", output)
	return f
}

func pa399SuccessfulCommand(t *testing.T, f pa399Fixture, user string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := verify.RunPythonApplicationCommand(ctx, f.command)
	if err != nil || p.ExitCode != 0 || string(p.Events) != "event bytes from application\n" || p.Stdout != "application stdout\n" || p.Stderr != "application stderr\n" {
		t.Fatalf("executor did not return application output: %+v, %v", p, err)
	}
	b, err := os.ReadFile(f.runLog)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	image := slices.Index(args, f.command.Image)
	if image < 0 {
		t.Fatalf("selected image missing: %q", args)
	}
	options := args[:image]
	values := func(flag string) []string {
		var out []string
		for i, arg := range options {
			if arg == flag && i+1 < len(options) {
				out = append(out, options[i+1])
			} else if strings.HasPrefix(arg, flag+"=") {
				out = append(out, strings.TrimPrefix(arg, flag+"="))
			}
		}
		return out
	}
	for flag, want := range map[string]string{
		"--user": user, "--network": "none", "--platform": "linux/amd64", "--pull": "never",
		"--memory": "512m", "--pids-limit": "128", "--cpus": "1", "--cap-drop": "ALL",
		"--security-opt": "no-new-privileges", "--hostname": "localhost",
		"--tmpfs": "/tmp:rw,nosuid,nodev,noexec,size=64m", "--entrypoint": "/usr/bin/env", "-w": "/src",
	} {
		if got := values(flag); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s = %q, want exactly %q", flag, got, want)
		}
	}
	for _, flag := range []string{"--rm", "--read-only"} {
		if !slices.Contains(options, flag) {
			t.Errorf("missing %s: %q", flag, options)
		}
	}
	for _, arg := range options {
		for _, forbidden := range []string{"--privileged", "--cap-add", "--userns", "--group-add"} {
			if arg == forbidden || strings.HasPrefix(arg, forbidden+"=") {
				t.Errorf("mount access must not relax isolation with %q", arg)
			}
		}
	}
	wantMounts := []string{f.command.Root + ":/src:ro", f.command.RuntimeDir + ":/runtime:ro",
		f.command.OutputDir + ":/output:rw", f.command.Overlays[0].Host + ":/src/driver.py:ro"}
	gotMounts := values("-v")
	slices.Sort(gotMounts)
	slices.Sort(wantMounts)
	if !slices.Equal(gotMounts, wantMounts) {
		t.Errorf("mounts = %q, want %q", gotMounts, wantMounts)
	}
	if image+1 >= len(args) || args[image+1] != "-i" {
		t.Fatal("application must start with an empty environment")
	}
	python := slices.Index(args[image+1:], f.command.Interpreter)
	if python < 0 {
		t.Fatal("absolute interpreter missing")
	}
	python += image + 1
	if !slices.Equal(args[python+1:], f.command.Args) {
		t.Errorf("Python argv changed: %q", args[python+1:])
	}
	for key, value := range f.command.Env {
		if !slices.Contains(args[image+2:python], key+"="+value) {
			t.Errorf("missing application environment %s=%s", key, value)
		}
	}
	pa399PrivateFilesUnchanged(t, f)
}

func pa399PrivateFilesUnchanged(t *testing.T, f pa399Fixture) {
	t.Helper()
	for name, mode := range f.modes {
		info, err := os.Stat(name)
		if err != nil {
			t.Errorf("private input or directory disappeared: %s: %v", name, err)
		} else if info.Mode().Perm() != mode {
			t.Errorf("permissions changed for %s: got %o, want %o", name, info.Mode().Perm(), mode)
		}
	}
}
