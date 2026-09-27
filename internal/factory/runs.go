package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent runs are recorded where every watcher can see them, before they
// start (D-0069). Each run has a ref, refs/invariant/runs/<issue>/<step>,
// created atomically, so two watchers can't both record one run. While the
// run goes, the ref points at an empty commit that says it's recorded. When
// the run finishes, the ref moves to its result: the commit a build pushes
// to the issue's branch, or a commit holding a draft. A watcher that finds
// a recorded run with no result knows it stopped partway, and says so
// instead of paying for another. A watcher that finds a result uses it,
// whoever ran it.
//
// A merge is recorded the same way before it happens, so a watcher that
// finds a pull request merged knows whether the factory merged it.

const runsRef = "refs/invariant/runs/"

// recordedMark begins the message of a run's ref while the run goes.
const recordedMark = "invariant: recorded "

// RunState is what a step's record says.
type RunState int

const (
	RunNone     RunState = iota // nothing recorded
	RunRecorded                 // recorded, with no result: it's going, or it stopped
	RunDone                     // it finished, and its ref holds its result
)

// ErrRecorded means another watcher recorded the step first.
var ErrRecorded = errors.New("another watcher recorded this step first")

func runRef(issue int, step string) string {
	return fmt.Sprintf("%s%d/%s", runsRef, issue, step)
}

// Run reads a step's record: its state, and the commit its ref points at.
func (c Clone) Run(ctx context.Context, issue int, step string) (RunState, string, error) {
	ref := runRef(issue, step)
	sha, err := c.remoteRef(ctx, ref)
	if err != nil || sha == "" {
		return RunNone, "", err
	}
	msg, err := c.git(ctx, "log", "-1", "--format=%B", sha)
	if err != nil {
		return RunNone, "", err
	}
	if strings.HasPrefix(msg, recordedMark) {
		return RunRecorded, sha, nil
	}
	return RunDone, sha, nil
}

// Record records a step before it happens. It fails with ErrRecorded if the
// step already has a record.
func (c Clone) Record(ctx context.Context, issue int, step, what string) error {
	// The nonce makes every record its own commit. Two watchers making the
	// same commit would both "succeed", since pushing a ref where it already
	// points changes nothing.
	commit, err := c.emptyCommit(ctx, recordedMark+what+"\n\n"+NewHolder())
	if err != nil {
		return err
	}
	if err := c.pushRef(ctx, runRef(issue, step), "", commit); err != nil {
		if state, _, readErr := c.Run(ctx, issue, step); readErr == nil && state != RunNone {
			return ErrRecorded
		}
		return err
	}
	return nil
}

// Finish moves a recorded step's ref to its result, the commit result.
func (c Clone) Finish(ctx context.Context, issue int, step, result string) error {
	state, recorded, err := c.Run(ctx, issue, step)
	switch {
	case err != nil:
		return err
	case state != RunRecorded:
		return fmt.Errorf("%s isn't recorded as going, so its result can't be recorded", runRef(issue, step))
	}
	return c.pushRef(ctx, runRef(issue, step), recorded, result)
}

// Save commits the files in dir, on their own, and returns the commit: a
// draft's result, for Finish.
func (c Clone) Save(ctx context.Context, dir, message string) (string, error) {
	index := filepath.Join(c.Dir, ".git", "invariant-save-index")
	defer os.Remove(index)
	env := []string{"GIT_INDEX_FILE=" + index, "GIT_WORK_TREE=" + dir}
	if _, err := runEnv(ctx, c.Dir, env, "git", "add", "--all", "--force", "."); err != nil {
		return "", err
	}
	tree, err := runEnv(ctx, c.Dir, env, "git", "write-tree")
	if err != nil {
		return "", err
	}
	out, err := run(ctx, c.Dir, "git", "-c", "user.name="+c.Name, "-c", "user.email="+c.Email, "commit-tree", strings.TrimSpace(tree), "-m", message)
	return strings.TrimSpace(out), err
}

// Load writes the files of commit, such as a draft's result, into dir.
func (c Clone) Load(ctx context.Context, commit, dir string) error {
	return c.Export(ctx, commit, []string{"."}, dir)
}

// emptyCommit makes a commit with no files and no parent.
func (c Clone) emptyCommit(ctx context.Context, message string) (string, error) {
	tree, err := runEnv(ctx, c.Dir, nil, "git", "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	out, err := run(ctx, c.Dir, "git", "-c", "user.name="+c.Name, "-c", "user.email="+c.Email, "commit-tree", strings.TrimSpace(tree), "-m", message)
	return strings.TrimSpace(out), err
}

// remoteRef is where ref points on the origin, fetched so its commit can be
// read here, or "" when it doesn't exist.
func (c Clone) remoteRef(ctx context.Context, ref string) (string, error) {
	env, err := c.auth(ctx)
	if err != nil {
		return "", err
	}
	out, err := runEnv(ctx, c.Dir, env, "git", "ls-remote", "origin", ref)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", nil
	}
	sha := fields[0]
	if _, err := c.git(ctx, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if _, err := runEnv(ctx, c.Dir, env, "git", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", "+"+ref+":"+ref); err != nil {
			return "", err
		}
	}
	return sha, nil
}

// pushRef moves ref from old, or creates it when old is empty, to commit.
// git refuses if the ref isn't at old on the origin.
func (c Clone) pushRef(ctx context.Context, ref, old, commit string) error {
	env, err := c.auth(ctx)
	if err != nil {
		return err
	}
	_, err = runEnv(ctx, c.Dir, env, "git", "push", "--quiet", "--force-with-lease="+ref+":"+old, "origin", commit+":"+ref)
	return err
}
