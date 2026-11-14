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

// untouched fails the test unless the founding refused and left everything
// as it was: the files under the world's root, and GitHub with no
// repository made, nothing pushed and no setting changed. And no refusal
// tells a person to commit a file: that advice can publish what was never
// meant to be.
func (w *world) untouched(before map[string]string, existed bool) {
	w.t.Helper()
	if w.err == nil {
		w.t.Fatalf("new went ahead:\n%s", w.said.String())
	}
	for _, advice := range []string{"git add", "git commit", "ommit it", "ommit them", "ommit each", "Commit "} {
		if strings.Contains(w.err.Error()+w.said.String(), advice) {
			w.t.Errorf("new's refusal tells a person to commit something, with %q: %v", advice, w.err)
		}
	}
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

// existing is how a refusal says a project isn't empty and what to use in
// its place, for a checkout: init, run as the README runs it.
func (w *world) existing(dir string) string {
	return ", so this isn't an empty project, and invariant new founds empty projects only. A repository that exists is invariant init's to set up, in its checkout:\n\ncd " + shell(dir) + " && invariant init -repo " + w.repo + "\n\nhttps://github.com/gitdek/invariant#use-it-on-your-repository says what follows"
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
		"git remote get-url --all origin",
		"git remote get-url --push --all origin",
		"git for-each-ref --count=1",
		"git config user.name",
		"git config user.email",
		"git symbolic-ref HEAD refs/heads/main",
		"git add -f -- " + founded,
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

// A fresh clone of the repository is adopted: a checkout whose origin is
// the repository, over https or ssh, whatever the case of its name, with
// nothing in it but what a founding writes. new clones nothing, and keeps a
// file that's already there with the bytes it would write.
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

// new founds empty projects only. A directory that holds anything a
// founding doesn't write is refused before anything is created, whatever
// git makes of the entry: untracked, ignored or tracked, a directory whose
// name differs from one of the founding's only by case, a repository inside
// this one, a gitlink, a link. It's read from the directory, so no ignore
// rule hides it, and no link is followed. The refusal names a handful of
// the entries and says what to use in new's place: init, run as the README
// runs it.
func TestNewRefusesADirectoryThatHoldsAnythingElse919(t *testing.T) {
	t.Parallel()
	exclude := func(w *world, dir, pattern string) {
		w.write(filepath.Join(dir, ".git", "info", "exclude"), pattern+"\n")
	}
	link := func(w *world, target, at string) {
		if err := os.Symlink(target, at); err != nil {
			w.t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name  string
		plain bool // the directory is no checkout
		make  func(w *world, dir string)
		names string
	}{
		{"an untracked file", false, func(w *world, dir string) { w.write(filepath.Join(dir, "notes.txt"), "notes\n") }, "notes.txt"},
		{"a file git ignores", false, func(w *world, dir string) {
			w.write(filepath.Join(dir, "local.env"), "SETTING=local\n")
			exclude(w, dir, "local.env")
		}, "local.env"},
		{"a tracked file", false, func(w *world, dir string) {
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
			w.write(filepath.Join(dir, "LICENSE"), "A license.\n")
			w.commit(dir, "A license", "LICENSE")
		}, "LICENSE"},
		{"a README of the person's own", false, func(w *world, dir string) { w.write(filepath.Join(dir, "README.md"), "# Mine\n") }, "README.md"},
		{"a spec nobody named with -spec", false, func(w *world, dir string) { w.write(filepath.Join(dir, "docs", "PRD.md"), "# A spec\n") }, "docs/"},
		{"an agent's rules and settings", false, func(w *world, dir string) {
			w.write(filepath.Join(dir, ".mcp.json"), "{}\n")
			w.write(filepath.Join(dir, ".claude", "settings.local.json"), "{}\n")
			exclude(w, dir, ".claude/")
		}, ".claude/, .mcp.json"},
		{"a workflow git ignores", false, func(w *world, dir string) {
			w.write(filepath.Join(dir, ".github", "workflows", "deploy.yml"), "name: deploy\non: workflow_dispatch\njobs: {}\n")
			exclude(w, dir, ".github/workflows/deploy.yml")
		}, ".github/workflows/deploy.yml"},
		{"directories that differ from the founding's only by case", false, func(w *world, dir string) {
			w.write(filepath.Join(dir, "Docs", "guide.md"), "# A guide\n")
			w.write(filepath.Join(dir, "Tools", "helper.sh"), "#!/bin/sh\n")
		}, "Docs/, Tools/"},
		{"a repository inside it", false, func(w *world, dir string) {
			w.checkout(filepath.Join(dir, "sub"), "https://github.com/acme/sub.git")
			w.write(filepath.Join(dir, "sub", "AGENTS.md"), "rules of the repository inside\n")
		}, "sub/"},
		{"a repository where a directory of the founding's goes", false, func(w *world, dir string) {
			w.checkout(filepath.Join(dir, "tools"), "https://github.com/acme/tools.git")
			w.write(filepath.Join(dir, "tools", "LICENSE"), "A license.\n")
		}, "tools/.git/, tools/LICENSE"},
		{"a gitlink", false, func(w *world, dir string) {
			sub := filepath.Join(dir, "sub")
			w.checkout(sub, "https://github.com/acme/sub.git")
			w.write(filepath.Join(sub, "AGENTS.md"), "rules of the submodule\n")
			w.commit(sub, "Rules", "AGENTS.md")
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
			w.mustGit(dir, "add", "sub")
			w.mustGit(dir, "commit", "--quiet", "-m", "A submodule")
		}, "sub/"},
		{"a link to a file, where a file of the founding's goes", false, func(w *world, dir string) {
			w.write(filepath.Join(w.root, "outside.md"), "# Outside\n")
			link(w, filepath.Join(w.root, "outside.md"), filepath.Join(dir, "SPEC.md"))
		}, "SPEC.md"},
		{"a link that leads nowhere", false, func(w *world, dir string) { link(w, filepath.Join(w.root, "nowhere"), filepath.Join(dir, "go.mod")) }, "go.mod"},
		{"a link to a directory, where a directory of the founding's goes", false, func(w *world, dir string) {
			if err := os.Mkdir(filepath.Join(w.root, "outside"), 0o755); err != nil {
				w.t.Fatal(err)
			}
			link(w, filepath.Join(w.root, "outside"), filepath.Join(dir, ".invariant"))
		}, ".invariant"},
		{"a format check of the founding's that can't run", false, func(w *world, dir string) {
			text, err := starters.ReadFile("starter/go/check-format.tmpl")
			if err != nil {
				w.t.Fatal(err)
			}
			w.write(filepath.Join(dir, "tools", "check-format"), string(text))
		}, "tools/check-format"},
		{"more than a handful", false, func(w *world, dir string) {
			for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
				w.write(filepath.Join(dir, name+".txt"), name+"\n")
			}
		}, "a.txt, b.txt, c.txt, d.txt, e.txt and 2 more"},
		{"files, and no checkout", true, func(w *world, dir string) {
			w.write(filepath.Join(dir, "notes.txt"), "notes\n")
			w.write(filepath.Join(dir, "src", "main.go"), "package main\n")
		}, "notes.txt, src/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			if !c.plain {
				w.checkout(dir, "https://github.com/acme/widgets.git")
			}
			c.make(w, dir)
			before := w.tree(w.root)
			_, err := w.found(Options{})
			w.untouched(before, false)
			want := dir + " holds what a founding doesn't write: " + c.names + w.existing(dir) + ". A founding that stopped part way is finished by running it again with the -spec and -invariant it was started with"
			if err.Error() != want {
				t.Errorf("new's refusal is\n%v\nwant\n%s", err, want)
			}
		})
	}
}

