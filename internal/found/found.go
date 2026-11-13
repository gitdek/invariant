// Package found starts a new project for the factory (#919): its repository
// on GitHub, its checkout, a starter for its language, its checks and its
// gate. A person took about ten steps to do that by hand. Project takes
// them in one command, and ends by printing the ones only a person can take.
//
// It adopts what exists, an empty directory, the repository's own checkout
// or an empty repository, and never overwrites a file. Each step looks for
// its own result first, so a founding that stopped can be run again, and a
// second run of a finished one changes nothing.
package found

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

// Options say what to found, and where.
type Options struct {
	// Repo is the repository on GitHub, as owner/name.
	Repo string
	// Dir is the checkout's directory. Empty is the repository's name
	// beside the checkout that holds WorkDir, or inside WorkDir when no
	// checkout holds it.
	Dir string
	// WorkDir is where a relative Dir or Spec starts from: the process's
	// own directory when empty.
	WorkDir string
	// Public creates the repository public. It's private otherwise.
	Public bool
	// Language is the starter's language. Only go has one.
	Language string
	// Spec is a file to copy to docs/PRD.md, or empty for none.
	Spec string
	// Invariant is the full commit of Invariant the gate builds.
	Invariant string
	// Runner runs gh and git.
	Runner Runner
	// Out is where the founding says what it does.
	Out io.Writer
}

var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	namePattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
)

// founding is one run of Project.
type founding struct {
	Options
	dir     string // the checkout, as an absolute path
	changed bool   // whether this run created, wrote or set anything
}

// checkout is what a founding finds where the project's checkout goes.
type checkout struct {
	exists  bool // the directory is the repository's checkout already
	born    bool // it has a commit
	founded bool // and that commit holds the gate and the checks
}

// hosted is what a founding finds of the repository on GitHub.
type hosted struct {
	exists       bool
	empty        bool // it has no commit
	mergeCommits bool // it allows merge commits
	visibility   string
}

