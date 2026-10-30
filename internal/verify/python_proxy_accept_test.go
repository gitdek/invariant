package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

var pyDepsProxyKeys = []string{
	"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "FTP_PROXY", "ftp_proxy",
	"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
}

const pyDepsProxyCredential = "http://invariant-proxy-user:invariant-proxy-password@127.0.0.1:9"

// Docker adds proxies from config.json even when the gate passes no host
// environment variables. The default layer emulates that client behavior;
// the real layer independently checks the actual container/build environment
// before forwarding each unchanged production invocation. Neither layer
// requires a particular defense: explicit empty overrides and a separate
// sanitized client configuration both work.
func TestPythonDependenciesSuppressDockerClientProxies(t *testing.T) {
	t.Run("client configuration at every gate boundary", func(t *testing.T) {
		// The observer delegates to the ordinary CLI double here. The real
		// subtest below executes the parser and recipe without that double.
		t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
		pyDepsFakeDocker(t)
		config := pyDepsProxyConfig(t, false)
		before := pyDepsFiles(t, config)
		log := pyDepsProxyObserver(t, false)
		out, err := exec.Command("docker", "run", "--rm", "--network", "none", toolchain.PythonImage, "/usr/bin/env").CombinedOutput()
		if err == nil || !bytes.Contains(out, []byte("Docker client configuration leaked")) {
			t.Fatalf("Docker double did not inject configured proxy defaults: %v\n%s", err, out)
		}
		root := pyDepsProxyFixture(t)
		first := pyDepsBuild(t, root)
		tc := toolchain.Toolchain{PythonImage: toolchain.PythonImage, JavaImage: "accept-java", TLCJar: filepath.Join(t.TempDir(), "tlc.jar")}
		r, err := Run(context.Background(), root, "", tc)
		pyDepsRequireGateChecks(t, r, err)
		pyDepsProxyRequireCalls(t, log, first.Image, false)
		pyDepsProxyRequireReceipt(t, r, first)
		if !reflect.DeepEqual(before, pyDepsFiles(t, config)) {
			t.Fatal("gate rewrote the user's Docker client configuration")
		}
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("real Docker config injection and generated recipe", func(t *testing.T) {
			pyDepsRequireRealDocker(t)
			// Obtain the model checker before changing the Docker client
			// fixture. Context/daemon selection is retained in the fixture.
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			defer cancel()
			tc, err := toolchain.Ensure(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pyDepsEnsurePinnedPipBase(t)
			config := pyDepsProxyConfig(t, true)
			before := pyDepsFiles(t, config)
			// This negative control uses the actual Docker client directly.
			// It proves the selected config/context injects all ten values,
			// rather than accepting an inert fixture or an ignored config.
			probe := "import json,os;print(json.dumps({k:os.environ.get(k) for k in " + pyDepsProxyKeyJSON() + "}))"
			out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--platform", "linux/amd64", toolchain.PythonImage,
				"/usr/local/bin/python", "-I", "-c", probe).CombinedOutput()
			var inherited map[string]string
			if err != nil || json.Unmarshal(bytes.TrimSpace(out), &inherited) != nil {
				t.Fatalf("real Docker proxy negative control failed: %v\n%s", err, out)
			}
			for _, key := range pyDepsProxyKeys {
				if inherited[key] != pyDepsProxyCredential {
					t.Fatalf("Docker did not inject configured credential into %s: %#v", key, inherited)
				}
			}
			log := pyDepsProxyObserver(t, true)
			root := pyDepsProxyFixture(t)
			originals := pyDepsFiles(t, root)
			first := pyDepsBuild(t, root)
			pyDepsCleanupRecipeImage(t, first.Image)
			pyDepsAssertEnvironment(t, root, first, "24.2")
			second := pyDepsBuild(t, root)
			if !reflect.DeepEqual(first, second) {
				t.Fatal("client proxy suppression changed dependency reuse or artifact evidence")
			}
			r, err := Run(ctx, root, "", tc)
			pyDepsRequireGateChecks(t, r, err)
			pyDepsProxyRequireReceipt(t, r, first)
			pyDepsProxyRequireCalls(t, log, first.Image, true)
			if !reflect.DeepEqual(originals, pyDepsFiles(t, root)) || !reflect.DeepEqual(before, pyDepsFiles(t, config)) {
				t.Fatal("proxy suppression changed application inputs or the user's Docker client configuration")
			}
		})
	}
}

func pyDepsProxyFixture(t *testing.T) string {
	t.Helper()
	root := pyDepsFixture(t)
	pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# Docker proxy regression %d\n-r deps/application.txt\n", time.Now().UnixNano()))
	guard := "import os\nassert not {k: os.environ[k] for k in " + pyDepsProxyKeyJSON() + " if os.environ.get(k)}, 'Docker client proxy reached application'\n"
	files := pyDepsFiles(t, root)
	for _, name := range []string{"test_dependency.py", "conformance.py"} {
		pyDepsWrite(t, root, name, guard+files[name])
	}
	return root
}

func pyDepsProxyKeyJSON() string {
	b, _ := json.Marshal(pyDepsProxyKeys)
	return string(b)
}

// Write only a new temporary client configuration. Retaining Docker context
// metadata preserves named desktop/remote contexts and their TLS connection;
// registry auth, credential stores and the host config itself are not copied
// or changed. DOCKER_HOST and DOCKER_CONTEXT remain as the caller set them.
func pyDepsProxyConfig(t *testing.T, preserveContext bool) string {
	t.Helper()
	dir := t.TempDir()
	config := map[string]any{"proxies": map[string]any{"default": map[string]string{
		"httpProxy": pyDepsProxyCredential, "httpsProxy": pyDepsProxyCredential,
		"ftpProxy": pyDepsProxyCredential, "allProxy": pyDepsProxyCredential, "noProxy": pyDepsProxyCredential,
	}}}
	if preserveContext {
		old := os.Getenv("DOCKER_CONFIG")
		if old == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			old = filepath.Join(home, ".docker")
		}
		original, err := os.ReadFile(filepath.Join(old, "config.json"))
		originalExists := err == nil
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if len(original) > 0 {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(original, &fields); err != nil {
				t.Fatal(err)
			}
			if current, present := fields["currentContext"]; present {
				config["currentContext"] = current
			}
		}
		t.Cleanup(func() {
			after, err := os.ReadFile(filepath.Join(old, "config.json"))
			if err != nil && !os.IsNotExist(err) || (err == nil) != originalExists || !bytes.Equal(original, after) {
				t.Error("acceptance fixture or gate modified the original Docker client config")
			}
		})
		contexts := filepath.Join(old, "contexts")
		err = filepath.WalkDir(contexts, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == contexts {
				return nil
			}
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(old, path)
			if err != nil {
				return err
			}
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dir, rel), 0o700)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, rel), b, 0o600)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", dir)
	return dir
}

