package verify

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type stagingSelection struct {
	Method string
	Files  []string
	Dirs   []string
}

// stagingFiles keeps each copier's boundaries and exclusions. Git supplies
// membership, never bytes: tracked edits and nonignored new files are copied
// from the work tree, and deleted tracked files are left out.
func stagingFiles(root string, excluded func(string, bool) bool, strict bool) (*stagingSelection, error) {
	inGit, err := stagingInGit(root, strict)
	if err != nil {
		return nil, err
	}
	selection := &stagingSelection{Method: "directory"}
	if !inGit {
		err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			if rel != "." && excluded(entry.Name(), entry.IsDir()) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				selection.Dirs = append(selection.Dirs, rel)
			} else if entry.Type().IsRegular() {
				selection.Files = append(selection.Files, rel)
			} else if strict {
				return fmt.Errorf("staging %q: not a regular file", rel)
			}
			return nil
		})
		return selection, err
	}
	selection.Method = "git"
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("staging %q: not a directory", root)
	}
	listed, err := stagingGit(root, nil, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	indexed, err := stagingGit(root, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	links, err := stagingGitlinks(indexed)
	if err != nil {
		return nil, err
	}
	// Gitlinks must be rejected even when their directory is uninitialized.
	for _, name := range links {
		if !stagingExcluded(name, true, excluded) {
			return nil, fmt.Errorf("staging %q: submodules are not supported", name)
		}
	}
	seen := map[string]bool{}
	for _, item := range bytes.Split(listed, []byte{0}) {
		name := string(item)
		if name == "" || seen[name] || stagingExcluded(name, false, excluded) {
			continue
		}
		// Git lists an untracked repository as its directory, with a slash.
		if strings.HasSuffix(name, "/") {
			return nil, fmt.Errorf("staging %q: nested Git repositories are not supported", strings.TrimSuffix(name, "/"))
		}
		seen[name] = true
		info, err := stagingPath(root, name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if excluded(path.Base(name), info.IsDir()) {
			continue
		}
		// A tracked file replaced by a directory: Git lists the files under
		// it on their own, as untracked ones, unless it's a repository.
		if info.IsDir() {
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name), ".git")); err == nil {
				return nil, fmt.Errorf("staging %q: nested Git repositories are not supported", name)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			if strict {
				return nil, fmt.Errorf("staging %q: not a regular file", name)
			}
			// Copiers that skip nonregular files outside Git skip them here
			// too, so a verdict doesn't depend on which mode selected them.
			continue
		}
		selection.Files = append(selection.Files, name)
	}
	sort.Strings(selection.Files)
	selection.Dirs = []string{"."}
	return selection, nil
}

func stagingGitlinks(indexed []byte) ([]string, error) {
	var links []string
	for _, record := range bytes.Split(indexed, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, name, ok := strings.Cut(string(record), "\t")
		if !ok {
			return nil, fmt.Errorf("invalid git ls-files index record")
		}
		if strings.HasPrefix(meta, "160000 ") {
			links = append(links, name)
		}
	}
	return links, nil
}

func stagingExcluded(name string, directory bool, excluded func(string, bool) bool) bool {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if excluded(part, directory || i < len(parts)-1) {
			return true
		}
	}
	return false
}

// stagingPath checks every parent before the leaf, so a tracked directory
// replaced by a symlink cannot redirect a selected file outside the copy.
func stagingPath(root, name string) (os.FileInfo, error) {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return nil, fmt.Errorf("staging %q: path must be contained and clean", name)
	}
	parts := strings.Split(name, "/")
	full := root
	for i, part := range parts {
		full = filepath.Join(full, filepath.FromSlash(part))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, err
		}
		if i == len(parts)-1 {
			return info, nil
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("staging %q: parent %q is not a directory", name, strings.Join(parts[:i+1], "/"))
		}
	}
	return nil, fmt.Errorf("staging %q: missing path", name)
}

