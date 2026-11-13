package found

import (
	"context"
	"errors"
	"maps"
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

// untouched fails the test unless a refusal left everything as it was: the
// files under the world's root, and GitHub with no repository made, nothing
// pushed and no setting changed.
func (w *world) untouched(before map[string]string, existed bool) {
	w.t.Helper()
	if after := w.tree(w.root); !reflect.DeepEqual(after, before) {
		w.t.Errorf("new changed files before it refused: %q, were %q", slices.Sorted(maps.Keys(after)), slices.Sorted(maps.Keys(before)))
	}
	if w.exists != existed || len(w.pushed) != 0 || w.body != "" {
		w.t.Errorf("new changed GitHub before it refused: a repository %t, pushes %d, protection %q", w.exists, len(w.pushed), w.body)
	}
	for _, did := range []string{"gh repo create", "gh repo clone", "gh api -X", "git symbolic-ref HEAD refs/heads/main", "git add", "git commit", "git push"} {
		if w.ran(did) {
			w.t.Errorf("new ran %q before it refused:\n%s", did, strings.Join(w.calls, "\n"))
		}
	}
}

// From nothing, new creates the repository, private, and its checkout,
// writes the Go starter, the gate and complete checks, commits them, pushes
// main, allows merge commits and requires the gate: these commands, in this
// order, and no others.
func TestNewFoundsAProjectFromNothing919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	want := []string{
		"gh api users/acme --jq .type",
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
	if got, want := slices.Sorted(maps.Keys(files)), strings.Fields(founded); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("the checkout holds %q, want %q", got, want)
	}
	for name, wants := range map[string][]string{
		"go.mod":                     {"module github.com/acme/widgets\n", "\ngo 1.27\n"},
		"doc.go":                     {"// Package widgets ", "\npackage widgets\n"},
		"tools/check-format":         {"#!/bin/sh\n", "$(gofmt -l .)", "exit 1"},
		".gitignore":                 {".DS_Store\n", "\n.env\n"},
		"README.md":                  {"# widgets\n", "SPEC.md", "AGENTS.md"},
		"SPEC.md":                    {"Nothing is built yet", "github.com/acme/widgets"},
		"AGENTS.md":                  {"SPEC.md", "go vet ./...", "tools/check-format", "Dependencies are a person's to add"},
		".github/workflows/gate.yml": {"ref: " + pin, "name: invariant/gate"},
	} {
		for _, want := range wants {
			if !strings.Contains(files[name], want) {
				t.Errorf("%s lacks %q:\n%s", name, want, files[name])
			}
		}
	}
	// The starter's rules are ones any project can keep: whose name goes
	// on a commit is each owner's to say.
	for _, own := range []string{"Co-Authored-By", "attribution"} {
		if strings.Contains(files["AGENTS.md"], own) {
			t.Errorf("the starter's AGENTS.md has a rule about %s:\n%s", own, files["AGENTS.md"])
		}
	}
	// Local settings stay out of the repository.
	w.write(filepath.Join(dir, ".env"), "SETTING=local\n")
	if status := w.mustGit(dir, "status", "--porcelain"); status != "" {
		t.Errorf("the checkout isn't clean after the founding, or doesn't ignore .env:\n%s", status)
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
	if committed := strings.Fields(w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD")); !slices.Equal(committed, slices.Sorted(slices.Values(strings.Fields(founded)))) {
		t.Errorf("the founding's commit holds %q, want the files it wrote and no others", committed)
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

// new says the repository and the directory before it runs a single
// command, so before it creates anything, with -dir or with its default.
func TestNewNamesTheRepositoryAndDirectoryBeforeItCreatesAnything919(t *testing.T) {
	t.Parallel()
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
		if commands == 0 || w.calls[0] != "gh api users/acme --jq .type" {
			t.Errorf("-dir given %t: new's first command was %q, want a read", named, w.calls)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
// it before it creates, clones or writes anything, the checks file
// included, and its message names the file and the command to run once
// it's out of the way.
func TestNewNeverOverwritesAFileAndNamesIt919(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		make  func(w *world, dir string)
		names string
		spec  bool
	}{
		{"a README of the person's own", func(w *world, dir string) { w.write(filepath.Join(dir, "README.md"), "# Mine\n") }, "README.md", false},
		{"a README of the person's own, committed", func(w *world, dir string) {
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
			w.write(filepath.Join(dir, "README.md"), "# Mine\n")
			w.commit(dir, "A README", "README.md")
		}, "README.md", false},
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
		{"a link that leads nowhere, where a file goes", func(w *world, dir string) {
			if err := os.Symlink(filepath.Join(w.root, "nowhere"), filepath.Join(dir, "go.mod")); err != nil {
				w.t.Fatal(err)
			}
		}, "go.mod", false},
		{"checks of the person's own", func(w *world, dir string) {
			w.write(filepath.Join(dir, ".invariant", "repository.json"), "{\"version\": 1}\n")
		}, filepath.Join(".invariant", "repository.json"), false},
		{"the draft init leaves in an empty checkout", func(w *world, dir string) {
			if _, err := setup.Write(dir, pin, false); err != nil {
				w.t.Fatal(err)
			}
			if _, unresolved, err := setup.WriteRepository(dir); err != nil || len(unresolved) == 0 {
				w.t.Fatalf("init's draft: %v, with %d unresolved", err, len(unresolved))
			}
		}, filepath.Join(".invariant", "repository.json"), false},
		{"a link where the checks' directory goes", func(w *world, dir string) {
			if err := os.Mkdir(filepath.Join(w.root, "outside"), 0o755); err != nil {
				w.t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(w.root, "outside"), filepath.Join(dir, ".invariant")); err != nil {
				w.t.Fatal(err)
			}
		}, ".invariant", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			w.checkout(dir, "https://github.com/acme/widgets.git")
			c.make(w, dir)
			o := Options{}
			if c.spec {
				o.Spec = filepath.Join(w.root, "spec.md")
				w.write(o.Spec, "# The spec\n")
			}
			before := w.tree(w.root)
			out, err := w.found(o)
			if err == nil {
				t.Fatalf("new went ahead over %s:\n%s", c.name, out)
			}
			if !strings.Contains(err.Error(), filepath.Join(dir, c.names)+" is ") {
				t.Errorf("new's refusal doesn't name %s: %v", filepath.Join(dir, c.names), err)
			}
			again := w.again(dir)
			if c.spec {
				again = w.again(dir, "-spec", o.Spec)
			}
			if !strings.HasSuffix(err.Error(), "then run it again:\n\n"+again) {
				t.Errorf("new's refusal doesn't end with the command to run again, %s: %v", again, err)
			}
			w.untouched(before, false)
		})
	}

	// With init's draft moved aside, as the refusal says, the same command
	// finishes, and keeps the gate init wrote.
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	if _, err := setup.Write(dir, pin, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := setup.WriteRepository(dir); err != nil {
		t.Fatal(err)
	}
	_, err := w.found(Options{})
	if err == nil {
		t.Fatal("new went ahead over init's draft")
	}
	if err := os.Rename(filepath.Join(dir, ".invariant", "repository.json"), filepath.Join(w.root, "aside.json")); err != nil {
		t.Fatal(err)
	}
	out, err := w.found(w.pasted(err.Error()))
	if err != nil || !strings.Contains(out, "Kept 1 of the founding's files") || !strings.Contains(out, "acme/widgets is founded.") {
		t.Fatalf("with the draft moved aside: %v\n%s", err, out)
	}
	w.contract(dir)
}

// new starts in an empty directory or in the repository's own checkout, and
// nowhere else: a directory with something in it, a file, or a checkout of
// another repository is refused before anything is created or asked of
// GitHub. It never repeats a remote's address, which can carry a token.
func TestNewRefusesADirectoryThatIsntTheProjects919(t *testing.T) {
	t.Parallel()
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
			w.untouched(before, false)
			if w.ran("gh ") {
				t.Errorf("new asked GitHub something before it refused:\n%s", strings.Join(w.calls, "\n"))
			}
		})
	}
}

// A repository that already holds commits isn't a new project. With no
// checkout of it, new refuses, creates nothing and says to clone it, or to
// run init. With a checkout that has none of its commits, a commit made
// there couldn't be pushed, so new refuses that too, before it writes a
// file, and says to pull them.
func TestNewRefusesARepositoryThatAlreadyHoldsCommits919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "public"
	before := w.tree(w.root)
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "already holds commits") || !strings.Contains(err.Error(), "clone it") || !strings.Contains(err.Error(), "invariant init") {
		t.Fatalf("new's answer to a repository with commits is %v, want a refusal that points at a clone and at init\n%s", err, out)
	}
	w.untouched(before, true)
	if _, err := os.Lstat(filepath.Join(w.root, "widgets")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("new made the directory of a project it refused: %v", err)
	}

	// Its checkout, made and never pulled.
	w = newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "public"
	w.checkout(dir, "https://github.com/acme/widgets.git")
	before = w.tree(w.root)
	out, err = w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "has none of them") || !strings.Contains(err.Error(), "git -C "+dir+" pull origin main") || !strings.HasSuffix(err.Error(), "\n\n"+w.again(dir)) {
		t.Fatalf("new's answer to a checkout with none of the repository's commits is %v\n%s", err, out)
	}
	w.untouched(before, true)
	if _, err := w.git(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		t.Error("new committed in a checkout that couldn't push")
	}
}

