package factory

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// Ensure clones the repository, unless it already has.
func (c Clone) Ensure(ctx context.Context) error {
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
	_, err := c.git(ctx, "fetch", "--quiet", "--prune", "origin")
	return err
}

// Dirs lists the directories directly under dir at ref.
func (c Clone) Dirs(ctx context.Context, ref, dir string) ([]string, error) {
	out, err := c.git(ctx, "ls-tree", "-d", "--name-only", ref+":"+dir)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// Worktree checks out a new branch, starting at from, in a directory of its
// own.
func (c Clone) Worktree(ctx context.Context, branch, from string) (string, error) {
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
	_, err := c.git(ctx, "worktree", "remove", "--force", dir)
	os.RemoveAll(dir)
	return err
}

// Commit commits everything under dir, and nothing outside it, as the
// factory's author.
func (c Clone) Commit(ctx context.Context, worktree, dir, message string) (string, error) {
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
	var env []string
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return err
		}
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		env = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic " + basic}
	}
	_, err := runEnv(ctx, worktree, env, "git", "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return err
}

func (c Clone) RevParse(ctx context.Context, ref string) (string, error) {
	out, err := c.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return strings.TrimSpace(out), err
}

func (c Clone) Show(ctx context.Context, ref, file string) ([]byte, error) {
	out, err := c.git(ctx, "show", ref+":"+file)
	return []byte(out), err
}

func (c Clone) Scope(ctx context.Context, base, head string) (scope.Result, error) {
	return scope.Check(ctx, c.Dir, base, head)
}

func (c Clone) git(ctx context.Context, args ...string) (string, error) {
	return run(ctx, c.Dir, "git", args...)
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	return runEnv(ctx, dir, nil, name, args...)
}

func runEnv(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
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
