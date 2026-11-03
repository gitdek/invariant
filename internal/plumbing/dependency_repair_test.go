package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Capture the actual Docker command, stopping before container creation.
// The installer tests execute its script with real local language tools and
// empty graphs, remapping only the container's private filesystem paths.
func captureDependencyInstaller(t *testing.T, q DependencyRequest) []string {
	t.Helper()
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "launch")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	info := dependencyImage{ID: baseID468, OS: "linux", Architecture: "amd64", RepoDigests: []string{image468}}
	data, err := json.Marshal([]dependencyImage{info})
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$1\" in\nimage) printf '%s\\n' " + quote(string(data)) + ";;\ncreate) printf '%s\\000' \"$@\" > " + quote(log) + "; exit 73;;\n*) exit 74;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := (&DockerDependencyRunner{}).Install(ctx, q); err == nil {
		t.Fatal("capture unexpectedly created a container")
	}
	data, err = os.ReadFile(log)
	if err != nil {
		t.Fatalf("installer never reached Docker create: %v", err)
	}
	launch := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	i := slices.Index(launch, baseID468)
	if i < 0 {
		t.Fatalf("installer did not resolve the base: %v", launch)
	}
	return launch[i+1:]
}

func dependencyInstallerFixture(t *testing.T, language string, files map[string]string) DependencyRequest {
	t.Helper()
	source := t.TempDir()
	p := part468(language, ".")
	contents := map[string][]byte{}
	for name, data := range files {
		write468(t, source, name, data)
		contents[name] = []byte(data)
		if !slices.Contains(p.Inputs, name) && !slices.Contains(p.Lockfiles, name) {
			p.Inputs = append(p.Inputs, name)
		}
	}
	argv, err := dependencyInstall(p, contents)
	if err != nil {
		t.Fatal(err)
	}
	return DependencyRequest{
		Token:       "invariant-deps-installer-repair",
		Image:       image468,
		Platform:    "linux/amd64",
		Directory:   ".",
		SourceDir:   source,
		Key:         hash468("installer repair"),
		Argv:        argv,
		Env:         dependencyEnv(language, true),
		Inputs:      hashes468(files),
		Network:     true,
		Pull:        true,
		Init:        true,
		MemoryBytes: 4 << 30,
		PidsLimit:   1024,
	}
}

func runCapturedDependencyInstaller(t *testing.T, launch []string, q DependencyRequest, tool string) {
	t.Helper()
	stage, err := stageDependencySource(context.Background(), q, true)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	replace := strings.NewReplacer("/invariant", filepath.Join(stage, "invariant"), "/tmp", filepath.Join(stage, "tmp"))
	args := slices.Clone(launch)
	for i := range args {
		args[i] = replace.Replace(args[i])
	}
	start := slices.Index(args, "invariant-dependencies")
	if start < 0 || start+1 >= len(args) {
		t.Fatalf("missing installer arguments: %v", args)
	}
	args[start+1] = tool
	if q.Argv[0] == "python3" {
		args = append(args, "--no-index")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/env", args...)
	cmd.Dir = filepath.Join(stage, "invariant", "work")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real empty-graph installer: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "shadowed")); !os.IsNotExist(err) {
		t.Fatalf("repository module ran in place of the installer: %v", err)
	}
}

func TestDependencyNpmDistinctEmptyConfiguration(t *testing.T) {
	q := dependencyInstallerFixture(t, "typescript", map[string]string{
		"package.json":      `{"name":"empty","version":"1.0.0"}`,
		"package-lock.json": `{"name":"empty","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"empty","version":"1.0.0"}}}`,
	})
	launch := captureDependencyInstaller(t, q)
	stage, err := stageDependencySource(context.Background(), q, true)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	user, global := q.Env["NPM_CONFIG_USERCONFIG"], q.Env["NPM_CONFIG_GLOBALCONFIG"]
	if user == global || !strings.HasPrefix(user, "/invariant/") || !strings.HasPrefix(global, "/invariant/") {
		t.Fatalf("npm needs distinct private configuration files: %q, %q", user, global)
	}
	for _, name := range []string{user, global} {
		data, err := os.ReadFile(filepath.Join(stage, strings.TrimPrefix(name, "/")))
		if err != nil || len(data) != 0 {
			t.Fatalf("npm configuration %s is not empty: %q, %v", name, data, err)
		}
		if !slices.Contains(launch, "NPM_CONFIG_USERCONFIG="+name) && !slices.Contains(launch, "NPM_CONFIG_GLOBALCONFIG="+name) {
			t.Fatalf("Docker did not receive private npm configuration %s", name)
		}
	}
	t.Run("real npm empty graph", func(t *testing.T) {
		npm, err := exec.LookPath("npm")
		if err != nil {
			t.Skip("npm is unavailable; real pinned npm remains merger verification")
		}
		runCapturedDependencyInstaller(t, launch, q, npm)
	})
}

func TestDependencyPythonInstallerIgnoresStagedModules(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	q := dependencyInstallerFixture(t, "python", map[string]string{
		"requirements.lock": "",
		"pip.py":            "from pathlib import Path\nPath('shadowed').write_text('pip')\n",
		"venv.py":           "from pathlib import Path\nPath('shadowed').write_text('venv')\n",
	})
	launch := captureDependencyInstaller(t, q)
	runCapturedDependencyInstaller(t, launch, q, python)
}

