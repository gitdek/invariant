package factory

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const route877 = "app/[slug]/page.tsx"
const neighbor877 = "app/s/page.tsx"

func writeBracket877(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitBracketFixture877(t *testing.T, root string) string {
	t.Helper()
	git(t, root, "add", "--all")
	git(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false", "commit", "-qm", "fixture")
	return git(t, root, "rev-parse", "HEAD")
}

func TestFactoryStagesOnlyTheLiteralBracketPath877(t *testing.T) {
	for _, selection := range []string{"file", "directory", "deletion", "forced ignored file"} {
		t.Run(selection, func(t *testing.T) {
			_, c := gitRepos(t)
			root := c.Dir
			writeBracket877(t, root, route877, "route base\n")
			writeBracket877(t, root, neighbor877, "neighbor base\n")
			base := commitBracketFixture877(t, root)
			if selection == "forced ignored file" {
				// Neither file exists in the index; both are ignored. Only the
				// bracketed one appears in the build's written-file list.
				writeBracket877(t, root, ".gitignore", "ignored/\n")
				base = commitBracketFixture877(t, root)
				literal, neighbor := "ignored/"+route877, "ignored/"+neighbor877
				writeBracket877(t, root, literal, "new route\n")
				writeBracket877(t, root, neighbor, "new neighbor\n")
				if _, err := c.Commit(t.Context(), root, ".", "Write the route", literal); err != nil {
					t.Fatal(err)
				}
				got := strings.Fields(git(t, root, "diff", "--name-only", base, "HEAD"))
				if !slices.Equal(got, []string{literal}) {
					t.Fatalf("force-add selected %v; want only %s", got, literal)
				}
				return
			}
			writeBracket877(t, root, route877, "route changed\n")
			writeBracket877(t, root, neighbor877, "neighbor changed\n")
			target := route877
			if selection == "directory" {
				target = "app/[slug]"
			}
			if selection == "deletion" {
				if err := os.Remove(filepath.Join(root, route877)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.Commit(t.Context(), root, target, "Change the route"); err != nil {
				t.Fatal(err)
			}
			got := strings.Fields(git(t, root, "diff", "--name-only", base, "HEAD"))
			if !slices.Equal(got, []string{route877}) {
				t.Errorf("commit selected %v; want only %s", got, route877)
			}
			if got := git(t, root, "show", "HEAD:"+neighbor877); got != "neighbor base" {
				t.Errorf("committed the neighbor: %q", got)
			}
		})
	}
}

func TestFactoryExportsAndFindsHistoryForOneBracketPath877(t *testing.T) {
	_, c := gitRepos(t)
	root := c.Dir
	writeBracket877(t, root, route877, "route base\n")
	writeBracket877(t, root, neighbor877, "neighbor base\n")
	base := commitBracketFixture877(t, root)
	writeBracket877(t, root, neighbor877, "neighbor changed first\n")
	commitBracketFixture877(t, root)
	writeBracket877(t, root, route877, "route changed second\n")
	wantCommit := commitBracketFixture877(t, root)
	first, err := c.FirstCommit(t.Context(), base, "HEAD", route877)
	if err != nil || first != wantCommit {
		t.Errorf("first route commit = %q, %v; want %s", first, err, wantCommit)
	}
	for _, target := range []string{route877, "app/[slug]"} {
		dst := t.TempDir()
		if err := c.Export(t.Context(), "HEAD", []string{target}, dst); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dst, route877))
		if err != nil || string(got) != "route changed second\n" {
			t.Errorf("export omitted or changed the literal route: %q, %v", got, err)
		}
		if _, err := os.Lstat(filepath.Join(dst, neighbor877)); !os.IsNotExist(err) {
			t.Errorf("export included a pattern-matching neighbor: %v", err)
		}
	}
}