// A checkout with a commit that isn't a founding's isn't an empty project
// either, whatever its directory holds now and wherever the commit is: a
// file committed and since deleted, a branch other than the one it's on, a
// branch fetched and never checked out, a commit no branch names.
func TestNewRefusesACheckoutWithCommitsThatArentAFounding919(t *testing.T) {
	t.Parallel()
	commit := func(w *world, dir string) string {
		return w.mustGit(dir, "commit-tree", "-m", "A commit", w.mustGit(dir, "write-tree"))
	}
	for _, c := range []struct {
		name string
		make func(w *world, dir string)
	}{
		{"a file committed and since deleted", func(w *world, dir string) {
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
			w.write(filepath.Join(dir, "README.md"), "# Mine\n")
			w.commit(dir, "A README", "README.md")
			if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
				w.t.Fatal(err)
			}
		}},
		{"a commit on another branch than the one it's on", func(w *world, dir string) {
			w.mustGit(dir, "update-ref", "refs/heads/main", commit(w, dir))
			w.mustGit(dir, "symbolic-ref", "HEAD", "refs/heads/scratch")
		}},
		{"a branch fetched and never checked out", func(w *world, dir string) {
			w.mustGit(dir, "update-ref", "refs/remotes/origin/main", commit(w, dir))
		}},
		{"a commit no branch names", func(w *world, dir string) {
			w.mustGit(dir, "update-ref", "--no-deref", "HEAD", commit(w, dir))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			w.checkout(dir, "https://github.com/acme/widgets.git")
			c.make(w, dir)
			before, refs := w.tree(w.root), w.mustGit(dir, "for-each-ref")
			_, err := w.found(Options{})
			w.untouched(before, false)
			if want := dir + " already has commits, and they don't make a founded project" + w.existing(dir); err.Error() != want {
				t.Errorf("new's refusal is\n%v\nwant\n%s", err, want)
			}
			if w.mustGit(dir, "for-each-ref") != refs {
				t.Error("new moved a branch of a checkout it refused")
			}
		})
	}
}

