package decisions

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Grown checks that a repository's journal only grew since base, a git ref:
// every line base has is still in the checkout, in its place. A pull request
// adds lines and files to the journal and never changes or removes one. Each
// file's hash chain can't show a file rewritten whole, or removed, and this
// can.
func Grown(ctx context.Context, repo Repo, base string) ([]string, error) {
	list, err := exec.CommandContext(ctx, "git", "-C", repo.Dir, "ls-tree", "-r", "-z", "--name-only", base, "--", "decisions/journal/").Output()
	if err != nil {
		return nil, fmt.Errorf("listing %s's journal at %s: %w", repo.Name, base, err)
	}
	var names []string
	for _, name := range strings.Split(strings.TrimRight(string(list), "\x00"), "\x00") {
		if strings.HasSuffix(name, ".jsonl") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	// One git process reads every file as base has it.
	var in bytes.Buffer
	for _, name := range names {
		fmt.Fprintf(&in, "%s:%s\n", base, name)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repo.Dir, "cat-file", "--batch")
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s's journal at %s: %w", repo.Name, base, err)
	}
	r := bufio.NewReader(bytes.NewReader(out))
	var problems []string
	for _, name := range names {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading %s at %s: %w", name, base, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[1] != "blob" {
			return nil, fmt.Errorf("reading %s at %s: git said %q", name, base, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, err
		}
		was := make([]byte, size+1) // the content, and the newline git ends it with
		if _, err := io.ReadFull(r, was); err != nil {
			return nil, err
		}
		was = was[:size]
		id := strings.TrimSuffix(filepath.Base(name), ".jsonl")
		now, err := os.ReadFile(filepath.Join(repo.Dir, filepath.FromSlash(name)))
		switch {
		case os.IsNotExist(err):
			problems = append(problems, fmt.Sprintf("%s/%s's journal is gone, but it only grows", repo.Name, id))
		case err != nil:
			return nil, err
		case !bytes.HasPrefix(now, was):
			problems = append(problems, fmt.Sprintf("%s/%s's journal changed a line it had at %s, but it only grows", repo.Name, id, base))
		}
	}
	return problems, nil
}
