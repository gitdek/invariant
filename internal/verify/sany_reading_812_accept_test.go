package verify_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// A pin means something only where the gate's reader and SANY, TLC's
// parser, read the same text (#812). Each probe here is a small module
// whose invariant Inv tells the two readings apart. Its one variable, x, is
// always 0, so x = 1 is false in every state. The reader either reads Inv as
// a definition that is false, and then the pinned TLC must find Inv violated,
// or refuses the module, and then the probe records how TLC reads it, which
// is why it's refused. An upgrade of TLC that reads any of them
// another way fails here before it can change what a pin covers.
//
// The reader's side runs always. The pinned TLC's side runs in CI's
// integration shards, through TestSANYReadsAsTheReaderDoesIntegration, and
// locally with:
//
//	INVARIANT_SANY_REAL=1 go test ./internal/verify -run '^TestSANYReadsAsTheReaderDoes812$' -count=1 -timeout 15m
func TestSANYReadsAsTheReaderDoes812(t *testing.T) {
	for _, p := range sany812Probes() {
		model, err := tla.Read(p.module, p.includes)
		switch {
		case p.refused != "":
			if err == nil || !strings.Contains(err.Error(), p.refused) {
				t.Errorf("%s: the reader's answer is %v; want a refusal saying %q", p.name, err, p.refused)
			}
		case err != nil:
			t.Errorf("%s: the reader refuses it: %v", p.name, err)
		default:
			def, err := model.Definition("Inv")
			if err != nil || tla.Canonical(def) != p.reads {
				t.Errorf("%s: the reader reads Inv as %q, %v; want %q", p.name, tla.Canonical(def), err, p.reads)
			}
		}
	}
	if os.Getenv("INVARIANT_SANY_REAL") == "1" {
		sanyReading812Real(t)
	}
}

type sany812Probe struct {
	name     string
	module   string
	includes map[string]string
	reads    string      // Inv's canonical text as the reader reads it, which is false
	refused  string      // what the reader's refusal says, when it refuses
	tlc      tlc.Outcome // what the pinned TLC finds
}

const (
	sany812Head = "---- MODULE Probe ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n"
	sany812Tail = "\n====\n"
)

