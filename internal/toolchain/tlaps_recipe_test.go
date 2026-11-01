package toolchain

import (
	"strings"
	"testing"
)

// The TLAPS sandbox installs exactly the pinned tarball on the pinned base, so
// the constants a receipt records are the ones the image was built from
// (#360).
func TestTLAPSRecipeUsesThePinnedTarball(t *testing.T) {
	recipe := string(TLAPSDockerfile)
	var from, add []string
	for _, line := range strings.Split(recipe, "\n") {
		switch f := strings.Fields(line); {
		case len(f) >= 3 && f[0] == "FROM":
			from = append(from, f[2])
		case len(f) >= 1 && f[0] == "ADD":
			add = f
		}
	}
	if len(from) != 2 || from[0] != JavaImage || from[1] != JavaImage {
		t.Errorf("the recipe builds from %v, want the Temurin base TLC runs in, JavaImage, for both stages", from)
	}
	if len(add) != 4 || add[1] != "--checksum=sha256:"+TLAPSTarballSHA256 || add[2] != TLAPSTarballURL {
		t.Errorf("the recipe adds %v, want the tarball %s pinned by sha256:%s", add, TLAPSTarballURL, TLAPSTarballSHA256)
	}
	if !strings.Contains(TLAPSTarballURL, "/tlapm-"+TLAPSRelease+"-"+TLAPSVersion+"/") || !strings.Contains(recipe, "FROM --platform=linux/amd64 ") {
		t.Errorf("the tarball %s isn't the mirror of release %s at %s, or the image isn't linux/amd64", TLAPSTarballURL, TLAPSRelease, TLAPSVersion)
	}
	if !strings.HasPrefix(TLAPSRecipe(), "sha256:") || len(TLAPSRecipe()) != len("sha256:")+64 {
		t.Errorf("TLAPSRecipe() = %q, want the recipe's SHA-256", TLAPSRecipe())
	}
}