type pyDepsProxyCall struct {
	Kind     string
	Image    string
	Count    bool
	Observed bool
	Proxies  map[string]string
}

func pyDepsProxyObserver(t *testing.T, real bool) string {
	t.Helper()
	target, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "proxy-calls.jsonl")
	t.Setenv("INVARIANT_PY_DEPS_PROXY_BINARY", binary)
	t.Setenv("INVARIANT_PY_DEPS_PROXY_TARGET", target)
	t.Setenv("INVARIANT_PY_DEPS_PROXY_LOG", log)
	t.Setenv("INVARIANT_PY_DEPS_PROXY_REAL", fmt.Sprint(real))
	script := "#!/bin/sh\nINVARIANT_PY_DEPS_PROXY_HELPER=1 exec \"$INVARIANT_PY_DEPS_PROXY_BINARY\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func pyDepsProxyRequireReceipt(t *testing.T, r *Report, want *toolchain.PythonDependencies) {
	t.Helper()
	b, err := json.Marshal(r.Toolchain)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(want)
	if !bytes.Equal(fields["python_dependencies"], expected) || bytes.Contains(b, []byte("invariant-proxy-password")) {
		t.Fatalf("proxy fixture changed dependency provenance or entered receipt: %s", b)
	}
}

func pyDepsProxyRequireCalls(t *testing.T, log, image string, real bool) {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	builds := 0
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		var call pyDepsProxyCall
		if err := json.Unmarshal(line, &call); err != nil {
			t.Fatal(err)
		}
		if real && !call.Observed {
			t.Fatalf("real Docker phase was not observed: %#v", call)
		}
		for key, value := range call.Proxies {
			if value != "" {
				t.Fatalf("Docker config injected %s into %s: %#v", key, call.Kind, call)
			}
		}
		phase := call.Kind
		if phase == "application" && call.Count {
			phase = "larger"
		}
		if phase == "run" && call.Image == image {
			phase = "evidence"
		}
		seen[phase] = true
		if phase == "build" {
			builds++
		}
	}
	for _, phase := range []string{"parser", "build", "evidence", "application", "larger"} {
		if !seen[phase] {
			t.Errorf("Docker proxy regression missed %s", phase)
		}
	}
	if builds != 1 {
		t.Fatalf("dependency image was not built once then reused: builds=%d", builds)
	}
}

