package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

// #929: check-macos is the gate's macOS job. It runs the checks of the
// parts the base's contract says build on macOS, as plain argument arrays
// in the part's directory of a private copy of the candidate, and nowhere
// but macOS. These fixtures name the host, so they run on Linux CI too.

// macOSRecorder929 records each check it's run as, where, with which
// GOTOOLCHAIN and token, and whether the projects directory is there, then
// exits with its first argument.
const macOSRecorder929 = `#!/bin/sh
printf '%s|%s|%s|%s|%s\n' "$2" "$(basename "$PWD")" "${GOTOOLCHAIN:-unset}" "${GH_TOKEN:-no token}" "$(if [ -e invariant ]; then echo projects; else echo no projects; fi)" >> "$MACOS_MARKER_929"
exit "$1"
`

type macOSFixture929 struct {
	t            *testing.T
	root, marker string
	config       repository.Config
}

// newMacOSFixture929 writes a Go part at the root whose macOS checks run the
// recorder, with test exiting testExit, and a Go part at api with none.
func newMacOSFixture929(t *testing.T, testExit string) *macOSFixture929 {
	t.Helper()
	f := &macOSFixture929{t: t, root: t.TempDir(), marker: filepath.Join(t.TempDir(), "ran")}
	t.Setenv("MACOS_MARKER_929", f.marker)
	t.Setenv("GH_TOKEN", "fixture-not-a-secret")
	t.Setenv("GOTOOLCHAIN", "auto")
	gateGit736(t, f.root, "init", "-q", "-b", "main")
	for name, text := range map[string]string{
		"AGENTS.md": "Read SPEC.md.\n", "SPEC.md": "The recorder.\n", "go.mod": "module example.invalid/recorder\n\ngo 1.27.1\n", "go.sum": "",
		"main.go": "package main\n\nfunc main() {}\n", "api/go.mod": "module example.invalid/api\n\ngo 1.27.1\n", "api/go.sum": "", "api/api.go": "package api\n",
	} {
		gateWrite736(t, f.root, name, text)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "tools", "macos-check"), []byte(macOSRecorder929), 0o755); err != nil {
		t.Fatal(err)
	}
	part := func(name, dir string) repository.Part {
		prefix := ""
		if dir != "." {
			prefix = dir + "/"
		}
		p := repository.Part{Name: name, Directory: dir, Language: "go", Image: "golang@sha256:" + strings.Repeat("a", 64),
			Lockfiles: []string{prefix + "go.sum"}, Inputs: []string{prefix + "go.mod"}, Commands: map[string]repository.Command{}}
		for _, phase := range []string{"install", "format", "lint", "build", "test"} {
			p.Commands[phase] = repository.Command{Argv: []string{"go", phase}}
		}
		p.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "-run", "{test}"}}
		return p
	}
	app := part("app", ".")
	app.MacOS = &repository.MacOS{
		Build:  repository.Command{Argv: []string{"./tools/macos-check", "0", "build"}},
		Vet:    repository.Command{Argv: []string{"./tools/macos-check", "0", "vet"}},
		Test:   repository.Command{Argv: []string{"./tools/macos-check", testExit, "test"}},
		Checks: []repository.MacOSCheck{{Name: "selftest", Argv: []string{"./tools/macos-check", "0", "self test"}}},
	}
	f.config = repository.Config{Version: 1, Parts: []repository.Part{app, part("api", "api")}, AgentRules: []string{"AGENTS.md"},
		CurrentState: []string{"SPEC.md"}, TrustedPaths: []string{"tools/"}, SharedPaths: []repository.SharedPath{}}
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	return f
}

func (f *macOSFixture929) commit() string {
	f.t.Helper()
	return gateCommit736(f.t, f.root)
}

func (f *macOSFixture929) check(host string, args ...string) (int, string, []string) {
	f.t.Helper()
	var out bytes.Buffer
	code := runMacOSCheck(f.t.Context(), append([]string{"-C", f.root}, args...), host, &out)
	b, err := os.ReadFile(f.marker)
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return code, out.String(), strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), " ", "_"))
}

func TestCheckMacOSRunsTheBasesChecks929(t *testing.T) {
	f := newMacOSFixture929(t, "1")
	base := f.commit()
	// The candidate makes its own test pass, adds a check and fills the
	// projects directory: none of it changes what it's checked by.
	f.config.Parts[0].MacOS.Test.Argv[1] = "0"
	f.config.Parts[0].MacOS.Checks = append(f.config.Parts[0].MacOS.Checks, repository.MacOSCheck{Name: "extra", Argv: []string{"./tools/macos-check", "0", "extra"}})
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	gateWrite736(t, f.root, "invariant/model/model.go", "package model\n")
	head := f.commit()
	code, out, ran := f.check("darwin", "-base", base, "HEAD")
	if code != 1 {
		t.Fatalf("a failing macOS test passed the job: %d\n%s", code, out)
	}
	want := []string{"build|candidate|local|no_token|no_projects", "vet|candidate|local|no_token|no_projects", "test|candidate|local|no_token|no_projects", "self_test|candidate|local|no_token|no_projects"}
	if strings.Join(ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("the job ran\n  %s\nwant\n  %s", strings.Join(ran, "\n  "), strings.Join(want, "\n  "))
	}
	for _, said := range []string{
		"macOS checks of " + head + ", as the contract committed at " + base + " declares them, on macOS ",
		"app/build: passed in ", "app/vet: passed in ", "app/test: failed in ", "app/selftest: passed in ",
		`(./tools/macos-check 0 "self test")`,
		"Failed on macOS: app/test.",
	} {
		if !strings.Contains(out, said) {
			t.Errorf("the job doesn't say %q:\n%s", said, out)
		}
	}
	if strings.Contains(out, "api/") || strings.Contains(out, "extra") || strings.Contains(out, "Everything passed") {
		t.Errorf("the job ran a check the base doesn't declare, or claimed a pass:\n%s", out)
	}
}