// Repository metadata or a worktree file makes Git authoritative. A broken
// repository or failed Git command must not widen selection to a tree walk.
// A root its repository ignores, with nothing tracked under it, holds
// nothing Git would commit, such as synthesize's default output under an
// ignored out/. Unless strict, it's selected as a directory outside Git is.
// The strict stager, the Python application's, keeps it in Git, so its
// ignored declarations are refused by name.
func stagingInGit(root string, strict bool) (bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	// Walk the real path, so a root reached through a symlink into a work
	// tree is still Git's to select.
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return false, err
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		metadata := filepath.Join(dir, ".git")
		info, err := os.Lstat(metadata)
		if err == nil {
			// A directory merely named .git is also a staging exclusion,
			// but it need not be a repository (for example, test fixtures).
			repository := !info.IsDir()
			if info.IsDir() {
				for _, name := range []string{"HEAD", "objects", "refs", "config", "index", "packed-refs", "commondir"} {
					_, err := os.Lstat(filepath.Join(metadata, name))
					if err == nil {
						repository = true
					} else if !os.IsNotExist(err) {
						return false, err
					}
				}
			}
			if repository {
				b, err := stagingGit(root, nil, "rev-parse", "--is-inside-work-tree")
				if err != nil {
					return false, err
				}
				if strings.TrimSpace(string(b)) != "true" {
					return false, fmt.Errorf("staging %s: Git did not identify a work tree", root)
				}
				if strict {
					return true, nil
				}
				return stagingSelectsByGit(root)
			}
		} else if !os.IsNotExist(err) {
			return false, err
		}
		if filepath.Dir(dir) == dir {
			return false, nil
		}
	}
}

// stagingSelectsByGit says whether a root in a work tree is Git's to select.
// One the repository ignores, with nothing tracked under it, isn't. Tracked
// files keep it Git's, so a work tree stages what a fresh clone does, even
// when a pattern or a person's own excludes cover the root.
func stagingSelectsByGit(root string) (bool, error) {
	// check-ignore exits 0 when the root is ignored and 1 when it isn't;
	// anything else is a failure, not a fallback.
	_, err := stagingGit(root, nil, "check-ignore", "-q", "--no-index", ".")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	tracked, err := stagingGit(root, nil, "ls-files", "--cached", "-z")
	if err != nil {
		return false, err
	}
	return len(tracked) != 0, nil
}

func stagingGit(root string, input []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false", "-C", root}, args...)...)
	// Select this root, not a repository or index inherited from the caller.
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES":
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return b, fmt.Errorf("git %s in %s: %w: %s", args[0], root, err, strings.TrimSpace(stderr.String()))
	}
	return b, nil
}

// Explicit declarations cannot silently disappear from the snapshot. Git's
// default check-ignore behavior admits tracked files even if patterns match.
// strict is stagingInGit's.
func stagingIgnored(root string, names []string, strict bool) error {
	inGit, err := stagingInGit(root, strict)
	if err != nil || !inGit {
		return err
	}
	indexed, err := stagingGit(root, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	links, err := stagingGitlinks(indexed)
	if err != nil {
		return err
	}
	var files, directories []byte
	for _, name := range names {
		for _, link := range links {
			if name == link || strings.HasPrefix(name, link+"/") {
				return fmt.Errorf("staging %q: submodule %q is not supported", name, link)
			}
		}
		if name == "" {
			continue
		}
		info, err := stagingPath(root, name)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		// check-ignore reads its input as pathspecs, so a name such as :x
		// would be taken as magic; ./ keeps every name literal.
		if err == nil && info.IsDir() {
			directories = append(directories, "./"+name...)
			directories = append(directories, 0)
		} else {
			files = append(files, "./"+name...)
			files = append(files, 0)
		}
	}
	for i, input := range [][]byte{files, directories} {
		if len(input) == 0 {
			continue
		}
		args := []string{"check-ignore", "--stdin", "-z"}
		if i == 1 {
			// A tracked descendant does not admit an explicitly named
			// ignored directory; only tracked files get that exception.
			args = append(args, "--no-index")
		}
		b, err := stagingGit(root, input, args...)
		// check-ignore exits 1 when no supplied name is ignored.
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				return err
			}
		}
		for _, name := range bytes.Split(b, []byte{0}) {
			if len(name) != 0 {
				return fmt.Errorf("staging %q: declared path is ignored by Git", bytes.TrimPrefix(name, []byte("./")))
			}
		}
	}
	return nil
}
