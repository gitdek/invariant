package found

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

// founded is every file a founding without a spec commits, in the order it
// names them to git.
const founded = ".gitignore AGENTS.md README.md SPEC.md doc.go go.mod tools/check-format .github/workflows/gate.yml .invariant/repository.json"

// contract is the checks committed in dir, read with the loader CI and the
// factory use. It refuses a file with an unresolved entry.
func (w *world) contract(dir string) repository.Config {
	w.t.Helper()
	s, err := repository.Load(context.Background(), dir, "HEAD")
	if err != nil {
		w.t.Fatalf("the committed checks don't load: %v", err)
	}
	return s.Config()
}

// From nothing, new creates the repository, private, and its checkout,
// writes the Go starter, the gate and complete checks, commits them, pushes
// main, allows merge commits and requires the gate: these commands, in this
// order, and no others.
func TestNewFoundsAProjectFromNothing919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	want := []string{
		"gh repo view acme/widgets --json isEmpty,mergeCommitAllowed,visibility",
		"gh repo create acme/widgets --private",
		"gh repo clone acme/widgets ROOT/widgets",
		"git symbolic-ref HEAD refs/heads/main",
		"git config user.name",
		"git config user.email",
		"git add -- " + founded,
		"git commit --quiet -m Start widgets: a Go module, its checks and Invariant's gate -- " + founded,
		"git push -u origin main",
		"gh api -X PATCH repos/acme/widgets -F allow_merge_commit=true",
		"gh api repos/acme/widgets/branches/main/protection",
		"gh api -X PUT repos/acme/widgets/branches/main/protection --input -",
	}
	if !slices.Equal(w.calls, want) {
		t.Errorf("the founding ran\n  %s\nwant\n  %s", strings.Join(w.calls, "\n  "), strings.Join(want, "\n  "))
	}
	if w.body != setup.Protection {
		t.Errorf("the gate was required with %q, not the protection init prints", w.body)
	}

	// The starter, as committed.
	files := w.tree(dir)
	if got, want := slices.Sorted(maps(files)), strings.Fields(founded); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("the checkout holds %q, want %q", got, want)
	}
	for name, wants := range map[string][]string{
		"go.mod":                     {"module github.com/acme/widgets\n", "\ngo 1.27\n"},
		"doc.go":                     {"// Package widgets ", "\npackage widgets\n"},
		"tools/check-format":         {"#!/bin/sh\n", "$(gofmt -l .)", "exit 1"},
		".gitignore":                 {".DS_Store"},
		"README.md":                  {"# widgets\n", "SPEC.md", "AGENTS.md"},
		"SPEC.md":                    {"Nothing is built yet", "github.com/acme/widgets"},
		"AGENTS.md":                  {"SPEC.md", "go vet ./...", "tools/check-format"},
		".github/workflows/gate.yml": {"ref: " + pin, "name: invariant/gate"},
	} {
		for _, want := range wants {
			if !strings.Contains(files[name], want) {
				t.Errorf("%s lacks %q:\n%s", name, want, files[name])
			}
		}
	}
	if status := w.mustGit(dir, "status", "--porcelain"); status != "" {
		t.Errorf("the checkout isn't clean after the founding:\n%s", status)
	}
	if mode := w.mustGit(dir, "ls-tree", "HEAD", "--", "tools/check-format"); !strings.HasPrefix(mode, "100755 ") {
		t.Errorf("the format check isn't committed as an executable: %s", mode)
	}
	if branch := w.mustGit(dir, "symbolic-ref", "--short", "HEAD"); branch != "main" {
		t.Errorf("the founding's commit is on %s, not main", branch)
	}
	if head := w.mustGit(dir, "rev-parse", "HEAD"); !slices.Equal(w.pushed, []string{head}) {
		t.Errorf("pushed %q, want the founding's one commit, %s", w.pushed, head)
	}
	if n := w.mustGit(dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("the founding made %s commits, want 1", n)
	}

	// The checks, as committed: complete, with the three things a person
	// did by hand.
	if strings.Contains(files[repository.Path], "unresolved") {
		t.Errorf("the checks file still has an unresolved entry:\n%s", files[repository.Path])
	}
	c := w.contract(dir)
	if len(c.Parts) != 1 || c.Parts[0].Directory != "." || c.Parts[0].Language != "go" {
		t.Fatalf("the checks' parts = %+v, want one Go part at the root", c.Parts)
	}
	wantCommands := map[string][]string{
		"install":  {"go", "mod", "download"},
		"format":   {"./tools/check-format"},
		"lint":     {"go", "vet", "./..."},
		"build":    {"go", "build", "./..."},
		"test":     {"go", "test", "./..."},
		"test_one": {"go", "test", "./...", "-run", "{test}", "-count=1"},
	}
	for name, argv := range wantCommands {
		if got := c.Parts[0].Commands[name].Argv; !slices.Equal(got, argv) {
			t.Errorf("the %s check runs %q, want %q", name, got, argv)
		}
	}
	if !slices.Equal(c.CurrentState, []string{"SPEC.md", "README.md"}) {
		t.Errorf("current_state = %q, want the starter's documents", c.CurrentState)
	}
	if !slices.Equal(c.TrustedPaths, []string{"tools/"}) {
		t.Errorf("trusted_paths = %q, want tools/", c.TrustedPaths)
	}
	if !slices.Equal(c.AgentRules, []string{"AGENTS.md"}) {
		t.Errorf("agent_rules = %q, want AGENTS.md", c.AgentRules)
	}

	// GitHub, as the founding left it.
	if !w.exists || w.empty || w.visibility != "private" || !w.mergeCommits || !w.required {
		t.Errorf("GitHub has exists=%t empty=%t visibility=%s mergeCommits=%t required=%t", w.exists, w.empty, w.visibility, w.mergeCommits, w.required)
	}
	for _, want := range []string{
		"Created acme/widgets on GitHub, private.", "Pushed main.", "Required invariant/gate on main",
		"acme/widgets is founded.",
		"Left for a person:",
		"https://github.com/settings/installations", "docs/factory-app.md#install-it",
		"\ninvariant watch -repo acme/widgets -projects invariant -language go\n", "-app-id",
		"-repo acme/widgets to invariant dashboard",
		"https://github.com/acme/widgets/issues/new", "/invariant solve",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the founding didn't say %q:\n%s", want, out)
		}
	}
}

// maps is the names of a tree's files.
func maps(files map[string]string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for name := range files {
			if !yield(name) {
				return
			}
		}
	}
}

