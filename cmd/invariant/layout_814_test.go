package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
)

// printsOf runs a command's function and returns its exit code and what it
// printed.
func printsOf(t *testing.T, run func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	printed := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		printed <- string(b)
	}()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	log.SetOutput(w)
	code := func() int {
		defer func() {
			os.Stdout, os.Stderr = stdout, stderr
			log.SetOutput(stderr)
		}()
		return run()
	}()
	w.Close()
	return code, <-printed
}

const layoutModel814 = "---- MODULE Small ----\nEXTENDS Naturals\nVARIABLE x\nInit == x = 0\nNext == x' = (x + 1) % 3\nSpec == Init /\\ [][Next]_x\n" +
	"Inv ==\n    /\\ x >= 0\n    /\\ x <= 3\n====\n"

// layoutCheckout814 is a repository whose one project a person ratified
// on GitHub before layout pins, committed as the factory commits it.
func layoutCheckout814(t *testing.T) (root, dir string, git func(...string) string) {
	t.Helper()
	root = t.TempDir()
	dir = filepath.Join(root, "examples", "small")
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	model, err := tla.Read(layoutModel814, nil)
	if err != nil {
		t.Fatal(err)
	}
	lock := project.Lock{Bounds: map[string]string{"N": "3"}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "x counts around."},
		{Name: "Inv", Kind: project.Invariant, Says: "x stays within 0 to 3."},
	}}
	for i, s := range lock.Statements {
		if lock.Statements[i].SHA256, err = model.PinHash(s.Name, project.Model); err != nil {
			t.Fatal(err)
		}
	}
	lock.Ratified = &project.Ratification{By: "someone", At: "2026-11-19T00:00:00Z", Issue: 1,
		Comment: "https://github.com/o/r/issues/1#issuecomment-1", Proposal: project.ProposalHash(lock.Bounds, lock.Statements)}
	b, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		".invariant/invariant.json":  `{"name": "small", "module": ".invariant/specs/Small.tla", "code": "small", "language": "go"}`,
		".invariant/specs/Small.tla": layoutModel814,
		".invariant/ratified.lock":   string(b) + "\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "Ratify the statements for #1")
	return root, dir, git
}

// invariant pin -layout migrates a lock ratified before layout pins, as
// another repository runs it when it moves to a gate with them, and leaves
// one that pins its layouts as it is (#814). Its pins and its proposal stay,
// so its ratifying comment still checks; CI's ratification check then holds
// the layout pins to the commit that recorded the ratification.
func TestPinLayoutMigratesALock814(t *testing.T) {
	_, dir, git := layoutCheckout814(t)
	ratifiedAt := git("rev-parse", "HEAD")
	before := proposalOf(t, dir)

	code, out := printsOf(t, func() int { return pinCmd([]string{"-layout", dir}) })
	if code != 0 || !strings.Contains(out, "pinned the layout of 2 statements, from the model at "+ratifiedAt+", which recorded the lock's ratification") {
		t.Fatalf("pin -layout exited %d:\n%s", code, out)
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Lock.LayoutState() != project.LayoutMigrated || proposalOf(t, dir) != before || p.Lock.Ratified.Proposal != before {
		t.Fatalf("the migrated lock is %s with proposal %s; want migrated, keeping %s", p.Lock.LayoutState(), proposalOf(t, dir), before)
	}
	code, out = printsOf(t, func() int { return pinCmd([]string{"-layout", dir}) })
	if code != 0 || !strings.Contains(out, "the lock already pins its statements' layouts (migrated)") {
		t.Errorf("pin -layout of a migrated lock exited %d:\n%s", code, out)
	}
	if note := layoutNote(p.Lock); note != ", layout migrated from the commit that recorded it" {
		t.Errorf("the ratification check would say %q", note)
	}

	// invariant pin on the migrated lock, nothing changed: it stays.
	code, out = printsOf(t, func() int { return pinCmd([]string{dir}) })
	if code != 0 || !strings.Contains(out, "Every pin and layout pin already matches the statements.") {
		t.Errorf("pin exited %d:\n%s", code, out)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "Pin the locks' layouts (#814)")

	// A pull request that re-indents Inv and moves its migrated layout pin
	// to match is refused before anything asks GitHub about the comment.
	moved := strings.Replace(layoutModel814, "    /\\ x <= 3", "      /\\ x <= 3", 1)
	if err := os.WriteFile(filepath.Join(dir, ".invariant", "specs", "Small.tla"), []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := tla.Read(moved, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range p.Lock.Statements {
		if p.Lock.Statements[i].MigratedLayout, err = model.LayoutHash(s.Name, project.Model); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.MarshalIndent(p.Lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".invariant", "ratified.lock"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "Re-indent Inv")
	code, out = printsOf(t, func() int { return ratificationCmd(context.Background(), []string{"-repo", "o/r", dir}) })
	if code != 1 || !strings.Contains(out, "❌ "+dir+": its layout pins: Inv's migrated layout pin is") || !strings.Contains(out, "only a new ratification changes a layout pin") {
		t.Errorf("the ratification check exited %d:\n%s", code, out)
	}
}

// invariant pin on a lock that pins no layout, nothing changed, says how to
// add its layout pins, and changes nothing.
func TestPinSaysHowToMigrate814(t *testing.T) {
	_, dir, _ := layoutCheckout814(t)
	before := proposalOf(t, dir)
	code, out := printsOf(t, func() int { return pinCmd([]string{dir}) })
	if code != 0 || !strings.Contains(out, "The lock pins no layout yet: `invariant pin -layout "+dir+"`") {
		t.Errorf("pin exited %d:\n%s", code, out)
	}
	if proposalOf(t, dir) != before {
		t.Error("pin changed a lock nothing changed in")
	}
}