// new commits to main and pushes main, so a checkout on any other branch is
// refused before anything is created: one with commits, founded or not, one
// on no branch at all, and one whose branch has no commit yet while main
// has. On main, a checkout with a commit is adopted, and the founding's
// commit goes on top.
func TestNewRefusesACheckoutThatIsntOnMain919(t *testing.T) {
	t.Parallel()
	licensed := func(w *world, dir, branch string) {
		w.checkout(dir, "https://github.com/acme/widgets.git")
		w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/"+branch)
		w.write(filepath.Join(dir, "LICENSE"), "A license.\n")
		w.commit(dir, "A license", "LICENSE")
	}
	for _, c := range []struct {
		name string
		make func(w *world, dir string)
		on   string
	}{
		{"commits on another branch", func(w *world, dir string) { licensed(w, dir, "trunk") }, "trunk"},
		{"a founded project on another branch", func(w *world, dir string) {
			if out, err := w.found(Options{}); err != nil {
				w.t.Fatalf("%v\n%s", err, out)
			}
			w.mustGit(dir, "checkout", "--quiet", "-b", "setup")
			w.calls, w.pushed, w.body = nil, nil, ""
		}, "setup"},
		{"no branch", func(w *world, dir string) {
			licensed(w, dir, "main")
			w.mustGit(dir, "checkout", "--quiet", "--detach")
		}, "no branch"},
		{"a branch with no commit, where main has one", func(w *world, dir string) {
			licensed(w, dir, "main")
			w.mustGit(dir, "checkout", "--quiet", "--orphan", "scratch")
			w.mustGit(dir, "rm", "--quiet", "-r", "-f", "--cached", ".")
			if err := os.Remove(filepath.Join(dir, "LICENSE")); err != nil {
				w.t.Fatal(err)
			}
		}, "scratch"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			c.make(w, dir)
			before, existed, refs := w.tree(w.root), w.exists, w.mustGit(dir, "for-each-ref")
			out, err := w.found(Options{})
			if err == nil || !strings.Contains(err.Error(), dir+" is on "+c.on+", not on main") || !strings.HasSuffix(err.Error(), "Switch it to main, then run it again:\n\n"+w.again(dir)) {
				t.Fatalf("new's answer to a checkout on %s is %v\n%s", c.on, err, out)
			}
			w.untouched(before, existed)
			if founds(out) || w.mustGit(dir, "for-each-ref") != refs {
				t.Errorf("new founded a project from another branch, or moved one:\n%s", out)
			}
		})
	}

	// On main, the same checkout is adopted.
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	licensed(w, dir, "main")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("new refused a checkout with a commit on main: %v\n%s", err, out)
	}
	if n := w.mustGit(dir, "rev-list", "--count", "HEAD"); n != "2" || len(w.pushed) != 1 {
		t.Errorf("the checkout has %s commits and %d pushes, want the founding's on top of the first", n, len(w.pushed))
	}
	if w.ran("git symbolic-ref HEAD refs/heads/main") {
		t.Errorf("new moved the branch of a checkout that has commits:\n%s", strings.Join(w.calls, "\n"))
	}
	// And a branch with no commit yet, where main has none either, is
	// simply moved to main.
	w = newWorld(t)
	dir = filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/scratch")
	if out, err := w.found(Options{}); err != nil || w.mustGit(dir, "symbolic-ref", "--short", "HEAD") != "main" {
		t.Errorf("new didn't move a checkout with no commit to main: %v\n%s", err, out)
	}
}