// new says the repository and the directory before it runs a single
// command, so before it creates anything, with -dir or with its default.
func TestNewNamesTheRepositoryAndDirectoryBeforeItCreatesAnything919(t *testing.T) {
	for _, named := range []bool{true, false} {
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		commands := 0
		w.before = func(c Command) {
			if commands++; commands > 1 {
				return
			}
			for _, want := range []string{"Repository: https://github.com/acme/widgets\n", "Directory:  " + dir + "\n"} {
				if !strings.Contains(w.said.String(), want) {
					t.Errorf("new ran %q before it said %q; it had said:\n%s", c, want, w.said.String())
				}
			}
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s was there before new's first command: %v", dir, err)
			}
			if w.exists {
				t.Error("the repository was there before new's first command")
			}
		}
		o := Options{Repo: w.repo, Language: "go", Invariant: pin, WorkDir: w.root, Runner: w, Out: &w.said}
		if named {
			o.Dir = dir
		}
		if err := Project(context.Background(), o); err != nil {
			t.Fatalf("-dir given %t: %v", named, err)
		}
		if commands == 0 || w.calls[0] != "gh repo view acme/widgets --json isEmpty,mergeCommitAllowed,visibility" {
			t.Errorf("-dir given %t: new's first command was %q, want a read of the repository", named, w.calls)
		}
		if said := w.said.String(); strings.Index(said, "Repository: ") > strings.Index(said, "Created ") || strings.Index(said, "Directory: ") > strings.Index(said, "Created ") {
			t.Errorf("-dir given %t: new named what it would create after creating it:\n%s", named, said)
		}
	}
}

// -dir defaults to the repository's name beside the checkout the current
// directory is in, and to that name in the current directory when it's in
// none. A relative -dir starts from the current directory.
func TestNewPutsTheCheckoutBesideTheCurrentCheckout919(t *testing.T) {
	w := newWorld(t)
	work := filepath.Join(w.root, "projects", "current", "cmd", "tool")
	w.write(filepath.Join(w.root, "projects", "current", ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ dir, want string }{
		{"", filepath.Join(w.root, "projects", "widgets")},
		{"elsewhere/w", filepath.Join(work, "elsewhere", "w")},
		{filepath.Join(w.root, "abs"), filepath.Join(w.root, "abs")},
	} {
		if got := directory(work, c.dir, "widgets"); got != c.want {
			t.Errorf("-dir %q from %s is %s, want %s", c.dir, work, got, c.want)
		}
	}
	// A linked worktree's .git is a file, and it's a checkout all the same.
	linked := filepath.Join(w.root, "trees", "feature")
	w.write(filepath.Join(linked, ".git"), "gitdir: elsewhere\n")
	if got, want := directory(linked, "", "widgets"), filepath.Join(w.root, "trees", "widgets"); got != want {
		t.Errorf("from a linked worktree the checkout goes to %s, want %s", got, want)
	}
	plain := filepath.Join(w.root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	inCheckout := false
	for d := w.root; filepath.Dir(d) != d; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			inCheckout = true
		}
	}
	if got, want := directory(plain, "", "widgets"), filepath.Join(plain, "widgets"); !inCheckout && got != want {
		t.Errorf("outside any checkout the checkout goes to %s, want %s", got, want)
	}
	// And the founding goes where that says.
	o := Options{Repo: w.repo, Language: "go", Invariant: pin, WorkDir: work, Runner: w, Out: &w.said}
	if err := Project(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if !w.ran("gh repo clone acme/widgets ROOT/projects/widgets") {
		t.Errorf("the checkout didn't go beside the current one:\n%s", strings.Join(w.calls, "\n"))
	}
	if _, err := os.Stat(filepath.Join(w.root, "projects", "widgets", "go.mod")); err != nil {
		t.Errorf("no starter beside the current checkout: %v", err)
	}
}

// An empty directory that's there already is adopted: the clone goes into
// it.
func TestNewAdoptsAnEmptyDirectory919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("new refused an empty directory: %v\n%s", err, out)
	}
	if !w.ran("gh repo clone acme/widgets ROOT/widgets") || len(w.pushed) != 1 {
		t.Errorf("new didn't found the project in the empty directory:\n%s", strings.Join(w.calls, "\n"))
	}
	w.contract(dir)
}

// A repository that's there already, with nothing in it, is adopted: new
// creates none, and leaves a setting that's already right alone.
func TestNewAdoptsAnEmptyRepository919(t *testing.T) {
	w := newWorld(t)
	w.exists, w.empty, w.mergeCommits, w.visibility = true, true, true, "private"
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("new refused an empty repository: %v\n%s", err, out)
	}
	if w.ran("gh repo create") {
		t.Errorf("new created a repository that was there:\n%s", strings.Join(w.calls, "\n"))
	}
	if w.ran("gh api -X PATCH") {
		t.Errorf("new set merge commits, which the repository allowed already:\n%s", strings.Join(w.calls, "\n"))
	}
	if !w.ran("gh repo clone acme/widgets ROOT/widgets") || len(w.pushed) != 1 || !w.required {
		t.Errorf("new didn't finish the founding in the repository it adopted:\n%s", strings.Join(w.calls, "\n"))
	}
	if !strings.Contains(out, "acme/widgets is on GitHub already, with nothing in it: adopted as it is.") || !strings.Contains(out, "acme/widgets is founded.") {
		t.Errorf("new didn't say it adopted the repository:\n%s", out)
	}
	// One that doesn't allow merge commits yet is made to.
	w = newWorld(t)
	w.exists, w.empty, w.mergeCommits, w.visibility = true, true, false, "private"
	if out, err := w.found(Options{}); err != nil || !w.mergeCommits || !strings.Contains(out, "Allowed merge commits") {
		t.Errorf("new left an adopted repository refusing merge commits: %v\n%s", err, out)
	}
}

