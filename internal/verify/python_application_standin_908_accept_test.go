package verify

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These cases reuse the gate's counter, Docker/TLC double and Nagini double.
// Their existing refusal phrases remain part of the contract.
func TestPythonApplicationStandInRemedies908(t *testing.T) {
	files := standIn908Files(t)
	for _, tc := range []struct {
		name, remove, write, body, reason string
	}{
		{name: "absent package", remove: "nagini_contracts", reason: "doesn't ship"},
		{name: "absent initializer", remove: "nagini_contracts/__init__.py", reason: "doesn't ship nagini_contracts/__init__.py"},
		{name: "absent contracts", remove: "nagini_contracts/contracts.py", reason: "doesn't ship nagini_contracts/contracts.py"},
		{name: "changed initializer", write: "nagini_contracts/__init__.py", body: "# changed\n", reason: "isn't Invariant's runtime stand-in"},
		{name: "changed contracts", write: "nagini_contracts/contracts.py", body: string(files["contracts.py"]) + "\n# changed\n", reason: "isn't Invariant's runtime stand-in"},
		{name: "extra file", write: "nagini_contracts/extra.py", body: "pass\n", reason: "which Invariant's runtime stand-in doesn't"},
		{name: "second package", write: "vendor/nagini_contracts/__init__.py", body: "", reason: "ships another nagini_contracts"},
		{name: "alternate module", write: "vendor/nagini_contracts.py", body: "", reason: "ships another nagini_contracts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, log := pa342Fake(t)
			nagini := gp359Verifier(t, false)
			f := gp359New(t, false)
			if tc.remove != "" {
				if err := os.RemoveAll(filepath.Join(f.root, filepath.FromSlash(tc.remove))); err != nil {
					t.Fatal(err)
				}
			}
			if tc.write != "" {
				pa342Write(t, f.root, tc.write, tc.body)
			}
			before := pa342Tree(t, f.root)
			started := 0
			pythonApplicationGateOptions.Execute = func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
				started++
				return PythonApplicationProcess{}, fmt.Errorf("application execution after stand-in rejection")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			r, err := Run(ctx, f.p.Dir, "", tools)
			if r != nil || err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("want refusal %q before a report, got report=%v, error=%v", tc.reason, r != nil, err)
			}
			standIn908Diagnostic(t, err.Error(), f.root, files)
			if started != 0 || *nagini != 0 {
				t.Errorf("rejection started %d application executions and %d Nagini runs", started, *nagini)
			}
			standIn908NoExecution(t, log)
			if after := pa342Tree(t, f.root); !reflect.DeepEqual(before, after) {
				t.Fatal("a refusal changed the application's files")
			}
		})
	}
	t.Run("exact two-file package still passes", func(t *testing.T) {
		tools, _ := pa342Fake(t)
		nagini := gp359Verifier(t, false)
		f := gp359New(t, false)
		before := pa342Tree(t, f.root)
		r := pa342Run(t, f, tools)
		if !r.Passed || *nagini == 0 || r.Application == nil || len(r.Application.Executions) == 0 {
			t.Fatalf("the exact shipped package no longer supports the gate: %s", gp359Dump(t, r))
		}
		if after := pa342Tree(t, f.root); !reflect.DeepEqual(before, after) {
			t.Fatal("verification changed the shipped application")
		}
	})
}

func standIn908Files(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, name := range []string{"__init__.py", "contracts.py"} {
		b, err := naginiRuntime.ReadFile("naginiruntime/nagini_contracts/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = b
	}
	return files
}

func standIn908Diagnostic(t *testing.T, diagnostic, root string, files map[string][]byte) {
	t.Helper()
	if !strings.Contains(diagnostic, filepath.Join(root, "nagini_contracts")) {
		t.Errorf("remedy must name the original application's destination, not a staged path:\n%s", diagnostic)
	}
	for name, b := range files {
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256(b))
		// Associate each exact byte digest with its file, rather than merely
		// finding two unlabelled hashes somewhere in the message.
		line := regexp.MustCompile(`(?m)^.*` + regexp.QuoteMeta(name) + `[^\n]*` + regexp.QuoteMeta(digest) + `.*$`)
		if !line.MatchString(diagnostic) {
			t.Errorf("remedy lacks %s's expected %s on its file's line:\n%s", name, digest, diagnostic)
		}
	}
	for _, word := range []string{"CI", "revision", "pinned"} {
		if !strings.Contains(diagnostic, word) {
			t.Errorf("remedy lacks %q guidance:\n%s", word, diagnostic)
		}
	}
}

func standIn908NoExecution(t *testing.T, log string) {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, kind := range []string{`"kind": "python"`, `"kind": "tlc"`, `"kind": "forbidden"`} {
		if strings.Contains(string(b), kind) {
			t.Errorf("a rejected package started a tool: %s", b)
		}
	}
}