// new founds a project in the repository's own checkout: one that fetches
// from the repository it was given and pushes to it, at every address it
// has for either. Any other is refused before anything is created or asked
// of GitHub, a founded project's too. An address is never repeated: it can
// carry a token.
func TestNewRefusesACheckoutThatFetchesOrPushesElsewhere919(t *testing.T) {
	t.Parallel()
	const own, other = "https://github.com/acme/widgets.git", "https://github.com/acme/gadgets.git"
	for _, c := range []struct {
		name string
		make func(w *world, dir string)
	}{
		{"another repository's checkout", func(w *world, dir string) { w.checkout(dir, other) }},
		{"a checkout with no origin", func(w *world, dir string) {
			w.checkout(dir, own)
			w.mustGit(dir, "remote", "remove", "origin")
		}},
		{"an origin that carries a token", func(w *world, dir string) {
			w.checkout(dir, "https://someone:not-a-real-token@github.com/acme/widgets.git")
		}},
		{"an origin that pushes elsewhere", func(w *world, dir string) {
			w.checkout(dir, own)
			w.mustGit(dir, "config", "remote.origin.pushurl", other)
		}},
		{"an origin that pushes with a token", func(w *world, dir string) {
			w.checkout(dir, own)
			w.mustGit(dir, "config", "remote.origin.pushurl", "https://someone:not-a-real-token@github.com/acme/widgets.git")
		}},
		{"an origin that pushes to a second place too", func(w *world, dir string) {
			w.checkout(dir, own)
			w.mustGit(dir, "remote", "set-url", "--add", "--push", "origin", own)
			w.mustGit(dir, "remote", "set-url", "--add", "--push", "origin", other)
		}},
		{"an origin that fetches from a second place too", func(w *world, dir string) {
			w.checkout(dir, own)
			w.mustGit(dir, "remote", "set-url", "--add", "origin", other)
		}},
		{"a founded project whose origin now pushes elsewhere", func(w *world, dir string) {
			if out, err := w.found(Options{}); err != nil {
				w.t.Fatalf("%v\n%s", err, out)
			}
			w.mustGit(dir, "config", "remote.origin.pushurl", other)
			// As if its push were still to be made.
			w.calls, w.pushed, w.body, w.empty = nil, nil, "", true
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.t = t
			dir := filepath.Join(w.root, "widgets")
			c.make(w, dir)
			before, existed := w.tree(w.root), w.exists
			out, err := w.found(Options{})
			w.untouched(before, existed)
			if want := dir + " is a git checkout whose origin isn't acme/widgets on GitHub, to fetch from and to push to. invariant new starts in an empty directory or in a fresh clone of the repository"; err.Error() != want {
				t.Errorf("new's refusal is\n%v\nwant\n%s", err, want)
			}
			if strings.Contains(err.Error()+out, "not-a-real-token") || w.ran("gh ") {
				t.Errorf("new repeated an address, or asked GitHub something before it refused:\n%s", strings.Join(w.calls, "\n"))
			}
		})
	}
	// A path that is a file is no directory to found in.
	w := newWorld(t)
	w.write(filepath.Join(w.root, "widgets"), "a file\n")
	before := w.tree(w.root)
	if _, err := w.found(Options{}); err == nil || !strings.Contains(err.Error(), "isn't a directory") {
		t.Errorf("new's answer to a file where the directory goes is %v", err)
	}
	w.untouched(before, false)
}

