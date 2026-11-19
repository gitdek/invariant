package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/verify"
)

// A receipt shows each statement's layout pin beside its pin, says when a
// lock pins no layout yet, and reports a layout that moved as a failure of
// its own, with the line that moved as written (#814).
func TestAReceiptShowsLayoutPins814(t *testing.T) {
	pin := "sha256:" + strings.Repeat("a", 64)
	layout := "sha256:" + strings.Repeat("b", 64)
	moved := "sha256:" + strings.Repeat("c", 64)
	report := func(l *verify.LayoutPin) *verify.Report {
		return &verify.Report{Project: "layout", ModelOnly: true, Design: verify.Design{Passed: true, Outcome: "passed"},
			Pins: []verify.Pin{{Name: "Inv", Kind: "invariant", Says: "Nothing holds.", Want: pin, Got: pin, Match: true, Layout: l}}}
	}

	md := Markdown(report(&verify.LayoutPin{Want: layout, Got: layout, Match: true}))
	for _, want := range []string{"| Statement | Kind | Says | Pin | Layout |", "| `Inv` | invariant | Nothing holds. | ✅ `sha256:aaaaaaaaaaaa` | ✅ `sha256:bbbbbbbbbbbb` |", "1 of 1 match; 1 of 1 layouts match"} {
		if !strings.Contains(md, want) {
			t.Errorf("a ratified layout pin: the receipt doesn't say %q:\n%s", want, md)
		}
	}

	md = Markdown(report(&verify.LayoutPin{Want: layout, Migrated: true, Got: layout, Match: true}))
	if !strings.Contains(md, "✅ `sha256:bbbbbbbbbbbb` migrated |") {
		t.Errorf("a migrated layout pin isn't shown as one:\n%s", md)
	}

	md = Markdown(report(&verify.LayoutPin{Got: layout}))
	for _, want := range []string{"➖ not pinned yet |", "1 of 1 match; layout not pinned yet"} {
		if !strings.Contains(md, want) {
			t.Errorf("a lock that pins no layout: the receipt doesn't say %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "What failed") {
		t.Errorf("a lock that pins no layout failed:\n%s", md)
	}

	r := report(&verify.LayoutPin{Want: layout, Got: moved, Moved: &tla.Move{Definition: "Inv", Line: 7, Now: `Inv == /\ FALSE => FALSE /\ FALSE`, Ratified: "Inv =="}})
	r.Passed = false
	md = Markdown(r)
	for _, want := range []string{
		"| Pinned statements | ❌",
		"1 of 1 match; 0 of 1 layouts match",
		"❌ `sha256:bbbbbbbbbbbb` |",
		"- Layout of pinned statement `Inv` moved: its text is the ratified text, and the first line that moved is in the definition of `Inv`, at line 7: `Inv == /\\ FALSE => FALSE /\\ FALSE`, where the ratified layout has `Inv ==`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("a moved layout: the receipt doesn't say %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Pinned statement `Inv` changed") {
		t.Errorf("a moved layout is reported as a changed text:\n%s", md)
	}

	r = report(&verify.LayoutPin{Want: layout, Got: moved})
	r.Passed = false
	if md = Markdown(r); !strings.Contains(md, "couldn't read the model as it was ratified to say which line moved") {
		t.Errorf("a moved layout with no ratified model to read:\n%s", md)
	}
}
