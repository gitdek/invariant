package plumbing

import (
	"bufio"
	"context"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// edges298Docker stands in for docker. It records the files a run gives
// gofmt and the packages it gives go test, flags each of those files that
// holds an unformatted test signature, as gofmt would, and fails a package
// whose directory holds no Go files, as go test does.
const edges298Docker = `#!/bin/sh
[ "$1" = run ] || exit 0
src= fmt= pkgs=
for a in "$@"; do
  case "$a" in
    *:/src) src=${a%:/src} ;;
    GOFMT_FILES=*) fmt=${a#GOFMT_FILES=} ;;
    TEST_PKGS=*) pkgs=${a#TEST_PKGS=} ;;
  esac
done
printf 'gofmt %s\ntest %s\n' "$fmt" "$pkgs" >> "$EDGES298_SEEN"
for f in $fmt; do
  if [ -f "$src/$f" ] && grep -qF 'testing.T){' "$src/$f"; then echo "@@gofmt $f"; fi
done
echo '@@invariant vet=0'
code=0
for p in $pkgs; do
  ls "$src/$p"/*.go > /dev/null 2>&1 || { echo "go: $p holds no Go files"; code=1; }
done
echo "@@invariant test=$code"
echo '--- PASS: TestPageSaysHello (0.00s)'
echo '@@invariant accept=0'
`

func edges298Write(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// edges298Checkout is a git checkout of files and issue 7's ratified plan,
// with edges298Docker on the PATH. It returns the checkout and the file
// where the stand-in records what each run was given.
func edges298Checkout(t *testing.T, p *Plan, files map[string]string) (root, seen string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in docker is a shell script")
	}
	bin := t.TempDir()
	root, seen = t.TempDir(), filepath.Join(t.TempDir(), "seen.txt")
	edges298Write(t, bin, "docker", edges298Docker)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EDGES298_SEEN", seen)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	l := Lock{Ratified: Ratification{By: "gitdek", At: "2026-10-31T12:00:00Z", Issue: 7, Comment: "https://github.com/o/r/issues/7#issuecomment-1", Proposal: p.Hash()}, Plan: *p}
	if err := l.Write(root); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		edges298Write(t, root, name, text)
	}
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
	return root, seen
}

