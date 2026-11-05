package plumbing

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Only Docker is replaced here. The real adapter constructs every launch,
// copies the candidate, executes commands, and owns removal. The executable
// does not provide Python, Go, Node, or a host cache to the candidate image.
func docker484(t *testing.T) (string, string) {
	t.Helper()
	bin, logs := t.TempDir(), t.TempDir()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := `#!/bin/sh
set -eu
printf '%s\000' "$@" > LOGS/$$
case " $* " in
  *" invariant-deps-484-startup-failure "*) [ "$1" != start ] || { echo 'startup diagnostic'; exit 29; } ;;
esac
case "$1" in
  create) echo fixture-container ;;
  cp) cat > /dev/null ;;
  exec)
    echo 'container diagnostic'
    for arg in "$@"; do
      case "$arg" in
        fail-command) echo 'failure diagnostic'; exit 23 ;;
        wait-for-cancellation) while :; do sleep 0.02; done ;;
      esac
    done
    ;;
  wait) echo 0 ;;
esac
`
	script = strings.ReplaceAll(script, "LOGS", quote(logs))
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fixture-host-secret")
	t.Setenv("GITHUB_TOKEN", "fixture-host-token")
	return bin, logs
}

func invocations484(t *testing.T, logs string) [][]string {
	t.Helper()
	entries, err := os.ReadDir(logs)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(logs, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"))
	}
	return calls
}

func flag484(args []string, names ...string) string {
	for i, arg := range args {
		for _, name := range names {
			if arg == name && i+1 < len(args) {
				return args[i+1]
			}
			if strings.HasPrefix(arg, name+"=") {
				return strings.TrimPrefix(arg, name+"=")
			}
		}
	}
	return ""
}

func request484(t *testing.T, language string) DependencyRequest {
	t.Helper()
	src := t.TempDir()
	write468(t, src, "app/source.txt", "candidate source\n")
	return DependencyRequest{Token: "invariant-deps-484-" + language, Image: hash468("prepared " + language),
		Platform: "linux/amd64", Directory: "app", SourceDir: src,
		Env: dependencyEnv(language, false), Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024}
}

func close484(t *testing.T, environment RepositoryCheckEnvironment) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
	defer cancel()
	if err := environment.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func removed484(calls [][]string, token string) bool {
	for _, args := range calls {
		if len(args) > 0 && (args[0] == "rm" || args[0] == "container" && len(args) > 1 && args[1] == "rm") && slices.Contains(args, token) && (slices.Contains(args, "--force") || slices.Contains(args, "-f")) {
			return true
		}
	}
	return false
}

