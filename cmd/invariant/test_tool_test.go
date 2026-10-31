package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

// A plumbing build's test tool checks its agent's changes as the factory's
// own checks do: a test the original checkout holds in a package the plan
// touches, which the agent may not edit, never fails its formatting, and a
// regression test the agent adds there still has to pass it (#298).
func TestThePlumbingTestToolFormatsOnlyTheTestsTheAgentAdds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in docker is a shell script")
	}
	ctx := context.Background()
	bin, root, ws := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(dir, name, text string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The stand-in flags each file it's given for gofmt that holds an
	// unformatted test signature, as gofmt would, and passes the rest.
	write(bin, "docker", `#!/bin/sh
[ "$1" = run ] || exit 0
src= fmt=
for a in "$@"; do
  case "$a" in
    *:/src) src=${a%:/src} ;;
    GOFMT_FILES=*) fmt=${a#GOFMT_FILES=} ;;
  esac
done
for f in $fmt; do
  if [ -f "$src/$f" ] && grep -qF 'testing.T){' "$src/$f"; then echo "@@gofmt $f"; fi
done
echo '@@invariant vet=0'
echo '@@invariant test=0'
echo '--- PASS: TestPageSaysHello (0.00s)'
echo '@@invariant accept=0'
`)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	const accept = "package page\n\nimport \"testing\"\n\nfunc TestPageSaysHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"Hello() isn't hello\")\n\t}\n}\n"
	p := plumbing.Plan{Name: "a hello page", Summary: "Add a page that says hello.", Files: []string{"page/page.go"},
		Tests:   []plumbing.Test{{Name: "TestPageSaysHello", File: "page/page_accept_test.go", Says: "The page says hello."}},
		Sources: map[string]string{"page/page_accept_test.go": accept}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	lock := plumbing.Lock{Ratified: plumbing.Ratification{By: "gitdek", Issue: 7, Comment: "https://github.com/o/r/issues/7#issuecomment-1", Proposal: p.Hash()}, Plan: p}
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	write(root, "page/page.go", "package page\n")
	write(root, "page/existing_test.go", "package page\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T){}\n")
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "-A"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "Ratify the plan for #7"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := plumbing.CopyCheckout(ctx, root, ws); err != nil {
		t.Fatal(err)
	}

	tool := testTool(root, 7, ws, plumbing.Sandbox{Image: "stand-in", ModCache: t.TempDir()}, 2, "")
	write(ws, "page/page.go", "package page\n\n// Hello says hello.\nfunc Hello() string { return \"hello\" }\n")
	if out, failed := tool.Call(ctx, json.RawMessage(`{}`)); failed || !strings.HasPrefix(out, "Everything passed") {
		t.Errorf("an existing test the agent may not edit failed its run: failed %v\n%s", failed, out)
	}
	write(ws, "page/review_test.go", "package page\n\nimport \"testing\"\n\nfunc TestReview(t *testing.T){}\n")
	if out, failed := tool.Call(ctx, json.RawMessage(`{}`)); failed || !strings.Contains(out, "gofmt would change: page/review_test.go\n") {
		t.Errorf("the agent's unformatted regression test escaped gofmt, or another file was flagged: failed %v\n%s", failed, out)
	}
}
