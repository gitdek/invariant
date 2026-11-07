package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/scope"
	"github.com/gitdek/invariant/internal/setup"
)

func checkRepositoryCmd(ctx context.Context, args []string) int {
	return runRepositoryCheck(ctx, args, plumbing.Sandbox{}, nil, os.Stdout)
}

// The injected sandbox supplies execution boundaries and shorter allowances,
// never a plan, snapshot, original checkout or alternate acceptance adapter.
func runRepositoryCheck(ctx context.Context, args []string, sb plumbing.Sandbox, comments factory.Comments, out io.Writer) (code int) {
	fs := flag.NewFlagSet("check-repository", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("C", ".", "the git repository")
	base := fs.String("base", "HEAD", "committed authority; an explicit base enables PR ownership selection")
	issue := fs.Int("issue", 0, "the factory issue; omitted for ordinary CI")
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "the repository ratifying comments must be in, as owner/name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	explicitBase, explicitIssue := false, false
	fs.Visit(func(f *flag.Flag) {
		explicitBase = explicitBase || f.Name == "base"
		explicitIssue = explicitIssue || f.Name == "issue"
	})
	if fs.NArg() > 1 || explicitIssue && *issue <= 0 || *issue > 0 && *repo == "" {
		fmt.Fprintln(out, "usage: invariant check-repository [-C DIR] [-base REF] [-issue N -repo OWNER/NAME] [HEAD]")
		return 2
	}
	limit := plumbing.SandboxTimeout
	if sb.Timeout > 0 && sb.Timeout < limit {
		limit = sb.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	fail := func(err error) int {
		fmt.Fprintln(out, "invariant:", err)
		return 1
	}
	checkout, err := filepath.Abs(*dir)
	if err != nil {
		return fail(err)
	}
	headRef := "HEAD"
	if fs.NArg() == 1 {
		headRef = fs.Arg(0)
	}
	head, err := repositoryCommit(ctx, checkout, headRef)
	if err != nil {
		return fail(err)
	}
	snapshot, err := repository.Load(ctx, checkout, *base)
	if errors.Is(err, repository.ErrMissing) {
		fmt.Fprintln(out, "No committed repository check configuration; modeled-project checks apply.")
		return 0
	}
	if err != nil {
		return fail(err)
	}
	baseCommit := snapshot.Commit()
	work, err := os.MkdirTemp("", "invariant-repository-ci-")
	if err != nil {
		return fail(err)
	}
	defer func() {
		if err := cleanupRepositoryCI(work); err != nil {
			code = fail(fmt.Errorf("repository CI cleanup: %w", err))
		}
	}()
	plan := &plumbing.Plan{Files: []string{repository.Path}}
	original := baseCommit
	if *issue > 0 {
		// Scope reads Git itself. A private object repository excludes the
		// checkout's config and replacement refs; environment redirection must
		// not send that separate reader back to another repository.
		if err := repositoryScopeEnvironment(); err != nil {
			return fail(err)
		}
		objects := filepath.Join(work, "objects.git")
		if _, err := repositoryGit(ctx, checkout, "-c", "protocol.file.allow=always", "clone", "--bare", "--shared", "--template=", "--quiet", "--", checkout, objects); err != nil {
			return fail(err)
		}
		r, err := scope.Check(ctx, objects, baseCommit, head, *issue)
		if err != nil {
			return fail(fmt.Errorf("factory scope: %w", err))
		}
		if !r.OK() {
			return fail(fmt.Errorf("factory scope: %s", strings.Join(r.Problems, "; ")))
		}
		if !r.Plumbing {
			// A modeled change adds no dependencies (D-0014). Scope reads only
			// its own languages' manifests, so check each part's declared
			// inputs and lockfiles as the base contract names them.
			for _, part := range snapshot.Config().Parts {
				for _, name := range append(slices.Clone(part.Inputs), part.Lockfiles...) {
					if slices.Contains(r.Files, name) {
						return fail(fmt.Errorf("factory scope: a modeled change can't change part %s's dependency input %s", part.Name, name))
					}
				}
			}
			// It selects parts as an ordinary pull request does: files no part
			// owns select every part.
			if parts, err := snapshot.PartsFor(r.Files); err == nil && len(parts) > 0 {
				plan.Files = r.Files
			}
		} else {
			data, err := repositoryBlob(ctx, objects, head, plumbing.LockPath(*issue))
			if err != nil {
				return fail(err)
			}
			lock, err := plumbing.ReadLock(data)
			if err != nil {
				return fail(err)
			}
			if r.Project != plumbing.LockPath(*issue) || lock.Ratified.Issue != *issue || !lock.Plan.Native() {
				return fail(fmt.Errorf("configured factory checks require issue #%d's ratified native plan", *issue))
			}
			plan = &lock.Plan
			from, err := repositoryMergeBase(ctx, objects, baseCommit, head)
			if err != nil {
				return fail(err)
			}
			original = from
			captured, err := repository.Load(ctx, objects, original)
			if err == nil {
				err = plan.ValidateRepository(ctx, captured)
			}
			if err == nil {
				err = plan.ValidateRepository(ctx, snapshot)
			}
			if err != nil {
				return fail(err)
			}
			if comments == nil {
				comments = github.Client{Repo: *repo}
			}
			if err := factory.VerifyPlan(ctx, comments, *repo, *lock); err != nil {
				return fail(err)
			}
			// Use the original inventory and inputs even after compatible movement
			// of the current base. Scope has checked pinned sources and regressions.
			snapshot = captured
		}
	} else if explicitBase {
		from, err := repositoryMergeBase(ctx, checkout, baseCommit, head)
		if err == nil {
			changed, err := repositoryGit(ctx, checkout, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", from, head, "--")
			if err == nil {
				files := strings.Split(strings.TrimSuffix(string(changed), "\x00"), "\x00")
				if parts, err := snapshot.PartsFor(files); err == nil && len(parts) > 0 {
					plan.Files = files
				}
			}
		}
		// Missing merge bases, empty diffs and unowned paths select all parts.
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	baseline, candidate := filepath.Join(work, "base"), filepath.Join(work, "candidate")
	if err := stageRepositoryCommit(ctx, checkout, original, baseline); err != nil {
		return fail(err)
	}
	if err := stageRepositoryCommit(ctx, checkout, head, candidate); err != nil {
		return fail(err)
	}
	if !plan.Native() {
		// Ordinary CI has no ratified file allowance. Name the captured
		// runner's additional suite files explicitly so its factory-only
		// implicit-regression policy cannot reject an ordinary policy edit.
		before, err := repositoryTreeNames(ctx, checkout, original)
		if err != nil {
			return fail(err)
		}
		after, err := repositoryTreeNames(ctx, checkout, head)
		if err != nil {
			return fail(err)
		}
		regressions, err := plumbing.NewRegressionScope(plan, before, func(name string) ([]byte, error) {
			return os.ReadFile(filepath.Join(baseline, filepath.FromSlash(name)))
		})
		if err != nil {
			return fail(err)
		}
		plan.Files = append(plan.Files, regressions.Files(after)...)
	}
	dependencies := plumbing.DependencyPreparer{CacheDir: filepath.Join(work, "dependencies"), Platform: "linux/amd64"}
	if sb.Repository != nil {
		dependencies.Runner = sb.Repository.Dependencies.Runner
		dependencies.Timeout = sb.Repository.Dependencies.Timeout
		if sb.Repository.Dependencies.CacheDir != "" {
			dependencies.CacheDir = sb.Repository.Dependencies.CacheDir
		}
		if sb.Repository.Dependencies.Platform != "" {
			dependencies.Platform = sb.Repository.Dependencies.Platform
		}
	}
	sb = plumbing.Sandbox{Original: baseline, Timeout: sb.Timeout, ShardTimeout: sb.ShardTimeout,
		Repository: &plumbing.RepositoryChecks{Snapshot: snapshot, Dependencies: dependencies}}
	result, err := sb.Run(ctx, candidate, plan)
	fmt.Fprint(out, result.Summary())
	fmt.Fprint(out, result.Output)
	if err != nil {
		return fail(err)
	}
	if !result.Passed() || ctx.Err() != nil {
		return 1
	}
	return 0
}

func cleanupRepositoryCI(dir string) error {
	return plumbing.CleanupRepositoryStage(dir)
}

// scope.Check owns its Git subprocesses. Allow inert identity and maintenance
// settings, but refuse environment policy that could redirect those reads.
func repositoryScopeEnvironment() error {
	allowed := map[string]bool{}
	if count := os.Getenv("GIT_CONFIG_COUNT"); count != "" {
		n, err := strconv.Atoi(count)
		if err != nil || n < 0 || n > 32 {
			return errors.New("factory scope: invalid GIT_CONFIG_COUNT")
		}
		allowed["GIT_CONFIG_COUNT"] = true
		for i := range n {
			key, value := "GIT_CONFIG_KEY_"+strconv.Itoa(i), "GIT_CONFIG_VALUE_"+strconv.Itoa(i)
			setting, contents := strings.ToLower(os.Getenv(key)), strings.ToLower(os.Getenv(value))
			if setting != "maintenance.auto" || contents != "false" {
				if setting != "gc.auto" || contents != "0" {
					return fmt.Errorf("factory scope: inherited Git setting %s must be removed", key)
				}
			}
			allowed[key], allowed[value] = true, true
		}
	}
	for _, variable := range os.Environ() {
		name, value, _ := strings.Cut(variable, "=")
		if !strings.HasPrefix(name, "GIT_") || allowed[name] || strings.HasPrefix(name, "GIT_AUTHOR_") || strings.HasPrefix(name, "GIT_COMMITTER_") {
			continue
		}
		switch {
		case (name == "GIT_CONFIG_GLOBAL" || name == "GIT_CONFIG_SYSTEM") && value == os.DevNull:
		case (name == "GIT_CONFIG_NOSYSTEM" || name == "GIT_NO_LAZY_FETCH" || name == "GIT_NO_REPLACE_OBJECTS" || name == "GIT_LFS_SKIP_SMUDGE") && value == "1":
		case (name == "GIT_TERMINAL_PROMPT" || name == "GIT_OPTIONAL_LOCKS") && value == "0":
		case name == "GIT_EDITOR" || name == "GIT_SEQUENCE_EDITOR" || name == "GIT_PAGER":
			// Scope's reads are non-interactive, so no editor or pager runs.
		default:
			return fmt.Errorf("factory scope requires an unredirected Git environment; unset %s", name)
		}
	}
	return nil
}

func checkRepositorySetup(ctx context.Context, gh setup.GitHub, repo, checkout, base string) error {
	commit, err := repositoryCommit(ctx, checkout, base)
	if err != nil {
		return err
	}
	_, err = repository.Load(ctx, checkout, commit)
	if err != nil && !errors.Is(err, repository.ErrMissing) {
		return err
	}
	return setup.CheckRepository(ctx, repositorySetupGitHub{gh, commit}, repo, strings.TrimPrefix(base, "origin/"), err == nil)
}

// Read the workflow at the same commit as the contract while retaining the
// branch's name in the manual upgrade instructions.
type repositorySetupGitHub struct {
	setup.GitHub
	commit string
}

func (g repositorySetupGitHub) Workflows(ctx context.Context, _ string) (map[string]string, error) {
	return g.GitHub.Workflows(ctx, g.commit)
}

func repositoryCommit(ctx context.Context, dir, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("invalid repository revision %q", ref)
	}
	data, err := repositoryGit(ctx, dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(data))
	if len(commit) != 40 && len(commit) != 64 || strings.Trim(commit, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid repository commit %q", commit)
	}
	return commit, nil
}

func repositoryTreeNames(ctx context.Context, dir, commit string) ([]string, error) {
	data, err := repositoryGit(ctx, dir, "ls-tree", "-r", "-z", "--name-only", "--full-tree", commit)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), nil
}

func repositoryMergeBase(ctx context.Context, dir, base, head string) (string, error) {
	data, err := repositoryGit(ctx, dir, "merge-base", "--all", base, head)
	if err != nil {
		return "", err
	}
	commits := strings.Fields(string(data))
	if len(commits) != 1 {
		return "", errors.New("repository checks require one unambiguous merge base")
	}
	return commits[0], nil
}

func repositoryGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := repositoryGitCommand(ctx, dir, args...)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], errors.Join(err, ctx.Err()), strings.TrimSpace(diagnostics.String()))
	}
	return data, ctx.Err()
}

func repositoryGitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	options := []string{"--no-replace-objects", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false", "-c", "protocol.allow=never", "-c", "credential.helper="}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	cmd.Dir = dir
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

func repositoryBlob(ctx context.Context, dir, commit, name string) ([]byte, error) {
	entries, err := repositoryGit(ctx, dir, "ls-tree", "-z", commit, "--", name)
	if err != nil {
		return nil, err
	}
	entry := strings.TrimSuffix(string(entries), "\x00")
	meta, file, ok := strings.Cut(entry, "\t")
	fields := strings.Fields(meta)
	if !ok || file != name || len(fields) != 3 || fields[1] != "blob" || fields[0] != "100644" && fields[0] != "100755" {
		return nil, fmt.Errorf("%s at %s is not a committed regular file", name, commit)
	}
	return repositoryGit(ctx, dir, "cat-file", "blob", fields[2])
}

// refuseGitLayout refuses a tree with a directory Git would open as a
// repository: a HEAD file beside objects/, refs/ or commondir. The sandbox
// runs plain git in the staged copy, outside Docker, and Git would read that
// directory's own config, which can name commands to run.
func refuseGitLayout(listing []byte) error {
	type markers struct{ head, other bool }
	dirs := map[string]*markers{}
	mark := func(dir string) *markers {
		if dirs[dir] == nil {
			dirs[dir] = &markers{}
		}
		return dirs[dir]
	}
	for _, entry := range strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00") {
		_, name, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		components := strings.Split(name, "/")
		for i, component := range components {
			dir := strings.Join(components[:i], "/")
			last := i == len(components)-1
			switch {
			case last && strings.EqualFold(component, "HEAD"):
				mark(dir).head = true
			case last && strings.EqualFold(component, "commondir"), !last && (strings.EqualFold(component, "objects") || strings.EqualFold(component, "refs")):
				mark(dir).other = true
			}
		}
	}
	for dir, m := range dirs {
		if m.head && m.other {
			if dir == "" {
				dir = "."
			}
			return fmt.Errorf("committed tree lays out a Git repository at %q, whose configuration Git would read; remove it from the repository", dir)
		}
	}
	return nil
}

