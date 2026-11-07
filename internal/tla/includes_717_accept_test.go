package tla_test

import (
	"github.com/gitdek/invariant/internal/tla"
	"testing"
)

func TestComparison717SharedIncludeNames(t *testing.T) {
	for _, name := range []string{"Helper.tla", "_Helper.tla", "_.tla", "A_09.tla", "main.tla"} {
		if !tla.ValidIncludeName(name, "Main") {
			t.Errorf("valid ASCII sibling %q refused", name)
		}
	}
	for _, name := range []string{"Main.tla", "", ".tla", "9Helper.tla", "Helper", "Helper.TLA", "Helper.tla.tla", "Helper.tla.bak", "Helper.tla/", "./Helper.tla", "../Helper.tla", "/Helper.tla", `C:\Helper.tla`, `dir\Helper.tla`, "dir/Helper.tla", "é.tla", "Σ.tla", "a-b.tla", "a b.tla", "Helper.tla\n", "Helper.tla\x00"} {
		if tla.ValidIncludeName(name, "Main") {
			t.Errorf("invalid or colliding sibling %q accepted", name)
		}
	}
	if tla.ValidIncludeName("_Helper.tla", "_Helper") {
		t.Fatal("underscore main-module collision accepted")
	}
}