// A checkout whose origin is the repository is adopted, over https or ssh,
// whatever the case of its name. new clones nothing, and keeps a file
// that's already there with the contents it would write.
func TestNewAdoptsTheRepositorysOwnCheckout919(t *testing.T) {
	for _, remote := range []string{"https://github.com/acme/widgets.git", "git@github.com:Acme/Widgets.git", "ssh://git@github.com/acme/widgets"} {
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		w.checkout(dir, remote)
		w.write(filepath.Join(dir, "go.mod"), "module github.com/acme/widgets\n\ngo 1.27\n")
		out, err := w.found(Options{})
		if err != nil {
			t.Fatalf("%s: new refused the repository's own checkout: %v\n%s", remote, err, out)
		}
		if w.ran("gh repo clone") {
			t.Errorf("%s: new cloned over a checkout that was there:\n%s", remote, strings.Join(w.calls, "\n"))
		}
		if !strings.Contains(out, dir+" is its checkout already: adopted as it is.") || !strings.Contains(out, "Kept 1 of the founding's files") {
			t.Errorf("%s: new didn't say what it adopted and kept:\n%s", remote, out)
		}
		if strings.Contains(out, "Wrote .gitignore, AGENTS.md, README.md, SPEC.md, doc.go, go.mod") {
			t.Errorf("%s: new says it wrote go.mod, which was there:\n%s", remote, out)
		}
		if len(w.pushed) != 1 || !w.required {
			t.Errorf("%s: new didn't finish the founding in the checkout it adopted", remote)
		}
		w.contract(dir)
	}
}

// new never overwrites a file. One that's there with other contents stops
// it before it creates anything, and its message names the file.
func TestNewNeverOverwritesAFileAndNamesIt919(t *testing.T) {
	for _, c := range []struct {
		name  string
		make  func(w *world, dir string)
		names string
		spec  bool
	}{
		{"a README of the person's own", func(w *world, dir string) { w.write(filepath.Join(dir, "README.md"), "# Mine\n") }, "README.md", false},
		{"another gate workflow", func(w *world, dir string) {
			w.write(filepath.Join(dir, ".github", "workflows", "gate.yml"), "name: mine\n")
		}, filepath.Join(".github", "workflows", "gate.yml"), false},
		{"another spec", func(w *world, dir string) { w.write(filepath.Join(dir, "docs", "PRD.md"), "# Another spec\n") }, filepath.Join("docs", "PRD.md"), true},
		{"a format check that can't run", func(w *world, dir string) {
			text, err := starters.ReadFile("starter/go/check-format.tmpl")
			if err != nil {
				w.t.Fatal(err)
			}
			w.write(filepath.Join(dir, "tools", "check-format"), string(text))
		}, filepath.Join("tools", "check-format"), false},
		{"a file where a directory goes", func(w *world, dir string) { w.write(filepath.Join(dir, "tools"), "not a directory\n") }, "tools", false},
		{"a link where a file goes", func(w *world, dir string) {
			w.write(filepath.Join(w.root, "outside.md"), "# Outside\n")
			if err := os.Symlink(filepath.Join(w.root, "outside.md"), filepath.Join(dir, "SPEC.md")); err != nil {
				w.t.Fatal(err)
			}
		}, "SPEC.md", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			w.checkout(dir, "https://github.com/acme/widgets.git")
			c.make(w, dir)
			before := w.tree(w.root)
			o := Options{}
			if c.spec {
				o.Spec = filepath.Join(w.root, "spec.md")
				w.write(o.Spec, "# The spec\n")
				before = w.tree(w.root)
			}
			out, err := w.found(o)
			if err == nil {
				t.Fatalf("new went ahead over %s:\n%s", c.name, out)
			}
			if !strings.Contains(err.Error(), filepath.Join(dir, c.names)) {
				t.Errorf("new's refusal doesn't name %s: %v", filepath.Join(dir, c.names), err)
			}
			if after := w.tree(w.root); !reflect.DeepEqual(after, before) {
				t.Errorf("new changed files before it refused: %q, were %q", slices.Sorted(maps(after)), slices.Sorted(maps(before)))
			}
			if w.exists || w.ran("gh repo create") || w.ran("git add") || w.ran("git commit") || w.ran("git push") {
				t.Errorf("new created something before it refused:\n%s", strings.Join(w.calls, "\n"))
			}
		})
	}
}

// new starts in an empty directory or in the repository's own checkout, and
// nowhere else: a directory with something in it, a file, or a checkout of
// another repository is refused before anything is created. It never
// repeats a remote's address, which can carry a token.
func TestNewRefusesADirectoryThatIsntTheProjects919(t *testing.T) {
	for _, c := range []struct {
		name string
		make func(w *world, dir string)
		want string
	}{
		{"a directory with a file in it", func(w *world, dir string) { w.write(filepath.Join(dir, "notes.txt"), "notes\n") }, "it holds notes.txt"},
		{"a file", func(w *world, dir string) { w.write(dir, "a file\n") }, "isn't a directory"},
		{"a checkout of another repository", func(w *world, dir string) { w.checkout(dir, "https://github.com/acme/gadgets.git") }, "origin isn't acme/widgets"},
		{"a checkout with no origin", func(w *world, dir string) {
			w.checkout(dir, "https://github.com/acme/widgets.git")
			w.mustGit(dir, "remote", "remove", "origin")
		}, "origin isn't acme/widgets"},
		{"a checkout whose origin carries a token", func(w *world, dir string) {
			w.checkout(dir, "https://someone:not-a-real-token@github.com/acme/widgets.git")
		}, "origin isn't acme/widgets"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			c.make(w, dir)
			before := w.tree(w.root)
			out, err := w.found(Options{})
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), dir) {
				t.Fatalf("new's answer to %s is %v, want a refusal naming %s and saying %q", c.name, err, dir, c.want)
			}
			if strings.Contains(err.Error()+out, "not-a-real-token") {
				t.Errorf("new repeated the token in the origin's address: %v\n%s", err, out)
			}
			if w.exists || w.ran("gh ") || !reflect.DeepEqual(w.tree(w.root), before) {
				t.Errorf("new created or asked GitHub something before it refused:\n%s", strings.Join(w.calls, "\n"))
			}
		})
	}
}

// A repository that already holds commits isn't a new project. With no
// checkout of it to adopt, new refuses, creates nothing and points at init.
func TestNewRefusesARepositoryThatAlreadyHoldsCommits919(t *testing.T) {
	w := newWorld(t)
	w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "public"
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "already holds commits") || !strings.Contains(err.Error(), "clone it") || !strings.Contains(err.Error(), "invariant init") {
		t.Fatalf("new's answer to a repository with commits is %v, want a refusal that points at a clone and at init\n%s", err, out)
	}
	if w.ran("gh repo clone") || w.ran("gh repo create") || w.ran("gh api") {
		t.Errorf("new went on after refusing:\n%s", strings.Join(w.calls, "\n"))
	}
	if _, err := os.Lstat(filepath.Join(w.root, "widgets")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("new made the directory of a project it refused: %v", err)
	}
}