// The founding's commit holds the files new wrote and no others. A checkout
// it adopts can hold more that its checks would name, an agent's rules, its
// settings, a lockfile, and new never adds one: where such a file isn't
// committed as it stands, new stops before it creates or writes anything
// and names each, however public the repository was to be.
func TestNewCommitsOnlyTheFilesItWrites919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	private := []string{".mcp.json", ".claude/settings.local.json", "CLAUDE.local.md", ".codex/auth.json", "sub/AGENTS.md"}
	for _, name := range private {
		w.write(filepath.Join(dir, filepath.FromSlash(name)), "a person's own, never meant for a commit\n")
	}
	w.write(filepath.Join(dir, ".env"), "SETTING=local\n")
	w.write(filepath.Join(dir, "notes.txt"), "plain notes\n")
	before := w.tree(w.root)
	out, err := w.found(Options{Public: true})
	if err == nil {
		t.Fatalf("new went ahead in a checkout with a person's agent files:\n%s\ncommitted: %s", out, w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD"))
	}
	for _, name := range private {
		if !strings.Contains(err.Error(), name+" (untracked)") {
			t.Errorf("new's refusal doesn't name %s: %v", name, err)
		}
	}
	for _, name := range []string{".env", "notes.txt"} {
		if strings.Contains(err.Error(), name) {
			t.Errorf("new's refusal names %s, which its checks wouldn't: %v", name, err)
		}
	}
	if !strings.Contains(err.Error(), "never adds them") || !strings.HasSuffix(err.Error(), "Commit or remove each, then run it again:\n\n"+w.again(dir, "-public")) {
		t.Errorf("new's refusal doesn't say what to do, and the command to run again: %v", err)
	}
	w.untouched(before, false)

	// With them gone, the same command founds the project, and its commit
	// is the founding's files alone.
	for _, name := range private {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	out, err = w.found(w.pasted(err.Error()))
	if err != nil {
		t.Fatalf("with the files removed: %v\n%s", err, out)
	}
	if committed := strings.Fields(w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD")); !slices.Equal(committed, slices.Sorted(slices.Values(strings.Fields(founded)))) {
		t.Errorf("the founding's commit holds %q, want the files it wrote and no others", committed)
	}
	if status := w.mustGit(dir, "status", "--porcelain"); status != "?? notes.txt" {
		t.Errorf("after the founding the checkout's status is %q, want the person's notes left untracked and .env ignored", status)
	}
	if after := w.tree(dir); after[".env"] != "SETTING=local\n" || after["notes.txt"] != "plain notes\n" {
		t.Errorf("the founding changed the person's other files: %q, %q", after[".env"], after["notes.txt"])
	}
	if w.visibility != "public" || len(w.pushed) != 1 {
		t.Errorf("the founding didn't finish: %s, %d pushes", w.visibility, len(w.pushed))
	}
}

// The same holds for a file git ignores, which a commit could never have
// taken, for an uncommitted change to a rule that is committed, and for a
// lockfile: each is named with what's the matter, before anything is
// created. What the checks don't name, as an edit to a license, is left out
// of the commit and out of the refusal.
func TestNewRefusesAFileItsChecksWouldNameThatIsntCommitted919(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		make  func(w *world, dir string)
		names []string
		fix   func(w *world, dir string)
	}{
		{"an agent's settings git ignores", func(w *world, dir string) {
			w.write(filepath.Join(dir, ".claude", "settings.local.json"), "{}\n")
			w.write(filepath.Join(dir, ".git", "info", "exclude"), ".claude/settings.local.json\n")
		}, []string{".claude/settings.local.json (ignored)"}, func(w *world, dir string) {
			if err := os.Remove(filepath.Join(dir, ".claude", "settings.local.json")); err != nil {
				w.t.Fatal(err)
			}
		}},
		{"a committed rule with a change that isn't", func(w *world, dir string) {
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
			w.write(filepath.Join(dir, "CLAUDE.md"), "committed rules\n")
			w.write(filepath.Join(dir, "LICENSE"), "A license.\n")
			w.commit(dir, "Rules and a license", "CLAUDE.md", "LICENSE")
			w.write(filepath.Join(dir, "CLAUDE.md"), "committed rules\nand a line the person hasn't decided on\n")
			w.write(filepath.Join(dir, "LICENSE"), "A license.\nAn edit in progress.\n")
		}, []string{"CLAUDE.md (changed)"}, func(w *world, dir string) { w.commit(dir, "The rule, decided", "CLAUDE.md") }},
		{"a lockfile and a version file", func(w *world, dir string) {
			w.write(filepath.Join(dir, "go.sum"), "")
			w.write(filepath.Join(dir, ".go-version"), "1.27.1\n")
		}, []string{"go.sum (untracked)", ".go-version (untracked)"}, func(w *world, dir string) {
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
			w.commit(dir, "Go's files", "go.sum", ".go-version")
		}},
		{"a rule that is staged and not committed", func(w *world, dir string) {
			w.write(filepath.Join(dir, "docs", "AGENTS.md"), "rules for the docs\n")
			w.mustGit(dir, "add", "docs/AGENTS.md")
		}, []string{"docs/AGENTS.md"}, func(w *world, dir string) {
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
			w.commit(dir, "Rules for the docs", "docs/AGENTS.md")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			w.checkout(dir, "https://github.com/acme/widgets.git")
			c.make(w, dir)
			before := w.tree(w.root)
			out, err := w.found(Options{})
			if err == nil {
				t.Fatalf("new went ahead:\n%s", out)
			}
			for _, name := range c.names {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("new's refusal doesn't name %s: %v", name, err)
				}
			}
			if strings.Contains(err.Error(), "LICENSE") || !strings.HasSuffix(err.Error(), "then run it again:\n\n"+w.again(dir)) {
				t.Errorf("new's refusal names a file its checks don't, or no command to run again: %v", err)
			}
			w.untouched(before, false)

			// Once the person has dealt with it, one more run founds the
			// project: the refusal left nothing in the way.
			c.fix(w, dir)
			license, _ := os.ReadFile(filepath.Join(dir, "LICENSE"))
			out, err = w.found(w.pasted(err.Error()))
			if err != nil || !strings.Contains(out, "acme/widgets is founded.") {
				t.Fatalf("after the person dealt with it: %v\n%s", err, out)
			}
			checks := w.contract(dir)
			for _, name := range slices.Concat(checks.AgentRules, checks.CurrentState, checks.Parts[0].Inputs, checks.Parts[0].Lockfiles) {
				if _, err := w.git(dir, "cat-file", "-e", "HEAD:"+name); err != nil {
					t.Errorf("the committed checks name %s, which the commit doesn't hold", name)
				}
			}
			if len(license) > 0 {
				if now, _ := os.ReadFile(filepath.Join(dir, "LICENSE")); string(now) != string(license) || w.mustGit(dir, "show", "HEAD:LICENSE") != "A license." {
					t.Errorf("the edit to the license was committed or lost: %q", now)
				}
			}
			if strings.Contains(w.mustGit(dir, "log", "-1", "--format=%s", "--", "LICENSE", "CLAUDE.md", "go.sum", ".go-version", "docs/AGENTS.md"), "Start widgets") {
				t.Error("the founding's commit took a file that isn't its own")
			}
		})
	}

	// A lockfile that is committed is one the checks name, and keep.
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	w.write(filepath.Join(dir, "go.sum"), "")
	w.commit(dir, "Go's sums", "go.sum")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if c := w.contract(dir); !slices.Equal(c.Parts[0].Lockfiles, []string{"go.sum"}) {
		t.Errorf("the checks' lockfiles = %q, want go.sum", c.Parts[0].Lockfiles)
	}
}

// What a person has staged is theirs to commit: new doesn't commit around
// it, and stops before it creates anything. Unstaged, it's left alone.
func TestNewRefusesACheckoutWithChangesOfItsOwnStaged919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "notes.txt"), "not the founding's\n")
	w.write(filepath.Join(dir, "more", "notes.txt"), "nor this\n")
	w.mustGit(dir, "add", "notes.txt", "more/notes.txt")
	before := w.tree(w.root)
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "staged: notes.txt, more/notes.txt") && !strings.Contains(err.Error(), "staged: more/notes.txt, notes.txt") {
		t.Fatalf("new's answer to staged changes is %v\n%s", err, out)
	}
	if !strings.HasSuffix(err.Error(), "Commit or unstage them, then run it again:\n\n"+w.again(dir)) {
		t.Errorf("new's refusal doesn't say what to do: %v", err)
	}
	w.untouched(before, false)

	// A file a person renamed is one change, by its new name.
	renamed := newWorld(t)
	there := filepath.Join(renamed.root, "widgets")
	renamed.checkout(there, "https://github.com/acme/widgets.git")
	renamed.mustGit(there, "symbolic-ref", "HEAD", "refs/heads/main")
	renamed.write(filepath.Join(there, "LICENSE"), "A license, long enough for git to follow it to its new name.\n")
	renamed.commit(there, "A license", "LICENSE")
	renamed.mustGit(there, "mv", "LICENSE", "COPYING")
	if _, err := renamed.found(Options{}); err == nil || !strings.Contains(err.Error(), "staged: COPYING. ") {
		t.Errorf("new's answer to a staged rename is %v", err)
	}

	w.mustGit(dir, "rm", "--quiet", "--cached", "notes.txt", "more/notes.txt")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("with nothing staged: %v\n%s", err, out)
	}
	if committed := w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(committed, "notes.txt") {
		t.Errorf("new committed a file that isn't its own:\n%s", committed)
	}
	if status := w.mustGit(dir, "status", "--porcelain"); status != "?? more/\n?? notes.txt" {
		t.Errorf("the person's files are now %q, want them left untracked", status)
	}
}

// Run a second time, new changes nothing and says the project is already
// founded: it only reads, whatever its flags say now, and however far the
// project has moved on since. A pin that isn't the gate's is remarked on,
// and the gate is left as it is.
func TestASecondRunChangesNothingAndSaysTheProjectIsFounded919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	reads := []string{
		"git remote get-url origin",
		"git symbolic-ref --short HEAD",
		"git ls-tree --name-only HEAD .github/workflows/ .invariant/repository.json",
		"git show HEAD:.github/workflows/gate.yml",
		"gh api users/acme --jq .type",
		"gh repo view acme/widgets --json isEmpty,mergeCommitAllowed,visibility",
		"git ls-tree --name-only refs/remotes/origin/main .github/workflows/ .invariant/repository.json",
		"git show refs/remotes/origin/main:.github/workflows/gate.yml",
		"gh api repos/acme/widgets/branches/main/protection",
	}
	other := filepath.Join(w.root, "another-spec.md")
	w.write(other, "# Another spec\n")
	again := func(o Options) string {
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
		return out
	}
	if out := again(Options{}); strings.Contains(out, "Its gate builds Invariant at") {
		t.Errorf("the second run remarks on a pin that is the gate's own:\n%s", out)
	}
	// Other flags don't make it a different project, and the gate keeps
	// the commit it was founded with.
	out := again(Options{Public: true, Spec: other, Invariant: strings.Repeat("a", 40)})
	if !strings.Contains(out, "Its gate builds Invariant at "+pin[:12]+", not at aaaaaaaaaaaa, the commit -invariant names. invariant new leaves a founded project's gate as it is.") {
		t.Errorf("the second run doesn't say its -invariant isn't the gate's pin:\n%s", out)
	}
	if gate := w.tree(dir)[".github/workflows/gate.yml"]; !strings.Contains(gate, "ref: "+pin) {
		t.Errorf("the second run rewrote the gate's pin:\n%s", gate)
	}
	// Nor does work since: a README that's no longer the starter's.
	w.write(filepath.Join(dir, "README.md"), "# widgets\n\nIt does things now.\n")
	w.commit(dir, "Say what widgets does", "README.md")
	again(Options{})
}

