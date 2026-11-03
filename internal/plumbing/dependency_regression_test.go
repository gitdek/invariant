package plumbing

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDependencyNpmTransitiveLocks(t *testing.T) {
	for _, change := range []string{"missing transitive", "wrong transitive", "recursive local", "duplicate field", "shrinkwrap"} {
		t.Run(change, func(t *testing.T) {
			p := part468("typescript", ".")
			files := map[string][]byte{}
			for name, text := range files468("typescript", ".") {
				files[name] = []byte(text)
			}
			var lock map[string]any
			if err := json.Unmarshal(files["package-lock.json"], &lock); err != nil {
				t.Fatal(err)
			}
			packages := lock["packages"].(map[string]any)
			node := packages["node_modules/is-number"].(map[string]any)
			node["dependencies"] = map[string]string{"nested": "^2.0.0"}
			nested := map[string]any{"version": "2.1.0", "resolved": "https://registry.npmjs.org/nested/-/nested-2.1.0.tgz", "integrity": "sha512-" + strings.Repeat("A", 86) + "=="}
			packages["node_modules/nested"] = nested
			valid, _ := json.Marshal(lock)
			files["package-lock.json"] = valid
			if _, err := dependencyInstall(p, files); err != nil {
				t.Fatalf("valid transitive lock refused: %v", err)
			}
			switch change {
			case "missing transitive":
				delete(packages, "node_modules/nested")
			case "wrong transitive":
				nested["version"] = "3.0.0"
			case "recursive local":
				nested["dependencies"] = map[string]string{"is-number": "file:../../elsewhere"}
			case "shrinkwrap":
				files["npm-shrinkwrap.json"] = []byte("{}")
			}
			files["package-lock.json"], _ = json.Marshal(lock)
			if change == "duplicate field" {
				files["package.json"] = []byte(`{"name":"fixture","version":"1.0.0","dependencies":{},"dependencies":{"is-number":"7.0.0"}}`)
			}
			if _, err := dependencyInstall(p, files); err == nil {
				t.Fatal("inconsistent or unsupported npm input admitted")
			}
		})
	}
}

func TestDependencyNpmRegistryRanges(t *testing.T) {
	for _, tc := range []struct {
		version, spec string
		match         bool
	}{
		{"7.1.0", "^7.0.0", true}, {"8.0.0", "^7.0.0", false},
		{"0.2.5", "^0.2.0", true}, {"0.3.0", "^0.2.0", false},
		{"1.2.9", "~1.2.0", true}, {"1.3.0", "~1.2.0", false},
		{"1.2.3", "1.x", true}, {"2.0.0", "1.x", false},
		{"2.1.0", ">=1.0.0 <3.0.0", true}, {"3.0.0", ">=1.0.0 <3.0.0", false},
		{"3.0.1", "^1.0.0 || ^3.0.0", true}, {"2.0.0", "1.0.0 - 2.0.0", true},
		{"2.0.0-beta.1", "*", false}, {"2.0.0-beta.2", "^2.0.0-beta.1", true},
	} {
		got, err := npmRange(tc.version, tc.spec)
		if err != nil || got != tc.match {
			t.Errorf("%s in %s = %v, %v; want %v", tc.version, tc.spec, got, err, tc.match)
		}
	}
	for _, spec := range []string{"latest", "file:local.tgz", "git+https://example.invalid/a", "npm:other@1.0.0", "workspace:*"} {
		if _, err := npmRange("1.0.0", spec); err == nil {
			t.Errorf("accepted unsupported spec %s", spec)
		}
	}
}