// A checkout with commits on another branch isn't committed to: new commits
// to main and pushes main, so it stops before it creates anything.
func TestNewRefusesACheckoutWithCommitsOnAnotherBranch919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "LICENSE"), "A license.\n")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/trunk")
	w.mustGit(dir, "add", "LICENSE")
	w.mustGit(dir, "commit", "--quiet", "-m", "A license")
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "isn't on main") {
		t.Fatalf("new's answer to a checkout on another branch is %v\n%s", err, out)
	}
	if w.exists || w.ran("git add") {
		t.Errorf("new created something before it refused:\n%s", strings.Join(w.calls, "\n"))
	}
	// On main, the same checkout is adopted, and the founding's commit
	// goes on top of what was there.
	w.mustGit(dir, "branch", "-m", "main")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("new refused a checkout with a commit on main: %v\n%s", err, out)
	}
	if n := w.mustGit(dir, "rev-list", "--count", "HEAD"); n != "2" || len(w.pushed) != 1 {
		t.Errorf("the checkout has %s commits and %d pushes, want the founding's on top of the first", n, len(w.pushed))
	}
	if w.ran("git symbolic-ref HEAD refs/heads/main") {
		t.Errorf("new moved the branch of a checkout that has commits:\n%s", strings.Join(w.calls, "\n"))
	}
}

// Run a second time, new changes nothing and says the project is already
// founded: it only reads, whatever its flags say now, and however far the
// project has moved on since.
func TestASecondRunChangesNothingAndSaysTheProjectIsFounded919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	reads := []string{
		"git remote get-url origin",
		"git ls-tree --name-only HEAD -- .github/workflows/gate.yml .invariant/repository.json",
		"gh repo view acme/widgets --json isEmpty,mergeCommitAllowed,visibility",
		"git ls-tree --name-only refs/remotes/origin/main -- .github/workflows/gate.yml .invariant/repository.json",
		"gh api repos/acme/widgets/branches/main/protection",
	}
	other := filepath.Join(w.root, "another-spec.md")
	w.write(other, "# Another spec\n")
	again := func(o Options) {
		t.Helper()
		tree, head, pushed := w.tree(dir), w.mustGit(dir, "rev-parse", "HEAD"), len(w.pushed)
		w.calls, w.body = nil, ""
		out, err := w.found(o)
		if err != nil {
			t.Fatalf("the second run failed: %v\n%s", err, out)
		}
		if !slices.Equal(w.calls, reads) {
			t.Errorf("the second run ran\n  %s\nwant only these reads\n  %s", strings.Join(w.calls, "\n  "), strings.Join(reads, "\n  "))
		}
		if !strings.Contains(out, "acme/widgets is already founded: nothing changed.") || strings.Contains(out, "is founded.") {
			t.Errorf("the second run didn't say the project is already founded:\n%s", out)
		}
		for _, did := range []string{"Created", "Cloned", "Wrote", "Committed", "Pushed", "Allowed", "Required"} {
			if strings.Contains(out, did) {
				t.Errorf("the second run says it %s something:\n%s", strings.ToLower(did), out)
			}
		}
		if !strings.Contains(out, "Left for a person:") {
			t.Errorf("the second run doesn't end with what's left for a person:\n%s", out)
		}
		if !reflect.DeepEqual(w.tree(dir), tree) || w.mustGit(dir, "rev-parse", "HEAD") != head || len(w.pushed) != pushed || w.body != "" {
			t.Error("the second run changed the checkout, pushed or set the branch's protection")
		}
		if status := w.mustGit(dir, "status", "--porcelain"); status != "" {
			t.Errorf("the second run left changes in the checkout:\n%s", status)
		}
	}
	again(Options{})
	// Other flags don't make it a different project.
	again(Options{Public: true, Spec: other, Invariant: strings.Repeat("a", 40)})
	// Nor does work since: a README that's no longer the starter's.
	w.write(filepath.Join(dir, "README.md"), "# widgets\n\nIt does things now.\n")
	w.mustGit(dir, "commit", "--quiet", "-m", "Say what widgets does", "--", "README.md")
	again(Options{})
}

// A checkout with no git identity can't commit, so new stops before it
// writes or commits anything and says how to set one. Run again once one
// is set, it carries on in the repository and checkout it made.
func TestNewStopsBeforeItsFirstCommitWithoutAGitIdentity919(t *testing.T) {
	w := newWorld(t)
	w.identity = false
	dir := filepath.Join(w.root, "widgets")
	out, err := w.found(Options{})
	if err == nil {
		t.Fatalf("new went ahead with no git identity:\n%s", out)
	}
	for _, want := range []string{
		"has no git identity", "user.name and user.email aren't set",
		"git -C " + dir + " config user.name ", "git -C " + dir + " config user.email ", "git config --global",
		"run the same command again",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("new's message lacks %q: %v", want, err)
		}
	}
	if w.ran("git add") || w.ran("git commit") || w.ran("git push") || w.ran("gh api") {
		t.Errorf("new went on past the missing identity:\n%s", strings.Join(w.calls, "\n"))
	}
	if files := w.tree(dir); len(files) != 0 {
		t.Errorf("new wrote %q before it stopped for an identity", slices.Sorted(maps(files)))
	}
	if _, err := w.git(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		t.Error("new committed with no git identity")
	}

	// Only the email is missing: the message says so.
	w.mustGit(dir, "config", "user.name", "A Person")
	if _, err := w.found(Options{}); err == nil || !strings.Contains(err.Error(), "user.email isn't set") || strings.Contains(err.Error(), "user.name and") {
		t.Errorf("with a name and no email, new says: %v", err)
	}

	w.identity = true
	w.calls = nil
	out, err = w.found(Options{})
	if err != nil {
		t.Fatalf("new didn't carry on once git had an identity: %v\n%s", err, out)
	}
	if w.ran("gh repo create") || w.ran("gh repo clone") {
		t.Errorf("new made the repository or the checkout a second time:\n%s", strings.Join(w.calls, "\n"))
	}
	if len(w.pushed) != 1 || !w.required || !strings.Contains(out, "acme/widgets is founded.") {
		t.Errorf("new didn't finish the founding:\n%s", out)
	}
	w.contract(dir)
}