// A checkout with no git identity can't commit, so new stops before it
// writes or commits anything and says how to set one. Run again once one
// is set, with the command it gave, it carries on in the repository and
// checkout it made.
func TestNewStopsBeforeItsFirstCommitWithoutAGitIdentity919(t *testing.T) {
	t.Parallel()
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
		"Then run it again, and it carries on from here:\n\n" + w.again(dir),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("new's message lacks %q: %v", want, err)
		}
	}
	if w.ran("git add") || w.ran("git commit") || w.ran("git push") || w.ran("gh api -X") || w.ran("gh api repos") {
		t.Errorf("new went on past the missing identity:\n%s", strings.Join(w.calls, "\n"))
	}
	if files := w.tree(dir); len(files) != 0 {
		t.Errorf("new wrote %q before it stopped for an identity", slices.Sorted(maps.Keys(files)))
	}
	if _, err := w.git(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		t.Error("new committed with no git identity")
	}

	// Only the email is missing: the message says so.
	w.mustGit(dir, "config", "user.name", "A Person")
	if _, err := w.found(Options{}); err == nil || !strings.Contains(err.Error(), "user.email isn't set") || strings.Contains(err.Error(), "user.name and") {
		t.Errorf("with a name and no email, new says: %v", err)
	}
	// A name set to nothing is no name.
	w.mustGit(dir, "config", "user.name", "")
	w.mustGit(dir, "config", "user.email", "person@example.com")
	if _, err := w.found(Options{}); err == nil || !strings.Contains(err.Error(), "user.name isn't set") {
		t.Errorf("with a name set to nothing, new says: %v", err)
	}
	w.mustGit(dir, "config", "--unset", "user.name")
	w.mustGit(dir, "config", "--unset", "user.email")

	_, err = w.found(Options{})
	if err == nil {
		t.Fatal("new went ahead with no git identity")
	}
	w.identity = true
	w.calls = nil
	out, err = w.found(w.pasted(err.Error()))
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

	// In a checkout that's there already the identity can be asked for
	// first, so nothing is created before the stop.
	w = newWorld(t)
	w.identity = false
	dir = filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	before := w.tree(w.root)
	if _, err := w.found(Options{}); err == nil || !strings.Contains(err.Error(), "has no git identity") {
		t.Fatalf("in a checkout with no identity, new says: %v", err)
	}
	w.untouched(before, false)
}

// Only Go has a starter. Any other language is refused in a sentence that
// says so, before anything is said or done.
func TestNewRefusesALanguageWithoutAStarter919(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	// It never falls back to a runner of its own: without one it stops.
	if err := Project(context.Background(), Options{Repo: "acme/widgets", Language: "go", Invariant: pin, Dir: filepath.Join(t.TempDir(), "widgets")}); err == nil || !strings.Contains(err.Error(), "needs a runner") {
		t.Errorf("a founding with no runner returned %v", err)
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

// Go refuses a module whose path has a file name Windows keeps for itself,
// so a starter for a repository named for one would fail its own checks.
// new refuses the name in a sentence, before anything is said or done, and
// refuses just the names Go does.
func TestNewRefusesANameGoWontBuild919(t *testing.T) {
	t.Parallel()
	refused := []string{"aux", "con", "CON", "prn", "nul.v2", "com1", "COM9.tar", "lpt1", "Lpt9"}
	taken := []string{"auxiliary", "console", "com0", "com10", "lpt", "null", "a.aux", "v2", "internal"}
	for _, name := range refused {
		w := newWorld(t)
		w.repo = "acme/" + name
		out, err := w.found(Options{})
		if err == nil || !strings.Contains(err.Error(), "a Go module can't be named "+name) || !strings.Contains(err.Error(), "Windows") {
			t.Errorf("new's answer to a repository named %s is %v", name, err)
		}
		if len(w.calls) != 0 || out != "" || len(w.tree(w.root)) != 0 {
			t.Errorf("new did something for %s before it refused: %q, said %q", name, w.calls, out)
		}
	}
	for _, name := range taken {
		if kept := device(name); kept != "" {
			t.Errorf("new would refuse a repository named %s, for %s", name, kept)
		}
	}
	// The rule is Go's own.
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("asking Go needs go: %v", err)
	}
	for _, name := range slices.Concat(refused, taken) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/acme/"+name+"\n\ngo 1.27\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(goTool, "list", "-m")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOFLAGS=")
		out, err := cmd.CombinedOutput()
		if goRefuses := err != nil; goRefuses != (device(name) != "") {
			t.Errorf("Go refuses a module named %s = %t, and new refuses it = %t:\n%s", name, goRefuses, device(name) != "", out)
		}
	}
}

// The package a starter declares is a name Go accepts, whatever the
// repository is called.
func TestAStarterPackageNameIsOneGoAccepts919(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
// visibility, and new says so, and what's left for a person goes by what
// the repository is, not by what was asked.
func TestNewLeavesAnAdoptedRepositorysVisibilityAsItIs919(t *testing.T) {
	t.Parallel()
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
	// One that's public is public, asked for or not.
	w = newWorld(t)
	w.exists, w.empty, w.mergeCommits, w.visibility = true, true, true, "public"
	out, err = w.found(Options{})
	if err != nil || strings.Contains(out, "gh auth setup-git") || strings.Contains(out, "add a private one") || strings.Contains(out, "It stays") {
		t.Errorf("what's left treats an adopted public repository as private: %v\n%s", err, out)
	}
}

// The App's installations are on one page for a person's account and on
// another for an organization's, and what's left for a person links the
// owner's own. An owner GitHub doesn't know, or can't be asked about, stops
// new before it creates anything.
func TestTheLinkForTheAppIsTheOwnersOwn919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	out, err := w.found(Options{})
	if err != nil || !strings.Contains(out, "at https://github.com/settings/installations.") || strings.Contains(out, "/organizations/") {
		t.Errorf("for a person's repository: %v\n%s", err, out)
	}
	w = newWorld(t)
	w.organization = true
	out, err = w.found(Options{})
	if err != nil || !strings.Contains(out, "at https://github.com/organizations/acme/settings/installations.") || strings.Contains(out, "https://github.com/settings/installations") {
		t.Errorf("for an organization's repository: %v\n%s", err, out)
	}
	for said, want := range map[string]string{
		"gh: Not Found (HTTP 404)":       "GitHub has no account or organization named acme",
		"gh: Bad Gateway (HTTP 502)":     "can't read acme on GitHub",
		"error connecting to github.com": "can't read acme on GitHub",
	} {
		w = newWorld(t)
		w.fails = func(c Command) error {
			if c.Name == "gh" && c.Args[1] == "users/acme" {
				return errors.New(said)
			}
			return nil
		}
		before := w.tree(w.root)
		if _, err := w.found(Options{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("with GitHub answering %q about the owner, new says: %v", said, err)
		}
		w.untouched(before, false)
	}
}

// -spec names the project's spec. It lands at docs/PRD.md byte for byte,
// is committed, and is a current-state document, and the starter's
// documents and the first issue point at it. Without one, nothing does.
func TestTheSpecLandsAtDocsPRDAndIsCurrentState919(t *testing.T) {
	t.Parallel()
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
	for _, unreadable := range []string{filepath.Join(w.root, "missing.md"), w.root} {
		w = newWorld(t)
		out, err = w.found(Options{Spec: unreadable})
		if err == nil || !strings.Contains(err.Error(), "reading the spec") || len(w.calls) != 0 || out != "" {
			t.Errorf("with %s for a spec, new said %q, ran %q and returned %v", unreadable, out, w.calls, err)
		}
	}
}

// A spec the checkout already holds is the project's once it's committed,
// or named with -spec: then it's a current-state document, and the
// starter's documents point at it. Left untracked, it's one more file the
// checks would name that the founding doesn't add.
func TestNewTakesASpecTheCheckoutAlreadyHolds919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "docs", "PRD.md"), "# The spec\n")
	before := w.tree(w.root)
	_, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "docs/PRD.md (untracked)") {
		t.Fatalf("new's answer to a spec that isn't committed is %v", err)
	}
	w.untouched(before, false)

	// Named with -spec, it's the founding's own file, kept as it is.
	out, err := w.found(Options{Spec: filepath.Join(dir, "docs", "PRD.md")})
	if err != nil {
		t.Fatalf("new refused the checkout's spec, named with -spec: %v\n%s", err, out)
	}
	if c := w.contract(dir); !slices.Equal(c.CurrentState, []string{"SPEC.md", "README.md", "docs/PRD.md"}) {
		t.Errorf("current_state = %q, want the spec too", c.CurrentState)
	}
	if files := w.tree(dir); !strings.Contains(files["README.md"], "docs/PRD.md") || files["docs/PRD.md"] != "# The spec\n" {
		t.Errorf("the README or the spec isn't the project's own: %q", files["README.md"])
	}

	// Committed, it needs no flag.
	w = newWorld(t)
	dir = filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	w.write(filepath.Join(dir, "docs", "PRD.md"), "# The spec\n")
	w.commit(dir, "The spec", "docs/PRD.md")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("new refused a checkout whose spec is committed: %v\n%s", err, out)
	}
	if c := w.contract(dir); !slices.Equal(c.CurrentState, []string{"SPEC.md", "README.md", "docs/PRD.md"}) || !strings.Contains(w.tree(dir)["SPEC.md"], "docs/PRD.md") {
		t.Errorf("current_state = %q, want the committed spec too, and the starter pointing at it", c.CurrentState)
	}
}

