package plumbing

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func reviewStageWrite281(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReviewRegressionFilesAreStagedAndFormattedWithinThePlan(t *testing.T) {
	ctx := context.Background()
	root := loopyCheckout(t)
	l, err := ReadLockFile(root, 7)
	if err != nil {
		t.Fatal(err)
	}
	plan := &l.Plan
	hash := plan.Hash()
	// Existing tests which the plan did not name remain outside its scope.
	const existing = "package page\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) {}\n"
	reviewStageWrite281(t, root, "page/existing_test.go", existing)
	ws := t.TempDir()
	if err := CopyCheckout(ctx, root, ws); err != nil {
		t.Fatal(err)
	}
	const regression = "page/spaces_review_test.go"
	const regressionText = "package page\n\nimport \"testing\"\n\nfunc TestSpaces(t *testing.T){t.Log(\"regression\")}\n"
	reviewStageWrite281(t, ws, "page/page.go", "package page\n\nfunc Hello() string { return \"hello\" }\n")
	reviewStageWrite281(t, ws, regression, regressionText)
	for _, name := range []string{"page/existing_test.go", "page/page_accept_test.go", "other/outside_test.go", "page/new.go", LockPath(7), "decisions/new_test.go", ".github/ci_test.go"} {
		reviewStageWrite281(t, ws, name, "not authorized\n")
	}
	diff, err := Diff(ctx, root, ws, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(diff.Written, regression) || slices.Contains(diff.Outside, regression) {
		t.Errorf("a new regression in the touched package was discarded: %+v", diff)
	}
	for _, name := range []string{"page/existing_test.go", "page/page_accept_test.go", "other/outside_test.go", "page/new.go", LockPath(7)} {
		if slices.Contains(diff.Written, name) {
			t.Errorf("the exception admitted %s", name)
		}
	}
	stage := t.TempDir()
	if err := Stage(ctx, root, ws, plan, stage); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{regression: regressionText, "page/existing_test.go": existing, "page/page_accept_test.go": loopyPageTest} {
		got, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Errorf("staged %s = %q, %v; want %q", name, got, err, want)
		}
	}
	stagedLock, err := ReadLockFile(stage, 7)
	if err != nil || stagedLock.Plan.Hash() != hash || plan.Hash() != hash {
		t.Fatalf("staging changed the ratified plan: %v", err)
	}
	for _, name := range []string{"other/outside_test.go", "page/new.go", "decisions/new_test.go", ".github/ci_test.go"} {
		if _, err := os.Lstat(filepath.Join(stage, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Errorf("staging admitted %s: %v", name, err)
		}
	}
	// A formatting failure in the added test must be visible to the same
	// sandbox which formats the plan's named files and tests its packages.
	bin := t.TempDir()
	const docker = `#!/bin/sh
[ "$1" = run ] || exit 0
fmt=
pkgs=
for a in "$@"; do
  case "$a" in
    GOFMT_FILES=*) fmt=${a#GOFMT_FILES=} ;;
    TEST_PKGS=*) pkgs=${a#TEST_PKGS=} ;;
  esac
done
case " $fmt " in *' page/spaces_review_test.go '*) echo '@@gofmt page/spaces_review_test.go' ;; esac
echo '@@invariant vet=0'
case " $pkgs " in *' ./page '*) echo '@@invariant test=0' ;; *) echo '@@invariant test=1' ;; esac
echo '--- PASS: TestPageSaysHello (0.00s)'
echo '@@invariant accept=0'
`
	reviewStageWrite281(t, bin, "docker", docker)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := (Sandbox{Image: "stand-in", ModCache: t.TempDir()}).Run(ctx, stage, plan)
	if err != nil || result.Passed() || !result.Tests || !slices.Contains(result.Gofmt, regression) {
		t.Errorf("the added regression escaped formatting or package tests: %+v, %v", result, err)
	}

	// Added regression tests pass through the same safe reads as named
	// production files; their new allowance cannot copy a host link.
	const secret = "stand-in host secret"
	for _, link := range []struct {
		name string
		make func(string, string) error
	}{{"symbolic", os.Symlink}, {"hard", os.Link}} {
		func() {
			linked := t.TempDir()
			if err := CopyCheckout(ctx, root, linked); err != nil {
				t.Fatal(err)
			}
			host := filepath.Join(t.TempDir(), "private.txt")
			if err := os.WriteFile(host, []byte(secret), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := link.make(host, filepath.Join(linked, filepath.FromSlash(regression))); err != nil {
				t.Fatal(err)
			}
			dst := t.TempDir()
			err := Stage(ctx, root, linked, plan, dst)
			got, _ := os.ReadFile(filepath.Join(dst, filepath.FromSlash(regression)))
			if strings.Contains(string(got), secret) {
				t.Fatalf("staging followed an added %s regression link", link.name)
			}
			// Rejecting the candidate or omitting a non-regular addition are
			// both safe; silently copying it is not.
			if err == nil && len(got) != 0 {
				t.Error("staging admitted an unsafe regression file")
			}
		}()
	}
}