// Only Go has a starter. Any other language is refused in a sentence that
// says so, before anything is said or done.
func TestNewRefusesALanguageWithoutAStarter919(t *testing.T) {
	for _, language := range []string{"rust", "typescript", "python", "Go", ""} {
		w := newWorld(t)
		out, err := w.foundIn(language)
		if err == nil {
			t.Fatalf("new started a %q project:\n%s", language, out)
		}
		if !strings.Contains(err.Error(), "a starter for Go only") || !strings.Contains(err.Error(), "can't start a "+language+" project") {
			t.Errorf("-language %s: new's refusal doesn't say Go is the only starter: %v", language, err)
		}
		if len(w.calls) != 0 || out != "" || len(w.tree(w.root)) != 0 {
			t.Errorf("-language %s: new did something before it refused: %q, said %q", language, w.calls, out)
		}
	}
}

// foundIn runs a founding in a language, as it's given.
func (w *world) foundIn(language string) (string, error) {
	err := Project(context.Background(), Options{Repo: w.repo, Dir: filepath.Join(w.root, "widgets"), Language: language, Invariant: pin, WorkDir: w.root, Runner: w, Out: &w.said})
	return w.said.String(), err
}

// A repository's name is OWNER/NAME, and the gate's pin a full commit.
// Anything else is refused before a command runs.
func TestNewRefusesAMalformedRepositoryOrPin919(t *testing.T) {
	for _, repo := range []string{"widgets", "acme/", "/widgets", "acme/widgets/extra", "acme/wid gets", "-acme/widgets", "acme/-widgets", "acme/widgets.git", "acme/..", "acme/widgets;rm", "https://github.com/acme/widgets"} {
		w := newWorld(t)
		out, err := w.found(Options{Repo: repo})
		if err == nil || !strings.Contains(err.Error(), "isn't a repository as OWNER/NAME") {
			t.Errorf("new took %q for a repository: %v", repo, err)
		}
		if len(w.calls) != 0 || out != "" {
			t.Errorf("new did something for the repository %q: %q, said %q", repo, w.calls, out)
		}
	}
	for _, ref := range []string{"main", "61eb049", pin + "0"} {
		w := newWorld(t)
		out, err := w.found(Options{Invariant: ref})
		if err == nil || !strings.Contains(err.Error(), "full commit hash") {
			t.Errorf("new pinned the gate to %q: %v", ref, err)
		}
		if len(w.calls) != 0 || out != "" {
			t.Errorf("new did something with the pin %q: %q, said %q", ref, w.calls, out)
		}
	}
	// A name with a dot, a dash and capitals is one.
	w := newWorld(t)
	w.repo = "Acme-Co/Widgets.v2"
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("new refused %s: %v\n%s", w.repo, err, out)
	}
	files := w.tree(filepath.Join(w.root, "widgets"))
	if !strings.Contains(files["go.mod"], "module github.com/Acme-Co/Widgets.v2\n") || !strings.Contains(files["doc.go"], "\npackage widgetsv2\n") {
		t.Errorf("the starter for %s has go.mod %q and doc.go %q", w.repo, files["go.mod"], files["doc.go"])
	}
}

// The package a starter declares is a name Go accepts, whatever the
// repository is called.
func TestAStarterPackageNameIsOneGoAccepts919(t *testing.T) {
	for name, want := range map[string]string{
		"widgets": "widgets", "On-Air": "onair", "ask3d": "ask3d", "log.buffer_v2": "logbufferv2",
		"3d-viewer": "pkg3dviewer", "go": "pkggo", "main": "pkgmain", "init": "pkginit", "range": "pkgrange", "---": "pkg",
	} {
		if got := packageName(name); got != want {
			t.Errorf("the package for %q is %q, want %q", name, got, want)
		}
	}
}

// The repository is private unless -public asks otherwise.
func TestNewCreatesAPrivateRepositoryUnlessPublicIsAsked919(t *testing.T) {
	for _, public := range []bool{false, true} {
		w := newWorld(t)
		out, err := w.found(Options{Public: public})
		if err != nil {
			t.Fatalf("-public %t: %v\n%s", public, err, out)
		}
		with, without := "--private", "--public"
		if public {
			with, without = without, with
		}
		for _, call := range w.calls {
			if strings.Contains(call, without) {
				t.Errorf("-public %t: new ran %q", public, call)
			}
		}
		if !w.ran("gh repo create acme/widgets "+with) || w.visibility != strings.TrimPrefix(with, "--") {
			t.Errorf("-public %t: the repository is %s, created by:\n%s", public, w.visibility, strings.Join(w.calls, "\n"))
		}
		if !strings.Contains(out, "Created acme/widgets on GitHub, "+strings.TrimPrefix(with, "--")+".") {
			t.Errorf("-public %t: new didn't say which it created:\n%s", public, out)
		}
		// What's left says what a private repository needs, and only then.
		for _, private := range []string{"gh auth setup-git", "add a private one only when that's meant"} {
			if strings.Contains(out, private) == public {
				t.Errorf("-public %t: what's left for a person is wrong about %q:\n%s", public, private, out)
			}
		}
	}
}

// -public applies to a repository new creates. One it adopts keeps its
// visibility, and new says so.
func TestNewLeavesAnAdoptedRepositorysVisibilityAsItIs919(t *testing.T) {
	w := newWorld(t)
	w.exists, w.empty, w.mergeCommits, w.visibility = true, true, true, "private"
	out, err := w.found(Options{Public: true})
	if err != nil {
		t.Fatal(err)
	}
	if w.visibility != "private" || !strings.Contains(out, "It stays private: -public applies only to a repository this command creates.") {
		t.Errorf("the adopted repository is %s, and new said:\n%s", w.visibility, out)
	}
	if !strings.Contains(out, "gh auth setup-git") {
		t.Errorf("what's left doesn't treat the repository as the private one it is:\n%s", out)
	}
	// Without -public there's nothing to say.
	w = newWorld(t)
	w.exists, w.empty, w.mergeCommits, w.visibility = true, true, true, "private"
	if out, err := w.found(Options{}); err != nil || strings.Contains(out, "It stays") {
		t.Errorf("new remarked on a visibility nobody asked to change: %v\n%s", err, out)
	}
}

