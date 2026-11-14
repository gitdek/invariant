// Package found starts a new project for the factory (#919): its repository
// on GitHub, its checkout, a starter for its language, its checks and its
// gate. A person took about ten steps to do that by hand. Project takes
// them in one command, and ends by printing the ones only a person can take.
//
// It founds empty projects only: a repository that exists is invariant
// init's to set up. A directory is the project's when it's empty, or holds
// a founding that stopped part way, nothing but files the founding writes,
// each with the bytes it would write now, or holds a project that is
// founded already. Anything else in it is refused before the first thing is
// created. Each step looks for its own result, so a founding that stopped
// is finished by running it again, and a second run of a finished one
// changes nothing. The one commit a founding pushes is its own: one commit,
// of its files and nothing else.
package found

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
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
	founded bool   // its commit holds the checks and a capable gate
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
		if spec, err = readSpec(f.spec); err != nil {
			return err
		}
	}
	// Every file of the founding, the checks included, is known before
	// anything is read or made: the spec is the one thing of a person's
	// that goes in.
	files, err := starter(owner, name, spec != nil)
	if err != nil {
		return err
	}
	if spec != nil {
		files = append(files, file{specPath, spec, 0o644})
	}
	files = append(files, file{setup.WorkflowPath, []byte(workflow), 0o644})
	contract, err := checks(files)
	if err != nil {
		return err
	}
	files = append(files, file{repository.Path, contract, 0o644})
	f.say("Repository: https://github.com/%s", o.Repo)
	f.say("Directory:  %s\n", f.dir)

	// What the directory and the repository hold is read here, before the
	// first thing is created, so a project that isn't empty is refused
	// with nothing made.
	local, err := f.local(ctx, files)
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
	case remote.exists && !remote.empty && !(local.founded && f.reached(ctx)):
		// A repository with commits is this project's only when its
		// founding is what the checkout has seen there.
		return f.existing(o.Repo+" already holds commits on GitHub", "path/to/your-repo")
	case local.founded:
		if err := f.loads(ctx); err != nil {
			return err
		}
		// Its push is still to be made, and what's pushed is main: it has
		// to be the founding's own commit, as a push that failed left it.
		if !remote.exists || remote.empty {
			if err := f.own(ctx, files); err != nil {
				return err
			}
		}
	case local.exists:
		if err := f.identity(ctx); err != nil {
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
		// The clone is held to what a checkout that was there is: the
		// repository's own, with nothing in it. It couldn't be asked for
		// an identity any sooner.
		if err := f.origin(ctx); err != nil {
			return err
		}
		if err := f.pristine(ctx, files, false); err != nil {
			return err
		}
		if err := f.identity(ctx); err != nil {
			return err
		}
	}

	if local.founded {
		if local.pin != "" && local.pin != o.Invariant {
			f.say("Its gate builds Invariant at %s, not at %s, the commit -invariant names. invariant new leaves a founded project's gate as it is.", local.pin[:12], o.Invariant[:12])
		}
		if spec != nil && !f.same(specPath, spec) {
			f.say("-spec wasn't used: the project is founded already, and its %s is its own to change.", specPath)
		}
	} else {
		if err := f.start(ctx, files); err != nil {
			return err
		}
		// The founding's own commit is read as CI will read it, before
		// it's pushed, and read back for what it holds, since a hook can
		// change a commit as it's made.
		if err := f.loads(ctx); err != nil {
			return err
		}
		if err := f.own(ctx, files); err != nil {
			return err
		}
	}
	// A repository with nothing in it hasn't had the founding's push, this
	// run's or an earlier one's that failed.
	if remote.empty {
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

// existing is the refusal of a project that isn't empty, for the reason
// given: invariant init sets up a repository that exists, run in its
// checkout, as the README has it.
func (f *founding) existing(reason, checkout string) error {
	return fmt.Errorf(`%s, so this isn't an empty project, and invariant new founds empty projects only. A repository that exists is invariant init's to set up, in its checkout:

cd %s && invariant init -repo %s

https://github.com/gitdek/invariant#use-it-on-your-repository says what follows`, reason, checkout, f.Repo)
}

// loads reads the checkout's committed checks with the loader CI and the
// factory use. Checks it refuses are never pushed as a founding's, and
// their project is never called founded.
func (f *founding) loads(ctx context.Context) error {
	if _, err := repository.Load(ctx, f.dir, "HEAD"); err != nil {
		return fmt.Errorf("the checks committed in %s don't load, so the project isn't founded: %w. https://github.com/gitdek/invariant/blob/main/docs/factory.md#drafting-repository-checks says how a person completes %s", f.dir, err, repository.Path)
	}
	return nil
}

// reached says whether the founding has reached GitHub, as far as the
// checkout knows: whether main as it last saw it there is founded. A
// project that has moved on since stays founded.
func (f *founding) reached(ctx context.Context) bool {
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

// has says whether the checkout holds a file at name, and same whether that
// file holds exactly data.
func (f *founding) has(name string) bool {
	info, err := os.Lstat(filepath.Join(f.dir, filepath.FromSlash(name)))
	return err == nil && info.Mode().IsRegular()
}

func (f *founding) same(name string, data []byte) bool {
	held, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(name)))
	return f.has(name) && err == nil && bytes.Equal(held, data)
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
// repository's checkout is the project's when the project is founded
// already, or when the checkout holds nothing but a founding that stopped.
// Anything else is refused: it isn't an empty project.
func (f *founding) local(ctx context.Context, files []file) (checkout, error) {
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
	i := slices.IndexFunc(entries, func(entry fs.DirEntry) bool { return entry.Name() == ".git" })
	if i < 0 {
		// No checkout, so nothing in it is a founding's.
		found, err := foreign(f.dir, nil)
		if err != nil {
			return checkout{}, err
		}
		return checkout{}, f.holds(found)
	}
	// A clone's .git is a directory. A link, or a file that leads to a
	// repository elsewhere, would have git commit there, outside the
	// directory, and push from there.
	if !entries[i].IsDir() {
		return checkout{}, fmt.Errorf("%s is a git checkout whose .git isn't a directory but a link, or a file that leads to a repository elsewhere, as a worktree's or a submodule's does. invariant new starts in an empty directory or in a fresh clone of the repository", f.dir)
	}
	if err := f.origin(ctx); err != nil {
		return checkout{}, err
	}
	c := checkout{exists: true}
	var commit bool
	if commit, c.founded, c.pin = f.read(ctx, "HEAD"); !c.founded {
		return c, f.pristine(ctx, files, commit)
	}
	// A founded project's main is what a founding pushes.
	if branch, err := f.git(ctx, "symbolic-ref", "--short", "HEAD"); err != nil || strings.TrimSpace(branch) != "main" {
		return c, fmt.Errorf("%s isn't on main, the branch invariant new pushes. Switch it to main, then run it again:\n\n%s", f.dir, f.again())
	}
	return c, nil
}

// origin refuses a checkout that doesn't fetch from the repository, or
// doesn't push to it, at every address it has for each. The addresses are
// compared and never printed: one can carry a token.
func (f *founding) origin(ctx context.Context) error {
	for _, addresses := range [][]string{{"remote", "get-url", "--all", "origin"}, {"remote", "get-url", "--push", "--all", "origin"}} {
		listed, err := f.git(ctx, addresses...)
		urls := strings.Split(strings.TrimSpace(listed), "\n")
		if err != nil || slices.ContainsFunc(urls, func(url string) bool { return !strings.EqualFold(setup.GitHubRepo(url), f.Repo) }) {
			return fmt.Errorf("%s is a git checkout whose origin isn't %s on GitHub, to fetch from and to push to. invariant new starts in an empty directory or in a fresh clone of the repository", f.dir, f.Repo)
		}
	}
	return nil
}

// pristine refuses a checkout that holds anything but a founding that
// stopped part way: an entry that isn't the founding's own, or a commit,
// at HEAD or under any name.
func (f *founding) pristine(ctx context.Context, files []file, commit bool) error {
	found, err := foreign(f.dir, files)
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return f.holds(found)
	}
	if refs, err := f.git(ctx, "for-each-ref", "--count=1"); commit || err != nil || strings.TrimSpace(refs) != "" {
		return f.existing(f.dir+" already has commits, and they don't make a founded project", shell(f.dir))
	}
	return nil
}