// The helper substitutes only the Docker executable. Installation, copying,
// inspection and publication still use the production adapter and preparer.
func TestDependencyDockerInstallAndInspection(t *testing.T) {
	for _, mode := range []string{"success", "failed process", "rewritten lock", "wrong base", "wrong layers", "volume"} {
		t.Run(mode, func(t *testing.T) {
			bin, state, source := t.TempDir(), t.TempDir(), t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			stub := "#!/bin/sh\nexec " + quote(executable) + " -test.run=^TestDependencyDockerProcess$ -- \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("INVARIANT_DEPENDENCY_DOCKER_TEST", state)
			t.Setenv("INVARIANT_DEPENDENCY_DOCKER_MODE", mode)
			write468(t, source, "go.mod", "module example.invalid/app\n\ngo 1.27.1\n")
			q := DependencyRequest{
				Token:       "invariant-deps-regression",
				Image:       image468,
				Platform:    "linux/amd64",
				Directory:   ".",
				SourceDir:   source,
				Key:         hash468("recipe"),
				Argv:        []string{"go", "mod", "download"},
				Env:         dependencyEnv("go", true),
				Inputs:      map[string]string{"go.mod": hash468("module example.invalid/app\n\ngo 1.27.1\n")},
				Network:     true,
				Pull:        true,
				Init:        true,
				MemoryBytes: 4 << 30,
				PidsLimit:   1024,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			runner := &DockerDependencyRunner{}
			a, err := runner.Install(ctx, q)
			if mode == "failed process" || mode == "rewritten lock" || mode == "volume" {
				if err == nil {
					t.Fatal("invalid installation succeeded")
				}
				if _, err := os.Stat(filepath.Join(state, "committed")); !os.IsNotExist(err) {
					t.Fatal("invalid installation committed an image")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				inspected, err := runner.Inspect(ctx, a.Image, q.Platform)
				if mode != "success" {
					if err == nil {
						t.Fatal("inspection accepted incorrect base ancestry")
					}
				} else {
					if err != nil || inspected.Image != a.Image || inspected.BaseImage != baseID468 || inspected.Key != q.Key || inspected.Inputs["go.mod"] != q.Inputs["go.mod"] {
						t.Fatalf("inspection: %+v, %v", inspected, err)
					}
					launchBytes, err := os.ReadFile(filepath.Join(state, "launch"))
					if err != nil {
						t.Fatal(err)
					}
					var launch []string
					if err := json.Unmarshal(launchBytes, &launch); err != nil {
						t.Fatal(err)
					}
					if !slices.Contains(launch, baseID468) || !slices.Contains(launch, "bridge") || !slices.Contains(launch, "/usr/bin/env") || slices.Contains(launch, "-v") || slices.Contains(launch, "--mount") {
						t.Fatalf("installation launch lost isolation/pinned identity: %v", launch)
					}
				}
			}
			cleanup, finish := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
			defer finish()
			if err := runner.Cleanup(cleanup, q.Token); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(state, "removed")); err != nil {
				t.Fatal("container was not removed")
			}
		})
	}
}

func TestDependencyDockerProcess(t *testing.T) {
	state := os.Getenv("INVARIANT_DEPENDENCY_DOCKER_TEST")
	if state == "" {
		return
	}
	mode := os.Getenv("INVARIANT_DEPENDENCY_DOCKER_MODE")
	args := os.Args[slices.Index(os.Args, "--")+1:]
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(state, name), data, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	switch args[0] {
	case "image":
		info := dependencyImage{ID: baseID468, OS: "linux", Architecture: "amd64", RepoDigests: []string{image468}}
		info.RootFS.Layers = []string{hash468("base layer")}
		if mode == "volume" {
			info.Config.Volumes = map[string]json.RawMessage{"/invariant": json.RawMessage("{}")}
		}
		if args[2] != image468 {
			label, err := os.ReadFile(filepath.Join(state, "label"))
			if err != nil {
				os.Exit(1)
			}
			info.ID = hash468("Docker environment")
			info.Config.Labels = map[string]string{dependencyLabel: string(label)}
			info.RootFS.Layers = append(info.RootFS.Layers, hash468("installation layer"))
			if mode == "wrong layers" {
				info.RootFS.Layers[0] = hash468("unrelated layer")
			}
		} else if mode == "wrong base" {
			if _, err := os.Stat(filepath.Join(state, "committed")); err == nil {
				info.ID = hash468("substituted base")
			}
		}
		data, _ := json.Marshal([]dependencyImage{info})
		fmt.Println(string(data))
	case "pull":
	case "create":
		data, _ := json.Marshal(args)
		write("launch", data)
		fmt.Println("fixture-container")
	case "cp":
		if strings.HasPrefix(args[1], "invariant-deps-") {
			data, err := os.ReadFile(filepath.Join(state, "input"))
			if err != nil {
				os.Exit(1)
			}
			if mode == "rewritten lock" {
				data = append(data, []byte("// installer changed this\n")...)
			}
			if err := os.WriteFile(args[2], data, 0o600); err != nil {
				os.Exit(1)
			}
		} else {
			if len(args) != 3 || args[1] != "-" || args[2] != "invariant-deps-regression:/" {
				fmt.Fprintln(os.Stderr, "copy needs the complete layout streamed to the container root")
				os.Exit(1)
			}
			archive := tar.NewReader(os.Stdin)
			found := false
			for {
				header, err := archive.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if header.Uid != 0 || header.Gid != 0 || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir) {
					t.Fatalf("invalid staged entry: %+v", header)
				}
				if header.Name == "invariant/work/go.mod" {
					data, err := io.ReadAll(archive)
					if err != nil {
						t.Fatal(err)
					}
					write("input", data)
					found = true
				}
			}
			if !found {
				t.Fatal("stream did not include go.mod")
			}
		}
	case "start":
		fmt.Println("installed dependencies")
	case "wait":
		if mode == "failed process" {
			fmt.Println("1")
		} else {
			fmt.Println("0")
		}
	case "commit":
		label := strings.TrimPrefix(args[2], "LABEL "+dependencyLabel+"=")
		var encoded string
		if err := json.Unmarshal([]byte(label), &encoded); err != nil {
			os.Exit(1)
		}
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			os.Exit(1)
		}
		write("label", []byte(encoded))
		write("committed", nil)
		fmt.Println(hash468("Docker environment"))
	case "rm":
		write("removed", nil)
	default:
		fmt.Fprintln(os.Stderr, "unexpected Docker call", args)
		os.Exit(1)
	}
	os.Exit(0)
}
