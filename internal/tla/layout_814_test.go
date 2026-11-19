package tla

import (
	"strings"
	"testing"
)

// The issue's two layouts (#814). TLA+ reads a bulleted list of /\ by the
// column each item starts in: as a list, Inv is (FALSE => FALSE) /\ FALSE,
// which is false, and joined on one line it's FALSE => (FALSE /\ FALSE),
// which is true. TestLayoutPinsTellTheIssuesLayoutsApartIntegration holds
// that against the pinned TLC.
const (
	listed814 = "---- MODULE Layout ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n" +
		"Inv ==\n    /\\ FALSE => FALSE\n    /\\ FALSE\n====\n"
	joined814 = "---- MODULE Layout ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n" +
		"Inv == /\\ FALSE => FALSE /\\ FALSE\n====\n"
)

func pins814(t *testing.T, src string, name string) (pin, layout string) {
	t.Helper()
	m, err := Read(src, nil)
	if err != nil {
		t.Fatalf("the reader refuses the module: %v\n%s", err, src)
	}
	if pin, err = m.PinHash(name, model); err != nil {
		t.Fatal(err)
	}
	if layout, err = m.LayoutHash(name, model); err != nil {
		t.Fatal(err)
	}
	return pin, layout
}

func TestTheIssuesTwoLayoutsShareAPinAndNotALayoutPin814(t *testing.T) {
	pin1, layout1 := pins814(t, listed814, "Inv")
	pin2, layout2 := pins814(t, joined814, "Inv")
	if pin1 != pin2 {
		t.Fatalf("the two layouts have different pins, %s and %s; the issue is that they share one", pin1, pin2)
	}
	if layout1 == layout2 {
		t.Fatalf("the two layouts share the layout pin %s", layout1)
	}
	def1, _ := Definition(listed814, "Inv")
	def2, _ := Definition(joined814, "Inv")
	if Canonical(def1) != `Inv == /\ FALSE => FALSE /\ FALSE` || Canonical(def1) != Canonical(def2) {
		t.Errorf("canonical texts %q and %q; want both `Inv == /\\ FALSE => FALSE /\\ FALSE`", Canonical(def1), Canonical(def2))
	}
}

// A layout pin covers what a pin covers: the statement and every definition
// it depends on, up to the factory's model.
func TestALayoutPinCoversWhatAStatementDependsOn814(t *testing.T) {
	src := "---- MODULE Deps ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n" +
		"Part ==\n    \\/ x = 0\n    \\/ x = 1\nInv == Part /\\ x \\in 0..1\n====\n"
	_, before := pins814(t, src, "Inv")
	moved := strings.Replace(src, "    \\/ x = 1", "      \\/ x = 1", 1)
	pinAfter, after := pins814(t, moved, "Inv")
	if pin, _ := pins814(t, src, "Inv"); pin != pinAfter {
		t.Fatal("re-indenting a line of Part changed Inv's pin, which hashes tokens only")
	}
	if before == after {
		t.Error("re-indenting a line of Part, which Inv depends on, left Inv's layout pin alone")
	}
	// The factory's model isn't pinned, so moving a token of it moves nothing.
	init := strings.Replace(src, "Init == x = 0", "Init ==\n    x = 0", 1)
	if _, l := pins814(t, init, "Inv"); l != before {
		t.Error("laying out Init, the factory's, changed Inv's layout pin")
	}
}

// Comments are blanked in place, trailing space trimmed and blank lines
// dropped, so a comment can be edited or added without moving a token, and
// none of that changes a layout pin. A comment written before a token on its
// line moves the token, so that does.
func TestCommentsAndSpaceLeaveALayoutPinAlone814(t *testing.T) {
	base := "---- MODULE Notes ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n" +
		"\\* x stays small.\nInv ==\n    /\\ x >= 0 \\* never negative\n    (* and never\n       large *)\n    /\\ x <= 3\n====\n"
	pin, layout := pins814(t, base, "Inv")
	same := map[string]func(string) string{
		"editing a line comment": func(s string) string {
			return strings.Replace(s, "\\* never negative", "\\* it never goes below zero, whatever happens", 1)
		},
		"adding a line comment on a line of its own": func(s string) string {
			return strings.Replace(s, "    /\\ x <= 3", "    \\* the upper bound:\n    /\\ x <= 3", 1)
		},
		"adding a comment after the code on a line": func(s string) string {
			return strings.Replace(s, "    /\\ x <= 3", "    /\\ x <= 3 (* the bound *)", 1)
		},
		"editing a block comment's lines": func(s string) string {
			return strings.Replace(s, "(* and never\n       large *)", "(* and never, not once,\n   larger than three *)", 1)
		},
		"adding a block comment of several lines": func(s string) string {
			return strings.Replace(s, "Inv ==\n", "Inv ==\n    (* What holds:\n       always. *)\n", 1)
		},
		"adding blank lines": func(s string) string {
			return strings.Replace(s, "    /\\ x <= 3", "\n\n    /\\ x <= 3", 1)
		},
		"space at a line's end": func(s string) string {
			return strings.Replace(s, "    /\\ x <= 3", "    /\\ x <= 3   \t", 1)
		},
		"a comment above the definition": func(s string) string {
			return strings.Replace(s, "\\* x stays small.", "\\* x stays small, as the issue asks.", 1)
		},
	}
	for name, edit := range same {
		edited := edit(base)
		if edited == base {
			t.Fatalf("%s: the edit changed nothing", name)
		}
		p, l := pins814(t, edited, "Inv")
		if p != pin || l != layout {
			t.Errorf("%s changed a pin: pin %v, layout pin %v", name, p != pin, l != layout)
		}
	}
	moved := map[string]func(string) string{
		"indenting a line":         func(s string) string { return strings.Replace(s, "    /\\ x <= 3", "     /\\ x <= 3", 1) },
		"joining two lines":        func(s string) string { return strings.Replace(s, "Inv ==\n    /\\ x >= 0", "Inv == /\\ x >= 0", 1) },
		"splitting a line":         func(s string) string { return strings.Replace(s, "    /\\ x <= 3", "    /\\ x <=\n         3", 1) },
		"a comment before a token": func(s string) string { return strings.Replace(s, "    /\\ x <= 3", "    (* c *) /\\ x <= 3", 1) },
		"a wider space between tokens": func(s string) string {
			return strings.Replace(s, "    /\\ x <= 3", "    /\\ x  <= 3", 1)
		},
	}
	for name, edit := range moved {
		p, l := pins814(t, edit(base), "Inv")
		if p != pin {
			t.Fatalf("%s changed the pin; the case is about layout", name)
		}
		if l == layout {
			t.Errorf("%s moved a token and left the layout pin alone", name)
		}
	}
}