// Build actual executables to exercise runtime/debug's real metadata and the
// production gate, without a test-only build-identity hook. The small main only
// supplies the already prepared toolchain that the CLI normally supplies.
const standIn908Probe = `package main
import (
    "context"
    "fmt"
    "os"
    "github.com/gitdek/invariant/internal/toolchain"
    "github.com/gitdek/invariant/internal/verify"
)
func main() {
    tc := toolchain.Toolchain{JavaImage: toolchain.JavaImage, PythonImage: toolchain.PythonImage, TLCJar: "unused-fixture.jar"}
    _, err := verify.Run(context.Background(), os.Args[1], "", tc)
    if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
    fmt.Fprintln(os.Stderr, "unexpected admission of missing stand-in")
    os.Exit(3)
}
`

func TestPythonApplicationStandInPackagedIdentity908(t *testing.T) {
	tools, log := pa342Fake(t)
	files := standIn908Files(t)
	checkout := filepath.Join(t.TempDir(), "Invariant checkout")
	standIn908CopyRepository(t, checkout)
	pa342Write(t, checkout, "cmd/standin908probe/main.go", standIn908Probe)
	standIn908Git(t, checkout, "init", "-q")
	standIn908Git(t, checkout, "add", "-f", ".")
	standIn908Git(t, checkout, "commit", "-qm", "offline stand-in fixture")
	revision := strings.TrimSpace(standIn908Git(t, checkout, "rev-parse", "HEAD"))
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) {
		t.Fatalf("fixture has no full Git revision: %q", revision)
	}
	binDir := t.TempDir()
	clean := standIn908Build(t, checkout, binDir, "clean", "true", revision, "false")
	unknown := standIn908Build(t, checkout, binDir, "unknown", "false", "", "")
	dirtyFiles := standIn908Files(t)
	dirtyFiles["contracts.py"] = append(dirtyFiles["contracts.py"], []byte("\n# modified build fixture\n")...)
	pa342Write(t, checkout, "internal/verify/naginiruntime/nagini_contracts/contracts.py", string(dirtyFiles["contracts.py"]))
	modified := standIn908Build(t, checkout, binDir, "modified", "true", revision, "true")
	pa342Write(t, checkout, "internal/verify/naginiruntime/nagini_contracts/contracts.py", string(files["contracts.py"]))

	f := gp359New(t, false)
	if err := os.RemoveAll(filepath.Join(f.root, "nagini_contracts")); err != nil {
		t.Fatal(err)
	}
	before := pa342Tree(t, f.root)
	// The binaries must not consult the directory recorded in their build
	// paths, or expect an Invariant checkout beside the application.
	hidden := checkout + " hidden"
	if err := os.Rename(checkout, hidden); err != nil {
		t.Fatal(err)
	}
	cleanDiagnostic := ""
	for _, tc := range []struct {
		name, binary string
		files        map[string][]byte
	}{
		{"clean", clean, files}, {"unknown", unknown, files}, {"modified", modified, dirtyFiles},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, tc.binary, f.p.Dir)
			cmd.Dir = f.root
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
				t.Fatalf("packaged gate did not reject the missing package: %v\n%s", err, out)
			}
			diagnostic := string(out)
			if !strings.Contains(diagnostic, "doesn't ship") {
				t.Fatalf("did not reach the shipping check: %s", diagnostic)
			}
			standIn908Diagnostic(t, diagnostic, f.root, tc.files)
			url := "https://github.com/gitdek/invariant/tree/" + revision + "/internal/verify/naginiruntime/nagini_contracts"
			if tc.name == "clean" {
				cleanDiagnostic = diagnostic
				for _, want := range []string{url, "--detach", "INVARIANT_CHECKOUT", "APPLICATION_ROOT", "same"} {
					if !strings.Contains(diagnostic, want) {
						t.Errorf("clean packaged build lacks %q:\n%s", want, diagnostic)
					}
				}
			} else {
				lower := strings.ToLower(diagnostic)
				if tc.name == "modified" && !strings.Contains(lower, "modified") {
					t.Errorf("modified build was not labeled: %s", diagnostic)
				}
				if tc.name == "unknown" && !strings.Contains(lower, "unknown") && !strings.Contains(lower, "unavailable") && !strings.Contains(lower, "absent") {
					t.Errorf("missing revision metadata was not labeled: %s", diagnostic)
				}
				if !strings.Contains(lower, "build") || !strings.Contains(lower, "run") || !strings.Contains(lower, "before") {
					t.Errorf("must direct building and running the CI pin before copying: %s", diagnostic)
				}
				if strings.Contains(diagnostic, url) || strings.Contains(diagnostic, "```sh") {
					t.Errorf("unreproducible build offered a clean-revision source or copy recipe: %s", diagnostic)
				}
			}
			if after := pa342Tree(t, f.root); !reflect.DeepEqual(before, after) {
				t.Fatal("the packaged gate changed application files")
			}
			standIn908NoExecution(t, log)
		})
	}
	if err := os.Rename(hidden, checkout); err != nil {
		t.Fatal(err)
	}
	recipe := standIn908Recipe(t, cleanDiagnostic)
	if recipe == "" {
		return // The missing recipe has already failed this acceptance test.
	}

	// A same-byte package at another HEAD is not the requested pinned checkout.
	pa342Write(t, checkout, "other-revision.txt", "another revision\n")
	standIn908Git(t, checkout, "add", "other-revision.txt")
	standIn908Git(t, checkout, "commit", "-qm", "other revision")
	standIn908CopyRecipe(t, recipe, checkout, f.root, false)
	standIn908Git(t, checkout, "checkout", "--detach", revision)
	pa342Write(t, checkout, "internal/verify/naginiruntime/nagini_contracts/contracts.py", "# wrong bytes at the pinned HEAD\n")
	standIn908CopyRecipe(t, recipe, checkout, f.root, false)
	pa342Write(t, checkout, "internal/verify/naginiruntime/nagini_contracts/contracts.py", string(files["contracts.py"]))
	standIn908CopyRecipe(t, recipe, checkout, f.root, true)
	entries, err := os.ReadDir(filepath.Join(f.root, "nagini_contracts"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("copy did not produce the exact two-file package: %v, %v", entries, err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(f.root, "nagini_contracts", name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("copied %s differs from the packaged build: %v", name, err)
		}
	}
	// Repeating instructions must not silently replace an application's edits.
	pa342Write(t, f.root, "nagini_contracts/contracts.py", "# application-owned edit\n")
	standIn908CopyRecipe(t, recipe, checkout, f.root, false)
	pa342Write(t, f.root, "nagini_contracts/contracts.py", string(files["contracts.py"]))
	standIn908Import(t, f.root)
	nagini := gp359Verifier(t, false)
	r := pa342Run(t, f, tools)
	if !r.Passed || *nagini == 0 {
		t.Fatalf("the package copied by the remedy did not pass the gate: %s", gp359Dump(t, r))
	}
}

func standIn908CopyRepository(t *testing.T, dst string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func standIn908Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ALLOW_PROTOCOL=file",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func standIn908Build(t *testing.T, checkout, dir, name, vcs, revision, modified string) string {
	t.Helper()
	binary := filepath.Join(dir, name)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=readonly", "-buildvcs="+vcs, "-o", binary, "./cmd/standin908probe")
	cmd.Dir = checkout
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline %s build: %v\n%s", name, err, out)
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["vcs.revision"] != revision || settings["vcs.modified"] != modified {
		t.Fatalf("fixture %s has wrong build identity: %v", name, settings)
	}
	return binary
}

func standIn908Recipe(t *testing.T, diagnostic string) string {
	t.Helper()
	const opening = "```sh\n"
	if strings.Count(diagnostic, opening) != 1 {
		t.Errorf("expected one fenced sh copy recipe in the clean-build remedy:\n%s", diagnostic)
		return ""
	}
	rest := strings.SplitN(diagnostic, opening, 2)[1]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Error("copy recipe has no closing fence")
		return ""
	}
	recipe := rest[:end]
	for _, command := range []string{"git clone", "git fetch", "curl ", "wget ", "pip install"} {
		if strings.Contains(recipe, command) {
			t.Errorf("the copy-from-checkout recipe must work offline: %s", recipe)
			return ""
		}
	}
	return recipe
}