// The clone new makes is held to what a checkout that was there is. One
// that comes back with something in it, or from another repository, as a
// repository that was renamed can, gets nothing written to it.
func TestNewHoldsTheCloneItMakesToTheSame919(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		change func(w *world, dir string)
		want   string
	}{
		"something in it": {func(w *world, dir string) { w.write(filepath.Join(dir, "surprise.txt"), "not the founding's\n") }, "holds what a founding doesn't write: surprise.txt, so this isn't an empty project"},
		"another origin": {func(w *world, dir string) {
			w.mustGit(dir, "remote", "set-url", "origin", "https://github.com/acme/gadgets.git")
		}, "is a git checkout whose origin isn't acme/widgets on GitHub"},
	} {
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		w.before = func(cmd Command) {
			if cmd.Name == "git" && strings.Join(cmd.Args, " ") == "remote get-url --all origin" {
				c.change(w, dir)
			}
		}
		out, err := w.found(Options{})
		if err == nil || !strings.Contains(err.Error(), dir+" "+c.want) {
			t.Fatalf("with %s in the clone, new says: %v\n%s", name, err, out)
		}
		if w.ran("git symbolic-ref") || w.ran("git add") || w.ran("git commit") || w.ran("git push") || w.ran("gh api -X") || len(w.tree(dir)) > 1 {
			t.Errorf("with %s in the clone, new went on:\n%s", name, strings.Join(w.calls, "\n"))
		}
	}
}

// A repository on GitHub that already holds commits isn't an empty project,
// with no checkout of it or with a fresh one, and new creates nothing: it
// says to use init, in the repository's checkout. A founded checkout
// doesn't change that when the commits aren't its founding as it has seen
// them there.
func TestNewRefusesARepositoryThatAlreadyHoldsCommits919(t *testing.T) {
	t.Parallel()
	for _, checkout := range []string{"none", "one with nothing in it", "a founded one that never saw its founding there"} {
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		switch checkout {
		case "one with nothing in it":
			w.checkout(dir, "https://github.com/acme/widgets.git")
		case "a founded one that never saw its founding there":
			if out, err := w.found(Options{}); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			w.mustGit(dir, "update-ref", "-d", "refs/remotes/origin/main")
			w.calls, w.pushed, w.body = nil, nil, ""
		}
		w.exists, w.empty, w.mergeCommits, w.visibility = true, false, true, "public"
		before := w.tree(w.root)
		_, err := w.found(Options{})
		w.untouched(before, true)
		if want := "acme/widgets already holds commits on GitHub" + w.existing("path/to/your-repo"); err.Error() != want {
			t.Errorf("with %s for a checkout, new's refusal is\n%v\nwant\n%s", checkout, err, want)
		}
		if _, err := os.Lstat(dir); checkout == "none" && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("new made the directory of a project it refused: %v", err)
		}
	}
}