// Project founds the project o describes. It says the repository and the
// directory before it creates anything, and what it did as it goes.
func Project(ctx context.Context, o Options) error {
	owner, name, _ := strings.Cut(o.Repo, "/")
	if !ownerPattern.MatchString(owner) || !namePattern.MatchString(name) || strings.HasSuffix(strings.ToLower(name), ".git") {
		return fmt.Errorf("%q isn't a repository as OWNER/NAME, each starting and ending with a letter or a digit", o.Repo)
	}
	if o.Language != "go" {
		return fmt.Errorf("there's a starter for Go only, so invariant new can't start a %s project: -language takes go", o.Language)
	}
	if o.Runner == nil {
		return errors.New("a founding needs a runner for gh and git")
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	workflow, err := setup.Workflow(o.Invariant)
	if err != nil {
		return err
	}
	work := o.WorkDir
	if work == "" {
		if work, err = os.Getwd(); err != nil {
			return err
		}
	}
	if work, err = filepath.Abs(work); err != nil {
		return err
	}
	var spec []byte
	if o.Spec != "" {
		from := o.Spec
		if !filepath.IsAbs(from) {
			from = filepath.Join(work, from)
		}
		if spec, err = os.ReadFile(from); err != nil {
			return fmt.Errorf("reading the spec: %w", err)
		}
	}
	f := &founding{Options: o, dir: directory(work, o.Dir, name)}
	f.say("Repository: https://github.com/%s", o.Repo)
	f.say("Directory:  %s\n", f.dir)

	// What is there already, read before anything is created, so a refusal
	// leaves nothing behind.
	local, err := f.local(ctx)
	if err != nil {
		return err
	}
	remote, err := f.hosted(ctx)
	if err != nil {
		return err
	}
	if remote.exists && !remote.empty && !local.exists {
		return fmt.Errorf("%s already holds commits, so there's no project to start, and %s isn't its checkout. To work on it, clone it. To set up a repository that has no gate yet, run invariant init in its checkout", o.Repo, f.dir)
	}
	var files []file
	if !local.founded {
		if files, err = starter(owner, name, spec != nil || f.has(specPath)); err != nil {
			return err
		}
		if spec != nil {
			files = append(files, file{specPath, spec, 0o644})
		}
		files = append(files, file{setup.WorkflowPath, []byte(workflow), 0o644})
		if err := blocked(f.dir, files); err != nil {
			return err
		}
	}

	private := !o.Public
	if remote.exists {
		private = remote.visibility != "public"
		kept := ""
		if o.Public && private {
			kept = fmt.Sprintf(" It stays %s: -public applies only to a repository this command creates.", remote.visibility)
		}
		f.say("%s is on GitHub already%s: adopted as it is.%s", o.Repo, map[bool]string{true: ", with nothing in it"}[remote.empty], kept)
	} else {
		visibility := map[bool]string{true: "public", false: "private"}[o.Public]
		if _, err := f.gh(ctx, "repo", "create", o.Repo, "--"+visibility); err != nil {
			return err
		}
		remote = hosted{exists: true, empty: true, visibility: visibility}
		f.did("Created %s on GitHub, %s.", o.Repo, visibility)
	}
	if local.exists {
		f.say("%s is its checkout already: adopted as it is.", f.dir)
	} else {
		if _, err := f.gh(ctx, "repo", "clone", o.Repo, f.dir); err != nil {
			return err
		}
		f.did("Cloned it into %s.", f.dir)
	}

	committed := false
	if !local.founded {
		if err := f.start(ctx, local, files); err != nil {
			return err
		}
		committed = true
	}
	// The loader CI and the factory read the checks with. A file it
	// refuses is never pushed as a founding's, or called founded.
	if _, err := repository.Load(ctx, f.dir, "HEAD"); err != nil {
		return fmt.Errorf("the checks committed in %s don't load, so the project isn't founded: %w. Complete %s by hand, as https://github.com/gitdek/invariant/blob/main/docs/factory.md#drafting-repository-checks says, and commit it", f.dir, err, repository.Path)
	}
	if committed || remote.empty || !f.pushed(ctx) {
		if _, err := f.git(ctx, "push", "-u", "origin", "main"); err != nil {
			return err
		}
		f.did("Pushed main. The gate's first run is at https://github.com/%s/actions.", o.Repo)
	}
	if !remote.mergeCommits {
		if _, err := f.gh(ctx, "api", "-X", "PATCH", "repos/"+o.Repo, "-F", "allow_merge_commit=true"); err != nil {
			return err
		}
		f.did("Allowed merge commits, which the factory merges with.")
	}
	if err := f.requireGate(ctx); err != nil {
		return err
	}

	if f.changed {
		f.say("\n%s is founded.", o.Repo)
	} else {
		f.say("\n%s is already founded: nothing changed.", o.Repo)
	}
	f.say("%s", left(o.Repo, private, f.has(specPath)))
	return nil
}

// pushed says whether the founding's commit has reached GitHub, as far as
// the checkout knows: whether main as it last saw it there holds the gate
// and the checks. A push that failed leaves it behind, so the next run
// pushes, and a project that has moved on since keeps both files, so a
// later run doesn't push whatever else the checkout holds.
func (f *founding) pushed(ctx context.Context) bool {
	_, both := f.holds(ctx, "refs/remotes/origin/main")
	return both
}

// holds says whether rev names a commit in the checkout, and whether that
// commit holds both the gate and the checks: what makes a project founded.
func (f *founding) holds(ctx context.Context, rev string) (commit, both bool) {
	held, err := f.git(ctx, "ls-tree", "--name-only", rev, "--", setup.WorkflowPath, repository.Path)
	if err != nil {
		return false, false
	}
	names := strings.Split(strings.TrimSpace(held), "\n")
	return true, slices.Contains(names, setup.WorkflowPath) && slices.Contains(names, repository.Path)
}

// has says whether the checkout holds a file at path.
func (f *founding) has(path string) bool {
	info, err := os.Lstat(filepath.Join(f.dir, filepath.FromSlash(path)))
	return err == nil && info.Mode().IsRegular()
}

// say tells the person one thing, and did tells them one thing this run
// changed.
func (f *founding) say(format string, args ...any) {
	fmt.Fprintf(f.Out, format+"\n", args...)
}

func (f *founding) did(format string, args ...any) {
	f.changed = true
	f.say(format, args...)
}

func (f *founding) run(ctx context.Context, c Command) (string, error) {
	out, err := f.Runner.Run(ctx, c)
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", c, err)
	}
	return string(out), nil
}

// git runs git in the checkout, and gh runs gh, which names the repository
// in its arguments.
func (f *founding) git(ctx context.Context, args ...string) (string, error) {
	return f.run(ctx, Command{Dir: f.dir, Name: "git", Args: args})
}

func (f *founding) gh(ctx context.Context, args ...string) (string, error) {
	return f.run(ctx, Command{Name: "gh", Args: args})
}