// planRefuses is GitHub's answer where an account's plan doesn't protect a
// private repository's branches, for a person's account and for an
// organization's.
func planRefuses(plan string) error {
	return errors.New("gh: Upgrade to GitHub " + plan + " or make this repository public to enable this feature. (HTTP 403)")
}

// Where the account's plan won't protect a private repository's branches,
// GitHub refuses the requirement with a 403. new says so, says what it
// means and how to change it, and carries on to what's left for a person. A
// second run finds the same and still changes nothing. The command it gives
// for running again is the one that was run, whatever its flags were.
func TestNewSaysWhatARefusedGateRequirementMeansAndCarriesOn919(t *testing.T) {
	t.Parallel()
	for _, plan := range []string{"Pro", "Team"} {
		for _, read := range []bool{false, true} {
			w := newWorld(t)
			// GitHub refuses the read of a protection the plan doesn't
			// give, as it does the requirement. Either is the refusal.
			w.refusal = planRefuses(plan)
			if read {
				w.unread = planRefuses(plan)
			}
			dir := filepath.Join(w.root, "code", "the widgets")
			spec := filepath.Join(w.root, "spec.md")
			w.write(spec, "# The spec\n")
			out, err := w.found(Options{Dir: dir, Spec: "spec.md", Public: plan == "Team"})
			if err != nil {
				t.Fatalf("%s: new failed where GitHub refused the requirement: %v\n%s", plan, err, out)
			}
			again := w.again("'"+dir+"'", "-spec", spec)
			if plan == "Team" {
				again = w.again("'"+dir+"'", "-public", "-spec", spec)
			}
			for _, want := range []string{
				"GitHub won't require invariant/gate on main",
				"The factory still merges only once the gate passes.",
				"A merge made by hand can skip it.",
				"make the repository public, at https://github.com/acme/widgets/settings, or change the account's plan",
				"Then run it again:\n\n" + again + "\n",
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
			if len(w.pushed) != 1 || (w.body != "") == read || !read && w.body != setup.Protection {
				t.Errorf("%s, the read refused %t: new sent the requirement %q after %d pushes", plan, read, w.body, len(w.pushed))
			}

			// The command it gave, pasted somewhere else: the same
			// project, still refused, and nothing changed.
			w.calls, w.body = nil, ""
			pasted := w.pasted(out)
			pasted.WorkDir = t.TempDir()
			out, err = w.found(pasted)
			if err != nil || !strings.Contains(out, "acme/widgets is already founded: nothing changed.") || !strings.Contains(out, "GitHub won't require invariant/gate on main") {
				t.Errorf("%s: the command new gave for running again: %v\n%s", plan, err, out)
			}
			if len(w.pushed) != 1 || w.ran("git commit") || w.ran("gh repo create") || w.ran("gh repo clone") {
				t.Errorf("%s: the second run changed something:\n%s", plan, strings.Join(w.calls, "\n"))
			}

			// Once the plan allows it, the next run requires the gate.
			w.refusal, w.unread = nil, nil
			out, err = w.found(pasted)
			if err != nil || !w.required || !strings.Contains(out, "Required invariant/gate on main") || strings.Contains(out, "already founded") {
				t.Errorf("%s: with the refusal gone, new: %v\n%s", plan, err, out)
			}
		}
	}
}

// Any other failure to require the gate is an error: a 403 that isn't the
// plan's, the plan's words under another status, or a refusal of the
// request itself.
func TestAnyOtherFailureToRequireTheGateStopsNew919(t *testing.T) {
	t.Parallel()
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

// The call that requires the gate replaces whatever protection main has, so
// new makes it only where GitHub says plainly that main has none. A read
// that fails any other way stops it with the error, and nothing there is
// changed: not on a project whose main requires the gate, a linter and two
// reviews, and not on one founded a moment ago.
func TestNewRequiresTheGateOnlyWhereMainIsPlainlyUnprotected919(t *testing.T) {
	t.Parallel()
	strict := `{"required_status_checks":{"strict":true,"contexts":["invariant/gate","lint"],"checks":[{"context":"invariant/gate","app_id":15368},{"context":"lint","app_id":15368}]},"required_pull_request_reviews":{"required_approving_review_count":2},"enforce_admins":{"enabled":false}}`
	for _, said := range []string{
		"gh: Bad Gateway (HTTP 502)",
		"gh: API rate limit exceeded for user ID 1. (HTTP 403)",
		"Get \"https://api.github.com/repos/acme/widgets/branches/main/protection\": net/http: TLS handshake timeout",
		"gh: Branch not found (HTTP 404)",
		"gh: Not Found (HTTP 404)",
		"gh: Branch not protected (HTTP 500)",
	} {
		// A founded project a person has since protected further.
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		if out, err := w.found(Options{}); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		w.protection, w.unread, w.calls, w.body = strict, errors.New(said), nil, ""
		out, err := w.found(Options{})
		if err == nil || !strings.Contains(err.Error(), "reading main's protection") || !strings.Contains(err.Error(), said) || !strings.Contains(err.Error(), "nothing there was changed") || !strings.HasSuffix(err.Error(), "Run it again:\n\n"+w.again(dir)) {
			t.Errorf("with the read answering %q, new says: %v", said, err)
		}
		if w.body != "" || w.ran("gh api -X PUT") || strings.Contains(out, "Required") || founds(out) {
			t.Errorf("with the read answering %q, new replaced the protection or called the project founded: %q\n%s", said, w.body, out)
		}

		// And a founding from nothing: the commit is pushed, the
		// requirement isn't guessed at, and the next run makes it.
		w = newWorld(t)
		w.unread = errors.New(said)
		if _, err := w.found(Options{}); err == nil || w.body != "" || len(w.pushed) != 1 {
			t.Errorf("with the read answering %q on a new project: %v, requirement %q, %d pushes", said, err, w.body, len(w.pushed))
		}
		w.unread = nil
		if out, err := w.found(Options{}); err != nil || w.body != setup.Protection || !strings.Contains(out, "acme/widgets is founded.") {
			t.Errorf("the run after the read answered %q: %v\n%s", said, err, out)
		}
	}
}

// A main that's protected already is a person's protection. Where it
// requires invariant/gate, however GitHub lists the check, new leaves it
// alone and says nothing. Where it doesn't, new replaces nothing either: it
// says what main requires, and gives the command that adds the gate and
// keeps the rest.
func TestNewLeavesAMainThatIsProtectedAlready919(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, protection string
		requires, body   string // what new says main requires, and the command's body; "" where the gate is required
	}{
		{"the gate, as a check of GitHub Actions, among others", `{"required_status_checks":{"strict":true,"contexts":[],"checks":[{"context":"lint","app_id":15368},{"context":"invariant/gate","app_id":15368}]},"required_pull_request_reviews":{"required_approving_review_count":2}}`, "", ""},
		{"the gate, as a context", `{"required_status_checks":{"strict":false,"contexts":["invariant/gate"]}}`, "", ""},
		{"another check, strictly, from any App", `{"required_status_checks":{"strict":true,"contexts":["lint"],"checks":[{"context":"lint","app_id":null}]},"required_pull_request_reviews":{"required_approving_review_count":2}}`,
			"lint", `{"strict":true,"checks":[{"context":"lint","app_id":-1},{"context":"invariant/gate","app_id":15368}]}`},
		{"two checks, one from an App", `{"required_status_checks":{"strict":false,"contexts":["lint","build"],"checks":[{"context":"lint","app_id":15368},{"context":"build","app_id":7}]}}`,
			"lint, build", `{"strict":false,"checks":[{"context":"lint","app_id":15368},{"context":"build","app_id":7},{"context":"invariant/gate","app_id":15368}]}`},
		{"a check listed as a context alone", `{"required_status_checks":{"strict":false,"contexts":["lint"]}}`,
			"lint", `{"strict":false,"checks":[{"context":"lint","app_id":-1},{"context":"invariant/gate","app_id":15368}]}`},
		{"reviews and no check", `{"required_pull_request_reviews":{"required_approving_review_count":1},"enforce_admins":{"enabled":true}}`,
			"no check", `{"strict":false,"checks":[{"context":"invariant/gate","app_id":15368}]}`},
	} {
		w := newWorld(t)
		w.protection = c.protection
		out, err := w.found(Options{})
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, out)
		}
		if w.body != "" || w.ran("gh api -X PUT") || strings.Contains(out, "Required invariant/gate on main") {
			t.Errorf("%s: new replaced a protection that was there: %q\n%s", c.name, w.body, out)
		}
		if !strings.Contains(out, "acme/widgets is founded.") || !strings.Contains(out, "Left for a person:") {
			t.Errorf("%s: new didn't carry on:\n%s", c.name, out)
		}
		if c.body == "" {
			if strings.Contains(out, "main is protected already") {
				t.Errorf("%s: new remarks on a main that requires the gate:\n%s", c.name, out)
			}
			continue
		}
		for _, want := range []string{
			"main is protected already, and requires " + c.requires + ": invariant/gate isn't among its checks. invariant new doesn't replace a protection it didn't set.",
			"A merge made by hand can skip it.",
			"To require the gate and keep everything else there, run:\n\ngh api -X PATCH repos/acme/widgets/branches/main/protection/required_status_checks --input - <<'EOF'\n" + c.body + "\nEOF\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: new didn't say %q:\n%s", c.name, want, out)
			}
		}
	}
	// An answer that can't be read says nothing plain, so nothing is
	// replaced on its account.
	w := newWorld(t)
	w.protection = "<html>Unicorn</html>"
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "can't be read, so nothing there was changed") || w.body != "" || founds(out) {
		t.Errorf("with an answer that isn't GitHub's, new: %v, requirement %q\n%s", err, w.body, out)
	}
}

