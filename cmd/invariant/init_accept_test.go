package main

import (
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initPin is a full commit of Invariant for the gate workflow to pin.
const initPin = "61eb0495cd8a5ff84be5bbdb4efc3bb2b7c1ae23"

// initPrints runs invariant init with args, and returns its exit code and
// everything it printed.
func initPrints(t *testing.T, args ...string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	printed := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		printed <- string(b)
	}()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	log.SetOutput(w)
	code := func() int {
		defer func() {
			os.Stdout, os.Stderr = stdout, stderr
			log.SetOutput(stderr)
		}()
		return initCmd(args)
	}()
	w.Close()
	return code, <-printed
}

// repoWithOrigin makes a git repository whose origin remote is remote, with
// one file at its root.
func repoWithOrigin(t *testing.T, remote, file, text string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet", "-b", "main", dir}, {"-C", dir, "remote", "add", "origin", remote}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// init names the repository from its origin remote, https or ssh, and takes
// -language from the project file at its root. Its steps are every one a
// person takes: require invariant/gate on main, allow merge commits, and run
// the factory, with the App optional.
func TestInitStepsFitTheRepository(t *testing.T) {
	for _, c := range []struct{ remote, file, text, repo, language string }{
		{"https://github.com/acme/widgets.git", "go.mod", "module example.com/widgets\n\ngo 1.27.1\n", "acme/widgets", "go"},
		{"git@github.com:acme/gadgets.git", "package.json", "{\"name\": \"gadgets\", \"private\": true}\n", "acme/gadgets", "typescript"},
		{"https://github.com/acme/tools", "pyproject.toml", "[project]\nname = \"tools\"\n", "acme/tools", "python"},
	} {
		dir := repoWithOrigin(t, c.remote, c.file, c.text)
		code, printed := initPrints(t, "-invariant", initPin, dir)
		if code != 0 {
			t.Fatalf("%s: init exited %d:\n%s", c.repo, code, printed)
		}
		if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "gate.yml")); err != nil {
			t.Errorf("%s: init didn't write the gate workflow: %v", c.repo, err)
		}
		for _, want := range []string{
			"invariant watch -repo " + c.repo,
			"-language " + c.language,
			"repos/" + c.repo + "/branches/main/protection",
			"invariant/gate",
			"allow_merge_commit=true",
			"docs/factory-app.md",
		} {
			if !strings.Contains(printed, want) {
				t.Errorf("%s: the steps lack %q:\n%s", c.repo, want, printed)
			}
		}
		if strings.Contains(printed, "OWNER/NAME") {
			t.Errorf("%s: the steps still say OWNER/NAME:\n%s", c.repo, printed)
		}
		// The App is optional, so the step that runs the factory works
		// without one.
		plain := false
		for _, line := range strings.Split(printed, "\n") {
			if strings.Contains(line, "invariant watch -repo "+c.repo) && !strings.Contains(line, "-app-id") {
				plain = true
			}
		}
		if !plain {
			t.Errorf("%s: no step runs the factory without the App:\n%s", c.repo, printed)
		}
	}
}

// Built by go run, or in a checkout with changes, init knows no commit of
// Invariant to pin the gate to. It writes nothing, and says how to fix it:
// build from a clean checkout, or pass -invariant.
func TestInitNamesTheFixWhenItDoesntKnowItsCommit(t *testing.T) {
	dir := repoWithOrigin(t, "https://github.com/acme/widgets.git", "go.mod", "module example.com/widgets\n")
	code, printed := initPrints(t, "-invariant", "", dir)
	if code == 0 {
		t.Fatalf("init went ahead with no commit to pin the gate to:\n%s", printed)
	}
	for _, want := range []string{"clean checkout", "-invariant"} {
		if !strings.Contains(printed, want) {
			t.Errorf("init's message lacks %q:\n%s", want, printed)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "gate.yml")); !os.IsNotExist(err) {
		t.Errorf("init wrote a gate workflow with no commit to pin: %v", err)
	}
}