// Read objects directly: archive attributes, checkout filters and mutable
// working-tree files cannot omit, rewrite or replace committed test inputs.
func stageRepositoryCommit(ctx context.Context, dir, commit, dst string) (stageErr error) {
	listing, err := repositoryGit(ctx, dir, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return err
	}
	if err := refuseGitLayout(listing); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	objects, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := repositoryGitCommand(objects, dir, "cat-file", "--batch")
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		if stageErr != nil {
			cancel()
		}
		if err := cmd.Wait(); err != nil {
			stageErr = errors.Join(stageErr, fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(diagnostics.String())))
		}
		stageErr = errors.Join(stageErr, ctx.Err())
	}()
	reader := bufio.NewReader(stdout)
	for _, entry := range strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00") {
		if entry == "" {
			continue
		}
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || fields[0] != "100644" && fields[0] != "100755" {
			return fmt.Errorf("committed entry %q is not a regular file", entry)
		}
		oid := fields[2]
		if len(oid) != 40 && len(oid) != 64 || strings.Trim(oid, "0123456789abcdef") != "" {
			return fmt.Errorf("invalid committed object %q", oid)
		}
		local, err := filepath.Localize(name)
		if err != nil || local == "." {
			return fmt.Errorf("invalid committed path %q", name)
		}
		for _, component := range strings.Split(name, "/") {
			if strings.EqualFold(component, ".git") {
				return fmt.Errorf("committed path %q contains Git metadata", name)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := io.WriteString(stdin, oid+"\n"); err != nil {
			return err
		}
		// Bound the header and stream the exact advertised body, so binary
		// contents and large blobs never become protocol or allocation sizes.
		header, err := reader.ReadSlice('\n')
		if err != nil {
			return fmt.Errorf("read object %s header: %w", oid, err)
		}
		response := strings.Split(strings.TrimSuffix(string(header), "\n"), " ")
		if len(response) != 3 || response[0] != oid || response[1] != "blob" || response[2] == "" || strings.Trim(response[2], "0123456789") != "" {
			return fmt.Errorf("invalid batch response for %s: %q", oid, header)
		}
		size, err := strconv.ParseInt(response[2], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid object length: %w", err)
		}
		to := filepath.Join(dst, local)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if fields[0] == "100755" {
			mode = 0o755
		}
		file, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, reader, size)
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return fmt.Errorf("stage object %s: %w", oid, err)
		}
		terminator, err := reader.ReadByte()
		if err != nil || terminator != '\n' {
			return fmt.Errorf("missing batch terminator for %s: %v", oid, err)
		}
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return fmt.Errorf("unexpected batch output after tree: %v", err)
	}
	return ctx.Err()
}