func standIn908CopyRecipe(t *testing.T, recipe, checkout, root string, success bool) {
	t.Helper()
	before := pa342Tree(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", recipe)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "INVARIANT_CHECKOUT="+checkout, "APPLICATION_ROOT="+root, "GIT_ALLOW_PROTOCOL=file")
	out, err := cmd.CombinedOutput()
	if (err == nil) != success {
		t.Fatalf("copy recipe success=%v, want %v: %v\n%s", err == nil, success, err, out)
	}
	if !success && !reflect.DeepEqual(before, pa342Tree(t, root)) {
		t.Fatal("a refused copy changed application files")
	}
}

func standIn908Import(t *testing.T, root string) {
	t.Helper()
	const script = `import pathlib, sys
root = pathlib.Path(sys.argv[1]).resolve()
sys.path.insert(0, str(root))
import nagini_contracts
from nagini_contracts import contracts
from counter.core import admit
assert pathlib.Path(nagini_contracts.__file__).resolve() == root / 'nagini_contracts' / '__init__.py'
assert pathlib.Path(contracts.__file__).resolve() == root / 'nagini_contracts' / 'contracts.py'
assert admit('counter', 0) == 1
print('shipped package imported')
`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-B", "-c", script, root)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "shipped package imported" {
		t.Fatalf("application did not import its shipped package: %v\n%s", err, out)
	}
}
