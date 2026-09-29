package factory

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/gitdek/invariant/internal/scope"
)

// ErrNothingToCommit is what Commit returns when nothing changed.
var ErrNothingToCommit = errors.New("nothing to commit")

// Clone is the factory's own clone of the repository, apart from anyone's
// working copy. Every job gets a worktree of its own.
type Clone struct {
	Dir    string // the clone
	Remote string // the repository's URL
	Name   string // the author of the factory's commits
	Email  string
	// Token, when set, is who pushes: the factory's App (D-0041). It reaches
	// git through the environment, never a command line or git's config.
	Token func(ctx context.Context) (string, error)
}

// A watcher's steps run at once and share its clone, and so does its lease
// (D-0113). So every call on a clone takes the clone's lock and runs alone:
// no two git commands in the clone overlap, and one call's commands never
// take in another's, as two results saved through Save's index would.
var clones sync.Map // a clone's directory → *sync.Mutex

// lock takes the clone's lock, and returns what gives it back.
func (c Clone) lock() (unlock func()) {
	dir, err := filepath.Abs(c.Dir)
	if err != nil {
		dir = filepath.Clean(c.Dir)
	}
	mu, _ := clones.LoadOrStore(dir, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}

// Ensure clones the repository, unless it already has.
func (c Clone) Ensure(ctx context.Context) error {
	defer c.lock()()
	if _, err := os.Stat(c.Dir + "/.git"); err == nil {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	_, err := run(ctx, "", "git", "clone", "--quiet", c.Remote, c.Dir)
	return err
}

func (c Clone) Fetch(ctx context.Context) error {
	defer c.lock()()
	_, err := c.git(ctx, "fetch", "--quiet", "--prune", "origin")
	return err
}

// Dirs lists the directories directly under dir at ref. A dir that isn't
// there at ref has none, like a new repository's projects directory (#101).
// Any other failure is an error, such as a ref that isn't there or a dir
// that's a file.
func (c Clone) Dirs(ctx context.Context, ref, dir string) ([]string, error) {
	defer c.lock()()
	out, err := c.git(ctx, "ls-tree", "-d", "--name-only", ref+":"+dir)
	if err != nil {
		// git fails the same way whether ref or dir isn't there, but
		// looking for dir in ref's tree finds nothing only when ref is there
		// and dir isn't.
		if found, err2 := c.git(ctx, "ls-tree", "--name-only", ref, "--", dir); err2 == nil && found == "" {
			return nil, nil
		}
		return nil, err
	}
	return strings.Fields(out), nil
}

// Worktree checks out a new branch, starting at from, in a directory of its
// own.
func (c Clone) Worktree(ctx context.Context, branch, from string) (string, error) {
	defer c.lock()()
	dir, err := os.MkdirTemp("", "invariant-worktree-")
	if err != nil {
		return "", err
	}
	if _, err := c.git(ctx, "worktree", "add", "--quiet", "-B", branch, dir, from); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func (c Clone) RemoveWorktree(ctx context.Context, dir string) error {
	defer c.lock()()
	_, err := c.git(ctx, "worktree", "remove", "--force", dir)
	os.RemoveAll(dir)
	return err
}

// ClearWorktrees force-removes every worktree registered in the clone but
// the clone itself, the first one git lists, whether or not its directory
// is still there, then runs git worktree prune. A watcher that stopped
// mid-build leaves its worktrees behind, each holding its branch.
func (c Clone) ClearWorktrees(ctx context.Context) error {
	defer c.lock()()
	out, err := c.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	var dirs []string
	for _, line := range strings.Split(out, "\n") {
		if dir, ok := strings.CutPrefix(line, "worktree "); ok {
			dirs = append(dirs, dir)
		}
	}
	for i, dir := range dirs {
		if i == 0 {
			continue // the clone itself
		}
		// git keeps a worktree locked until it's checked out, so one that a
		// watcher stopped while adding it takes a second --force.
		if _, err := c.git(ctx, "worktree", "remove", "--force", "--force", dir); err != nil {
			return err
		}
	}
	_, err = c.git(ctx, "worktree", "prune")
	return err
}

// Commit commits everything under dir, and nothing outside it, as the
// factory's author.
func (c Clone) Commit(ctx context.Context, worktree, dir, message string) (string, error) {
	defer c.lock()()
	if _, err := run(ctx, worktree, "git", "add", "--all", "--", dir); err != nil {
		return "", err
	}
	if _, err := run(ctx, worktree, "git", "diff", "--cached", "--quiet"); err == nil {
		return "", ErrNothingToCommit
	}
	if _, err := run(ctx, worktree, "git", "-c", "user.name="+c.Name, "-c", "user.email="+c.Email,
		"commit", "--quiet", "--no-verify", "-m", message); err != nil {
		return "", err
	}
	out, err := run(ctx, worktree, "git", "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// Push pushes a worktree's branch. It never forces.
func (c Clone) Push(ctx context.Context, worktree, branch string) error {
	defer c.lock()()
	env, err := c.auth(ctx)
	if err != nil {
		return err
	}
	_, err = runEnv(ctx, worktree, env, "git", "push", "--quiet", receivePack, "origin", "HEAD:refs/heads/"+branch)
	return err
}

func (c Clone) RevParse(ctx context.Context, ref string) (string, error) {
	defer c.lock()()
	out, err := c.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return strings.TrimSpace(out), err
}

// Manifests are the paths of every project's manifest at ref (#153).
func (c Clone) Manifests(ctx context.Context, ref string) ([]string, error) {
	defer c.lock()()
	out, err := c.git(ctx, "ls-tree", "-r", "--name-only", ref)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\n") {
		if p == ".invariant/invariant.json" || strings.HasSuffix(p, "/.invariant/invariant.json") {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func (c Clone) Show(ctx context.Context, ref, file string) ([]byte, error) {
	defer c.lock()()
	out, err := c.git(ctx, "show", ref+":"+file)
	return []byte(out), err
}

// Export writes the files under paths, as they are at ref, into dst: the
// existing code an issue names, for the formalizer to read (D-0054). It
// never reads the working tree, and it keeps every file inside dst.
func (c Clone) Export(ctx context.Context, ref string, paths []string, dst string) error {
	defer c.lock()()
	return c.export(ctx, ref, paths, dst)
}

// export is Export, for a call that holds the clone's lock.
func (c Clone) export(ctx context.Context, ref string, paths []string, dst string) error {
	cmd := exec.CommandContext(ctx, "git", slices.Concat(noDetach, []string{"archive", "--format=tar", ref, "--"}, paths)...)
	cmd.Dir, cmd.Env = c.Dir, append(os.Environ(), noLFS)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git archive %s %s: %w: %s", ref, strings.Join(paths, " "), err, strings.TrimSpace(errOut.String()))
	}
	tr := tar.NewReader(&out)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if h.Typeflag != tar.TypeReg || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return err
		}
	}
}

func (c Clone) Scope(ctx context.Context, base, head string, issue int) (scope.Result, error) {
	defer c.lock()()
	return scope.Check(ctx, c.Dir, base, head, issue)
}

func (c Clone) git(ctx context.Context, args ...string) (string, error) {
	return run(ctx, c.Dir, "git", args...)
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	return runEnv(ctx, dir, nil, name, args...)
}

// noLFS keeps git from fetching the large files a repository keeps in Git
// LFS, such as media: the factory's checkouts hold LFS pointers only, and
// the factory never needs more (D-0055).
const noLFS = "GIT_LFS_SKIP_SMUDGE=1"

// noDetach goes before the subcommand of every git command a clone runs, so
// the upkeep the command starts, git gc --auto by way of git maintenance run
// --auto, has ended when it returns. By default git runs it in the
// background, where it goes on writing to the clone after the call that ran
// git has returned, even alongside the clone's next command, which should
// run alone (D-0113, #170). Upkeep isn't turned off: a command that finds it
// due takes that much longer.
var noDetach = []string{"-c", "gc.autoDetach=false", "-c", "maintenance.autoDetach=false"}

// receivePack has every push run the receive-pack of the repository it
// pushes to with noDetach as well, since git passes no -c setting to the
// receive-pack of a repository on the same machine. GitHub, over HTTPS, runs
// its own and ignores it.
var receivePack = "--receive-pack=git " + strings.Join(noDetach, " ") + " receive-pack"

// runEnv runs a command with env added to its environment. When it fails,
// its error names the command as its caller wrote it, without noDetach.
func runEnv(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	command := args
	if name == "git" {
		command = slices.Concat(noDetach, args)
		env = append(env, noLFS)
	}
	cmd := exec.CommandContext(ctx, name, command...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