func TestDependencyNpmRejectsCaseAliases(t *testing.T) {
	for _, tc := range []struct {
		name, file, from, to string
	}{
		{"local resolved hidden", "package-lock.json", `"resolved":"https://`, `"resolved":"file:local.tgz","Resolved":"https://`},
		{"integrity missing", "package-lock.json", `"integrity":`, `"Integrity":`},
		{"integrity empty", "package-lock.json", `"integrity":`, `"integrity":"","Integrity":`},
		{"version floating", "package-lock.json", `"version":"7.0.0"`, `"version":"latest","Version":"7.0.0"`},
		{"lock version missing", "package-lock.json", `"lockfileVersion":`, `"LockfileVersion":`},
		{"packages alias", "package-lock.json", `"packages":`, `"Packages":`},
		{"manifest local hidden", "package.json", `"dependencies":`, `"dependencies":{"local":"file:local.tgz"},"Dependencies":`},
		{"workspace hidden", "package.json", `"dependencies":`, `"workspaces":["local"],"Workspaces":null,"dependencies":`},
		{"peer optional hidden", "package-lock.json", `"version":"7.0.0"`, `"peerDependencies":{"missing":"*"},"peerDependenciesMeta":{"missing":{"optional":false,"Optional":true}},"version":"7.0.0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := part468("typescript", ".")
			files := map[string][]byte{}
			for name, text := range files468("typescript", ".") {
				files[name] = []byte(text)
			}
			if _, err := dependencyInstall(p, files); err != nil {
				t.Fatalf("valid control: %v", err)
			}
			files[tc.file] = []byte(strings.ReplaceAll(string(files[tc.file]), tc.from, tc.to))
			if _, err := dependencyInstall(p, files); err == nil {
				t.Fatal("npm case alias bypassed admission")
			}
		})
	}
	// Arbitrary metadata and map keys retain JSON's case-sensitive semantics.
	var metadata struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := dependencyJSON([]byte(`{"scripts":{"check":"one","Check":"two"},"custom":{"Foo":1,"foo":2}}`), &metadata); err != nil || len(metadata.Scripts) != 2 {
		t.Fatalf("case-sensitive map keys were conflated: %+v, %v", metadata, err)
	}
}

type dependencyReadFunc func([]byte) (int, error)

func (f dependencyReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestDependencyCopyStopsBetweenReadsAndBoundsGrowth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	reader := dependencyReadFunc(func(p []byte) (int, error) {
		reads++
		if reads == 2 {
			cancel()
		}
		return len(p), nil
	})
	var dst bytes.Buffer
	if err := copyDependencyBytes(ctx, &dst, reader, 1<<30); !errors.Is(err, context.Canceled) || reads != 2 || dst.Len() > 64<<10 {
		t.Fatalf("copy failed to stop between reads: bytes=%d reads=%d err=%v", dst.Len(), reads, err)
	}
	reads = 0
	if err := copyDependencyBytes(context.Background(), io.Discard, reader, 123456); err != nil || reads != 2 {
		t.Fatalf("copy followed growth beyond the captured length: reads=%d err=%v", reads, err)
	}
}

type dependencyCheckingContext struct {
	context.Context
	check func()
}

func (c dependencyCheckingContext) Err() error {
	c.check()
	return c.Context.Err()
}

func TestDependencyStagingCancellationDuringFileAndDirectoryReads(t *testing.T) {
	for _, mode := range []string{"file", "directories"} {
		t.Run(mode, func(t *testing.T) {
			source, scratch := t.TempDir(), t.TempDir()
			t.Setenv("TMPDIR", scratch)
			if mode == "file" {
				f, err := os.Create(filepath.Join(source, "large"))
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate(64 << 20)
				if closeErr := f.Close(); err != nil || closeErr != nil {
					t.Fatal(errors.Join(err, closeErr))
				}
			} else {
				for i := 0; i < 300; i++ {
					if err := os.Mkdir(filepath.Join(source, fmt.Sprint(i)), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks, copied := 0, int64(0)
			ctx := dependencyCheckingContext{Context: parent, check: func() {
				checks++
				if mode == "directories" {
					if checks == 8 {
						cancel()
					}
					return
				}
				stages, err := os.ReadDir(scratch)
				if err != nil {
					t.Fatal(err)
				}
				for _, stage := range stages {
					info, err := os.Stat(filepath.Join(scratch, stage.Name(), "invariant", "work", "large"))
					if err == nil && info.Size() > 0 {
						copied = info.Size()
						cancel()
					}
				}
			}}
			stage, err := stageDependencySource(ctx, DependencyRequest{SourceDir: source, Directory: "."}, false)
			if stage != "" {
				defer os.RemoveAll(stage)
			}
			if !errors.Is(err, context.Canceled) || copied > 64<<10 {
				t.Fatalf("staging did not stop during %s: copied=%d checks=%d err=%v", mode, copied, checks, err)
			}
			left, err := os.ReadDir(scratch)
			if err != nil || len(left) != 0 {
				t.Fatalf("cancelled staging left private files: %v, %v", left, err)
			}
		})
	}
}

func TestDependencyStagingStillRefusesLinksAndPipes(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "pipe"} {
		t.Run(mode, func(t *testing.T) {
			source := t.TempDir()
			outside := filepath.Join(t.TempDir(), "private")
			if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(source, "unsafe")
			var err error
			switch mode {
			case "symlink":
				err = os.Symlink(outside, name)
			case "hardlink":
				err = os.Link(outside, name)
			case "pipe":
				err = syscall.Mkfifo(name, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stage, err := stageDependencySource(ctx, DependencyRequest{SourceDir: source, Directory: "."}, false)
			if stage != "" {
				defer os.RemoveAll(stage)
			}
			if err == nil || ctx.Err() != nil {
				t.Fatalf("unsafe file was read or blocked staging: %v", err)
			}
		})
	}
}