func sany812Probes() []sany812Probe {
	in := func(body string) string { return sany812Head + body + sany812Tail }
	return []sany812Probe{
		// Read the same by both.
		{name: "a line-comment marker inside a block comment", module: in("(* a \\* b *)\nInv == x = 1\n(* c *)"), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "a block opener inside a line comment", module: in("\\* a (* b\nInv == x = 1\n\\* c *)"), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "comment markers inside strings", module: in("S == \"(*\"\nInv == x = 1\nT == \"*)\""), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "an escaped quote inside a string", module: in(`Inv == x = 1 \/ "a\"" = "b\""`), reads: `Inv == x = 1 \/ "a\"" = "b\""`, tlc: tlc.Violated},
		{name: "an escaped backslash at the end of a string", module: in("S == \"a\\\\\"\nInv == x = 1"), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "nested block comments", module: in("(* a (* b *)\nInv == x = 0\n*)\nInv == x = 1"), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "a quote inside a block comment", module: in("(* it is \"quoted *)\nInv == x = 1\n(* end\" *)"), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "a quote inside a line comment", module: in("\\* a \"quote\nInv == x = 1\n\\* b\""), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "a comment at the left margin inside a definition", module: in("Inv ==\n    /\\ x = 0\n\\* note\n    /\\ x = 1"), reads: `Inv == /\ x = 0 /\ x = 1`, tlc: tlc.Violated},
		{name: "a separator line", module: in("Helper == TRUE\n----\nInv == x = 1"), reads: "Inv == x = 1", tlc: tlc.Violated},
		{name: "a definition local to a LET", module: in("Inv == LET a == x = 1 IN a"), reads: "Inv == LET a == x = 1 IN a", tlc: tlc.Violated},
		{name: "a range that ends in a LET", module: strings.Replace(in("Inv == x \\in 2 .. LET n == 3 IN n"), "VARIABLE x", "EXTENDS Naturals\nVARIABLE x", 1), reads: `Inv == x \in 2 .. LET n == 3 IN n`, tlc: tlc.Violated},
		{name: "a function's definition", module: in("F[i \\in {1}] == x = 1\nInv == F[1]"), reads: "Inv == F[1]", tlc: tlc.Violated},
		{name: "text after the closing line", module: in("Inv == x = 1") + "Inv == x = 0\n(* never closed \"\n", reads: "Inv == x = 1", tlc: tlc.Violated},

		// Read by TLC as the reader's line-by-line predecessor didn't, so
		// refused. In each, TLC finds a definition of Inv that is false.
		{name: "text before the header", module: "Inv == x = 0\n" + in("Inv == x = 1"), refused: "TLC ignores whatever comes before the header", tlc: tlc.Violated},
		{name: "a header after other text on its line", module: "see " + in("Inv == x = 1"), refused: "TLC ignores whatever comes before the header", tlc: tlc.Violated},
		{name: "an indented definition", module: in("Helper == TRUE\n Inv == x = 1"), refused: "a second definition inside the definition of Helper", tlc: tlc.Violated},
		{name: "a definition after a comment on its line", module: in("Helper == TRUE\n(* c *) Inv == x = 1"), refused: "a second definition inside the definition of Helper", tlc: tlc.Violated},
		{name: "two definitions on one line", module: in("Helper == TRUE Inv == x = 1"), refused: "a second definition inside the definition of Helper", tlc: tlc.Violated},
		{name: "a head split over two lines", module: in("Inv\n  == x = 1"), refused: "isn't one the gate reads", tlc: tlc.Violated},
		{name: "a closing marker that isn't a line of its own", module: sany812Head + "Inv == x = 1 ====\nInv == x = 0\n====\n", refused: "==== closes the module", tlc: tlc.Violated},
		{name: "a reserved word as a record's field", module: in("Inv == [LET |-> x = 1].LET"), refused: "LET as a record's field", tlc: tlc.Violated},
		{name: "a lone carriage return ending a comment", module: in("\\* c\rInv == x = 1"), refused: "a control character", tlc: tlc.Violated},
		{name: "a file beside the model named like a module TLC ships", module: "---- MODULE Probe ----\nEXTENDS FiniteSets\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\nInv == x = 1 \\/ ~Shadowed\n====\n",
			includes: map[string]string{"FiniteSets.tla": "---- MODULE FiniteSets ----\nShadowed == TRUE\n====\n"}, refused: "would load this file in its place", tlc: tlc.Violated},

		// Refused by both.
		{name: "an escape TLC doesn't know", module: in(`Inv == "a \* b" = "c"`), refused: `a string holds \*`, tlc: tlc.Failed},
		{name: "a string over two lines", module: in("S == \"a\nInv == x = 1\nb\""), refused: "a string must close on the line it opens on", tlc: tlc.Failed},
		{name: "three equals signs", module: in("Inv === x = 1"), refused: "isn't an operator", tlc: tlc.Failed},
		{name: "a definition sign outside ASCII", module: in("Helper == TRUE\n Inv \u225c x = 1"), refused: "a character outside ASCII", tlc: tlc.Failed},
		{name: "a form feed", module: in("Helper == TRUE\n\fInv == x = 1"), refused: "a control character", tlc: tlc.Failed},
	}
}