// A founded project is pushed from main, so new refuses one that's on
// another branch, before it pushes anything, and says to switch.
func TestNewRefusesAFoundedProjectThatIsntOnMain919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	w.mustGit(dir, "checkout", "--quiet", "-b", "setup")
	// As if its push were still to be made.
	w.calls, w.pushed, w.body, w.empty = nil, nil, "", true
	before, refs := w.tree(w.root), w.mustGit(dir, "for-each-ref")
	out, err := w.found(Options{})
	w.untouched(before, true)
	if want := dir + " isn't on main, the branch invariant new pushes. Switch it to main, then run it again:\n\n" + w.again(dir); err.Error() != want {
		t.Errorf("new's refusal is\n%v\nwant\n%s", err, want)
	}
	if founds(out) || w.mustGit(dir, "for-each-ref") != refs {
		t.Errorf("new founded a project from another branch, or moved one:\n%s", out)
	}
	w.mustGit(dir, "checkout", "--quiet", "main")
	if out, err := w.found(Options{}); err != nil || len(w.pushed) != 1 {
		t.Errorf("back on main, new: %v, %d pushes\n%s", err, len(w.pushed), out)
	}
}

// The founding's files are new's own, so it adds them whatever a person's
// ignore rules say of their names, in the checkout's own exclude file or in
// one for every checkout: a plain add would be refused, and the founding
// left half made.
func TestNewAddsItsOwnFilesWhateverGitIgnores919(t *testing.T) {
	t.Parallel()
	const ignored = "AGENTS.md\n*.md\n.invariant/\n.github/\ntools/\ngo.mod\n.gitignore\ndoc.go\n"
	for _, where := range []string{"the checkout's exclude file", "an excludes file for every checkout"} {
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		w.checkout(dir, "https://github.com/acme/widgets.git")
		if where == "the checkout's exclude file" {
			w.write(filepath.Join(dir, ".git", "info", "exclude"), ignored)
		} else {
			w.write(filepath.Join(w.root, "excludes"), ignored)
			w.mustGit(dir, "config", "core.excludesFile", filepath.Join(w.root, "excludes"))
		}
		out, err := w.found(Options{})
		if err != nil {
			t.Fatalf("with %s ignoring the founding's files: %v\n%s", where, err, out)
		}
		if committed := strings.Fields(w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD")); !slices.Equal(committed, slices.Sorted(slices.Values(strings.Fields(founded)))) {
			t.Errorf("with %s ignoring them, the founding's commit holds %q", where, committed)
		}
		if len(w.pushed) != 1 || !founds(out) {
			t.Errorf("with %s ignoring them, the founding didn't finish:\n%s", where, out)
		}
		w.contract(dir)
	}
}

// The founding's commit holds its files and nothing else that's staged: a
// file a person staged and took out of the directory stays staged, and out
// of the commit.
func TestNewLeavesWhatIsStagedOutOfItsCommit919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	w.checkout(dir, "https://github.com/acme/widgets.git")
	w.write(filepath.Join(dir, "notes.txt"), "not the founding's\n")
	w.mustGit(dir, "add", "notes.txt")
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	if committed := strings.Fields(w.mustGit(dir, "ls-tree", "-r", "--name-only", "HEAD")); !slices.Equal(committed, slices.Sorted(slices.Values(strings.Fields(founded)))) {
		t.Errorf("the founding's commit holds %q, want the files it wrote and no others", committed)
	}
	if status := w.mustGit(dir, "status", "--porcelain"); status != "AD notes.txt" {
		t.Errorf("the person's staged file is now %q, want it still staged", status)
	}
}