// A founding that stopped part way is finished by running it again, at
// whichever step it stopped: the next run finds what the first one made and
// carries on from there, and makes nothing twice.
func TestARerunFinishesAFoundingThatStopped919(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		fails   func(Command) bool
		without []string // what the next run doesn't do again
	}{
		{"the clone", func(c Command) bool { return c.Name == "gh" && c.Args[1] == "clone" }, []string{"gh repo create"}},
		{"the commit, refused by a hook", func(c Command) bool { return c.Name == "git" && c.Args[0] == "commit" }, []string{"gh repo create", "gh repo clone"}},
		{"the push", func(c Command) bool { return c.Name == "git" && c.Args[0] == "push" }, []string{"gh repo create", "gh repo clone", "git commit"}},
		{"allowing merge commits", func(c Command) bool { return c.Name == "gh" && len(c.Args) > 2 && c.Args[2] == "PATCH" }, []string{"gh repo create", "gh repo clone", "git commit", "git push"}},
		{"requiring the gate", func(c Command) bool { return c.Name == "gh" && len(c.Args) > 2 && c.Args[2] == "PUT" }, []string{"gh repo create", "gh repo clone", "git commit", "git push"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			// A repository that doesn't allow merge commits until it's
			// told to, so that step is one a run can stop at.
			w.mergeless = true
			failed := errors.New("it failed, for now")
			w.fails = func(cmd Command) error {
				if c.fails(cmd) {
					return failed
				}
				return nil
			}
			out, err := w.found(Options{})
			if !errors.Is(err, failed) || founds(out) {
				t.Fatalf("the first run: %v\n%s", err, out)
			}
			w.fails, w.calls = nil, nil
			out, err = w.found(Options{})
			if err != nil || !strings.Contains(out, "acme/widgets is founded.") {
				t.Fatalf("the next run didn't finish the founding: %v\n%s", err, out)
			}
			for _, again := range c.without {
				if w.ran(again) {
					t.Errorf("the next run ran %q a second time:\n%s", again, strings.Join(w.calls, "\n"))
				}
			}
			head := w.mustGit(dir, "rev-parse", "HEAD")
			if n := w.mustGit(dir, "rev-list", "--count", "HEAD"); n != "1" || !slices.Equal(w.pushed, []string{head}) || !w.mergeCommits || !w.required {
				t.Errorf("after the next run: %s commits, pushed %q, merge commits %t, the gate required %t", n, w.pushed, w.mergeCommits, w.required)
			}
			w.contract(dir)
			// And the run after that changes nothing.
			if out, err := w.found(Options{}); err != nil || !strings.Contains(out, "already founded: nothing changed") {
				t.Errorf("the run after: %v\n%s", err, out)
			}
		})
	}
}