// directory is where the checkout goes: dir, from the working directory
// when it's relative, or the repository's name beside the checkout the
// working directory is in. Outside any checkout, it's that name inside the
// working directory, where a clone would go.
func directory(work, dir, name string) string {
	switch {
	case dir == "":
		dir = filepath.Join(work, name)
		for d := work; ; d = filepath.Dir(d) {
			if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
				dir = filepath.Join(filepath.Dir(d), name)
				break
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	case !filepath.IsAbs(dir):
		dir = filepath.Join(work, dir)
	}
	return filepath.Clean(dir)
}

// local reads what's where the checkout goes. A directory that isn't
// there, or is empty, is free to clone into. One that holds the
// repository's checkout is adopted. Anything else is refused: it isn't the
// project's, and a founding writes nowhere else.
func (f *founding) local(ctx context.Context) (checkout, error) {
	info, err := os.Lstat(f.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return checkout{}, nil
	}
	if err != nil {
		return checkout{}, err
	}
	if !info.IsDir() {
		return checkout{}, fmt.Errorf("%s is there already, and isn't a directory", f.dir)
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return checkout{}, err
	}
	if len(entries) == 0 {
		return checkout{}, nil
	}
	const takes = "invariant new starts in an empty directory or in the repository's own checkout"
	if _, err := os.Lstat(filepath.Join(f.dir, ".git")); err != nil {
		return checkout{}, fmt.Errorf("%s isn't empty, and isn't a checkout of %s: it holds %s. %s", f.dir, f.Repo, entries[0].Name(), takes)
	}
	// The origin's address is compared and never printed: one can carry a
	// token.
	origin, err := f.git(ctx, "remote", "get-url", "origin")
	if err != nil || !strings.EqualFold(setup.GitHubRepo(strings.TrimSpace(origin)), f.Repo) {
		return checkout{}, fmt.Errorf("%s is a git checkout whose origin isn't %s on GitHub. %s", f.dir, f.Repo, takes)
	}
	c := checkout{exists: true}
	// A clone of an empty repository has no commit yet.
	c.born, c.founded = f.holds(ctx, "HEAD")
	if c.born && !c.founded {
		// The founding's commit goes on main, and main is what it pushes.
		if branch, err := f.git(ctx, "symbolic-ref", "--short", "HEAD"); err != nil || strings.TrimSpace(branch) != "main" {
			return c, fmt.Errorf("%s has commits and isn't on main, the branch invariant new commits to and pushes. Switch it to main, then run the same command again", f.dir)
		}
	}
	return c, nil
}

// hosted reads the repository from GitHub, through gh. One GitHub doesn't
// find is one to create. Any other failure is an error, since creating a
// repository on a guess could collide with one that's there.
func (f *founding) hosted(ctx context.Context) (hosted, error) {
	out, err := f.gh(ctx, "repo", "view", f.Repo, "--json", "isEmpty,mergeCommitAllowed,visibility")
	if err != nil {
		if strings.Contains(err.Error(), "Could not resolve to a Repository") || strings.Contains(err.Error(), "(HTTP 404)") {
			return hosted{}, nil
		}
		return hosted{}, fmt.Errorf("can't tell whether %s is on GitHub: %w", f.Repo, err)
	}
	var v struct {
		IsEmpty            bool   `json:"isEmpty"`
		MergeCommitAllowed bool   `json:"mergeCommitAllowed"`
		Visibility         string `json:"visibility"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return hosted{}, fmt.Errorf("gh's answer about %s: %w", f.Repo, err)
	}
	return hosted{exists: true, empty: v.IsEmpty, mergeCommits: v.MergeCommitAllowed, visibility: strings.ToLower(v.Visibility)}, nil
}

// start makes the founding's commit in the checkout: the starter, the gate
// and the checks. It stops before it writes anything when the checkout has
// no git identity, since a commit needs one.
func (f *founding) start(ctx context.Context, local checkout, files []file) error {
	if !local.born {
		// A clone of an empty repository is on whatever branch git's own
		// configuration names.
		if _, err := f.git(ctx, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
			return err
		}
	}
	var unset []string
	for _, key := range []string{"user.name", "user.email"} {
		if value, err := f.git(ctx, "config", key); err != nil || strings.TrimSpace(value) == "" {
			unset = append(unset, key)
		}
	}
	if len(unset) > 0 {
		return fmt.Errorf(`%[1]s has no git identity, so nothing can be committed there: %[2]s. Set one for this checkout:

git -C %[3]s config user.name "Your Name"
git -C %[3]s config user.email "you@example.com"

or for every checkout, with git config --global. Then run the same command again: it carries on from here`,
			f.dir, strings.Join(unset, " and ")+map[bool]string{true: " isn't set", false: " aren't set"}[len(unset) == 1], shell(f.dir))
	}
	wrote, err := place(f.dir, files)
	if err != nil {
		return err
	}
	// The checks are drafted from the checkout, so they come once the
	// starter and the gate are in it, as init drafts them.
	contract, named, err := checks(f.dir)
	if err != nil {
		return err
	}
	files = append(files, file{repository.Path, contract, 0o644})
	more, err := place(f.dir, files[len(files)-1:])
	if err != nil {
		return err
	}
	if wrote = append(wrote, more...); len(wrote) > 0 {
		f.did("Wrote %s.", strings.Join(wrote, ", "))
	}
	if kept := len(files) - len(wrote); kept > 0 {
		f.say("Kept %d of the founding's files, there already with the same contents.", kept)
	}
	f.say("The gate builds Invariant at %s, and %s declares every check, with nothing unresolved.", f.Invariant[:12], repository.Path)
	// The founding's own files are committed, and any other the checks
	// name, which a checkout it adopted can hold: nothing else, whatever
	// is staged there.
	var paths []string
	for _, file := range files {
		paths = append(paths, file.path)
	}
	for _, name := range named {
		if !slices.Contains(paths, name) {
			paths = append(paths, name)
		}
	}
	if _, err := f.git(ctx, append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	message := "Start " + f.Repo[strings.Index(f.Repo, "/")+1:] + ": a Go module, its checks and Invariant's gate"
	if _, err := f.git(ctx, append([]string{"commit", "--quiet", "-m", message, "--"}, paths...)...); err != nil {
		return err
	}
	f.did("Committed them to main.")
	return nil
}

// planRefusal is how GitHub says the account's plan doesn't protect a
// private repository's branches: Pro for a person's account, Team for an
// organization's.
var planRefusal = regexp.MustCompile(`Upgrade to GitHub \w+ or make this repository public`)

// requireGate has GitHub refuse any merge to main that invariant/gate
// didn't pass, with the call init prints for a person. A branch that
// requires the gate already is left as it is, whatever else a person has
// set there. Where the account's plan refuses the requirement, the founding
// says what that means and carries on.
func (f *founding) requireGate(ctx context.Context) error {
	protection := "repos/" + f.Repo + "/branches/main/protection"
	if out, err := f.gh(ctx, "api", protection); err == nil {
		var p struct {
			Required struct {
				Contexts []string `json:"contexts"`
				Checks   []struct {
					Context string `json:"context"`
				} `json:"checks"`
			} `json:"required_status_checks"`
		}
		if json.Unmarshal([]byte(out), &p) == nil {
			if slices.Contains(p.Required.Contexts, "invariant/gate") {
				return nil
			}
			for _, check := range p.Required.Checks {
				if check.Context == "invariant/gate" {
					return nil
				}
			}
		}
	}
	_, err := f.run(ctx, Command{Name: "gh", Args: []string{"api", "-X", "PUT", protection, "--input", "-"}, Stdin: setup.Protection})
	switch {
	case err == nil:
		f.did("Required invariant/gate on main: GitHub now refuses any merge the gate didn't pass.")
	case strings.Contains(err.Error(), "(HTTP 403)") && planRefusal.MatchString(err.Error()):
		f.say(`GitHub won't require invariant/gate on main: this account's plan doesn't protect the branches of a private repository.
The factory still merges only once the gate passes. A merge made by hand can skip it.
To have GitHub refuse those too, make the repository public, at https://github.com/%[1]s/settings, or change the account's plan. Then run invariant new %[1]s again.`, f.Repo)
	default:
		return fmt.Errorf("requiring invariant/gate on main: %w", err)
	}
	return nil
}

// left is what a founding leaves for a person, as commands and links: the
// App's access, a watcher, the dashboard and the first issue. None of them
// can be read with gh's own login, so a second run prints them too.
func left(repo string, private, spec bool) string {
	setupGit, shown := "", ""
	if private {
		setupGit = " The watcher reads the repository with your git credentials, so run gh auth setup-git first."
		shown = " Its public page shows a repository's issue titles and charts, so add a private one only when that's meant."
	}
	issue := "Describe what to build and what must always be true of it, and comment /invariant solve."
	if spec {
		issue = "Name the part of " + specPath + " to build first, link the file, and comment /invariant plan: the factory proposes a plan of issues for a writer to ratify."
	}
	return fmt.Sprintf(`
Left for a person:

1. Give the factory's App access to %[1]s, if the factory acts as an App that's installed on selected repositories. Add it under Repository access in the installation's settings, at https://github.com/settings/installations. https://github.com/gitdek/invariant/blob/main/docs/factory-app.md#install-it has the steps.

2. Start a watcher. It runs its coding agent on your account:

invariant watch -repo %[1]s -projects invariant -language go

Add -app-id with the App's ID to have it act as the App.%[2]s

3. Add the repository to the dashboard: pass -repo %[1]s to invariant dashboard, beside the ones it shows.%[3]s

4. Open the first issue, at https://github.com/%[1]s/issues/new. %[4]s`, repo, setupGit, shown, issue)
}

// shell is s as one word of a shell command.
func shell(s string) string {
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+:@%", r))
	}) < 0 && s != "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
