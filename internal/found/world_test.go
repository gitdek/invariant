package found

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pin is a full commit of Invariant for the gate to build.
const pin = "61eb0495cd8a5ff84be5bbdb4efc3bb2b7c1ae23"

// world stands in for GitHub and for every command that would reach it. It
// is the runner each test hands a founding, so no test creates a
// repository, clones one or pushes: gh never runs at all, and git runs only
// the local commands a founding uses, inside the test's own directory,
// without the machine's git configuration.
type world struct {
	t    *testing.T
	root string // every directory a command runs in is under it
	repo string

	// identity says whether git has a user.name and user.email.
	identity bool

	// The repository as GitHub would show it.
	exists, empty, mergeCommits bool
	visibility                  string
	required                    bool   // main requires invariant/gate
	protection                  string // main's protection as GitHub shows it, when a test sets one
	refusal                     error  // what GitHub answers the requirement with
	pushFails                   error  // what the next push answers with

	calls  []string      // every command, in order, with the root as ROOT
	body   string        // what the requirement was sent
	pushed []string      // each commit pushed to main
	before func(Command) // called before each command runs
	said   bytes.Buffer  // what the founding printed
}

// newWorld is a world with no repository yet, where git has an identity.
func newWorld(t *testing.T) *world {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, root: root, repo: "acme/widgets", identity: true}
}

// found runs a founding of the world's repository with o, filling in what o
// leaves out, and returns what it printed.
func (w *world) found(o Options) (string, error) {
	w.t.Helper()
	if o.Repo == "" {
		o.Repo = w.repo
	}
	if o.Language == "" {
		o.Language = "go"
	}
	if o.Invariant == "" {
		o.Invariant = pin
	}
	if o.Dir == "" {
		o.Dir = filepath.Join(w.root, "widgets")
	}
	o.WorkDir, o.Runner, o.Out = w.root, w, &w.said
	w.said.Reset()
	err := Project(context.Background(), o)
	return w.said.String(), err
}

func (w *world) Run(_ context.Context, c Command) ([]byte, error) {
	w.t.Helper()
	if w.before != nil {
		w.before(c)
	}
	w.calls = append(w.calls, strings.ReplaceAll(c.String(), w.root, "ROOT"))
	args := strings.Join(c.Args, " ")
	switch c.Name {
	case "gh":
		if c.Dir != "" {
			w.t.Fatalf("gh %s ran in %s: gh names its repository, and runs where the command does", args, c.Dir)
		}
		return w.gh(c, args)
	case "git":
		w.inside(c.Dir)
		switch c.Args[0] {
		case "push":
			if args != "push -u origin main" {
				w.t.Fatalf("a founding pushed with %q", args)
			}
			if err := w.pushFails; err != nil {
				w.pushFails = nil
				return nil, err
			}
			// git refuses to push a branch that isn't there.
			head, err := w.git(c.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/main")
			if err != nil {
				return nil, errors.New("error: src refspec main does not match any")
			}
			w.pushed, w.empty = append(w.pushed, strings.TrimSpace(string(head))), false
			// A push that lands moves the checkout's record of the remote.
			return w.git(c.Dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
		case "remote", "ls-tree", "symbolic-ref", "config", "add", "commit":
			return w.git(c.Dir, c.Args...)
		}
	}
	w.t.Fatalf("a founding ran %q, which no test expects", c)
	return nil, nil
}

// gh answers as GitHub would, from the world's own record of the repository.
func (w *world) gh(c Command, args string) ([]byte, error) {
	w.t.Helper()
	protection := "repos/" + w.repo + "/branches/main/protection"
	switch {
	case args == "repo view "+w.repo+" --json isEmpty,mergeCommitAllowed,visibility":
		if !w.exists {
			return nil, fmt.Errorf("GraphQL: Could not resolve to a Repository with the name '%s'. (repository)", w.repo)
		}
		return fmt.Appendf(nil, `{"isEmpty":%t,"mergeCommitAllowed":%t,"visibility":"%s"}`, w.empty, w.mergeCommits, strings.ToUpper(w.visibility)), nil
	case args == "repo create "+w.repo+" --private" || args == "repo create "+w.repo+" --public":
		if w.exists {
			return nil, errors.New("GraphQL: Name already exists on this account (createRepository)")
		}
		// GitHub allows merge commits in a repository it creates.
		w.exists, w.empty, w.mergeCommits, w.visibility = true, true, true, strings.TrimPrefix(c.Args[3], "--")
		return []byte("https://github.com/" + w.repo + "\n"), nil
	case strings.HasPrefix(args, "repo clone "+w.repo+" ") && len(c.Args) == 4:
		if !w.exists || !w.empty {
			w.t.Fatalf("a founding cloned %s, which this world can't: it clones a repository that exists and is empty", w.repo)
		}
		dir := c.Args[3]
		w.inside(dir)
		if _, err := w.git(w.root, "init", "--quiet", dir); err != nil {
			return nil, err
		}
		return w.git(dir, "remote", "add", "origin", "https://github.com/"+w.repo+".git")
	case args == "api -X PATCH repos/"+w.repo+" -F allow_merge_commit=true":
		w.mergeCommits = true
		return []byte("{}"), nil
	case args == "api "+protection:
		if w.protection != "" {
			return []byte(w.protection), nil
		}
		if !w.required {
			return nil, errors.New("gh: Branch not protected (HTTP 404)")
		}
		return []byte(`{"required_status_checks":{"strict":false,"contexts":["invariant/gate"],"checks":[{"context":"invariant/gate","app_id":15368}]},"enforce_admins":{"enabled":true}}`), nil
	case args == "api -X PUT "+protection+" --input -":
		w.body = c.Stdin
		if w.refusal != nil {
			return nil, w.refusal
		}
		w.required = true
		return []byte("{}"), nil
	}
	w.t.Fatalf("a founding ran gh %s, which no test expects", args)
	return nil, nil
}

// inside fails the test for a directory outside the world's root.
func (w *world) inside(dir string) {
	w.t.Helper()
	rel, err := filepath.Rel(w.root, dir)
	if dir == "" || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		w.t.Fatalf("a founding reached %q, outside the directory it was given", dir)
	}
}

// git runs git in dir, with none of the machine's configuration and no way
// to reach a remote: whoever runs the test, it has an identity only when
// the world says so.
func (w *world) git(dir string, args ...string) ([]byte, error) {
	w.t.Helper()
	w.inside(dir)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=none")
	if w.identity {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=A Person",
			"GIT_CONFIG_KEY_1=user.email", "GIT_CONFIG_VALUE_1=person@example.com")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// mustGit runs git in dir and fails the test when it fails.
func (w *world) mustGit(dir string, args ...string) string {
	w.t.Helper()
	out, err := w.git(dir, args...)
	if err != nil {
		w.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// checkout makes a checkout in dir whose origin is remote, with no commit.
func (w *world) checkout(dir, remote string) {
	w.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.mustGit(dir, "init", "--quiet", ".")
	w.mustGit(dir, "remote", "add", "origin", remote)
}

// write puts a file in the world, making the directories it needs.
func (w *world) write(path, text string) {
	w.t.Helper()
	w.inside(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// tree is every file under dir but git's own, by path, with its contents.
func (w *world) tree(dir string) map[string]string {
	w.t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return files
}

// ran says whether any command so far starts with prefix.
func (w *world) ran(prefix string) bool {
	for _, call := range w.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}