// -spec names the project's spec. It lands at docs/PRD.md byte for byte,
// is committed, and is a current-state document, and the starter's
// documents and the first issue point at it. Without one, nothing does.
func TestTheSpecLandsAtDocsPRDAndIsCurrentState919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	spec := "# Widgets\r\n\r\nMilestone 1: a caf\u00e9 of widgets.\n\n- No trailing newline"
	w.write(filepath.Join(w.root, "notes", "spec.md"), spec)
	// A relative path starts from the current directory.
	out, err := w.found(Options{Spec: filepath.Join("notes", "spec.md")})
	if err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	files := w.tree(dir)
	if files["docs/PRD.md"] != spec {
		t.Errorf("docs/PRD.md = %q, want the spec byte for byte", files["docs/PRD.md"])
	}
	if committed := w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD"); !strings.Contains(committed, "docs/PRD.md") {
		t.Errorf("the spec isn't committed:\n%s", committed)
	}
	if c := w.contract(dir); !slices.Equal(c.CurrentState, []string{"SPEC.md", "README.md", "docs/PRD.md"}) {
		t.Errorf("current_state = %q, want the starter's documents and the spec", c.CurrentState)
	}
	for _, name := range []string{"README.md", "SPEC.md", "AGENTS.md"} {
		if !strings.Contains(files[name], "docs/PRD.md") {
			t.Errorf("%s doesn't point at the spec:\n%s", name, files[name])
		}
	}
	if !strings.Contains(out, "docs/PRD.md to build first") || !strings.Contains(out, "/invariant plan") || strings.Contains(out, "/invariant solve") {
		t.Errorf("the first issue isn't one from the spec:\n%s", out)
	}

	// Without a spec there's no docs/PRD.md, and nothing names one.
	w = newWorld(t)
	dir = filepath.Join(w.root, "widgets")
	out, err = w.found(Options{})
	if err != nil {
		t.Fatal(err)
	}
	files = w.tree(dir)
	if _, ok := files["docs/PRD.md"]; ok {
		t.Error("new wrote a spec nobody gave it")
	}
	for name, text := range files {
		if strings.Contains(text, "PRD.md") {
			t.Errorf("%s names a spec the project doesn't have:\n%s", name, text)
		}
	}
	if strings.Contains(out, "PRD.md") || strings.Contains(out, "/invariant plan") {
		t.Errorf("what's left names a spec the project doesn't have:\n%s", out)
	}

	// A spec that can't be read stops new before it says or does anything.
	w = newWorld(t)
	out, err = w.found(Options{Spec: filepath.Join(w.root, "missing.md")})
	if err == nil || !strings.Contains(err.Error(), "reading the spec") || len(w.calls) != 0 || out != "" {
		t.Errorf("with a spec that isn't there, new said %q, ran %q and returned %v", out, w.calls, err)
	}
}

// A spec the checkout already holds is the project's, with or without
// -spec: it's kept, committed and a current-state document.
func TestNewKeepsASpecTheCheckoutAlreadyHolds919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "docs", "PRD.md"), "# The spec\n")
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("new refused a checkout that holds its spec: %v\n%s", err, out)
	}
	if c := w.contract(dir); !slices.Equal(c.CurrentState, []string{"SPEC.md", "README.md", "docs/PRD.md"}) {
		t.Errorf("current_state = %q, want the spec too", c.CurrentState)
	}
	if files := w.tree(dir); !strings.Contains(files["README.md"], "docs/PRD.md") || files["docs/PRD.md"] != "# The spec\n" {
		t.Errorf("the README or the spec isn't the project's own: %q", files["README.md"])
	}
}

// Where the account's plan won't protect a private repository's branches,
// GitHub refuses the requirement with a 403. new says so, says what it
// means and how to change it, and carries on to what's left for a person. A
// second run finds the same and still changes nothing.
func TestNewSaysWhatARefusedGateRequirementMeansAndCarriesOn919(t *testing.T) {
	for _, plan := range []string{"Pro", "Team"} {
		w := newWorld(t)
		w.refusal = errors.New("gh: Upgrade to GitHub " + plan + " or make this repository public to enable this feature. (HTTP 403)")
		out, err := w.found(Options{})
		if err != nil {
			t.Fatalf("%s: new failed where GitHub refused the requirement: %v\n%s", plan, err, out)
		}
		for _, want := range []string{
			"GitHub won't require invariant/gate on main",
			"The factory still merges only once the gate passes.",
			"A merge made by hand can skip it.",
			"make the repository public, at https://github.com/acme/widgets/settings, or change the account's plan",
			"run invariant new acme/widgets again",
			"acme/widgets is founded.",
			"Left for a person:",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: new didn't say %q:\n%s", plan, want, out)
			}
		}
		if strings.Contains(out, "Required invariant/gate") || w.required {
			t.Errorf("%s: new says it required a gate GitHub refused:\n%s", plan, out)
		}
		if strings.Index(out, "GitHub won't require") > strings.Index(out, "Left for a person:") {
			t.Errorf("%s: new didn't carry on to what's left after the refusal:\n%s", plan, out)
		}
		if w.body != setup.Protection || len(w.pushed) != 1 {
			t.Errorf("%s: new didn't attempt the requirement init prints, after pushing: %q", plan, w.body)
		}

		w.calls = nil
		out, err = w.found(Options{})
		if err != nil || !strings.Contains(out, "acme/widgets is already founded: nothing changed.") || !strings.Contains(out, "GitHub won't require invariant/gate on main") {
			t.Errorf("%s: the second run, still refused: %v\n%s", plan, err, out)
		}
		if len(w.pushed) != 1 || w.ran("git commit") || w.ran("gh repo create") {
			t.Errorf("%s: the second run changed something:\n%s", plan, strings.Join(w.calls, "\n"))
		}

		// Once the plan allows it, the next run requires the gate.
		w.refusal = nil
		out, err = w.found(Options{})
		if err != nil || !w.required || !strings.Contains(out, "Required invariant/gate on main") || strings.Contains(out, "already founded") {
			t.Errorf("%s: with the refusal gone, new: %v\n%s", plan, err, out)
		}
	}
}

