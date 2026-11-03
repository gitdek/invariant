package plumbing

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// This separately exercises the real adapter, with only the Docker executable
// replaced. The typed fake used by the other tests cannot establish that the
// default adapter actually applies its request's isolation to a container.
func TestRepositoryDependencyDockerConsumerEnforcesIsolation(t *testing.T) {
	bin, logs, src, cache := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	write468(t, src, "app/main.py", "print('fixture')\n")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nset -eu\nprintf '%s\\000' \"$@\" > " + quote(logs) + "/$$\ncase \"$1\" in\ncreate) printf '%s\\n' fixture-container;;\ncp) [ \"$#\" -eq 3 ] && [ \"$2\" = '-' ] && [ \"$3\" = 'invariant-deps-468-consumer:/' ] || exit 1; cat > /dev/null;;\nrun|start|logs) printf '%s\\n' fixture-check;;\nwait) printf '0\\n';;\nesac\n"
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AWS_SECRET_ACCESS_KEY", "host-secret-must-not-enter-container")
	t.Setenv("GITHUB_TOKEN", "host-token-must-not-enter-container")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := DependencyRequest{
		Token: "invariant-deps-468-consumer", Image: hash468("prepared image"),
		Platform: "linux/amd64", Directory: "app", SourceDir: src, CacheDir: cache,
		Argv:    []string{"python3", "main.py", "literal argument; $(not-a-shell)"},
		Env:     map[string]string{"HOME": "/tmp", "GOTOOLCHAIN": "local", "GOPROXY": "off"},
		Network: false, Pull: false, Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024,
	}
	r := &DockerDependencyRunner{}
	out, err := r.Run(ctx, q)
	cleanup, finish := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
	cleanupErr := r.Cleanup(cleanup, q.Token)
	finish()
	if err != nil || cleanupErr != nil {
		t.Fatalf("default offline adapter: run=%v cleanup=%v", err, cleanupErr)
	}
	if !strings.Contains(string(out), "fixture-check") {
		t.Fatalf("default adapter lost the container output: %q", out)
	}
	entries, err := os.ReadDir(logs)
	if err != nil {
		t.Fatal(err)
	}
	var launch []string
	removed := false
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(logs, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
		if len(args) > 0 && (args[0] == "run" || args[0] == "create") && slices.Contains(args, q.Image) {
			launch = args
		}
		if len(args) > 0 && (args[0] == "pull" || args[0] == "build") {
			t.Errorf("offline consumer acquired an image: %v", args)
		}
		if len(args) > 1 && (args[0] == "rm" || (len(args) > 2 && args[0] == "container" && args[1] == "rm")) {
			removed = true
		}
	}
	if launch == nil {
		t.Fatal("default adapter never launched the immutable prepared image")
	}
	var opts []string
	for _, arg := range launch[1:slices.Index(launch, q.Image)] {
		if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
			name, value, _ := strings.Cut(arg, "=")
			opts = append(opts, name, value)
		} else {
			opts = append(opts, arg)
		}
	}
	flag := func(name string) string {
		for i, a := range opts {
			if a == name && i+1 < len(opts) {
				return opts[i+1]
			}
			if strings.HasPrefix(a, name+"=") {
				return strings.TrimPrefix(a, name+"=")
			}
		}
		return ""
	}
	if !slices.Contains(opts, "--init") || flag("--network") != "none" || flag("--pull") != "never" || flag("--platform") != q.Platform || flag("--pids-limit") != "1024" {
		t.Errorf("Docker did not enforce offline sandbox policy: %v", launch)
	}
	if !removed && !slices.Contains(opts, "--rm") {
		t.Error("finished consumer container was not removed")
	}
	if m := strings.ToLower(flag("--memory")); m != "4g" && m != "4gb" && m != "4294967296" {
		t.Errorf("Docker memory limit = %q", m)
	}
	// The image's own ENTRYPOINT cannot substitute a command for this request.
	if !slices.ContainsFunc(opts, func(s string) bool { return s == "--entrypoint" || strings.HasPrefix(s, "--entrypoint=") }) {
		t.Error("Docker retained an arbitrary image entrypoint")
	}
	env := map[string]string{}
	for i, a := range opts {
		if a == "--privileged" || a == "--env-file" || strings.HasPrefix(a, "--env-file=") {
			t.Errorf("unsafe container option %q", a)
		}
		if a == "-e" || a == "--env" {
			if i+1 < len(opts) {
				v := opts[i+1]
				key, value, _ := strings.Cut(v, "=")
				env[key] = value
				if !strings.Contains(v, "=") || strings.Contains(v, "host-secret-") || strings.Contains(v, "host-token-") || strings.HasPrefix(v, "AWS_SECRET_ACCESS_KEY=") || strings.HasPrefix(v, "GITHUB_TOKEN=") {
					t.Errorf("container inherited host environment: %q", v)
				}
			}
		}
		if a == "-v" || a == "--volume" {
			if i+1 >= len(opts) {
				t.Fatal("volume without value")
			}
			mount := opts[i+1]
			host := strings.Split(mount, ":")[0]
			if host != src && host != cache {
				t.Errorf("unrelated host mount %q", mount)
			}
			if host == src && !strings.HasSuffix(mount, ":ro") {
				t.Errorf("candidate source mount is writable: %q", mount)
			}
		}
		if a == "--mount" && i+1 < len(opts) {
			mount := opts[i+1]
			for _, field := range strings.Split(mount, ",") {
				if strings.HasPrefix(field, "src=") || strings.HasPrefix(field, "source=") {
					_, host, _ := strings.Cut(field, "=")
					if host != src && host != cache {
						t.Errorf("unrelated host mount %q", mount)
					}
					if host == src && !strings.Contains(mount, "readonly") && !strings.Contains(mount, ",ro") {
						t.Errorf("candidate source mount is writable: %q", mount)
					}
				}
			}
		}
	}
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "FTP_PROXY", "ftp_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Errorf("Docker client proxy defaults are not explicitly suppressed for %s", key)
		}
	}
}
