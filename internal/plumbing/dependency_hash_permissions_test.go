package plumbing

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDependencyPythonHashesBootstrapPackages(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	probe := exec.CommandContext(ctx, python, "-I", "-c", "import ensurepip; print(ensurepip.version())")
	out, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap pip version: %v\n%s", err, out)
	}
	version := strings.TrimSpace(string(out))
	wheels := t.TempDir()
	wheel := dependencyBootstrapWheel(t, version)
	if err := os.WriteFile(filepath.Join(wheels, "pip-"+version+"-py3-none-any.whl"), wheel, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprintf("valid hash %t", valid), func(t *testing.T) {
			hash := strings.Repeat("0", 64)
			if valid {
				hash = strings.TrimPrefix(hash468(string(wheel)), "sha256:")
			}
			q := dependencyInstallerFixture(t, "python", map[string]string{
				"requirements.lock": "pip==" + version + " --hash=sha256:" + hash + "\n",
			})
			launch := captureDependencyInstaller(t, q)
			stage, err := stageDependencySource(ctx, q, true)
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
			args[start+1] = python
			// Use only a local wheel with the venv's bootstrap version. The
			// production installer must verify it even though pip is present.
			args = append(args, "--no-index", "--find-links", wheels)
			cmd := exec.CommandContext(ctx, "/usr/bin/env", args...)
			cmd.Dir = filepath.Join(stage, "invariant", "work")
			out, err := cmd.CombinedOutput()
			if !valid {
				if err == nil || !strings.Contains(string(out), "DO NOT MATCH THE HASHES") {
					t.Fatalf("bootstrap pip bypassed its incorrect hash: %v\n%s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid bootstrap wheel: %v\n%s", err, out)
			}
			check := exec.CommandContext(ctx, filepath.Join(stage, "invariant", "venv", "bin", "python"), "-I", "-c", "import pip; assert pip.invariant_hash_fixture")
			if out, err := check.CombinedOutput(); err != nil {
				t.Fatalf("declared wheel was not installed over bootstrap pip: %v\n%s", err, out)
			}
		})
	}
}

// A minimal test wheel shares bootstrap pip's version but carries a marker,
// so the successful case also detects an unverified already-satisfied result.
func dependencyBootstrapWheel(t *testing.T, version string) []byte {
	t.Helper()
	dist := "pip-" + version + ".dist-info/"
	files := []struct{ name, body string }{
		{"pip/__init__.py", "__version__ = '" + version + "'\ninvariant_hash_fixture = True\n"},
		{dist + "METADATA", "Metadata-Version: 2.1\nName: pip\nVersion: " + version + "\n"},
		{dist + "WHEEL", "Wheel-Version: 1.0\nGenerator: invariant-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
	}
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	var record strings.Builder
	write := func(name, body string) {
		f, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		write(file.name, file.body)
		hash := sha256.Sum256([]byte(file.body))
		fmt.Fprintf(&record, "%s,sha256=%s,%d\n", file.name, base64.RawURLEncoding.EncodeToString(hash[:]), len(file.body))
	}
	fmt.Fprintf(&record, "%sRECORD,,\n", dist)
	write(dist+"RECORD", record.String())
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestDependencyCandidatePreservesExecutablePermissions(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{
		{"nonexecutable", 0o644},
		{"executable", 0o755},
		{"private nonexecutable", 0o600},
		{"private executable", 0o700},
		{"special nonexecutable", 0o644 | os.ModeSetuid | os.ModeSetgid | os.ModeSticky},
		{"special executable", 0o751 | os.ModeSetuid | os.ModeSetgid | os.ModeSticky},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			file := filepath.Join(source, "check.sh")
			if err := os.WriteFile(file, []byte("#!/bin/sh\nprintf 'checked\\n'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(file, tc.mode); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stage, err := stageDependencySource(ctx, DependencyRequest{SourceDir: source, Directory: "."}, false)
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(stage)
			copied := filepath.Join(stage, "invariant", "work", "check.sh")
			info, err := os.Stat(copied)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode() != tc.mode.Perm() {
				t.Errorf("copied mode = %v, want ordinary permissions %v", info.Mode(), tc.mode.Perm())
			}
			out, err := exec.CommandContext(ctx, copied).CombinedOutput()
			if tc.mode&0o111 == 0 {
				if !errors.Is(err, os.ErrPermission) || len(out) != 0 {
					t.Fatalf("nonexecutable candidate ran: %q, %v", out, err)
				}
			} else if err != nil || string(out) != "checked\n" {
				t.Fatalf("executable candidate failed: %q, %v", out, err)
			}
		})
	}
}