// A push that fails stops new with git's own words. The next run pushes the
// commit the first one made, and makes no second one.
func TestNewCarriesOnFromAPushThatFailed919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.pushFails = errors.New("fatal: unable to access 'https://github.com/acme/widgets.git/': Could not resolve host: github.com")
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "git push -u origin main") || !strings.Contains(err.Error(), "Could not resolve host") {
		t.Fatalf("new's answer to a failed push is %v\n%s", err, out)
	}
	if w.ran("gh api -X") || w.ran("gh api repos") || strings.Contains(out, "is founded") {
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
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "private"
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	w.write(filepath.Join(dir, "LICENSE"), "A license.\n")
	w.commit(dir, "A license", "LICENSE")
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

// A repository that's empty gets the founding's commit whatever the
// checkout remembers of a push: one deleted and made again holds nothing.
// And one that's gone is made again, as -public says, and pushed to.
func TestNewPushesToARepositoryThatIsEmptyAgain919(t *testing.T) {
	t.Parallel()
	for _, gone := range []bool{false, true} {
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		if out, err := w.found(Options{}); err != nil {
			t.Fatalf("the founding failed: %v\n%s", err, out)
		}
		head := w.mustGit(dir, "rev-parse", "HEAD")
		w.calls, w.pushed, w.empty, w.required, w.exists = nil, nil, !gone, false, !gone
		out, err := w.found(Options{Public: true})
		if err != nil {
			t.Fatalf("gone %t: the run on the emptied repository failed: %v\n%s", gone, err, out)
		}
		if !slices.Equal(w.pushed, []string{head}) || w.ran("git commit") || !w.required || !strings.Contains(out, "acme/widgets is founded.") {
			t.Errorf("gone %t: new pushed %q, want the founding's commit %s:\n%s", gone, w.pushed, head, out)
		}
		if gone != w.ran("gh repo create acme/widgets --public") || gone != (w.visibility == "public") {
			t.Errorf("gone %t: the repository is %s, after:\n%s", gone, w.visibility, strings.Join(w.calls, "\n"))
		}
	}
}

// A commit new has just made is pushed, even into a checkout that has seen
// a founded main on GitHub it never merged: git then says the two have
// parted, where skipping the push would call a project founded whose
// commit never left the machine.
func TestNewPushesTheCommitItMakesWhateverTheCheckoutRemembers919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "private"
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	w.write(filepath.Join(dir, "LICENSE"), "A license.\n")
	w.commit(dir, "A license", "LICENSE")
	// What GitHub's main holds, fetched and never merged: a founding made
	// somewhere else.
	elsewhere := newWorld(t)
	if out, err := elsewhere.found(Options{}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	theirs := elsewhere.tree(filepath.Join(elsewhere.root, "widgets"))
	for _, name := range []string{".github/workflows/gate.yml", ".invariant/repository.json"} {
		w.write(filepath.Join(dir, filepath.FromSlash(name)), theirs[name])
	}
	w.mustGit(dir, "add", ".github", ".invariant")
	w.mustGit(dir, "update-ref", "refs/remotes/origin/main", w.mustGit(dir, "commit-tree", "-m", "Founded elsewhere", w.mustGit(dir, "write-tree")))
	w.mustGit(dir, "rm", "--quiet", "-r", "-f", ".github", ".invariant")
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	if head := w.mustGit(dir, "rev-parse", "HEAD"); !w.ran("git commit") || !slices.Equal(w.pushed, []string{head}) {
		t.Errorf("new pushed %q, want the commit it made, %s:\n%s", w.pushed, head, strings.Join(w.calls, "\n"))
	}
}

// The checks new commits are the ones the starter passes: run as they're
// committed, format, vet, build and test all succeed, and the format check
// fails once a Go file isn't formatted, where gofmt -l alone wouldn't.
func TestTheStarterPassesTheChecksItCommits919(t *testing.T) {
	t.Parallel()
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

// handMade is checks a person completes by hand for a checkout whose one
// part is a TypeScript package in web/, as the loader takes them.
const handMade = `{
  "version": 1,
  "parts": [
    {
      "name": "web",
      "directory": "web",
      "language": "typescript",
      "image": "node@sha256:` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `",
      "lockfiles": [],
      "inputs": ["web/package.json"],
      "commands": {
        "install": {"not_applicable": "It has no dependencies."},
        "format": {"not_applicable": "It has no formatter yet."},
        "lint": {"not_applicable": "It has no linter yet."},
        "build": {"not_applicable": "It has nothing to build."},
        "test": {"not_applicable": "It has no tests yet."},
        "test_one": {"not_applicable": "It has no tests yet."}
      }
    }
  ],
  "agent_rules": [],
  "current_state": [],
  "trusted_paths": [],
  "shared_paths": [{"path": ".github/workflows/gate.yml", "parts": ["web"]}]
}
`

// new completes only what it knows how to. A checkout whose draft would
// leave anything else unresolved, as one with a part new has no starter
// for, stops it before it creates or writes anything. Its message lists
// what's left and says how to finish by hand, and doing just that works:
// with the gate and the completed checks committed on main, the command it
// gave pushes them and finishes the founding, and writes no starter.
func TestNewStopsOnChecksItCantCompleteAndSaysHowToFinish919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "web", "package.json"), "{\"name\": \"web\", \"private\": true}\n")
	before := w.tree(w.root)
	out, err := w.found(Options{})
	if err == nil {
		t.Fatalf("new completed checks for a part it has no starter for:\n%s", out)
	}
	for _, want := range []string{
		"can't settle, so it created and wrote nothing", "\n- web (typescript) ",
		"Run invariant init -repo acme/widgets -invariant " + pin + " " + dir + ", complete .invariant/repository.json, and commit it on main with .github/workflows/gate.yml and every file the checks name.",
		"Then run it again, to push main and finish the founding:\n\n" + w.again(dir),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("new's message lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), ". (go) format") {
		t.Errorf("new lists the format check it does complete: %v", err)
	}
	w.untouched(before, false)

	// What the message says, done by hand: init's gate, checks a person
	// completed, and the file they name, committed on main.
	if _, err := setup.Write(dir, pin, false); err != nil {
		t.Fatal(err)
	}
	w.write(filepath.Join(dir, ".invariant", "repository.json"), handMade)
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	w.commit(dir, "The gate and the checks, by hand", ".github/workflows/gate.yml", ".invariant/repository.json", "web/package.json")
	head, tree := w.mustGit(dir, "rev-parse", "HEAD"), w.tree(dir)
	w.calls = nil
	out, err = w.found(w.pasted(err.Error()))
	if err != nil {
		t.Fatalf("after doing what new's message says, the command it gave fails: %v\n%s", err, out)
	}
	if !strings.Contains(out, "acme/widgets is founded.") || !slices.Equal(w.pushed, []string{head}) || !w.exists || !w.required {
		t.Errorf("the command new gave didn't finish the founding: pushed %q, want %s\n%s", w.pushed, head, out)
	}
	if !reflect.DeepEqual(w.tree(dir), tree) || w.ran("git commit") || w.ran("git add") {
		t.Errorf("new wrote or committed in a project set up by hand:\n%s", strings.Join(w.calls, "\n"))
	}
}

// Only the root's Go part is the starter's, so only its format check is
// new's to settle. A second Go module in the checkout keeps its own
// unresolved, and stops the founding.
func TestNewSettlesOnlyItsOwnPartsFormatCheck919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "cmd", "tool", "go.mod"), "module github.com/acme/widgets/cmd/tool\n\ngo 1.27\n")
	before := w.tree(w.root)
	_, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "\n- cmd/tool (go) format: ") || strings.Contains(err.Error(), "\n- . (go) format") {
		t.Fatalf("new's answer to a second Go module is %v", err)
	}
	w.untouched(before, false)
}

// A checkout whose commit holds a gate and a checks file the loader
// refuses, as a draft committed with its unresolved entries, isn't founded,
// and new doesn't say it is: it names the file, pushes nothing and sets
// nothing.
func TestNewDoesntCallAProjectFoundedWhoseChecksDontLoad919(t *testing.T) {
	t.Parallel()
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
	w.commit(dir, "An unfinished draft", repository.Path)
	w.calls, w.pushed, w.body, w.empty, w.required = nil, nil, "", true, false
	before := w.tree(w.root)
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "don't load") || !strings.Contains(err.Error(), repository.Path) || !strings.Contains(err.Error(), "unresolved") || !strings.HasSuffix(err.Error(), "then run it again:\n\n"+w.again(dir)) {
		t.Fatalf("new's answer to committed checks that don't load is %v\n%s", err, out)
	}
	w.untouched(before, true)
	if founds(out) || w.required {
		t.Errorf("new treated the project as founded:\n%s\n%s", out, strings.Join(w.calls, "\n"))
	}
}

// new reads its own commit back before it pushes, with the loader CI uses.
// Checks that changed under it between the writing and the commit, as a
// hook or an editor can change a file, aren't pushed as a founding's.
func TestNewPushesOnlyChecksThatLoad919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	path := filepath.Join(dir, filepath.FromSlash(repository.Path))
	w.before = func(c Command) {
		if c.Name == "git" && c.Args[0] == "add" {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			w.write(path, strings.Replace(string(b), "{\n", "{\n  \"unresolved\": [\"something a person has to settle\"],\n", 1))
		}
	}
	out, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "don't load") || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("new's answer to checks that changed before its commit is %v\n%s", err, out)
	}
	if len(w.pushed) != 0 || w.ran("git push") || w.ran("gh api -X") || founds(out) {
		t.Errorf("new pushed checks that don't load, or called the project founded:\n%s", strings.Join(w.calls, "\n"))
	}
}