// A definition written on one line, a space between tokens, is laid out as
// its canonical text, so its layout pin is its pin.
func TestALayoutOnOneLineIsTheCanonicalText814(t *testing.T) {
	def := "Safe == x >= 0 /\\ x <= 3 \\* in range"
	if Layout(def) != Canonical(def) {
		t.Errorf("Layout = %q, Canonical = %q", Layout(def), Canonical(def))
	}
	src := "---- MODULE One ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\nSafe == x >= 0 /\\ x <= 3\n====\n"
	if pin, layout := pins814(t, src, "Safe"); pin != layout {
		t.Errorf("Safe's pin %s and layout pin %s differ", pin, layout)
	}
	if got, want := Layout("Inv ==\n    /\\ a \\* note\n\n    (* gone *)\n    /\\ b   "), "Inv ==\n    /\\ a\n    /\\ b"; got != want {
		t.Errorf("Layout = %q, want %q", got, want)
	}
	if got, want := Layout("Inv == a (* c *) /\\ b"), "Inv == a         /\\ b"; got != want {
		t.Errorf("a comment is blanked in place: Layout = %q, want %q", got, want)
	}
	if got, want := Layout(`Says == "a (* b *) c"`), `Says == "a (* b *) c"`; got != want {
		t.Errorf("a string's text isn't a comment: Layout = %q, want %q", got, want)
	}
}

// The gate names the definition and the first line that moved, from the
// model as it was ratified, in the module or a module beside it.
func TestFirstMoveNamesTheDefinitionAndLine814(t *testing.T) {
	ratified, err := Read(listed814, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := Read(joined814, nil)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := joined.FirstMove(ratified, "Inv", model)
	if err != nil || mv == nil {
		t.Fatalf("FirstMove = %v, %v", mv, err)
	}
	if mv.Definition != "Inv" || mv.File != "" || mv.Line != 6 || mv.Now != `Inv == /\ FALSE => FALSE /\ FALSE` || mv.Ratified != "Inv ==" {
		t.Errorf("FirstMove = %+v", *mv)
	}
	want := "the definition of Inv, at line 6: `Inv == /\\ FALSE => FALSE /\\ FALSE`, where the ratified layout has `Inv ==`"
	if mv.String() != want {
		t.Errorf("String() = %q\nwant       %q", mv.String(), want)
	}
	if mv, err := ratified.FirstMove(ratified, "Inv", model); err != nil || mv != nil {
		t.Errorf("a model laid out as ratified moved: %v, %v", mv, err)
	}

	// A line that moved inside the list, and a dependency in a module beside
	// the model.
	main := "---- MODULE Main ----\nEXTENDS Helper\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\nInv == Small(x)\n====\n"
	helper := "---- MODULE Helper ----\nEXTENDS Naturals\nSmall(v) ==\n    /\\ v >= 0\n    /\\ v <= 3\n====\n"
	before, err := Read(main, map[string]string{"Helper.tla": helper})
	if err != nil {
		t.Fatal(err)
	}
	after, err := Read(main, map[string]string{"Helper.tla": strings.Replace(helper, "    /\\ v <= 3", "      /\\ v <= 3", 1)})
	if err != nil {
		t.Fatal(err)
	}
	mv, err = after.FirstMove(before, "Inv", model)
	if err != nil || mv == nil {
		t.Fatalf("FirstMove = %v, %v", mv, err)
	}
	if mv.Definition != "Small" || mv.File != "Helper.tla" || mv.Line != 5 || mv.Now != `      /\ v <= 3` || mv.Ratified != `    /\ v <= 3` {
		t.Errorf("FirstMove = %+v", *mv)
	}

	// A line the ratified layout doesn't have, and one it goes on with.
	split := strings.Replace(listed814, "    /\\ FALSE\n====", "    /\\ FALSE\n    /\\ FALSE\n====", 1)
	longer, err := Read(split, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mv, _ := longer.FirstMove(ratified, "Inv", model); mv == nil || mv.Line != 9 || mv.Ratified != "" || !strings.Contains(mv.String(), "a line the ratified layout doesn't have") {
		t.Errorf("an added line: %+v", mv)
	}
	if mv, _ := ratified.FirstMove(longer, "Inv", model); mv == nil || mv.Line != 8 || mv.Now != "" || !strings.Contains(mv.String(), "which ends at line 8") {
		t.Errorf("a dropped line: %+v", mv)
	}
}
