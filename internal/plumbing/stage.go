package plumbing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

// stageRepositoryBase reads only the captured commit. Neither the planner's
// workspace nor working-tree edits in the original checkout supply base code.
func stageRepositoryBase(ctx context.Context, repo string, snapshot *repository.Snapshot, dst string) error {
	read := func(args ...string) ([]byte, error) {
		options := []string{"--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "protocol.allow=never"}
		cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
		cmd.Dir = repo
		for _, variable := range os.Environ() {
			if !strings.HasPrefix(variable, "GIT_") {
				cmd.Env = append(cmd.Env, variable)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return out, err
	}
	out, err := read("ls-tree", "-r", "-z", "--full-tree", snapshot.Commit())
	if err != nil {
		return fmt.Errorf("listing the captured base: %w", err)
	}
	for _, entry := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		metadata, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 {
			return fmt.Errorf("invalid captured base entry %q", entry)
		}
		if fields[1] != "blob" || fields[0] != "100644" && fields[0] != "100755" {
			return fmt.Errorf("captured base entry %q is not a regular file (mode %s, type %s)", name, fields[0], fields[1])
		}
		local, err := filepath.Localize(name)
		if err != nil || local == "." {
			return fmt.Errorf("invalid captured base path %q", name)
		}
		for _, component := range strings.Split(name, "/") {
			if strings.EqualFold(component, ".git") {
				return fmt.Errorf("captured base path %q contains Git metadata", name)
			}
		}
		oid := fields[2]
		if len(oid) != 40 && len(oid) != 64 || strings.Trim(oid, "0123456789abcdef") != "" {
			return fmt.Errorf("invalid captured base object %q", oid)
		}
		// Tree names are literal filenames, not declaration paths or globs.
		// Read the captured object with the same isolation as the tree listing.
		data, err := read("cat-file", "blob", oid)
		if err != nil {
			return fmt.Errorf("reading captured base file %q: %w", name, err)
		}
		to := filepath.Join(dst, local)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if fields[0] == "100755" {
			perm = 0o755
		}
		if err := os.WriteFile(to, data, perm); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Stage lays out what a build made, for a test run or a commit: the
// checkout at root as it is, the plan's record and acceptance tests
// included, with named writable files and new regression tests in the
// plan's packages taken from the workspace. root remains the original
// checkout throughout repairs. Nothing else the build wrote counts.
func Stage(ctx context.Context, root, ws string, plan *Plan, dst string) error {
	if plan.Native() {
		if err := plan.ratifiable(); err != nil {
			return err
		}
		for f, source := range plan.Sources {
			data, err := regular.ReadFile(root, f)
			if err != nil || !SameSource(string(data), source) {
				return fmt.Errorf("original acceptance source %s differs from the ratified plan: %v", f, err)
			}
		}
	}
	writable, err := writableFiles(ctx, root, ws, plan)
	if err != nil {
		return err
	}
	if err := copyCheckout(ctx, root, dst); err != nil {
		return err
	}
	if plan.Native() {
		// The checkout holds each source as git stored it. The tests run
		// the bytes that were ratified.
		for f, source := range plan.Sources {
			if err := os.WriteFile(filepath.Join(dst, filepath.FromSlash(f)), []byte(source), 0o644); err != nil {
				return err
			}
		}
	}
	for _, f := range writable {
		if err := take(ws, f, filepath.Join(dst, filepath.FromSlash(f))); err != nil {
			return err
		}
	}
	return nil
}

// SameSource says whether data is a ratified acceptance source as git may
// store or check it out: the same bytes up to line-ending normalization,
// such as a repository's text=auto. Go, Python and JavaScript read either
// ending the same way, so a normalized source is still the one ratified.
func SameSource(data, source string) bool {
	return data == source || strings.ReplaceAll(data, "\r\n", "\n") == strings.ReplaceAll(source, "\r\n", "\n")
}

func writableFiles(ctx context.Context, root, ws string, plan *Plan) ([]string, error) {
	base, err := checkoutNames(ctx, root)
	if err != nil {
		return nil, err
	}
	now, err := walk(ws)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, len(now))
	for f := range now {
		candidates = append(candidates, f)
	}
	return append(append([]string(nil), plan.Files...), plan.RegressionFiles(base, candidates)...), nil
}

// take copies a file the plan names from the build's workspace to to, or
// removes to when the build removed it. It takes a regular file only, and
// refuses a link by name, never following it (#153).
func take(ws, f, to string) error {
	text, err := regular.ReadFile(ws, f)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Remove(to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	case err != nil:
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, text, 0o644)
}

// Changes is what a build did in its workspace, set against the checkout
// it started from.
type Changes struct {
	Written []string // files the plan lets it write, changed, added or removed
	Outside []string // files it changed that the plan doesn't let it write, which don't count
}

// Diff compares the build's workspace with the checkout it was copied from.
func Diff(ctx context.Context, root, ws string, plan *Plan) (Changes, error) {
	var c Changes
	writable, err := writableFiles(ctx, root, ws, plan)
	if err != nil {
		return c, err
	}
	allowed := map[string]bool{}
	for _, f := range writable {
		allowed[f] = true
	}
	was, err := files(ctx, root)
	if err != nil {
		return c, err
	}
	now, err := walk(ws)
	if err != nil {
		return c, err
	}
	seen := map[string]bool{}
	for f := range was {
		seen[f] = true
	}
	for f := range now {
		seen[f] = true
	}
	for f := range seen {
		a, aok := was[f]
		b, bok := now[f]
		if aok == bok && (!aok || bytes.Equal(read(a), read(b))) {
			continue
		}
		if allowed[f] {
			c.Written = append(c.Written, f)
		} else {
			c.Outside = append(c.Outside, f)
		}
	}
	sort.Strings(c.Written)
	sort.Strings(c.Outside)
	return c, nil
}

func read(p string) []byte {
	b, _ := os.ReadFile(p)
	return b
}

// files is every file in a checkout that git tracks or would add, by path
// from its root.
func files(ctx context.Context, root string) (map[string]string, error) {
	names, err := checkoutNames(ctx, root)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, f := range names {
		p := filepath.Join(root, filepath.FromSlash(f))
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			m[f] = p
		}
	}
	return m, nil
}

// checkoutNames also works for an immutable baseline copied outside git.
// Git's inventory includes non-regular paths, so replacing an existing
// link cannot turn it into an allowed new regression test.
func checkoutNames(ctx context.Context, root string) ([]string, error) {
	if !isTop(ctx, root) {
		var names []string
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			names = append(names, filepath.ToSlash(rel))
			return nil
		})
		return names, err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "-c", "-o", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the checkout's files: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return strings.Split(strings.TrimRight(string(out), "\x00"), "\x00"), nil
}

// walk is every regular file under dir, by its slash path from dir.
func walk(dir string) (map[string]string, error) {
	m := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			m[filepath.ToSlash(rel)] = p
		}
		return nil
	})
	return m, err
}

// CopyCheckout copies a checkout's files, tracked and new, to dst: a
// workspace an agent can work in outside the home directory.
func CopyCheckout(ctx context.Context, root, dst string) error { return copyCheckout(ctx, root, dst) }