func TestRepositoryCheckContainerRunsWithoutPythonOrHostCache(t *testing.T) {
	_, logs := docker484(t)
	for _, language := range []string{"go", "typescript"} {
		q := request484(t, language)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		environment, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		literal := "one argument; $(not-a-shell) `also-literal`"
		for _, phase := range phases484 {
			out, err := environment.Run(ctx, []string{"fixture-check", phase, literal})
			if err != nil || !strings.Contains(string(out), "container diagnostic") {
				t.Fatalf("%s: %v %q", phase, err, out)
			}
		}
		close484(t, environment)
		cancel()
		calls := invocations484(t, logs)
		var launch []string
		executed := map[string]bool{}
		for _, args := range calls {
			if len(args) == 0 {
				continue
			}
			if args[0] == "pull" || args[0] == "build" || args[0] == "commit" {
				t.Errorf("check acquired or published an image: %v", args)
			}
			if (args[0] == "create" || args[0] == "run") && slices.Contains(args, q.Image) {
				if launch != nil {
					t.Fatal("created more than one environment for a part's phases")
				}
				launch = args
			}
			if args[0] == "exec" && slices.Contains(args, q.Token) {
				i := slices.Index(args, "fixture-check")
				if i < 0 || i+3 != len(args) || args[i+2] != literal {
					t.Fatalf("lost argument boundaries: %v", args)
				}
				if executed[args[i+1]] {
					t.Fatal("executed a phase twice")
				}
				executed[args[i+1]] = true
				if !slices.Contains(args[:i], "-i") || !slices.Contains(args[:i], "/usr/bin/env") {
					t.Fatalf("docker exec inherited the image environment: %v", args)
				}
				// A Docker exec can use the captured container workdir or restate it.
				if cwd := flag484(args[:i], "--workdir", "-w"); cwd != "" && cwd != path.Join("/invariant/work", q.Directory) {
					t.Fatalf("wrong exec directory: %s", cwd)
				}
			}
		}
		if launch == nil || len(executed) != 4 {
			t.Fatalf("missing launch/commands: launch=%v executed=%v", launch, executed)
		}
		imageAt := slices.Index(launch, q.Image)
		opts := launch[1:imageAt]
		for _, f := range []struct{ key, want string }{{"--network", "none"}, {"--pull", "never"}, {"--platform", q.Platform}, {"--pids-limit", "1024"}, {"--hostname", "localhost"}} {
			if got := flag484(opts, f.key); got != f.want {
				t.Errorf("%s=%s; want %s", f.key, got, f.want)
			}
		}
		if !slices.Contains(opts, "--init") || flag484(opts, "--entrypoint") == "" || flag484(opts, "--workdir", "-w") != "/invariant/work/app" {
			t.Fatalf("lost init/entrypoint/cwd: %v", launch)
		}
		if memory := strings.ToLower(flag484(opts, "--memory", "-m")); memory != "4g" && memory != "4gb" && memory != "4294967296" {
			t.Errorf("memory = %q", memory)
		}
		for _, arg := range launch {
			if arg == "--privileged" || arg == "--env-file" || arg == "-v" || arg == "--volume" || arg == "--mount" || strings.Contains(arg, "fixture-host-secret") || strings.Contains(arg, "fixture-host-token") {
				t.Fatalf("host authority reached candidate: %v", launch)
			}
			if strings.Contains(arg, "python3") || strings.Contains(arg, "runner.py") {
				t.Fatalf("arbitrary %s image must not supply a Python runner: %v", language, launch)
			}
		}
		for key, value := range q.Env {
			found := false
			for i, arg := range opts {
				if (arg == "--env" || arg == "-e") && i+1 < len(opts) && opts[i+1] == key+"="+value {
					found = true
				}
				if arg == "--env="+key+"="+value {
					found = true
				}
			}
			if !found {
				t.Errorf("prepared environment variable %s missing from %v", key, launch)
			}
		}
		for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "FTP_PROXY", "ftp_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
			if !slices.Contains(opts, key+"=") && !slices.Contains(opts, "--env="+key+"=") {
				t.Errorf("Docker proxy default %s not suppressed", key)
			}
		}
		if !removed484(calls, q.Token) {
			t.Fatal("completed private container not removed")
		}
		if read484(t, q.SourceDir, "app/source.txt") != "candidate source\n" {
			t.Fatal("adapter altered the original candidate")
		}
	}
}

func TestRepositoryCheckContainerFailureAndCancellationRemoveContainer(t *testing.T) {
	for _, mode := range []string{"failure", "cancellation", "startup"} {
		t.Run(mode, func(t *testing.T) {
			_, logs := docker484(t)
			q := request484(t, "go")
			if mode == "startup" {
				q.Token = "invariant-deps-484-startup-failure"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			environment, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
			if mode == "startup" {
				if err == nil || !removed484(invocations484(t, logs), q.Token) {
					t.Fatalf("failed startup succeeded or left a container: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			argv := []string{"fixture-check", "fail-command"}
			commandCtx := ctx
			if mode == "cancellation" {
				var stop context.CancelFunc
				commandCtx, stop = context.WithTimeout(ctx, 150*time.Millisecond)
				defer stop()
				argv[1] = "wait-for-cancellation"
			}
			out, err := environment.Run(commandCtx, argv)
			close484(t, environment)
			if err == nil || !strings.Contains(string(out), "container diagnostic") {
				t.Fatalf("failure/cancellation lost status or partial diagnostics: %v %q", err, out)
			}
			if !removed484(invocations484(t, logs), q.Token) {
				t.Fatal("failed/canceled private container not force-removed")
			}
		})
	}
}