func init() {
	if os.Getenv("INVARIANT_PY_DEPS_PROXY_HELPER") == "1" {
		os.Exit(pyDepsProxyDocker(os.Args[1:]))
	}
}

// This wrapper is independent of pyDepsDocker: merely inspecting explicit
// -e arguments cannot detect client configuration defaults. In real mode the
// real Docker binary also executes probes with the same configuration and
// overrides. Production's parser, recipe and commands are then forwarded
// unchanged; no probe result is used as dependency or check evidence.
func pyDepsProxyDocker(args []string) int {
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 72 }
	global, command, err := pyDepsProxyClientArguments(args)
	if err != nil {
		return fail(err)
	}
	child := func(argv []string) *exec.Cmd {
		cmd := exec.Command(os.Getenv("INVARIANT_PY_DEPS_PROXY_TARGET"), argv...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "INVARIANT_PY_DEPS_PROXY_HELPER=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		return cmd
	}
	if len(command) > 0 && (command[0] == "run" || command[0] == "build") {
		proxies, err := pyDepsProxyDefaults(global)
		if err != nil {
			return fail(err)
		}
		call := pyDepsProxyCall{Kind: command[0], Proxies: proxies}
		var options []string
		var buildArgs []string
		if command[0] == "run" {
			var env map[string]string
			options, call.Image, env, err = pyDepsProxyRunArguments(command[1:])
			if err != nil {
				return fail(err)
			}
			for key, value := range env {
				if _, proxy := proxies[key]; proxy {
					proxies[key] = value
				}
			}
			if env["DRIVER"] != "" {
				call.Kind, call.Count = "application", env["INVARIANT_COUNT"] != ""
			} else if call.Image == toolchain.PythonImage {
				call.Kind = "parser"
			}
		} else {
			for i := 1; i < len(command); i++ {
				key, value, joined := strings.Cut(command[i], "=")
				if key != "--build-arg" {
					continue
				}
				if !joined {
					i++
					if i >= len(command) {
						return fail(fmt.Errorf("missing build arg"))
					}
					value = command[i]
				}
				buildArgs = append(buildArgs, "--build-arg", value)
				name, content := pyDepsProxyAssignment(value)
				if _, proxy := proxies[name]; proxy {
					proxies[name] = content
				}
			}
		}
		if os.Getenv("INVARIANT_PY_DEPS_PROXY_REAL") == "true" {
			check := "import os; assert not {k:os.environ[k] for k in " + pyDepsProxyKeyJSON() + " if os.environ.get(k)}, 'Docker client proxy leaked'; print('invariant-proxy-probe-ok')"
			if command[0] == "run" {
				probe := append(append([]string{}, global...), "run")
				probe = append(probe, options...)
				// Proxy injection is client-side and independent of the image.
				// TLC's Java image has no Python, so use the pinned probe base
				// with the production options, then run the original image and
				// command unchanged. Application fixtures separately assert
				// that their actual dependency image contains no proxy values.
				probe = append(probe, "--rm", "--platform", "linux/amd64", "--entrypoint", "/usr/local/bin/python", toolchain.PythonImage, "-I", "-c", check)
				out, err := child(probe).CombinedOutput()
				if err != nil || !bytes.Contains(out, []byte("invariant-proxy-probe-ok")) {
					return fail(fmt.Errorf("observing %s proxy environment: %v\n%s", call.Kind, err, out))
				}
			} else {
				// Execute a separate uncached RUN using the same client config
				// and exact build-arg overrides. The actual recipe is untouched
				// and runs afterward against its original build context.
				probe := append(append([]string{}, global...), "build", "--no-cache", "--network", "none", "--platform", "linux/amd64", "-q")
				probe = append(probe, buildArgs...)
				probe = append(probe, "-")
				run, _ := json.Marshal([]string{"/usr/local/bin/python", "-I", "-c", check})
				cmd := child(probe)
				cmd.Stdin = strings.NewReader("FROM " + toolchain.PythonImage + "\nRUN " + string(run) + "\n")
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil || !pyDepsDigest.Match(bytes.TrimSpace(out)) {
					return fail(fmt.Errorf("observing Docker build proxy environment: %v\n%s\n%s", err, out, stderr.Bytes()))
				}
				cleanup := append(append([]string{}, global...), "image", "rm", string(bytes.TrimSpace(out)))
				if out, err := child(cleanup).CombinedOutput(); err != nil {
					return fail(fmt.Errorf("cleaning proxy build probe: %v\n%s", err, out))
				}
			}
			call.Observed = true
			// The actual Docker environment is authoritative in this layer,
			// including endpoint-specific settings in a sanitized config.
			// Both real probes succeeded only if every value was empty.
			for key := range proxies {
				proxies[key] = ""
			}
		}
		for key, value := range proxies {
			if value != "" {
				return fail(fmt.Errorf("Docker client configuration leaked %s into %s", key, call.Kind))
			}
		}
		f, err := os.OpenFile(os.Getenv("INVARIANT_PY_DEPS_PROXY_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fail(err)
		}
		err = json.NewEncoder(f).Encode(call)
		f.Close()
		if err != nil {
			return fail(err)
		}
	}
	forward := args
	if os.Getenv("INVARIANT_PY_DEPS_PROXY_REAL") != "true" {
		// The older generic double has no global client-flag parser. They
		// have already been applied by this independent config observer.
		forward = command
	}
	cmd := child(forward)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return fail(err)
	}
	return 0
}