// own refuses main unless it's the founding's own commit: one commit, with
// none before it, that holds the founding's files and nothing else, each at
// its mode and stored as git stores the bytes the founding writes now. A
// founding pushes main, so it never pushes a person's commit, a file it
// didn't write, or a change to one it did.
func (f *founding) own(ctx context.Context, files []file) error {
	commits, err := f.git(ctx, "rev-list", "--parents", "--max-count=1", "HEAD")
	if err != nil {
		return err
	}
	if len(strings.Fields(commits)) != 1 {
		return f.existing(f.dir+" has commits on main besides a founding's", shell(f.dir))
	}
	listed, err := f.git(ctx, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if err != nil {
		return err
	}
	missing := map[string]file{}
	for _, file := range files {
		missing[file.path] = file
	}
	var differ []string
	for _, entry := range strings.Split(listed, "\x00") {
		if entry == "" {
			continue
		}
		about, name, _ := strings.Cut(entry, "\t")
		fields := strings.Fields(about) // mode, type, object
		file, mine := missing[name]
		delete(missing, name)
		mode := "100644"
		if file.mode&0o111 != 0 {
			mode = "100755"
		}
		if !mine || len(fields) != 3 || fields[0] != mode || !f.stores(ctx, file, fields[2]) {
			differ = append(differ, name)
		}
	}
	differ = slices.Sorted(slices.Values(append(differ, slices.Collect(maps.Keys(missing))...)))
	if len(differ) == 0 {
		return nil
	}
	named := strings.Join(differ[:min(len(differ), 5)], ", ")
	if more := len(differ) - 5; more > 0 {
		named += fmt.Sprintf(" and %d more", more)
	}
	return fmt.Errorf("%w. A founding that stopped part way is finished by running it again with the -spec and -invariant it was started with", f.existing(f.dir+" has a commit on main that isn't a founding's: it differs from one at "+named, shell(f.dir)))
}

// stores says whether object is what git stores file as: its bytes as a
// blob, or, where git's own settings convert a file as it's committed, as
// a line-ending setting does, what git makes of them at its path.
func (f *founding) stores(ctx context.Context, file file, object string) bool {
	if blob(file.data, len(object)) == object {
		return true
	}
	stored, err := f.run(ctx, Command{Dir: f.dir, Name: "git", Args: []string{"hash-object", "--path=" + file.path, "--stdin"}, Stdin: string(file.data)})
	return err == nil && strings.TrimSpace(stored) == object
}

// blob is git's name for data stored as a blob, in a repository whose names
// are n hex digits: SHA-1's 40, or SHA-256's 64.
func blob(data []byte, n int) string {
	var h hash.Hash
	switch n {
	case 2 * sha1.Size:
		h = sha1.New()
	case 2 * sha256.Size:
		h = sha256.New()
	default:
		return ""
	}
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// largest is the largest file GitHub takes in a push.
const largest = 100 << 20

// readSpec reads the spec: a file, wherever a link to it leads, that GitHub
// would take in a push. Anything else is refused before it's read, so a
// device that never ends, or a spec no push could carry, stops the founding
// before it starts.
func readSpec(name string) ([]byte, error) {
	tooLarge := fmt.Errorf("the spec, %s, is larger than the 100 MiB GitHub takes in a push", name)
	info, err := os.Stat(name)
	if err != nil {
		return nil, fmt.Errorf("reading the spec: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("reading the spec: %s isn't a file", name)
	}
	if info.Size() > largest {
		return nil, tooLarge
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("reading the spec: %w", err)
	}
	if len(data) > largest {
		return nil, tooLarge
	}
	return data, nil
}

// holds is the refusal of a directory with entries that aren't a
// founding's own: a handful of them are named. A founding that stopped
// with other flags leaves files that aren't this one's either, so it says
// how that's finished.
func (f *founding) holds(entries []string) error {
	named := strings.Join(entries[:min(len(entries), 5)], ", ")
	if more := len(entries) - 5; more > 0 {
		named += fmt.Sprintf(" and %d more", more)
	}
	return fmt.Errorf("%w. A founding that stopped part way is finished by running it again with the -spec and -invariant it was started with", f.existing(f.dir+" holds what a founding doesn't write: "+named, shell(f.dir)))
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
	return fmt.Errorf(`%[1]s has no git identity, so the founding can't make its commit there: %[2]s. Set one for this checkout:

git -C %[3]s config user.name "Your Name"
git -C %[3]s config user.email "you@example.com"

or for every checkout, with git config --global. Then run it again, and it carries on from here:

%[4]s`, f.dir, strings.Join(unset, " and ")+map[bool]string{true: " isn't set", false: " aren't set"}[len(unset) == 1], shell(f.dir), f.again())
}

// start writes the founding's files into the checkout and commits them, and
// nothing else, on main. The files are its own, so they're added whatever
// a person's ignore rules say of their names.
func (f *founding) start(ctx context.Context, files []file) error {
	// A clone of an empty repository is on whatever branch git's own
	// configuration names.
	if _, err := f.git(ctx, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return err
	}
	wrote, err := place(f.dir, files)
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
	if _, err := f.git(ctx, append([]string{"add", "-f", "--"}, paths...)...); err != nil {
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

// requireGate has GitHub refuse any merge to main that invariant/gate
// didn't pass, with the call init prints for a person. It makes that call
// only where GitHub says plainly that main isn't protected, since the call
// replaces whatever protection is there. A branch that requires the gate
// already is left as it is. One that's protected without it is left too,
// with what a person adds to it. Where the account's plan refuses
// protection, the founding says what that means and carries on. A read that
// fails any other way stops it: nothing says what the call would replace.
func (f *founding) requireGate(ctx context.Context) error {
	protection := "repos/" + f.Repo + "/branches/main/protection"
	const skipped = "The factory still merges only once the gate passes. A merge made by hand can skip it."
	refused := func() {
		f.say(`GitHub won't require invariant/gate on main: this account's plan doesn't protect the branches of a private repository.
%s
To have GitHub refuse those too, make the repository public, at https://github.com/%s/settings, or change the account's plan. Then run it again:

%s`, skipped, f.Repo, f.again())
	}
	byPlan := func(err error) bool {
		return strings.Contains(err.Error(), "(HTTP 403)") && planRefusal.MatchString(err.Error())
	}
	out, err := f.gh(ctx, "api", protection)
	switch {
	case err == nil:
		var p struct {
			Required struct {
				Contexts []string `json:"contexts"`
				Checks   []struct {
					Context string `json:"context"`
				} `json:"checks"`
			} `json:"required_status_checks"`
		}
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			return fmt.Errorf("GitHub's answer about main's protection can't be read, so nothing there was changed: %w. Run it again:\n\n%s", err, f.again())
		}
		requires := slices.Clone(p.Required.Contexts)
		for _, check := range p.Required.Checks {
			if !slices.Contains(requires, check.Context) {
				requires = append(requires, check.Context)
			}
		}
		if slices.Contains(requires, "invariant/gate") {
			return nil
		}
		// The protection is a person's, and so is the change to it.
		what := "no status check"
		if len(requires) > 0 {
			what = strings.Join(requires, ", ")
		}
		f.say(`main is protected already, and requires %s: invariant/gate isn't among its checks. invariant new doesn't replace a protection it didn't set.
%s
To have GitHub refuse those too, add the status check invariant/gate, from GitHub Actions, to the checks main requires: https://github.com/%s/settings/branches`, what, skipped, f.Repo)
		return nil
	case byPlan(err):
		refused()
		return nil
	case !strings.Contains(err.Error(), "(HTTP 404)") || !strings.Contains(err.Error(), "Branch not protected"):
		return fmt.Errorf("reading main's protection, so nothing there was changed: %w. Run it again:\n\n%s", err, f.again())
	}
	_, err = f.run(ctx, Command{Name: "gh", Args: []string{"api", "-X", "PUT", protection, "--input", "-"}, Stdin: setup.Protection})
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