// Every part that builds on macOS is checked, in the order of their names,
// on every run, and the job names the hosted runner's image it ran on.
func TestCheckMacOSPassesOnlyWhenEveryCheckPasses929(t *testing.T) {
	f := newMacOSFixture929(t, "0")
	t.Setenv("ImageOS", "macos26")
	t.Setenv("ImageVersion", "20261102.0473.1")
	f.config.Parts[1].MacOS = &repository.MacOS{
		Build: repository.Command{Argv: []string{"../tools/macos-check", "0", "api-build"}},
		Vet:   repository.Command{Argv: []string{"../tools/macos-check", "0", "api-vet"}},
		Test:  repository.Command{Argv: []string{"../tools/macos-check", "0", "api-test"}},
	}
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	f.commit()
	code, out, ran := f.check("darwin")
	if code != 0 || !strings.Contains(out, "Everything passed on macOS: every check the contract declares for the parts that build on macOS (api, app).") {
		t.Fatalf("the job: %d, ran %q\n%s", code, ran, out)
	}
	want := []string{"api-build|api", "api-vet|api", "api-test|api", "build|candidate", "vet|candidate", "test|candidate", "self_test|candidate"}
	for i := range ran {
		ran[i] = strings.Join(strings.Split(ran[i], "|")[:2], "|")
	}
	if strings.Join(ran, " ") != strings.Join(want, " ") {
		t.Errorf("the job ran %q, want %q", ran, want)
	}
	if !strings.Contains(out, ", runner image macos26 20261102.0473.1, and go version go") {
		t.Errorf("the job doesn't name the image it ran on:\n%s", out)
	}
}

// Off macOS the job fails, and runs nothing: a skipped required check
// would count as passed.
func TestCheckMacOSRunsOnMacOSOnly929(t *testing.T) {
	for _, host := range []string{"linux", "windows", ""} {
		f := newMacOSFixture929(t, "0")
		f.commit()
		code, out, ran := f.check(host)
		if code != 1 || len(ran) != 0 || !strings.Contains(out, "runs on macOS only") || !strings.Contains(out, setup.MacOSCheck) {
			t.Errorf("on %q the job: %d, ran %q\n%s", host, code, ran, out)
		}
	}
}

// A base whose contract declares no macOS part, as before the pull request
// that adds one, has nothing checked, and says so. An invalid one fails.
func TestCheckMacOSChecksNothingTheBaseDoesntDeclare929(t *testing.T) {
	f := newMacOSFixture929(t, "0")
	if err := os.Remove(filepath.Join(f.root, filepath.FromSlash(repository.Path))); err != nil {
		t.Fatal(err)
	}
	none := f.commit()
	code, out, ran := f.check("darwin", "-base", none)
	if code != 0 || len(ran) != 0 || !strings.Contains(out, "No repository contract is committed at "+none+", so no part builds on macOS, and nothing was checked here.") {
		t.Errorf("without a contract: %d, ran %q\n%s", code, ran, out)
	}
	f.config.Parts[0].MacOS = nil
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	linux := f.commit()
	code, out, ran = f.check("darwin", "-base", linux)
	if code != 0 || len(ran) != 0 || !strings.Contains(out, "declares no part that builds on macOS, so nothing was checked here.") {
		t.Errorf("without a macOS part: %d, ran %q\n%s", code, ran, out)
	}
	gateWrite736(t, f.root, repository.Path, `{"version":1,"parts":[{"name":"app","macos":{}}]}`)
	broken := f.commit()
	if code, out, ran = f.check("darwin", "-base", broken); code != 1 || len(ran) != 0 || !strings.Contains(out, "invalid repository contract") {
		t.Errorf("with an invalid contract: %d, ran %q\n%s", code, ran, out)
	}
}

// check-repository runs a part's commands in Linux containers, so it never
// says a part that builds on macOS was checked there.
func TestCheckRepositoryLeavesTheMacOSPartUnchecked929(t *testing.T) {
	for _, macos := range []bool{false, true} {
		f := gateFixtureFor736(t, "go")
		if macos {
			f.config.Parts[0].MacOS = &repository.MacOS{
				Build: repository.Command{Argv: []string{"go", "build", "./..."}}, Vet: repository.Command{Argv: []string{"go", "vet", "./..."}},
				Test: repository.Command{Argv: []string{"go", "test", "./..."}}, Checks: []repository.MacOSCheck{{Name: "selftest", Argv: []string{"go", "run", ".", "selftest"}}},
			}
			gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
			f.base = gateCommit736(t, f.root)
		}
		code, out := f.run(t.Context(), f.runner())
		if code != 0 {
			t.Fatalf("macos %t: the checks failed: %d\n%s", macos, code, out)
		}
		note := "api on macOS: not checked here. check-repository runs a part's commands in Linux containers, which skip the files only macOS builds. Only the invariant/gate-macos job runs its macOS build, vet, test and selftest, on a hosted macOS runner, and check-repository can't tell whether that job ran.\n"
		if got := strings.Contains(out, note); got != macos {
			t.Errorf("macos %t: the note is there: %t\n%s", macos, got, out)
		}
		if !macos && strings.Contains(out, "macOS") {
			t.Errorf("a repository without a macOS part hears of macOS:\n%s", out)
		}
	}
}
