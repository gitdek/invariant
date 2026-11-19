package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
)

// The issue's two layouts (#814): the same tokens, so the same pin, and two
// meanings. As a list, Inv is (FALSE => FALSE) /\ FALSE, which TLC finds
// violated; joined on one line, it's FALSE => (FALSE /\ FALSE), which holds.
// TestLayoutPinsTellTheIssuesLayoutsApartIntegration runs both on the
// pinned TLC.
const (
	layoutListed814 = "---- MODULE Layout ----\nCONSTANT N\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n" +
		"Inv ==\n    /\\ FALSE => FALSE\n    /\\ FALSE\n====\n"
	layoutJoined814 = "---- MODULE Layout ----\nCONSTANT N\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n" +
		"Inv == /\\ FALSE => FALSE /\\ FALSE\n====\n"
)

// layoutProject814 writes a project whose model is src, ratified with its
// lock pinned as invariant pin pins it: each statement's pin and layout pin.
// With unpinned, the lock pins no layout, as one ratified before layout pins
// does.
func layoutProject814(t *testing.T, src string, unpinned bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".invariant", "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := project.Manifest{Name: "layout", Module: ".invariant/specs/Layout.tla", Code: "layout", Language: "go"}
	lock := project.Lock{Decision: "a test", Bounds: map[string]string{"N": "1"}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "x stays 0."},
		{Name: "Inv", Kind: project.Invariant, Says: "Nothing holds: Inv is false."},
	}}
	for file, v := range map[string]any{"invariant.json": manifest, "ratified.lock": lock} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".invariant", file), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".invariant", "specs", "Layout.tla"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	if unpinned {
		p = loadProject814(t, dir)
		for i := range p.Lock.Statements {
			p.Lock.Statements[i].Layout = ""
		}
		b, err := json.MarshalIndent(p.Lock, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".invariant", "ratified.lock"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func loadProject814(t *testing.T, dir string) *project.Project {
	t.Helper()
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// rewrite814 replaces a project's model, as an agent or a person editing it
// would.
func rewrite814(t *testing.T, dir, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".invariant", "specs", "Layout.tla"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The gate fails the joined layout against a lock ratified with the listed
// one, with a failure of its own that names the definition and the first
// line that moved, and passes the pins of a lock that pins no layout yet.
// This is the part that needs no TLC: the pins, and how a report says what
// failed.
func TestTheGateFailsAMovedLayoutOnItsOwn814(t *testing.T) {
	ratified := layoutProject814(t, layoutListed814, false)
	lock := loadProject814(t, ratified).Lock
	if lock.LayoutState() != project.LayoutRatified {
		t.Fatalf("invariant pin left the lock %s", lock.LayoutState())
	}

	pins := checkPins(layoutJoined814, nil, lock.Statements)
	inv := pins[1]
	if inv.Name != "Inv" || !inv.Match || inv.Layout == nil || inv.Layout.Match || inv.Layout.Want == "" || inv.Layout.Got == inv.Layout.Want {
		t.Fatalf("the joined layout's pin is %+v, layout %+v; want its pin to match and its layout pin not to", inv, inv.Layout)
	}
	if !inv.LayoutMoved() || inv.LayoutHolds() || !pins[0].LayoutHolds() || pins[0].LayoutMoved() {
		t.Fatalf("LayoutMoved %v, LayoutHolds %v; Spec's %v", inv.LayoutMoved(), inv.LayoutHolds(), pins[0].LayoutHolds())
	}

	// Without a copy of the model as ratified, the gate says it moved, and
	// can't say where.
	nameMoves(context.Background(), pins, layoutJoined814, nil, t.TempDir(), "", lock)
	if pins[1].Layout.Moved != nil {
		t.Fatalf("a move was named with nothing to name it from: %+v", pins[1].Layout.Moved)
	}
	r := &Report{ModelOnly: true, Pins: pins, Design: Design{Passed: true, Outcome: "passed"}}
	if modelPassed(r) {
		t.Fatal("a report whose layout moved passed")
	}
	if got := strings.Join(Failed(r), ", "); got != "layout Inv" {
		t.Errorf("Failed = %q, want \"layout Inv\"", got)
	}
	if fb := Feedback(r); !strings.Contains(fb, "## Layout of pinned statement Inv") || !strings.Contains(fb, "couldn't read the model as it was ratified") ||
		strings.Contains(fb, "## Pinned statement Inv") {
		t.Errorf("Feedback doesn't report the layout on its own:\n%s", fb)
	}

	// With the ratified project to read, as a synthesis gate has, it names
	// the definition and the line.
	nameMoves(context.Background(), pins, layoutJoined814, nil, t.TempDir(), ratified, lock)
	mv := pins[1].Layout.Moved
	if mv == nil || mv.Definition != "Inv" || mv.Line != 7 || mv.Now != `Inv == /\ FALSE => FALSE /\ FALSE` || mv.Ratified != "Inv ==" {
		t.Fatalf("Moved = %+v", mv)
	}
	if fb := Feedback(r); !strings.Contains(fb, "the first line that moved is in the definition of Inv, at line 7: `Inv == /\\ FALSE => FALSE /\\ FALSE`, where the ratified layout has `Inv ==`") {
		t.Errorf("Feedback doesn't name the line:\n%s", fb)
	}

	// A copy that isn't laid out as the lock pins names nothing.
	stale := layoutProject814(t, layoutJoined814, false)
	pins = checkPins(layoutJoined814, nil, lock.Statements)
	nameMoves(context.Background(), pins, layoutJoined814, nil, t.TempDir(), stale, lock)
	if pins[1].Layout.Moved != nil {
		t.Errorf("a copy laid out otherwise named a move: %+v", pins[1].Layout.Moved)
	}

	// A text that changed is a pin's failure, and the layout's isn't
	// reported beside it.
	changed := strings.Replace(layoutJoined814, "/\\ FALSE\n====", "/\\ TRUE\n====", 1)
	pins = checkPins(changed, nil, lock.Statements)
	if pins[1].Match || pins[1].LayoutMoved() {
		t.Fatalf("a changed text: %+v", pins[1])
	}
	if got := strings.Join(Failed(&Report{ModelOnly: true, Pins: pins, Design: Design{Passed: true}}), ", "); got != "pin Inv" {
		t.Errorf("Failed = %q, want \"pin Inv\"", got)
	}

	// The listed layout is the ratified one.
	pins = checkPins(layoutListed814, nil, lock.Statements)
	if !pins[1].Match || !pins[1].Layout.Match || !pins[1].LayoutHolds() {
		t.Errorf("the ratified layout doesn't match its own lock: %+v %+v", pins[1], pins[1].Layout)
	}
}

// A lock ratified before layout pins, and not migrated, keeps verifying:
// its pins are checked as before, and its layout isn't pinned yet.
func TestALockWithoutLayoutPinsKeepsVerifying814(t *testing.T) {
	legacy := loadProject814(t, layoutProject814(t, layoutListed814, true)).Lock
	if legacy.LayoutState() != project.LayoutUnpinned {
		t.Fatalf("the lock is %s", legacy.LayoutState())
	}
	pins := checkPins(layoutJoined814, nil, legacy.Statements)
	for _, pin := range pins {
		if !pin.Match || pin.Layout == nil || pin.Layout.Want != "" || pin.Layout.Match || !pin.LayoutHolds() || pin.LayoutMoved() {
			t.Errorf("%s: %+v, layout %+v", pin.Name, pin, pin.Layout)
		}
	}
	r := &Report{ModelOnly: true, Pins: pins, Design: Design{Passed: true, Outcome: "passed"}}
	if !modelPassed(r) || len(Failed(r)) != 0 {
		t.Errorf("a lock that pins no layout failed: %v", Failed(r))
	}
}

// A receipt from before layout pins has none, and keeps its fingerprint
// when it's read back: the field is left out of its evidence.
func TestAReceiptFromBeforeLayoutPinsKeepsItsFingerprint814(t *testing.T) {
	old := Report{Project: "p", Passed: true, Bounds: map[string]string{"N": "1"},
		Pins: []Pin{{Name: "Inv", Kind: "invariant", Says: "s", Want: "sha256:aa", Got: "sha256:aa", Match: true}}}
	b, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"layout"`) {
		t.Fatalf("a pin with no layout writes one: %s", b)
	}
	var read Report
	if err := json.Unmarshal(b, &read); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(read) != Fingerprint(old) || read.Pins[0].Layout != nil || !read.Pins[0].LayoutHolds() {
		t.Error("a receipt from before layout pins reads back differently")
	}
	withLayout := old
	withLayout.Pins = []Pin{old.Pins[0]}
	withLayout.Pins[0].Layout = &LayoutPin{Want: "sha256:bb", Got: "sha256:cc"}
	if Fingerprint(withLayout) == Fingerprint(old) {
		t.Error("a layout pin is left out of the fingerprint")
	}
	// Where a layout moved is named only where the gate can read the
	// history, so a local run and CI's shallow one keep one fingerprint.
	named := withLayout
	named.Pins = []Pin{withLayout.Pins[0]}
	moved := *withLayout.Pins[0].Layout
	moved.Moved = &tla.Move{Definition: "Inv", Line: 7, Now: "Inv == TRUE", Ratified: "Inv =="}
	named.Pins[0].Layout = &moved
	if Fingerprint(named) != Fingerprint(withLayout) {
		t.Error("where a layout moved changes the fingerprint")
	}
	if named.Pins[0].Layout.Moved == nil {
		t.Error("Fingerprint changed the report it was given")
	}
}
