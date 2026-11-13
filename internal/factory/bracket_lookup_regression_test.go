package factory

import (
	"slices"
	"testing"
)

func TestBracketedDirectoryLookupAndAbsentExport(t *testing.T) {
	_, clone := gitRepos(t)
	writeBracket877(t, clone.Dir, "app/[slug]/literal/file.txt", "literal\n")
	writeBracket877(t, clone.Dir, "app/s/neighbor/file.txt", "neighbor\n")
	commitBracketFixture877(t, clone.Dir)

	for dir, want := range map[string][]string{
		"app/[slug]": {"literal"},
		"app/s":      {"neighbor"},
		"app/[s]":    nil,
		"app/open[":  nil,
		"app/close]": nil,
	} {
		got, err := clone.Dirs(t.Context(), "HEAD", dir)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Dirs(%q) = %v, %v; want %v", dir, got, err, want)
		}
	}
	for _, name := range []string{"app/[s]", "app/[s]/neighbor/file.txt"} {
		if err := clone.Export(t.Context(), "HEAD", []string{name}, t.TempDir()); err == nil {
			t.Errorf("export of absent literal path %q matched its neighbor", name)
		}
	}
	if _, err := clone.Dirs(t.Context(), "HEAD", "app/[slug]/literal/file.txt"); err == nil {
		t.Fatal("a bracketed file was accepted as a directory")
	}
}