// Any other failure to require the gate is an error: a 403 that isn't the
// plan's, the plan's words under another status, or a refusal of the
// request itself.
func TestAnyOtherFailureToRequireTheGateStopsNew919(t *testing.T) {
	for _, refusal := range []string{
		"gh: Resource not accessible by personal access token (HTTP 403)",
		"gh: Upgrade to GitHub Pro or make this repository public to enable this feature. (HTTP 500)",
		"gh: Validation Failed (HTTP 422)",
		"gh: Branch not found (HTTP 404)",
		"Post \"https://api.github.com/\": dial tcp: lookup api.github.com: no such host",
	} {
		w := newWorld(t)
		w.refusal = errors.New(refusal)
		out, err := w.found(Options{})
		if err == nil {
			t.Fatalf("new carried on past %q:\n%s", refusal, out)
		}
		if !strings.Contains(err.Error(), "requiring invariant/gate on main") || !strings.Contains(err.Error(), refusal) {
			t.Errorf("new's error for %q doesn't say what failed and why: %v", refusal, err)
		}
		if strings.Contains(out, "is founded") || strings.Contains(out, "Left for a person") || strings.Contains(out, "GitHub won't require") {
			t.Errorf("new reported %q as a founding:\n%s", refusal, out)
		}
	}
}

// A push that fails stops new with git's own words. The next run pushes the
// commit the first one made, and makes no second one.
func TestNewCarriesOnFromAPushThatFailed919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.pushFails = errors.New("fatal: unable to access 'https://github.com/acme/widgets.git/': Could not resolve host: github.com")
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "git push -u origin main") || !strings.Contains(err.Error(), "Could not resolve host") {
		t.Fatalf("new's answer to a failed push is %v\n%s", err, out)
	}
	if w.ran("gh api") || strings.Contains(out, "is founded") {
		t.Errorf("new went on past the failed push:\n%s", out)
	}
	head := w.mustGit(dir, "rev-parse", "HEAD")
	w.calls = nil
	out, err = w.found(Options{})
	if err != nil {
		t.Fatalf("the next run failed: %v\n%s", err, out)
	}
	if w.ran("git commit") || w.ran("gh repo create") || w.ran("gh repo clone") || !slices.Equal(w.pushed, []string{head}) {
		t.Errorf("the next run pushed %q, want the first run's commit %s, with:\n%s", w.pushed, head, strings.Join(w.calls, "\n"))
	}
	if !strings.Contains(out, "Pushed main.") || !strings.Contains(out, "acme/widgets is founded.") {
		t.Errorf("the next run didn't say it finished the founding:\n%s", out)
	}
}

// The same holds in a repository new adopted with commits in it, where a
// push can't be told from the repository's being empty: the checkout's own
// record of the remote says the founding's commit hasn't landed.
func TestNewPushesAFoundingThatNeverReachedARepositoryWithCommits919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "private"
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	w.write(filepath.Join(dir, "LICENSE"), "A license.\n")
	w.mustGit(dir, "add", "LICENSE")
	w.mustGit(dir, "commit", "--quiet", "-m", "A license")
	w.mustGit(dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	w.pushFails = errors.New("fatal: unable to access 'https://github.com/acme/widgets.git/': Could not resolve host: github.com")
	if out, err := w.found(Options{}); err == nil || !strings.Contains(err.Error(), "Could not resolve host") {
		t.Fatalf("new's answer to a failed push is %v\n%s", err, out)
	}
	head := w.mustGit(dir, "rev-parse", "HEAD")
	w.calls = nil
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("the next run failed: %v\n%s", err, out)
	}
	if w.ran("git commit") || !slices.Equal(w.pushed, []string{head}) || !strings.Contains(out, "acme/widgets is founded.") {
		t.Errorf("the next run pushed %q, want the founding's commit %s:\n%s", w.pushed, head, out)
	}
	// And once it has landed, the run after that pushes nothing.
	w.calls = nil
	if out, err := w.found(Options{}); err != nil || w.ran("git push") || !strings.Contains(out, "already founded: nothing changed") {
		t.Errorf("the run after the push landed: %v\n%s\n%s", err, out, strings.Join(w.calls, "\n"))
	}
}

// new commits its own files and nothing else: what a person had staged in
// the checkout it adopts stays staged.
func TestNewCommitsOnlyItsOwnFiles919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "notes.txt"), "not the founding's\n")
	w.mustGit(dir, "add", "notes.txt")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	if committed := w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(committed, "notes.txt") {
		t.Errorf("new committed a file that isn't its own:\n%s", committed)
	}
	if status := w.mustGit(dir, "status", "--porcelain"); status != "A  notes.txt" {
		t.Errorf("the person's staged file is now %q, want it still staged", status)
	}
}

// The checks new commits are the ones the starter passes: run as they're
// committed, format, vet, build and test all succeed, and the format check
// fails once a Go file isn't formatted, where gofmt -l alone wouldn't.
func TestTheStarterPassesTheChecksItCommits919(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("the starter's checks need go: %v", err)
	}
	goroot, err := exec.Command(goTool, "env", "GOROOT").Output()
	if err != nil {
		t.Skipf("the starter's checks need go's own tools: %v", err)
	}
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	part := w.contract(dir).Parts[0]
	check := func(name string) (string, error) {
		t.Helper()
		argv := part.Commands[name].Argv
		if len(argv) == 0 {
			t.Fatalf("the %s check has no command", name)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		// gofmt sits beside go, and nothing here needs the network.
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(strings.TrimSpace(string(goroot)), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOFLAGS=")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, name := range []string{"format", "lint", "build", "test"} {
		if out, err := check(name); err != nil {
			t.Errorf("the starter fails its own %s check: %v\n%s", name, err, out)
		}
	}
	w.write(filepath.Join(dir, "unformatted.go"), "package widgets\n\nfunc  f( ) { }\n")
	out, err := check("format")
	if err == nil || !strings.Contains(out, "unformatted.go") {
		t.Errorf("the format check passed a file gofmt would change: %v\n%s", err, out)
	}
	// A file gofmt can't read fails it too, rather than passing unseen.
	w.write(filepath.Join(dir, "unformatted.go"), "package widgets\n\nfunc {\n")
	if out, err := check("format"); err == nil {
		t.Errorf("the format check passed a file gofmt can't parse:\n%s", out)
	}
}

// new completes only what it knows how to. A checkout whose draft leaves
// anything else unresolved gets no checks file and no commit: the message
// lists what's left and points at init.
func TestNewStopsOnChecksItCantComplete919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "web", "package.json"), "{\"name\": \"web\", \"private\": true}\n")
	out, err := w.found(Options{})
	if err == nil {
		t.Fatalf("new completed checks for a part it has no starter for:\n%s", out)
	}
	for _, want := range []string{"can't settle", "web (typescript)", "invariant init", repository.Path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("new's message lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), ". (go) format") {
		t.Errorf("new lists the format check it does complete: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(repository.Path))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("new wrote a checks file it couldn't complete: %v", err)
	}
	if w.ran("git add") || w.ran("git commit") || w.ran("git push") {
		t.Errorf("new committed checks it couldn't complete:\n%s", strings.Join(w.calls, "\n"))
	}
}

// A checkout whose commit holds a gate and a checks file the loader
// refuses, as a draft committed with its unresolved entries, isn't founded,
// and new doesn't say it is: it names the file, pushes nothing and sets
// nothing.
func TestNewDoesntCallAProjectFoundedWhoseChecksDontLoad919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	path := filepath.Join(dir, filepath.FromSlash(repository.Path))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	w.write(path, strings.Replace(string(b), "{\n", "{\n  \"unresolved\": [\". (go) format: declare a supported check command\"],\n", 1))
	w.mustGit(dir, "commit", "--quiet", "-m", "An unfinished draft", "--", repository.Path)
	w.calls, w.empty, w.required = nil, true, false
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "don't load") || !strings.Contains(err.Error(), repository.Path) || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("new's answer to committed checks that don't load is %v\n%s", err, out)
	}
	if strings.Contains(out, "founded") || w.ran("git push") || w.ran("gh api") || w.required {
		t.Errorf("new treated the project as founded:\n%s\n%s", out, strings.Join(w.calls, "\n"))
	}
}