func sanyReading812Real(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 14*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err) // Opted-in and integration runs must not silently skip.
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	check := func(module string, includes map[string]string) tlc.Result {
		t.Helper()
		dir := t.TempDir()
		name, err := tla.ModuleName(module[strings.Index(module, "----"):])
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{name + ".tla": module}
		for file, text := range includes {
			files[file] = text
		}
		for file, text := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		res, err := runner.Check(ctx, dir, name, tlc.Config{Specification: "Spec", Invariants: []string{"Inv"}})
		if err != nil {
			t.Fatalf("the pinned TLC didn't run: %v", err)
		}
		return res
	}
	for _, p := range sany812Probes() {
		res := check(p.module, p.includes)
		if res.Outcome != p.tlc || (p.tlc == tlc.Violated && res.Invariant != "Inv") {
			t.Errorf("%s: the pinned TLC's outcome is %s (invariant %q): %s\nwant %s", p.name, res.Outcome, res.Invariant, res.Message, p.tlc)
		}
	}

	// A model's EXTENDS isn't pinned, so the modules a model may extend must
	// give each operator one meaning. Naturals and Integers share theirs, and
	// TLC runs its own code for each: this holds that the two agree on every
	// operator but \div, which a pin may cover only under Integers, and that
	// \div differs exactly where the reader says it does.
	grids := map[string][][]string{}
	for _, numbers := range []string{"Naturals", "Integers"} {
		module := "---- MODULE Arith ----\nEXTENDS " + numbers + ", TLC\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\n" +
			"D == 0..6\nDivisor(b) == IF b < 3 THEN b - 3 ELSE b - 2\n" +
			"Grid == [a \\in D |-> [b \\in D |-> <<(a - 3) + (b - 3), (a - 3) - (b - 3), (a - 3) * (b - 3), (a - 3) < (b - 3), (a - 3) <= (b - 3), (a - 3) > (b - 3), (a - 3) >= (b - 3), (a - 3) .. (b - 3), (a - 3) % (b + 1), (a - 3) ^ (b + 1), (a - 3) \\in Nat, (a - 3) \\div Divisor(b)>>]]\n" +
			"Inv == PrintT(Grid)\n====\n"
		res := check(module, nil)
		if res.Outcome != tlc.Passed {
			t.Fatalf("arithmetic under %s: %s: %s", numbers, res.Outcome, res.Message)
		}
		for _, cell := range regexp.MustCompile(`<<[^>]*>>`).FindAllString(strings.Join(res.Printed, "\n"), -1) {
			grids[numbers] = append(grids[numbers], strings.Split(strings.Trim(cell, "<>"), ", "))
		}
		if len(grids[numbers]) < 49 {
			t.Fatalf("arithmetic under %s printed %d cells; want 49:\n%s", numbers, len(grids[numbers]), strings.Join(res.Printed, "\n"))
		}
		grids[numbers] = grids[numbers][:49]
	}
	differs := 0
	for i := range 49 {
		a, b := i/7-3, i%7
		divisor := b - 2
		if b < 3 {
			divisor = b - 3
		}
		n, z := grids["Naturals"][i], grids["Integers"][i]
		if len(n) != 12 || len(z) != 12 {
			t.Fatalf("cell %d has %d and %d values; want 12 each", i, len(n), len(z))
		}
		if strings.Join(n[:11], ", ") != strings.Join(z[:11], ", ") {
			t.Errorf("an operator other than \\div differs at a=%d, b=%d:\nNaturals %v\nIntegers %v", a, b-3, n[:11], z[:11])
		}
		// Integers rounds down. Naturals rounds down too, except that it
		// leaves a quotient of zero alone.
		floor := a / divisor
		if a%divisor != 0 && (a < 0) != (divisor < 0) {
			floor--
		}
		natural := floor
		if a/divisor == 0 {
			natural = 0
		}
		if z[11] != fmt.Sprint(floor) || n[11] != fmt.Sprint(natural) {
			t.Errorf("%d \\div %d is %s under Naturals and %s under Integers; want %d and %d", a, divisor, n[11], z[11], natural, floor)
		}
		if n[11] != z[11] {
			differs++
		}
	}
	if differs == 0 {
		t.Error("\\div no longer differs between Naturals and Integers: the rule that pins it only under Integers can go")
	}
}