func pyDepsProxyClientArguments(args []string) ([]string, []string, error) {
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[:i], args[i:], nil
		}
		key, _, joined := strings.Cut(args[i], "=")
		if !joined {
			switch key {
			case "--config", "--context", "-c", "--host", "-H", "--log-level", "-l", "--tlscacert", "--tlscert", "--tlskey":
				i++
			}
		}
	}
	return nil, nil, fmt.Errorf("Docker proxy observer found no command")
}

func pyDepsProxyDefaults(global []string) (map[string]string, error) {
	dir := os.Getenv("DOCKER_CONFIG")
	for i := 0; i < len(global); i++ {
		if global[i] == "--config" && i+1 < len(global) {
			i++
			dir = global[i]
		} else if strings.HasPrefix(global[i], "--config=") {
			dir = strings.TrimPrefix(global[i], "--config=")
		}
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".docker")
	}
	var config struct {
		Proxies map[string]map[string]string `json:"proxies"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &config); err != nil {
			return nil, err
		}
	}
	proxies := map[string]string{}
	for _, key := range pyDepsProxyKeys {
		name := strings.TrimSuffix(strings.ToLower(key), "_proxy") + "Proxy"
		proxies[key] = config.Proxies["default"][name]
	}
	return proxies, nil
}

func pyDepsProxyAssignment(value string) (string, string) {
	key, content, assigned := strings.Cut(value, "=")
	if !assigned {
		content = os.Getenv(key)
	}
	return key, content
}

func pyDepsProxyRunArguments(args []string) ([]string, string, map[string]string, error) {
	env := map[string]string{}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[:i], args[i], env, nil
		}
		key, value, joined := strings.Cut(args[i], "=")
		if !joined {
			switch key {
			case "--name", "--network", "--platform", "--memory", "--pids-limit", "-w", "--workdir", "--user", "--entrypoint", "--cap-drop", "--security-opt", "--tmpfs", "--pull", "--mount", "-e", "--env", "--env-file", "-v", "--volume":
				i++
				if i >= len(args) {
					return nil, "", nil, fmt.Errorf("Docker option %s has no value", key)
				}
				value = args[i]
			}
		}
		if key == "-e" || key == "--env" {
			name, content := pyDepsProxyAssignment(value)
			env[name] = content
		}
		if key == "--env-file" {
			b, err := os.ReadFile(value)
			if err != nil {
				return nil, "", nil, err
			}
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					name, content := pyDepsProxyAssignment(line)
					env[name] = content
				}
			}
		}
	}
	return nil, "", nil, fmt.Errorf("Docker proxy observer found no run image")
}