// Everything a founding runs names the repository or runs in the checkout,
// and every file it writes is inside the checkout, even past a link that
// leads out of it.
func TestNewCreatesNothingOutsideItsDirectoryAndRepository919(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	spec := filepath.Join(w.root, "spec.md")
	w.write(spec, "# The spec\n")
	w.before = func(c Command) {
		switch {
		case c.Name == "git" && c.Dir == dir:
		case c.Name == "gh" && c.Dir == "" && (slices.Contains(c.Args, w.repo) || slices.ContainsFunc(c.Args, func(arg string) bool {
			return arg == "repos/"+w.repo || strings.HasPrefix(arg, "repos/"+w.repo+"/")
		})):
		default:
			t.Errorf("new ran %q in %q, which is neither in its checkout nor about its repository", c, c.Dir)
		}
		for _, arg := range c.Args {
			if strings.Contains(arg, "auth") || strings.Contains(arg, "token") || strings.Contains(arg, "credential") {
				t.Errorf("new ran %q, which reads a credential", c)
			}
		}
	}
	if out, err := w.found(Options{Spec: spec}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	if got := slices.Sorted(maps(w.tree(w.root))); !slices.Equal(got, slices.Sorted(slices.Values(append(prefixed("widgets/", strings.Fields(founded)), "widgets/docs/PRD.md", "spec.md")))) {
		t.Errorf("after the founding the world holds %q", got)
	}

	// A docs directory that's a link out of the checkout is refused, and
	// nothing lands where it leads.
	w = newWorld(t)
	dir = filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	outside := filepath.Join(w.root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "docs")); err != nil {
		t.Fatal(err)
	}
	spec = filepath.Join(w.root, "spec.md")
	w.write(spec, "# The spec\n")
	out, err := w.found(Options{Spec: spec})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "docs")+" is in the way") {
		t.Fatalf("new's answer to a link where docs/ goes is %v\n%s", err, out)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Errorf("new wrote through the link: %v, %v", entries, err)
	}
	if w.exists {
		t.Error("new created the repository before it refused the link")
	}
}

// prefixed is names, each with prefix before it.
func prefixed(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = prefix + name
	}
	return out
}

// GitHub failing to say whether the repository exists isn't the repository
// not existing: new creates nothing on a guess.
func TestNewCreatesNothingWhenItCantTellWhetherTheRepositoryExists919(t *testing.T) {
	w := newWorld(t)
	w.before = func(c Command) {
		if c.Name == "gh" && c.Args[1] == "create" {
			t.Errorf("new created a repository it couldn't see: %q", c)
		}
	}
	failing := &failingView{world: w, err: errors.New("error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com")}
	var said strings.Builder
	err := Project(context.Background(), Options{Repo: w.repo, Dir: filepath.Join(w.root, "widgets"), Language: "go", Invariant: pin, Runner: failing, Out: &said})
	if err == nil || !strings.Contains(err.Error(), "can't tell whether acme/widgets is on GitHub") || !strings.Contains(err.Error(), "check your internet connection") {
		t.Fatalf("new's answer to a GitHub it can't reach is %v", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("new went on without knowing whether the repository exists: %q", w.calls)
	}
}

// failingView is a world whose read of the repository fails.
type failingView struct {
	*world
	err error
}

func (f *failingView) Run(ctx context.Context, c Command) ([]byte, error) {
	if c.Name == "gh" && len(c.Args) > 1 && c.Args[0] == "repo" && c.Args[1] == "view" {
		return nil, f.err
	}
	return f.world.Run(ctx, c)
}

// Exec, the runner new really uses, returns what a command printed, and
// what a failed one said of its failure. It hands a command what it reads.
func TestExecReturnsWhatACommandPrintedAndWhyItFailed919(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	out, err := Exec{}.Run(ctx, Command{Name: "sh", Args: []string{"-c", "cat; pwd"}, Stdin: "read\n", Dir: dir})
	if err != nil || !strings.HasPrefix(string(out), "read\n") || !strings.Contains(string(out), filepath.Base(dir)) {
		t.Errorf("a command that reads and prints in %s returned %q, %v", dir, out, err)
	}
	_, err = Exec{}.Run(ctx, Command{Name: "sh", Args: []string{"-c", "echo '{\"message\":\"body\"}'; echo 'gh: Not Found (HTTP 404)' >&2; exit 1"}})
	if err == nil || err.Error() != "gh: Not Found (HTTP 404)" {
		t.Errorf("a command that fails returned %v, want what it said on stderr", err)
	}
	_, err = Exec{}.Run(ctx, Command{Name: "sh", Args: []string{"-c", "echo only on stdout; exit 3"}})
	if err == nil || err.Error() != "only on stdout" {
		t.Errorf("a command that fails with nothing on stderr returned %v", err)
	}
	if _, err := (Exec{}).Run(ctx, Command{Name: "sh", Args: []string{"-c", "exit 4"}}); err == nil || !strings.Contains(err.Error(), "exit status 4") {
		t.Errorf("a command that fails silently returned %v", err)
	}
	if got := (Command{Name: "gh", Args: []string{"repo", "view", "acme/widgets"}}).String(); got != "gh repo view acme/widgets" {
		t.Errorf("the command reads %q", got)
	}
}
