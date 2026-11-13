// Package found starts a new project for the factory (#919): its repository
// on GitHub, its checkout, a starter for its language, its checks and its
// gate. A person took about ten steps to do that by hand. Project takes
// them in one command, and ends by printing the ones only a person can take.
//
// It adopts what exists, an empty directory, the repository's own checkout
// or an empty repository, and never overwrites a file. Everything that can
// refuse a founding is read first, from the directory and the repository as
// they are, so a refusal leaves nothing behind. Each step then looks for
// its own result, so a founding that stopped can be run again, and a second
// run of a finished one changes nothing.
package found

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
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
	owner, name string
	dir         string // the checkout, as an absolute path
	spec        string // the spec's file, as an absolute path, or ""
	changed     bool   // whether this run created, wrote or set anything
}

// checkout is what a founding finds where the project's checkout goes.
type checkout struct {
	exists  bool   // the directory is the repository's checkout already
	born    bool   // it has a commit
	founded bool   // and that commit holds the checks and a capable gate
	pin     string // the commit of Invariant that gate builds, where it says
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
	if kept := device(name); kept != "" {
		return fmt.Errorf("a Go module can't be named %s: Go refuses %s in a module's path, a file name Windows keeps for itself, so the starter wouldn't build. Give the repository another name", name, kept)
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
	f := &founding{Options: o, owner: owner, name: name, dir: directory(work, o.Dir, name)}
	var spec []byte
	if o.Spec != "" {
		if f.spec = o.Spec; !filepath.IsAbs(f.spec) {
			f.spec = filepath.Join(work, f.spec)
		}
		if spec, err = os.ReadFile(f.spec); err != nil {
			return fmt.Errorf("reading the spec: %w", err)
		}
	}
	f.say("Repository: https://github.com/%s", o.Repo)
	f.say("Directory:  %s\n", f.dir)

	// Everything that can refuse the founding is read here, from the
	// directory and the repository as they are, before the first thing is
	// created, cloned or written.
	local, err := f.local(ctx)
	if err != nil {
		return err
	}
	organization, err := f.organization(ctx)
	if err != nil {
		return err
	}
	remote, err := f.hosted(ctx)
	if err != nil {
		return err
	}
	switch {
	case remote.exists && !remote.empty && !local.exists:
		return fmt.Errorf("%s already holds commits, so there's no project to start, and %s isn't its checkout. To work on it, clone it. To set up a repository that has no gate yet, run invariant init in its checkout", o.Repo, f.dir)
	case remote.exists && !remote.empty && !local.born:
		return fmt.Errorf("%s already holds commits, and its checkout in %s has none of them, so a commit made there couldn't be pushed. Pull them first, with git -C %s pull origin main, then run it again:\n\n%s", o.Repo, f.dir, shell(f.dir), f.again())
	}
	var files []file
	if local.founded {
		if err := f.loads(ctx); err != nil {
			return err
		}
	} else if files, err = f.plan(ctx, local, spec, workflow); err != nil {
		return err
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
	if local.founded {
		if local.pin != "" && local.pin != o.Invariant {
			f.say("Its gate builds Invariant at %s, not at %s, the commit -invariant names. invariant new leaves a founded project's gate as it is.", local.pin[:12], o.Invariant[:12])
		}
	} else {
		if err := f.start(ctx, local, files); err != nil {
			return err
		}
		// The founding's own commit is read as CI will read it, before
		// it's pushed.
		if err := f.loads(ctx); err != nil {
			return err
		}
		committed = true
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
	f.say("%s", left(o.Repo, organization, private, f.has(specPath)))
	return nil
}

// again is the command that runs this founding again, with every flag that
// decides what it does, so it can be pasted from anywhere and by a CLI built
// from another commit.
func (f *founding) again() string {
	words := []string{"invariant", "new", f.Repo, "-dir", shell(f.dir)}
	if f.Public {
		words = append(words, "-public")
	}
	if f.spec != "" {
		words = append(words, "-spec", shell(f.spec))
	}
	return strings.Join(append(words, "-invariant", f.Invariant), " ")
}

// loads reads the checkout's committed checks with the loader CI and the
// factory use. Checks it refuses are never pushed as a founding's, and
// their project is never called founded.
func (f *founding) loads(ctx context.Context) error {
	if _, err := repository.Load(ctx, f.dir, "HEAD"); err != nil {
		return fmt.Errorf("the checks committed in %s don't load, so the project isn't founded: %w. Complete %s by hand, as https://github.com/gitdek/invariant/blob/main/docs/factory.md#drafting-repository-checks says, commit it, then run it again:\n\n%s", f.dir, err, repository.Path, f.again())
	}
	return nil
}

// pushed says whether the founding has reached GitHub, as far as the
// checkout knows: whether main as it last saw it there is founded. A push
// that failed leaves it behind, so the next run pushes, and a project that
// has moved on since stays founded, so a later run doesn't push whatever
// else the checkout holds.
func (f *founding) pushed(ctx context.Context) bool {
	_, founded, _ := f.read(ctx, "refs/remotes/origin/main")
	return founded
}

// held is the workflows a commit holds, as setup's check reads a
// repository's. Merge commits are the repository's setting, read from
// GitHub, so they aren't this check's to refuse.
type held map[string]string

func (h held) Workflows(context.Context, string) (map[string]string, error) { return h, nil }
func (held) MergeCommits(context.Context) (bool, error)                     { return true, nil }

// read says whether rev names a commit in the checkout, and whether that
// commit makes a project founded: it holds the checks, and a workflow whose
// invariant/gate job is one the factory will merge on, as the watcher's own
// check of a repository reads it. pin is the commit of Invariant that gate
// builds, where it says.
func (f *founding) read(ctx context.Context, rev string) (commit, founded bool, pin string) {
	listed, err := f.git(ctx, "ls-tree", "--name-only", rev, path.Dir(setup.WorkflowPath)+"/", repository.Path)
	if err != nil {
		return false, false, ""
	}
	names := strings.Split(strings.TrimSpace(listed), "\n")
	workflows := held{}
	for _, name := range names {
		// GitHub Actions runs the .yml and .yaml files there, and nothing else.
		if name == repository.Path || !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		if text, err := f.git(ctx, "show", rev+":"+name); err == nil {
			workflows[name] = text
		}
	}
	gates := setup.RepositoryGates(workflows)
	for _, name := range slices.Sorted(maps.Keys(gates)) {
		if pin = setup.Pinned(gates[name]); pin != "" {
			break
		}
	}
	capable := setup.CheckRepository(ctx, workflows, f.Repo, "main", true) == nil
	return true, capable && slices.Contains(names, repository.Path), pin
}

// has says whether the checkout holds a file at name.
func (f *founding) has(name string) bool {
	return regular(filepath.Join(f.dir, filepath.FromSlash(name)))
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
// repository's checkout is adopted, on main: main is the branch a founding
// commits to and pushes. Anything else is refused: it isn't the project's,
// and a founding writes nowhere else.
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
	// A detached HEAD is on no branch, and a clone of an empty repository
	// has a branch with no commit yet.
	branch, err := f.git(ctx, "symbolic-ref", "--short", "HEAD")
	if branch = strings.TrimSpace(branch); err != nil {
		branch = "no branch"
	}
	c.born, c.founded, c.pin = f.read(ctx, "HEAD")
	if branch != "main" {
		// A branch with no commit yet is moved to main, unless main has
		// commits of its own for the founding to land on.
		elsewhere := c.born
		if !c.born {
			_, err := f.git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/main")
			elsewhere = err == nil
		}
		if elsewhere {
			return c, fmt.Errorf("%s is on %s, not on main, the branch invariant new commits to and pushes. Switch it to main, then run it again:\n\n%s", f.dir, branch, f.again())
		}
	}
	return c, nil
}

// organization reads who the repository's owner is on GitHub: a person's
// account or an organization, whose App installations are on another page.
// An owner GitHub doesn't know stops the founding before it creates
// anything.
func (f *founding) organization(ctx context.Context) (bool, error) {
	kind, err := f.gh(ctx, "api", "users/"+f.owner, "--jq", ".type")
	if err != nil {
		if strings.Contains(err.Error(), "(HTTP 404)") {
			return false, fmt.Errorf("GitHub has no account or organization named %s", f.owner)
		}
		return false, fmt.Errorf("can't read %s on GitHub: %w", f.owner, err)
	}
	return strings.TrimSpace(kind) == "Organization", nil
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

// plan is the files a founding puts in the checkout: the starter, the spec,
// the gate and the checks. It refuses, before anything is created or
// written, whatever would stop them going in: a file with other contents, a
// draft with requirements a person has to settle, changes of the checkout's
// own that the founding's commit would sweep in or leave out, and a
// checkout that can't commit.
func (f *founding) plan(ctx context.Context, local checkout, spec []byte, workflow string) ([]file, error) {
	files, err := starter(f.owner, f.name, spec != nil || f.has(specPath))
	if err != nil {
		return nil, err
	}
	if spec != nil {
		files = append(files, file{specPath, spec, 0o644})
	}
	files = append(files, file{setup.WorkflowPath, []byte(workflow), 0o644})
	if err := blocked(f.dir, files, f.again()); err != nil {
		return nil, err
	}
	if err := clear(f.dir, repository.Path, f.again()); err != nil {
		return nil, err
	}
	base := ""
	if local.exists {
		base = f.dir
	}
	contract, named, unsettled, err := checks(base, files)
	if err != nil {
		return nil, err
	}
	if len(unsettled) > 0 {
		return nil, fmt.Errorf(`the checks drafted for %[1]s leave requirements invariant new can't settle, so it created and wrote nothing:
- %[2]s
Set this project up by hand instead. Run invariant init -repo %[3]s -invariant %[4]s %[5]s, complete %[6]s, and commit it on main with %[7]s and every file the checks name. Then run it again, to push main and finish the founding:

%[8]s`, f.dir, strings.Join(unsettled, "\n- "), f.Repo, f.Invariant, shell(f.dir), repository.Path, setup.WorkflowPath, f.again())
	}
	files = append(files, file{repository.Path, contract, 0o644})
	if err := blocked(f.dir, files[len(files)-1:], f.again()); err != nil {
		return nil, err
	}
	if local.exists {
		if err := f.clean(ctx, files, named); err != nil {
			return nil, err
		}
		if err := f.identity(ctx); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// clean refuses a checkout whose own changes the founding's commit would
// touch. The commit holds the founding's files and nothing else, so changes
// a person has staged stay out of it, and it isn't made around them. And
// the checks name files beyond the founding's, as an agent's rules or a
// lockfile the checkout holds: one that isn't committed as it stands would
// be named by checks whose commit lacks it, and it's never the founding's
// to add.
func (f *founding) clean(ctx context.Context, files []file, named []string) error {
	own := map[string]bool{}
	for _, file := range files {
		own[file.path] = true
	}
	changed, err := f.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=no")
	if err != nil {
		return err
	}
	var staged []string
	for _, entry := range status(changed) {
		if entry.state[0] != ' ' && !own[entry.path] {
			staged = append(staged, entry.path)
		}
	}
	if len(staged) > 0 {
		return fmt.Errorf("%s has changes of its own staged: %s. invariant new commits the files it writes and nothing else, and doesn't commit around what's staged. Commit or unstage them, then run it again:\n\n%s", f.dir, strings.Join(staged, ", "), f.again())
	}
	var others []string
	for _, name := range named {
		if !own[name] && !slices.Contains(others, name) {
			others = append(others, name)
		}
	}
	if len(others) == 0 {
		return nil
	}
	args := []string{"status", "--porcelain=v1", "-z", "--ignored", "--untracked-files=all", "--"}
	for _, name := range others {
		args = append(args, ":(literal)"+name)
	}
	listed, err := f.git(ctx, args...)
	if err != nil {
		return err
	}
	var loose []string
	for _, entry := range status(listed) {
		how := "changed"
		switch entry.state {
		case "??":
			how = "untracked"
		case "!!":
			how = "ignored"
		}
		loose = append(loose, fmt.Sprintf("%s (%s)", entry.path, how))
	}
	if len(loose) > 0 {
		return fmt.Errorf("the checks for %s would name files it holds that aren't committed as they stand: %s. invariant new commits the files it writes and nothing else, so it never adds them. Commit or remove each, then run it again:\n\n%s", f.dir, strings.Join(loose, ", "), f.again())
	}
	return nil
}

// change is one path git status lists, with its two-letter state.
type change struct{ state, path string }

// status reads git status --porcelain=v1 -z. A rename or a copy is followed
// by the path it came from, which isn't an entry of its own.
func status(listed string) []change {
	var changes []change
	fields := strings.Split(listed, "\x00")
	for i := 0; i < len(fields); i++ {
		if len(fields[i]) < 4 {
			continue
		}
		c := change{fields[i][:2], fields[i][3:]}
		if c.state[0] == 'R' || c.state[0] == 'C' {
			i++
		}
		changes = append(changes, c)
	}
	return changes
}

// identity refuses a checkout that can't commit: one with no user.name or
// no user.email. It says how to set them.
func (f *founding) identity(ctx context.Context) error {
	var unset []string
	for _, key := range []string{"user.name", "user.email"} {
		if value, err := f.git(ctx, "config", key); err != nil || strings.TrimSpace(value) == "" {
			unset = append(unset, key)
		}
	}
	if len(unset) == 0 {
		return nil
	}
	return fmt.Errorf(`%[1]s has no git identity, so nothing can be committed there: %[2]s. Set one for this checkout:

git -C %[3]s config user.name "Your Name"
git -C %[3]s config user.email "you@example.com"

or for every checkout, with git config --global. Then run it again, and it carries on from here:

%[4]s`, f.dir, strings.Join(unset, " and ")+map[bool]string{true: " isn't set", false: " aren't set"}[len(unset) == 1], shell(f.dir), f.again())
}

// start writes the founding's files into the checkout and commits them, and
// nothing else, on main.
func (f *founding) start(ctx context.Context, local checkout, files []file) error {
	if !local.born {
		// A clone of an empty repository is on whatever branch git's own
		// configuration names.
		if _, err := f.git(ctx, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
			return err
		}
	}
	if !local.exists {
		// A checkout this run cloned couldn't be asked any sooner.
		if err := f.identity(ctx); err != nil {
			return err
		}
	}
	wrote, err := place(f.dir, files, f.again())
	if err != nil {
		return err
	}
	if len(wrote) > 0 {
		f.did("Wrote %s.", strings.Join(wrote, ", "))
	}
	if kept := len(files) - len(wrote); kept > 0 {
		f.say("Kept %d of the founding's files, there already with the same contents.", kept)
	}
	f.say("The gate builds Invariant at %s, and %s declares every check, with nothing unresolved.", f.Invariant[:12], repository.Path)
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.path
	}
	if _, err := f.git(ctx, append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	message := "Start " + f.name + ": a Go module, its checks and Invariant's gate"
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

// protection is main's protection as GitHub shows it: the checks it
// requires, each with the App that must report it, when it names one.
type protection struct {
	Required *struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
			App     *int64 `json:"app_id"`
		} `json:"checks"`
	} `json:"required_status_checks"`
}

// requireGate has GitHub refuse any merge to main that invariant/gate
// didn't pass, with the call init prints for a person. It makes that call
// only where GitHub says plainly that main isn't protected, since the call
// replaces whatever protection is there. A branch that requires the gate
// already is left as it is. One that's protected without it is left too,
// with the command that adds the gate and keeps the rest. Where the
// account's plan refuses protection, the founding says what that means and
// carries on. A read that fails any other way stops it: nothing says what
// the call would replace.
func (f *founding) requireGate(ctx context.Context) error {
	path := "repos/" + f.Repo + "/branches/main/protection"
	refused := func() {
		f.say(`GitHub won't require invariant/gate on main: this account's plan doesn't protect the branches of a private repository.
The factory still merges only once the gate passes. A merge made by hand can skip it.
To have GitHub refuse those too, make the repository public, at https://github.com/%s/settings, or change the account's plan. Then run it again:

%s`, f.Repo, f.again())
	}
	byPlan := func(err error) bool {
		return strings.Contains(err.Error(), "(HTTP 403)") && planRefusal.MatchString(err.Error())
	}
	out, err := f.gh(ctx, "api", path)
	switch {
	case err == nil:
		var p protection
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			return fmt.Errorf("GitHub's answer about main's protection can't be read, so nothing there was changed: %w. Run it again:\n\n%s", err, f.again())
		}
		return f.protected(p)
	case byPlan(err):
		refused()
		return nil
	case !strings.Contains(err.Error(), "(HTTP 404)") || !strings.Contains(err.Error(), "Branch not protected"):
		return fmt.Errorf("reading main's protection, so nothing there was changed: %w. Run it again:\n\n%s", err, f.again())
	}
	_, err = f.run(ctx, Command{Name: "gh", Args: []string{"api", "-X", "PUT", path, "--input", "-"}, Stdin: setup.Protection})
	switch {
	case err == nil:
		f.did("Required invariant/gate on main: GitHub now refuses any merge the gate didn't pass.")
	case byPlan(err):
		refused()
	default:
		return fmt.Errorf("requiring invariant/gate on main: %w", err)
	}
	return nil
}

// protected answers a main that is protected already. Requiring the gate
// there is a person's change to make: it's their protection.
func (f *founding) protected(p protection) error {
	type check struct {
		Context string `json:"context"`
		App     int64  `json:"app_id"`
	}
	body := struct {
		Strict bool    `json:"strict"`
		Checks []check `json:"checks"`
	}{Checks: []check{}}
	var requires []string
	if p.Required != nil {
		body.Strict = p.Required.Strict
		for _, c := range p.Required.Checks {
			// No App named is any App, which the call that sets checks
			// writes as -1.
			app := int64(-1)
			if c.App != nil {
				app = *c.App
			}
			body.Checks = append(body.Checks, check{c.Context, app})
			requires = append(requires, c.Context)
		}
		for _, context := range p.Required.Contexts {
			if !slices.Contains(requires, context) {
				body.Checks = append(body.Checks, check{context, -1})
				requires = append(requires, context)
			}
		}
	}
	if slices.Contains(requires, "invariant/gate") {
		return nil
	}
	body.Checks = append(body.Checks, check{"invariant/gate", 15368})
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	what := "no check"
	if len(requires) > 0 {
		what = strings.Join(requires, ", ")
	}
	f.say(`main is protected already, and requires %[1]s: invariant/gate isn't among its checks. invariant new doesn't replace a protection it didn't set.
The factory still merges only once the gate passes. A merge made by hand can skip it.
To require the gate and keep everything else there, run:

gh api -X PATCH repos/%[2]s/branches/main/protection/required_status_checks --input - <<'EOF'
%[3]s
EOF`, what, f.Repo, b)
	return nil
}

// left is what a founding leaves for a person, as commands and links: the
// App's access, a watcher, the dashboard and the first issue. None of them
// can be read with gh's own login, so a second run prints them too.
func left(repo string, organization, private, spec bool) string {
	owner, _, _ := strings.Cut(repo, "/")
	installations := "https://github.com/settings/installations"
	if organization {
		installations = "https://github.com/organizations/" + owner + "/settings/installations"
	}
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

1. Give the factory's App access to %[1]s, if the factory acts as an App that's installed on selected repositories. Add it under Repository access in the installation's settings, at %[5]s. https://github.com/gitdek/invariant/blob/main/docs/factory-app.md#install-it has the steps.

2. Start a watcher. It runs its coding agent on your account:

invariant watch -repo %[1]s -projects invariant -language go

Add -app-id with the App's ID to have it act as the App.%[2]s

3. Add the repository to the dashboard: pass -repo %[1]s to invariant dashboard, beside the ones it shows.%[3]s

4. Open the first issue, at https://github.com/%[1]s/issues/new. %[4]s`, repo, setupGit, shown, issue, installations)
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
