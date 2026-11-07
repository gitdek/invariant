package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/github"
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
	defer c.lock()()
	return c.readRun(ctx, issue, step)
}

// readRun is Run, for a call that holds the clone's lock.
func (c Clone) readRun(ctx context.Context, issue int, step string) (RunState, string, error) {
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

// Recorded is what a step's record says it's for, while it's recorded with
// no result, or "" otherwise.
func (c Clone) Recorded(ctx context.Context, issue int, step string) (string, error) {
	defer c.lock()()
	state, sha, err := c.readRun(ctx, issue, step)
	if err != nil || state != RunRecorded {
		return "", err
	}
	msg, err := c.git(ctx, "log", "-1", "--format=%B", sha)
	if err != nil {
		return "", err
	}
	what := strings.TrimSpace(strings.TrimPrefix(msg, recordedMark))
	if i := strings.LastIndex(what, "\n\n"); i >= 0 {
		what = what[:i] // the nonce
	}
	return strings.TrimSpace(what), nil
}

// Record records a step before it happens. It fails with ErrRecorded if the
// step already has a record.
func (c Clone) Record(ctx context.Context, issue int, step, what string) error {
	unlock := c.lock()
	// The nonce makes every record its own commit. Two watchers making the
	// same commit would both "succeed", since pushing a ref where it already
	// points changes nothing.
	commit, err := c.emptyCommit(ctx, recordedMark+what+"\n\n"+NewHolder())
	unlock()
	if err != nil {
		return err
	}
	return c.pushRun(ctx, runRef(issue, step), "", commit)
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
	return c.pushRun(ctx, runRef(issue, step), recorded, result)
}

// pushRun retries only a failed run push whose readback still shows old.
// Every attempt keeps the same commit and lease expectation. Neither lock
// is held while waiting, so the watcher can renew its lease (#545).
func (c Clone) pushRun(ctx context.Context, ref, old, commit string) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f, ok := ctx.Value(runPermissionKey{}).(*Factory); ok {
			if err := f.mayAct(ctx); err != nil {
				return err
			}
		}
		retry, err := c.pushRunOnce(ctx, ref, old, commit)
		if err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !retry || attempt == 4 {
			return err
		}
		timer := time.NewTimer((15 * time.Second) << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// pushRunOnce reads back even a refusal: the server may have published the
// commit before its answer was lost. A different record belongs to another
// call, even when it says the same thing, since each Record has a nonce.
func (c Clone) pushRunOnce(ctx context.Context, ref, old, commit string) (bool, error) {
	unlock, err := c.lockRun(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err = c.pushRef(ctx, ref, old, commit)
	if err == nil {
		return false, nil
	}
	now, readErr := c.remoteRef(ctx, ref)
	switch {
	case readErr != nil:
		return false, err
	case now == commit:
		return false, nil
	case now != old:
		if old == "" {
			return false, ErrRecorded
		}
		return false, err
	}
	return github.Transient(err), err
}

// Save commits the files in dir, on top of parent when it isn't empty, and
// returns the commit: a run's result, for Finish. A build's result sits on
// top of the code it pushes.
func (c Clone) Save(ctx context.Context, dir, message, parent string) (string, error) {
	defer c.lock()()
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
	args := []string{"-c", "user.name=" + c.Name, "-c", "user.email=" + c.Email, "commit-tree", strings.TrimSpace(tree), "-m", message}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	out, err := run(ctx, c.Dir, "git", args...)
	return strings.TrimSpace(out), err
}

// Load writes the files of commit, such as a draft's result, into dir.
func (c Clone) Load(ctx context.Context, commit, dir string) error {
	defer c.lock()()
	return c.export(ctx, commit, []string{"."}, dir)
}

// Holds says whether ref already holds commit.
func (c Clone) Holds(ctx context.Context, ref, commit string) (bool, error) {
	defer c.lock()()
	_, err := c.git(ctx, "merge-base", "--is-ancestor", commit, ref)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, err
}

// FirstCommit is the first commit on head, past base, that touched path, or
// "" when none did: a plan's ratification, which records it (#271).
func (c Clone) FirstCommit(ctx context.Context, base, head, path string) (string, error) {
	defer c.lock()()
	out, err := c.git(ctx, "rev-list", "--reverse", base+".."+head, "--", path)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(first), nil
}

// PushCommit pushes commit to branch. It never forces, so the branch must
// be behind it.
func (c Clone) PushCommit(ctx context.Context, commit, branch string) error {
	defer c.lock()()
	env, err := c.auth(ctx)
	if err != nil {
		return err
	}
	_, err = runEnv(ctx, c.Dir, env, "git", "push", "--quiet", receivePack, "origin", commit+":refs/heads/"+branch)
	return err
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
	_, err = runEnv(ctx, c.Dir, env, "git", "push", "--quiet", receivePack, "--force-with-lease="+ref+":"+old, "origin", commit+":"+ref)
	return err
}

// mergedByOther says whether GitHub names someone other than the factory as
// who merged a pull request. A record of a merge the factory meant to make
// doesn't make a person's merge the factory's, as when a watcher stopped
// between recording a merge and making it (#756). With no login of its own,
// or no name from GitHub, it can't tell, and the record stands as before.
func mergedByOther(self string, pr github.PullRequest) bool {
	return self != "" && pr.MergedBy != nil && pr.MergedBy.Login != "" && pr.MergedBy.Login != self
}