// Run a second time on a founding that finished, new changes nothing and
// says the project is already founded: it only reads, whatever its flags
// say now, and however far the project has moved on since. A pin that
// isn't the gate's is remarked on, and the gate is left as it is. So is a
// -spec that isn't the project's: it's said not to have been used.
func TestASecondRunChangesNothingAndSaysTheProjectIsFounded919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	reads := []string{
		"git remote get-url --all origin",
		"git remote get-url --push --all origin",
		"git ls-tree --name-only HEAD .github/workflows/ .invariant/repository.json",
		"git show HEAD:.github/workflows/gate.yml",
		"git symbolic-ref --short HEAD",
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
	const unused = "-spec wasn't used: the project is founded already, and its docs/PRD.md is its own to change."
	if !strings.Contains(out, unused) || w.tree(dir)["docs/PRD.md"] != "" {
		t.Errorf("the second run doesn't say its -spec wasn't used, or used it:\n%s", out)
	}
	if gate := w.tree(dir)[".github/workflows/gate.yml"]; !strings.Contains(gate, "ref: "+pin) {
		t.Errorf("the second run rewrote the gate's pin:\n%s", gate)
	}
	// Nor does work since: a README that's no longer the starter's, and
	// files a founding never wrote.
	w.write(filepath.Join(dir, "README.md"), "# widgets\n\nIt does things now.\n")
	w.write(filepath.Join(dir, "widget.go"), "package widgets\n")
	w.commit(dir, "Say what widgets does", "README.md", "widget.go")
	w.mustGit(dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	again(Options{})

	// A project founded with a spec: the same spec again is the one it
	// has, and nothing is said of it. Another isn't used, and that's said.
	w = newWorld(t)
	dir = filepath.Join(w.root, "widgets")
	spec := filepath.Join(w.root, "spec.md")
	w.write(spec, "# The spec\n")
	if out, err := w.found(Options{Spec: spec}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	if out := again(Options{Spec: spec}); strings.Contains(out, "-spec") {
		t.Errorf("the second run remarks on the spec the project was founded with:\n%s", out)
	}
	if out := again(Options{Spec: other}); !strings.Contains(out, unused) || w.tree(dir)["docs/PRD.md"] != "# The spec\n" {
		t.Errorf("the second run doesn't say another -spec wasn't used, or used it:\n%s", out)
	}
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
	_, err = w.found(Options{})
	w.untouched(before, false)
	if !strings.Contains(err.Error(), "has no git identity") {
		t.Errorf("in a checkout with no identity, new says: %v", err)
	}
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
		"widgets": "widgets", "Top-Hat": "tophat", "web3d": "web3d", "log.buffer_v2": "logbufferv2",
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
		_, err := w.found(Options{})
		w.untouched(before, false)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("with GitHub answering %q about the owner, new says: %v", said, err)
		}
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
			again := w.again(dir, "-spec", spec)
			if plan == "Team" {
				again = w.again(dir, "-public", "-spec", spec)
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
// says what main requires, and what a person adds to it and where, in
// words, since it has no call for that it has seen GitHub take.
func TestNewLeavesAMainThatIsProtectedAlready919(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, protection string
		requires         string // what new says main requires, or "" where that's the gate
	}{
		{"the gate, as a check of GitHub Actions, among others", `{"required_status_checks":{"strict":true,"contexts":[],"checks":[{"context":"lint","app_id":15368},{"context":"invariant/gate","app_id":15368}]},"required_pull_request_reviews":{"required_approving_review_count":2}}`, ""},
		{"the gate, as a context", `{"required_status_checks":{"strict":false,"contexts":["invariant/gate"]}}`, ""},
		{"another check", `{"required_status_checks":{"strict":true,"contexts":["lint"],"checks":[{"context":"lint","app_id":null}]},"required_pull_request_reviews":{"required_approving_review_count":2}}`, "lint"},
		{"two checks, one listed as a check alone", `{"required_status_checks":{"strict":false,"contexts":["lint"],"checks":[{"context":"lint","app_id":15368},{"context":"build","app_id":7}]}}`, "lint, build"},
		{"a check listed as a context alone", `{"required_status_checks":{"strict":false,"contexts":["lint"]}}`, "lint"},
		{"reviews and no check", `{"required_pull_request_reviews":{"required_approving_review_count":1},"enforce_admins":{"enabled":true}}`, "no status check"},
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
		// It gives no command for a protection it didn't set.
		if strings.Contains(out, "gh api") || strings.Contains(out, "EOF") {
			t.Errorf("%s: new printed a call for a person to make on a protection that isn't its own:\n%s", c.name, out)
		}
		if said := "main is protected already, and requires " + c.requires + ": invariant/gate isn't among its checks. invariant new doesn't replace a protection it didn't set.\n" +
			"The factory still merges only once the gate passes. A merge made by hand can skip it.\n" +
			"To have GitHub refuse those too, add the status check invariant/gate, from GitHub Actions, to the checks main requires: https://github.com/acme/widgets/settings/branches\n"; strings.Contains(out, said) != (c.requires != "") || c.requires == "" && strings.Contains(out, "main is protected already") {
			t.Errorf("%s: new said, of a main that requires %q:\n%s", c.name, c.requires, out)
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
		{"adding its files", func(c Command) bool { return c.Name == "git" && c.Args[0] == "add" }, []string{"gh repo create", "gh repo clone"}},
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
			spec := filepath.Join(w.root, "spec.md")
			w.write(spec, "# The spec\n")
			out, err := w.found(Options{Spec: spec})
			if !errors.Is(err, failed) || founds(out) || len(w.pushed) != 0 && c.name != "allowing merge commits" && c.name != "requiring the gate" {
				t.Fatalf("the first run: %v, %d pushes\n%s", err, len(w.pushed), out)
			}
			w.fails, w.calls = nil, nil
			out, err = w.found(Options{Spec: spec})
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

// A founding that stopped is finished by the command that started it. Run
// with another -invariant, or without its -spec, the files it left aren't
// the ones this run would write, so they're refused as any others are, and
// the refusal says how a founding that stopped is finished.
func TestARerunWithOtherFlagsIsntTheFoundingThatStopped919(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := filepath.Join(w.root, "widgets")
	spec := filepath.Join(w.root, "spec.md")
	w.write(spec, "# The spec\n")
	w.fails = func(c Command) error {
		if c.Name == "git" && c.Args[0] == "commit" {
			return errors.New("a hook refused the commit")
		}
		return nil
	}
	if out, err := w.found(Options{Spec: spec}); err == nil {
		t.Fatalf("the founding didn't stop:\n%s", out)
	}
	w.fails = nil
	const finished = ". A founding that stopped part way is finished by running it again with the -spec and -invariant it was started with"
	for _, c := range []struct {
		o     Options
		names string
	}{
		{Options{Spec: spec, Invariant: strings.Repeat("a", 40)}, ".github/workflows/gate.yml"},
		{Options{}, ".invariant/repository.json, AGENTS.md, README.md, SPEC.md, docs/"},
	} {
		before := w.tree(w.root)
		w.calls = nil
		_, err := w.found(c.o)
		if err == nil || !strings.Contains(err.Error(), dir+" holds what a founding doesn't write: "+c.names+", so this isn't an empty project") || !strings.HasSuffix(err.Error(), finished) {
			t.Errorf("run again with other flags, new says: %v", err)
		}
		if !reflect.DeepEqual(w.tree(w.root), before) || w.ran("git add") || w.ran("git commit") || w.ran("git push") {
			t.Errorf("run again with other flags, new went on:\n%s", strings.Join(w.calls, "\n"))
		}
	}
	if out, err := w.found(Options{Spec: spec}); err != nil || !strings.Contains(out, "Kept 10 of the founding's files") || !founds(out) {
		t.Errorf("run again as it was started: %v\n%s", err, out)
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

// A starter is complete or it isn't written: checks that would leave a
// requirement unresolved are an error before anything is created. Only the
// root's Go part is the starter's, so only its format check is settled: a
// second Go module among the files keeps its own unresolved.
func TestAStarterWithUnsettledChecksIsNeverWritten919(t *testing.T) {
	t.Parallel()
	files, err := starter("acme", "widgets", false)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := checks(files)
	if err != nil || !strings.Contains(string(contract), "./tools/check-format") || strings.Contains(string(contract), "unresolved") {
		t.Fatalf("the starter's own checks: %v\n%s", err, contract)
	}
	nested := append(slices.Clone(files), file{"cmd/tool/go.mod", []byte("module github.com/acme/widgets/cmd/tool\n\ngo 1.27\n"), 0o644})
	contract, err = checks(nested)
	if err == nil || !strings.Contains(err.Error(), "\n- cmd/tool (go) format: ") || strings.Contains(err.Error(), "\n- . (go) format") || contract != nil {
		t.Errorf("checks with a second module's format check unresolved: %v\n%s", err, contract)
	}
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
	w.untouched(before, true)
	if !strings.Contains(err.Error(), "the checks committed in "+dir+" don't load, so the project isn't founded") || !strings.Contains(err.Error(), "unresolved") || !strings.HasSuffix(err.Error(), "docs/factory.md#drafting-repository-checks says how a person completes "+repository.Path) {
		t.Fatalf("new's answer to committed checks that don't load is %v\n%s", err, out)
	}
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
// other: such a checkout has commits that aren't a founding's, and is
// refused.
func TestAProjectIsFoundedOnlyWithItsChecksAndACapableGate919(t *testing.T) {
	t.Parallel()
	workflow, err := setup.Workflow(pin)
	if err != nil {
		t.Fatal(err)
	}
	founding := func(t *testing.T) (*world, string) {
		w := newWorld(t)
		dir := filepath.Join(w.root, "widgets")
		if out, err := w.found(Options{}); err != nil {
			t.Fatalf("the founding failed: %v\n%s", err, out)
		}
		w.calls, w.pushed, w.body = nil, nil, ""
		return w, dir
	}
	for name, change := range map[string]func(w *world, dir string){
		"the checks alone": func(w *world, dir string) {
			w.mustGit(dir, "rm", "--quiet", ".github/workflows/gate.yml")
			w.mustGit(dir, "commit", "--quiet", "-m", "Remove the gate")
		},
		"the gate alone": func(w *world, dir string) {
			w.mustGit(dir, "rm", "--quiet", ".invariant/repository.json")
			w.mustGit(dir, "commit", "--quiet", "-m", "Remove the checks")
		},
		"another workflow at the gate's path": func(w *world, dir string) {
			w.write(filepath.Join(dir, ".github", "workflows", "gate.yml"), "name: something else\non: push\njobs: {}\n")
			w.commit(dir, "Not the gate", ".github/workflows/gate.yml")
		},
		"a gate whose job doesn't run its checks": func(w *world, dir string) {
			w.write(filepath.Join(dir, ".github", "workflows", "gate.yml"), "name: gate\non:\n  push:\n  pull_request:\njobs:\n  gate:\n    name: invariant/gate\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n")
			w.commit(dir, "Not the gate", ".github/workflows/gate.yml")
		},
	} {
		w, dir := founding(t)
		change(w, dir)
		before := w.tree(w.root)
		out, err := w.found(Options{})
		w.untouched(before, true)
		if founds(out) || !strings.Contains(err.Error(), "isn't an empty project") {
			t.Errorf("with %s, new says: %v\n%s", name, err, out)
		}
	}

	// The gate under another file's name is the gate all the same.
	w, dir := founding(t)
	w.mustGit(dir, "mv", ".github/workflows/gate.yml", ".github/workflows/ci.yml")
	w.mustGit(dir, "commit", "--quiet", "-m", "The gate, in ci.yml")
	w.mustGit(dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	if text := w.tree(dir)[".github/workflows/ci.yml"]; text != workflow {
		t.Fatalf("the gate didn't move:\n%s", text)
	}
	out, err := w.found(Options{})
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
	if err == nil || !strings.Contains(err.Error(), dir+" holds what a founding doesn't write: docs, so this isn't an empty project") {
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
