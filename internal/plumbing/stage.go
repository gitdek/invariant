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
)

// Stage lays out what a build made, for a test run or a commit: the
// checkout at root as it is, the plan's record and acceptance tests
// included, with each file the plan lets the build write taken from the
// build's workspace instead. Nothing else the build wrote counts.
func Stage(ctx context.Context, root, ws string, plan *Plan, dst string) error {
	if err := copyCheckout(ctx, root, dst); err != nil {
		return err
	}
	for _, f := range plan.Files {
		from, to := filepath.Join(ws, filepath.FromSlash(f)), filepath.Join(dst, filepath.FromSlash(f))
		info, err := os.Lstat(from)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Remove(to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		case err != nil:
			return err
		case !info.Mode().IsRegular():
			return fmt.Errorf("%s isn't a regular file", f)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := copyFile(from, to, 0o644); err != nil {
			return err
		}
	}
	return nil
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
	allowed := map[string]bool{}
	for _, f := range plan.Files {
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
	out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "-c", "-o", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the checkout's files: %w", err)
	}
	m := map[string]string{}
	for _, f := range bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0}) {
		if len(f) == 0 {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(string(f)))
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			m[string(f)] = p
		}
	}
	return m, nil
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