// A project is founded once its commit holds its checks and a gate the
// factory will merge on: a workflow whose invariant/gate job runs the
// gate's checks, as the watcher's own check of a repository reads one. A
// file by the gate's name isn't that, and neither half is without the
// other.
func TestAProjectIsFoundedOnlyWithItsChecksAndACapableGate919(t *testing.T) {
	t.Parallel()
	workflow, err := setup.Workflow(pin)
	if err != nil {
		t.Fatal(err)
	}
	// The gate alone, as in a repository set up before there were checks:
	// the founding goes on.
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "private"
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	w.write(filepath.Join(dir, ".github", "workflows", "gate.yml"), workflow)
	w.commit(dir, "The gate", ".github/workflows/gate.yml")
	w.mustGit(dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	out, err := w.found(Options{})
	if err != nil {
		t.Fatalf("with the gate alone, the founding failed: %v\n%s", err, out)
	}
	if !w.ran("git commit") || len(w.pushed) != 1 || !strings.Contains(out, "acme/widgets is founded.") {
		t.Errorf("new took a checkout with the gate alone for a founded project:\n%s", out)
	}
	w.contract(dir)

	// The checks alone, the gate removed: it's put back.
	w.mustGit(dir, "rm", "--quiet", ".github/workflows/gate.yml")
	w.mustGit(dir, "commit", "--quiet", "-m", "Remove the gate")
	w.mustGit(dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	w.calls, w.pushed = nil, nil
	out, err = w.found(Options{})
	if err != nil {
		t.Fatalf("with the checks alone, the founding failed: %v\n%s", err, out)
	}
	if held := w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD"); !strings.Contains(held, ".github/workflows/gate.yml") || len(w.pushed) != 1 || strings.Contains(out, "already founded") {
		t.Errorf("new took a checkout with the checks alone for a founded project:\n%s\n%s", out, held)
	}

	// A workflow at the gate's path that isn't the gate: not founded, and
	// not overwritten, so new stops and names it.
	for name, other := range map[string]string{
		"another workflow":                        "name: something else\non: push\njobs: {}\n",
		"a gate whose job doesn't run its checks": "name: gate\non:\n  push:\n  pull_request:\njobs:\n  gate:\n    name: invariant/gate\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n",
	} {
		w.write(filepath.Join(dir, ".github", "workflows", "gate.yml"), other)
		w.commit(dir, "Not the gate", ".github/workflows/gate.yml")
		w.mustGit(dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
		w.calls, w.pushed = nil, nil
		out, err = w.found(Options{})
		if err == nil || founds(out) || !strings.Contains(err.Error(), filepath.Join(dir, ".github", "workflows", "gate.yml")+" is already there") {
			t.Errorf("with %s at the gate's path, new says: %v\n%s", name, err, out)
		}
		if w.ran("git commit") || w.ran("git push") || w.ran("gh api -X") {
			t.Errorf("with %s at the gate's path, new went on:\n%s", name, strings.Join(w.calls, "\n"))
		}
	}

	// The gate under another file's name is the gate all the same.
	w.mustGit(dir, "mv", ".github/workflows/gate.yml", ".github/workflows/ci.yml")
	w.write(filepath.Join(dir, ".github", "workflows", "ci.yml"), workflow)
	w.mustGit(dir, "add", ".github/workflows/ci.yml")
	w.mustGit(dir, "commit", "--quiet", "-m", "The gate, in ci.yml")
	w.mustGit(dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	w.calls, w.pushed = nil, nil
	out, err = w.found(Options{})
	if err != nil || !strings.Contains(out, "acme/widgets is already founded: nothing changed.") || w.ran("git commit") || w.ran("git push") {
		t.Errorf("with the gate in ci.yml, new says: %v\n%s", err, out)
	}
}

// Everything a founding runs names the repository or its owner, or runs in
// the checkout, and every file it writes is inside the checkout, even past
// a link that leads out of it.
func TestNewCreatesNothingOutsideItsDirectoryAndRepository919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	spec := filepath.Join(w.root, "spec.md")
	w.write(spec, "# The spec\n")
	w.before = func(c Command) {
		switch {
		case c.Name == "git" && c.Dir == dir:
		case c.Name == "gh" && c.Dir == "" && (slices.Contains(c.Args, w.repo) || slices.ContainsFunc(c.Args, func(arg string) bool {
			return arg == "users/acme" || arg == "repos/"+w.repo || strings.HasPrefix(arg, "repos/"+w.repo+"/")
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
	if got := slices.Sorted(maps.Keys(w.tree(w.root))); !slices.Equal(got, slices.Sorted(slices.Values(append(prefixed("widgets/", strings.Fields(founded)), "widgets/docs/PRD.md", "spec.md")))) {
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
// not existing: new creates nothing on a guess. A plain "not found" is, in
// either form gh gives it.
func TestNewCreatesNothingWhenItCantTellWhetherTheRepositoryExists919(t *testing.T) {
	t.Parallel()
	viewFails := func(w *world, said string) {
		w.fails = func(c Command) error {
			if c.Name == "gh" && c.Args[0] == "repo" && c.Args[1] == "view" {
				return errors.New(said)
			}
			return nil
		}
	}
	w := newWorld(t)
	viewFails(w, "error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com")
	before := w.tree(w.root)
	_, err := w.found(Options{})
	if err == nil || !strings.Contains(err.Error(), "can't tell whether acme/widgets is on GitHub") || !strings.Contains(err.Error(), "check your internet connection") {
		t.Fatalf("new's answer to a GitHub it can't reach is %v", err)
	}
	w.untouched(before, false)

	for _, missing := range []string{"GraphQL: Could not resolve to a Repository with the name 'acme/widgets'. (repository)", "gh: Not Found (HTTP 404)"} {
		w = newWorld(t)
		viewFails(w, missing)
		if out, err := w.found(Options{}); err != nil || !w.ran("gh repo create acme/widgets --private") || !strings.Contains(out, "acme/widgets is founded.") {
			t.Errorf("with GitHub answering %q, new didn't create the repository: %v\n%s", missing, err, out)
		}
	}
}

// The commands new prints for a person can be pasted as they are: a
// directory with a space or a quote in its name is one word of each, and
// the command to run again carries every flag that decides what it does.
func TestTheCommandsNewPrintsQuoteTheirDirectory919(t *testing.T) {
	t.Parallel()
	for dir, want := range map[string]string{
		"/home/someone/widgets":       "/home/someone/widgets",
		"/home/someone/my projects/w": "'/home/someone/my projects/w'",
		"/home/someone/it's/w":        `'/home/someone/it'\''s/w'`,
		"/home/someone/$HOME/w":       "'/home/someone/$HOME/w'",
		"":                            "''",
	} {
		if got := shell(dir); got != want {
			t.Errorf("%q as a word of a command is %s, want %s", dir, got, want)
		}
	}
	w := newWorld(t)
	w.identity = false
	dir := filepath.Join(w.root, "my project's", "widgets")
	spec := filepath.Join(w.root, "the spec.md")
	w.write(spec, "# The spec\n")
	_, err := w.found(Options{Dir: dir, Spec: spec, Public: true})
	quoted := "'" + strings.ReplaceAll(dir, "'", `'\''`) + "'"
	if err == nil || !strings.Contains(err.Error(), "git -C "+quoted+" config user.name ") {
		t.Fatalf("the command to set an identity doesn't quote %s: %v", dir, err)
	}
	if want := "invariant new acme/widgets -dir " + quoted + " -public -spec '" + spec + "' -invariant " + pin; !strings.HasSuffix(err.Error(), "\n\n"+want) {
		t.Errorf("the command to run again isn't %s: %v", want, err)
	}
	// Read back as a shell reads it, it's the founding that was asked for.
	if o := w.pasted(err.Error()); o.Repo != w.repo || o.Dir != dir || o.Spec != spec || !o.Public || o.Invariant != pin {
		t.Errorf("the command to run again reads as %+v", o)
	}
}

// Exec, the runner new really uses, returns what a command printed, and
// what a failed one said of its failure. It hands a command what it reads.
func TestExecReturnsWhatACommandPrintedAndWhyItFailed919(t *testing.T) {
	t.Parallel()
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