// edges298Last is what the stand-in's last run was given for a step,
// gofmt or test.
func edges298Last(t *testing.T, seen, step string) []string {
	t.Helper()
	f, err := os.Open(seen)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var last []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if rest, ok := strings.CutPrefix(scanner.Text(), step+" "); ok {
			last = strings.Fields(rest)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return last
}

// A plan may delete a package. The sandbox tests the packages the plan
// touches that the candidate still holds Go files in, so it never asks go
// test for one that's gone, while a page a package embeds still tests that
// package (#298).
func TestTheSandboxDoesNotTestAPackageThePlanDeletes(t *testing.T) {
	ctx := context.Background()
	p := &Plan{Name: "drop the old packages", Summary: "Remove two obsolete packages, restyle the site, and have the page say hello.",
		Files:   []string{"old/old.go", "old/old_test.go", "page/legacy/legacy.go", "page/page.go", "site/web/app.js"},
		Tests:   []Test{{Name: "TestPageSaysHello", File: "page/page_accept_test.go", Says: "The page says hello."}},
		Sources: map[string]string{"page/page_accept_test.go": loopyPageTest}}
	root, seen := edges298Checkout(t, p, map[string]string{
		"old/old.go":            "package old\n",
		"old/old_test.go":       "package old\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n",
		"page/legacy/legacy.go": "package legacy\n",
		"page/page.go":          "package page\n",
		"site/site.go":          "package site\n",
		"site/web/app.js":       "export const color = 'blue';\n",
	})
	ws := t.TempDir()
	if err := CopyCheckout(ctx, root, ws); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old/old.go", "old/old_test.go", "page/legacy/legacy.go"} {
		if err := os.Remove(filepath.Join(ws, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	edges298Write(t, ws, "page/page.go", "package page\n\n// Hello says hello.\nfunc Hello() string { return \"hello\" }\n")
	edges298Write(t, ws, "site/web/app.js", "export const color = 'green';\n")
	staged := t.TempDir()
	if err := Stage(ctx, root, ws, p, staged); err != nil {
		t.Fatal(err)
	}
	r, err := Sandbox{Image: "stand-in", ModCache: t.TempDir()}.Run(ctx, staged, p)
	if err != nil {
		t.Fatal(err)
	}
	if got := edges298Last(t, seen, "test"); !slices.Equal(got, []string{"./page", "./site"}) || !r.Tests || !r.Passed() {
		t.Errorf("the sandbox tested %q, and its tests passed %v and its run %v; want ./page and ./site, both passing:\n%s", got, r.Tests, r.Passed(), r.Output)
	}
}

// A test the original checkout holds in a package the plan touches is one
// the build may not edit, so however it's formatted, it never blocks the
// build. The regression tests the build adds there are still formatted
// (#298).
func TestAnExistingTestNeverBlocksABuildsFormatting(t *testing.T) {
	const existing = "package page\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T){}\n"
	const formatted = "package page\n\nimport \"testing\"\n\nfunc TestReview(t *testing.T) {}\n"
	const unformatted = "package page\n\nimport \"testing\"\n\nfunc TestReview(t *testing.T){}\n"
	// The stand-in's gofmt flags just what the real one would.
	for text, clean := range map[string]bool{existing: false, formatted: true, unformatted: false, loopyPageTest: true} {
		b, err := format.Source([]byte(text))
		if err != nil || (string(b) == text) != clean || strings.Contains(text, "testing.T){") == clean {
			t.Fatalf("the stand-in's gofmt and the real one disagree on %q: %v", text, err)
		}
	}
	for _, tc := range []struct {
		name   string
		added  string // the regression test the build adds
		passed bool
		gofmt  []string
	}{
		{name: "a formatted regression test", added: formatted, passed: true},
		{name: "an unformatted regression test", added: unformatted, gofmt: []string{"page/review_test.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plan{Name: "a hello page", Summary: "Add a page that says hello.", Files: []string{"page/page.go"},
				Tests:   []Test{{Name: "TestPageSaysHello", File: "page/page_accept_test.go", Says: "The page says hello."}},
				Sources: map[string]string{"page/page_accept_test.go": loopyPageTest}}
			root, seen := edges298Checkout(t, p, map[string]string{"page/page.go": "package page\n", "page/existing_test.go": existing})
			agent := &loopyAgent{effort: "max", writes: map[string]map[string]string{"max": {
				"page/page.go":        "package page\n\n// Hello says hello.\nfunc Hello() string { return \"hello\" }\n",
				"page/review_test.go": tc.added,
			}}}
			b := Builder{Backend: agent, Reviewer: &loopyReviewer{}, Binary: "invariant", TestRuns: 6, Timeout: time.Minute,
				Sandbox: Sandbox{Image: "stand-in", ModCache: t.TempDir()}}
			r, err := b.Build(context.Background(), root, 7, filepath.Join(t.TempDir(), "build"))
			if err != nil {
				t.Fatal(err)
			}
			if r.Passed != tc.passed || !slices.Equal(r.Final.Gofmt, tc.gofmt) {
				t.Errorf("passed %v, with gofmt flagging %q; want %v, flagging %q:\n%s", r.Passed, r.Final.Gofmt, tc.passed, tc.gofmt, r.Summary)
			}
			if checked := edges298Last(t, seen, "gofmt"); slices.Contains(checked, "page/existing_test.go") || !slices.Contains(checked, "page/review_test.go") {
				t.Errorf("gofmt checked %q; want the added regression test, and not the existing one", checked)
			}
		})
	}
}
