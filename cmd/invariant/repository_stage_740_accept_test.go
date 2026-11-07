package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func quoteGate740(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// A POSIX sh wrapper observes Git's object boundary. It never runs the
// generated workflow, Bash, Docker, or any candidate executable.
func wrapObjectReader740(t *testing.T, body string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "objects.log")
	script := "#!/bin/sh\nset -eu\nobject=no\nbatch=no\nfor arg do\n" +
		"  [ \"$arg\" != cat-file ] || object=yes\n  [ \"$arg\" != --batch ] || batch=yes\ndone\n" +
		"if [ \"$object\" = yes ]; then\n  printf '%s\\n' \"$batch\" >> " + quoteGate740(log) + "\n" + body + "\nfi\n" +
		"exec " + quoteGate740(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestRepositoryGate740BatchStaging(t *testing.T) {
	root := t.TempDir()
	gateGit736(t, root, "init", "-q", "-b", "main")
	files := map[string]string{
		"data/binary":      "\x00\xff\nnot a batch header\n\x00",
		"data/empty":       "",
		"data/space name":  "space\n",
		"data/tab\tname":   "tab\n",
		"data/line\nbreak": "newline in a path\n",
		".gitattributes":   "data/* export-ignore\n",
		"bin/run-fixture":  "#!/bin/sh\nexit 0\n",
	}
	for i := range 512 {
		files[fmt.Sprintf("data/%03d/item-%04d", i%16, i)] = fmt.Sprintf("unique committed content %d\n", i)
	}
	for name, body := range files {
		gateWrite736(t, root, name, body)
	}
	if err := os.Chmod(filepath.Join(root, "bin/run-fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := gateCommit736(t, root)
	gateWrite736(t, root, "data/space name", "candidate committed value\n")
	head := gateCommit736(t, root)
	gateWrite736(t, root, "data/space name", "dirty value must never be staged\n")
	log := wrapObjectReader740(t, "")
	work := filepath.Join(t.TempDir(), "ci-stage")
	for _, tree := range []struct{ commit, dir string }{{base, "base"}, {head, "candidate"}} {
		dst := filepath.Join(work, tree.dir)
		if err := stageRepositoryCommit(t.Context(), root, tree.commit, dst); err != nil {
			t.Fatal(err)
		}
		for name, want := range files {
			if tree.dir == "candidate" && name == "data/space name" {
				want = "candidate committed value\n"
			}
			b, err := os.ReadFile(filepath.Join(dst, name))
			if err != nil || !bytes.Equal(b, []byte(want)) {
				t.Fatalf("%s/%q changed during staging: %q, %v", tree.dir, name, b, err)
			}
		}
		info, err := os.Stat(filepath.Join(dst, "bin/run-fixture"))
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("executable mode was lost: %v %v", info, err)
		}
	}
	if err := cleanupRepositoryCI(work); err != nil {
		t.Fatalf("tree cleanup failed: %v", err)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("CI returned before removing both staged trees: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "yes\nyes\n" {
		t.Fatalf("staging two trees must start exactly two cat-file --batch processes, one per tree; got %d processes (%d batch)", len(strings.Fields(string(b))), strings.Count(string(b), "yes\n"))
	}
}

func TestRepositoryGate740BatchFailures(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"wrong object", "printf '%s blob 4\\nabc\\n\\n' 0000000000000000000000000000000000000000\n"},
		{"wrong type", "printf '%s tree 4\\nabc\\n\\n' \"$oid\"\n"},
		{"invalid size", "printf '%s blob nope\\n' \"$oid\"\n"},
		{"negative size", "printf '%s blob -1\\n' \"$oid\"\n"},
		{"truncated body", "printf '%s blob 99\\nabc\\n\\n' \"$oid\"\n"},
		{"missing terminator", "printf '%s blob 4\\nabc\\n' \"$oid\"\n"},
		{"process failure", "printf '%s blob 4\\nabc\\n\\n' \"$oid\"\nexit 7\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			gateGit736(t, root, "init", "-q", "-b", "main")
			gateWrite736(t, root, "file", "abc\n")
			commit := gateCommit736(t, root)
			wrapObjectReader740(t, "  if [ \"$batch\" = yes ]; then\n    IFS= read -r oid\n"+tc.response+"    exit 0\n  fi")
			err := stageRepositoryCommit(t.Context(), root, commit, filepath.Join(t.TempDir(), "stage"))
			if err == nil {
				t.Fatalf("staging accepted %s from the batch object process", tc.name)
			}
		})
	}
	t.Run("canceled staging", func(t *testing.T) {
		root := t.TempDir()
		gateGit736(t, root, "init", "-q", "-b", "main")
		gateWrite736(t, root, "file", "abc\n")
		commit := gateCommit736(t, root)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := stageRepositoryCommit(ctx, root, commit, filepath.Join(t.TempDir(), "stage")); err == nil {
			t.Fatal("canceled staging succeeded")
		}
	})
}
