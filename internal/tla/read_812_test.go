package tla

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// honest812 holds every form the reader accepts that the audit's forgeries
// sit next to: comments that nest and hold quotes and the other comment's
// markers, a string that holds comment markers, a comment at the left margin
// inside a definition, declarations over several lines, a separator, an
// assumption, a recursive operator, a definition local to a LET, a
// parameter that is itself an operator, and a function's definition.
const honest812 = `---- MODULE Honest ----
EXTENDS Naturals, FiniteSets, TLC
CONSTANTS Limit,   \* how far the counter goes
          Workers
VARIABLES n, held

(* A block comment that holds a "quote, a \* marker,
   and (* a nested comment *) before it closes. *)
vars == <<n, held>>

RECURSIVE Sum(_)
Sum(s) == IF s = {} THEN 0 ELSE LET x == CHOOSE y \in s : TRUE IN x + Sum(s \ {x})

ASSUME Limit \in Nat

-----------------------------------------------------------------------------
\* A line comment with (* an opener and a "quote.
TypeOK ==
    /\ n \in 0..Limit
\* A comment at the left margin doesn't end the definition.
    /\ held \subseteq Workers

Label == "(* not a comment *)  -- nor this"
Range == 1 .. LET top == Limit IN top
Pair(f(_), x) == f(x)
Double[i \in 0..Limit] == i + i

Init == n = 0 /\ held = {}
Next == n < Limit /\ n' = n + 1 /\ UNCHANGED held
Spec == Init /\ [][Next]_vars
=============================================================================
`

func TestHonestFormsAreRead812(t *testing.T) {
	m, err := Read(honest812, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.Definitions(), []string{"vars", "Sum", "TypeOK", "Label", "Range", "Pair", "Double", "Init", "Next", "Spec"}; !reflect.DeepEqual(got, want) {
		t.Errorf("definitions = %v\nwant %v", got, want)
	}
	if got, want := m.Variables(), []string{"n", "held"}; !reflect.DeepEqual(got, want) {
		t.Errorf("variables = %v; want %v", got, want)
	}
	if got, want := m.Main.Extends, []string{"Naturals", "FiniteSets", "TLC"}; m.Name() != "Honest" || !reflect.DeepEqual(got, want) {
		t.Errorf("module %s extends %v; want Honest, %v", m.Name(), got, want)
	}
	for name, want := range map[string]string{
		// TLC reads the conjunct under the comment as part of TypeOK, so the
		// pin covers it.
		"TypeOK": `TypeOK == /\ n \in 0..Limit /\ held \subseteq Workers`,
		// A string is kept as written, spaces and comment markers included.
		"Label": `Label == "(* not a comment *)  -- nor this"`,
		"Sum":   `Sum(s) == IF s = {} THEN 0 ELSE LET x == CHOOSE y \in s : TRUE IN x + Sum(s \ {x})`,
		"Range": `Range == 1 .. LET top == Limit IN top`,
		"vars":  `vars == <<n, held>>`,
		// A function's definition is read, so one a statement uses is pinned.
		"Double": `Double[i \in 0..Limit] == i + i`,
	} {
		def, err := m.Definition(name)
		if err != nil || Canonical(def) != want {
			t.Errorf("%s reads as %q, %v\nwant %q", name, Canonical(def), err, want)
		}
	}
	if closure, err := m.Closure("Spec", map[string]bool{"Init": true, "Next": true}); err != nil || !reflect.DeepEqual(closure, []string{"Spec", "vars"}) {
		t.Errorf("Spec's closure = %v, %v", closure, err)
	}
	// The conjunct under the comment is pinned: changing it changes the pin.
	weaker, err := PinHash(strings.Replace(honest812, `/\ held \subseteq Workers`, `/\ TRUE`, 1), "TypeOK", nil)
	if pin, _ := m.PinHash("TypeOK", nil); err != nil || pin == weaker {
		t.Errorf("a conjunct under a comment at the left margin isn't pinned (%v)", err)
	}
}

const pool812 = `---- MODULE Pool ----
EXTENDS Naturals, FiniteSets
VARIABLE holders
vars == <<holders>>
AtMostOne == Cardinality(holders) <= 1
Init == holders = {}
Next == holders' = holders
Spec == Init /\ [][Next]_vars
====
`

// Each model here shows the old reader the ratified AtMostOne, or leaves
// its pin alone, while TLC checks something else (#812): the audit
// reproduced the first four on the pinned TLC, and
// TestSANYReadsAsTheReaderDoes holds how TLC reads each. The reader refuses
// every one, and names the line.
func TestAModelTLCReadsDifferentlyIsRefused812(t *testing.T) {
	replace := func(old, new string) string {
		t.Helper()
		if !strings.Contains(pool812, old) {
			t.Fatalf("the fixture has no %q", old)
		}
		return strings.Replace(pool812, old, new, 1)
	}
	weak := "AtMostOne == TRUE"
	strong := "AtMostOne == Cardinality(holders) <= 1"
	hidden := replace("EXTENDS Naturals, FiniteSets", "EXTENDS Naturals")
	cases := []struct{ name, model, at, want string }{
		{"the ratified text before the header", strong + "\n" + replace(strong, weak), strong, "TLC ignores whatever comes before the header"},
		{"junk on the header's line", "see ---- MODULE Pool ----" + strings.TrimPrefix(pool812, "---- MODULE Pool ----"), "see ----", "TLC ignores whatever comes before the header"},
		{"a closing line that ends the module early", replace(strong, weak+" ====\n"+strong), weak, "==== closes the module"},
		{"a nested module", replace(strong, strong+"\n---- MODULE Inner ----\nAtMostOne == TRUE\n===="), "---- MODULE Inner", "isn't one the gate reads"},
		{"an indented definition", strings.Replace(hidden, strong, "\\* a helper\n Cardinality(S) == 0\n"+strong, 1), " Cardinality(S)", "a second definition inside the definition of vars"},
		{"a definition after a comment on its line", strings.Replace(hidden, strong, "(* a helper *) Cardinality(S) == 0\n"+strong, 1), "(* a helper *)", "a second definition inside the definition of vars"},
		{"two definitions on one line", strings.Replace(hidden, strong, strong+" Cardinality(S) == 0", 1), strong, "a second definition inside the definition of AtMostOne"},
		{"a definition inside a declaration", strings.Replace(hidden, "VARIABLE holders", "VARIABLE holders\n  Cardinality(S) == 0", 1), "  Cardinality(S)", "a second definition inside the VARIABLE"},
		{"a declaration inside a definition", replace(strong, strong+"\n  CONSTANT Free"), "  CONSTANT Free", "CONSTANT inside the definition of AtMostOne"},
		{"a reserved word as a field, hiding a LET", replace(strong, "AtMostOne == [a |-> 1].LET\n  Cardinality(S) == [b |-> 0].IN"), "AtMostOne == [a", "LET as a record's field"},
		{"a field named IN", replace(strong, "AtMostOne == LET a == 1 IN [IN |-> a]"), "AtMostOne == LET", "IN as a record's field"},
		{"a LET that never closes", replace(strong, "AtMostOne == LET a == 1\n  Cardinality(S) == 0"), "AtMostOne == LET", "a LET with no IN"},
		{"an IN with no LET", replace(strong, "AtMostOne == TRUE IN"), "AtMostOne == TRUE IN", "an IN with no LET"},
		{"a lone carriage return ending a comment", replace(strong, "\\* a note\rCardinality(S) == 0\n"+strong), "\\* a note", "a control character"},
		{"a form feed", replace(strong, "\f"+strong), "\f", "a control character"},
		{"a comment that never closes", replace(strong, "(* "+strong), "(* ", "a (* comment never closes"},
		{"a string that doesn't close", replace(strong, `AtMostOne == "open`), `AtMostOne == "open`, "a string must close on the line it opens on"},
		{"an escape TLC refuses", replace(strong, `AtMostOne == "a \* b" = "c"`), `AtMostOne == "a`, `a string holds \*`},
		{"three equals signs", replace(strong, "AtMostOne === TRUE"), "AtMostOne ===", "isn't an operator"},
		{"a name defined twice", replace(strong, strong+"\n"+weak), weak, "AtMostOne is defined twice"},
		{"an INSTANCE", replace(strong, "INSTANCE Sequences\n"+strong), "INSTANCE", "can't pin what an INSTANCE brings"},
		{"an INSTANCE inside a definition", replace(strong, "Seqs == INSTANCE Sequences\n"+strong), "Seqs ==", "INSTANCE inside the definition of Seqs"},
		{"a LOCAL definition", replace(strong, "LOCAL "+strong), "LOCAL", "can't pin what an INSTANCE brings"},
		{"a theorem", replace(strong, strong+"\nTHEOREM Spec => []AtMostOne"), "THEOREM", "doesn't read one in a model"},
		{"a character outside ASCII", replace(strong, strong+"\n Cardinality(S) \u225c 0"), " Cardinality(S)", "a character outside ASCII"},
		{"an operator symbol's definition", replace(strong, "a ++ b == a\n"+strong), "a ++ b", "isn't one the gate reads"},
		{"a head split over two lines", replace(strong, "AtMostOne\n  == TRUE"), "AtMostOne\n", "isn't one the gate reads"},
		{"a reserved word as a name", replace(strong, "IF == TRUE\n"+strong), "IF ==", "isn't one the gate reads"},
		{"an indented line that continues nothing", replace("EXTENDS Naturals, FiniteSets", "  EXTENDS Naturals, FiniteSets"), "  EXTENDS", "an indented line that continues nothing"},
		{"a separator inside a definition", replace(strong, strong+"\n  ---- "), "  ---- ", "a ---- separator inside the definition of AtMostOne"},
		{"an EXTENDS that isn't first", replace("VARIABLE holders", "VARIABLE holders\nEXTENDS TLC"), "EXTENDS TLC", "EXTENDS comes first"},
		{"a module TLC ships and the gate doesn't take", replace("EXTENDS Naturals, FiniteSets", "EXTENDS Naturals, FiniteSets, Randomization"), "EXTENDS", "EXTENDS Randomization"},
		{"a module that isn't beside the model", replace("EXTENDS Naturals, FiniteSets", "EXTENDS Naturals, FiniteSets, Helper"), "EXTENDS", "there's no Helper.tla beside the model"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := PinHash(c.model, "AtMostOne", nil)
			if err == nil {
				t.Fatal("the model was read, and its pin taken")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal doesn't say %q:\n%v", c.want, err)
			}
			i := strings.Index(c.model, c.at)
			if i < 0 {
				t.Fatalf("the model has no %q", c.at)
			}
			if line := fmt.Sprintf("line %d:", 1+strings.Count(c.model[:i], "\n")); !strings.HasPrefix(err.Error(), line) {
				t.Errorf("the refusal doesn't start with %q:\n%v", line, err)
			}
			for name, read := range map[string]func() error{
				"Closure": func() error { _, err := Closure(c.model, "AtMostOne", nil); return err },
				"Read":    func() error { _, err := Read(c.model, nil); return err },
			} {
				if got := read(); got == nil || got.Error() != err.Error() {
					t.Errorf("%s: %v\nwant the refusal: %v", name, got, err)
				}
			}
			// What a module extends is the model's to resolve. Everything
			// else is refused however the module is read.
			if strings.Contains(c.want, "EXTENDS ") || strings.Contains(c.want, "beside the model") {
				return
			}
			for name, read := range map[string]func() error{
				"Definition": func() error { _, err := Definition(c.model, "AtMostOne"); return err },
				"Steps":      func() error { _, err := Steps(c.model); return err },
				"Skeleton": func() error {
					_, err := Skeleton(c.model, map[string]bool{"AtMostOne": true, "Spec": true}, []string{"Spec"})
					return err
				},
			} {
				if got := read(); got == nil || got.Error() != err.Error() {
					t.Errorf("%s: %v\nwant the refusal: %v", name, got, err)
				}
			}
			if Definitions(c.model) != nil || Variables(c.model) != nil {
				t.Error("a refused model still names definitions or variables")
			}
		})
	}
	// A copy of the ratified text in a comment, or after the closing line,
	// where TLC reads nothing, is never read: the pin is taken from the
	// definition TLC reads. The Toolbox keeps a file's history after the
	// closing line, so text there is ignored, not refused.
	strongPin, err := PinHash(pool812, "AtMostOne", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, model := range map[string]string{
		"a block comment":        replace(strong, "(*\n"+strong+"\n*)\n"+weak),
		"a nested comment":       replace(strong, "(* (* x *)\n"+strong+"\n*)\n"+weak),
		"text after the closing": replace(strong, weak) + "\\* Modification History\n" + strong + "\n(* never closed \"\n",
	} {
		pin, err := PinHash(model, "AtMostOne", nil)
		if err != nil || pin == strongPin || pin != Hash(weak) {
			t.Errorf("the ratified text in %s: the pin is %s, %v; want the pin of the definition TLC reads", name, pin, err)
		}
	}
	// One module read alone says what it defines itself, whatever it
	// extends, and a pin is never taken that way.
	wrapper := "---- MODULE Check ----\nEXTENDS Pool, TLC\nVARIABLE seen\nNever == ~AtMostOne\n====\n"
	if def, err := Definition(wrapper, "Never"); err != nil || def != "Never == ~AtMostOne" || !reflect.DeepEqual(Variables(wrapper), []string{"seen"}) || !reflect.DeepEqual(Definitions(wrapper), []string{"Never"}) {
		t.Errorf("a module read alone: %q, %v, %v, %v", def, err, Variables(wrapper), Definitions(wrapper))
	}
	if _, err := PinHash(wrapper, "Never", nil); err == nil || !strings.Contains(err.Error(), "there's no Pool.tla beside the model") {
		t.Errorf("a pin taken from a module without what it extends: %v", err)
	}
}

// A comment marker inside a string is text, and so are the string's spaces.
// The patterns the pin used to strip comments with read "a(*" \/ X \/ "*)bc"
// as "abc", so a statement could gain a disjunct and keep its pin.
func TestCanonicalKeepsStrings812(t *testing.T) {
	for def, want := range map[string]string{
		`X == y = "a(*" \/ Evil \/ y = "*)bc"`:          `X == y = "a(*" \/ Evil \/ y = "*)bc"`,
		`X == y = "a  b"   \* two spaces`:               `X == y = "a  b"`,
		`X == "\* not a comment" \o "x"`:                `X == "\* not a comment" \o "x"`,
		`X == "a\"(*" \o "*)"`:                          `X == "a\"(*" \o "*)"`,
		"X == (* one (* two *) still one *) 1":          `X == 1`,
		"X == 1 \\* a (* that opens nothing\n    + 2":   `X == 1 + 2`,
		"X == (* a \\* marker inside *) 1\n    + 2":     `X == 1 + 2`,
		"X ==\n    /\\ a   (* \"quoted *)\n    /\\ b\n": `X == /\ a /\ b`,
		"X == a(* glued *)b":                            `X == a b`,
	} {
		if got := Canonical(def); got != want {
			t.Errorf("Canonical(%q)\n = %q\nwant %q", def, got, want)
		}
	}
	if Hash(`X == y = "a(*" \/ Evil \/ y = "*)bc"`) == Hash(`X == y = "abc"`) {
		t.Error("a disjunct spliced between two strings kept the pin")
	}
	if Hash(`X == y = "a  b"`) == Hash(`X == y = "a b"`) {
		t.Error("a string's spaces aren't part of its pin")
	}
}

const (
	extending812 = `---- MODULE Main ----
EXTENDS Naturals, Helper
VARIABLE x
vars == <<x>>
Safe == Small(x) /\ x >= 0
Init == x = 0
Next == x' = x
Spec == Init /\ [][Next]_vars
====
`
	helper812 = `---- MODULE Helper ----
EXTENDS Naturals
Top == 3
Small(v) == v <= Top
====
`
)

// A module beside the model that the model extends is read the same way, and
// its definitions join the closure, so one can't change under a pin.
func TestModulesBesideTheModelArePinned812(t *testing.T) {
	stop := map[string]bool{"Init": true, "Next": true}
	includes := map[string]string{"Helper.tla": helper812, "Unused.tla": "not a module, and nothing extends it"}
	m, err := Read(extending812, includes)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.Definitions(), []string{"Top", "Small", "vars", "Safe", "Init", "Next", "Spec"}; !reflect.DeepEqual(got, want) {
		t.Errorf("definitions = %v\nwant %v", got, want)
	}
	if closure, err := m.Closure("Safe", stop); err != nil || !reflect.DeepEqual(closure, []string{"Safe", "Small", "Top"}) {
		t.Errorf("Safe's closure = %v, %v", closure, err)
	}
	pin, err := m.PinHash("Safe", stop)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Read(extending812, map[string]string{"Helper.tla": strings.Replace(helper812, "Top == 3", "Top == 300", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if other, err := changed.PinHash("Safe", stop); err != nil || other == pin {
		t.Errorf("changing a definition in the module beside the model kept Safe's pin (%v)", err)
	}
	if def, err := m.Definition("Small"); err != nil || def != "Small(v) == v <= Top" {
		t.Errorf("Small reads as %q, %v", def, err)
	}
	for name, c := range map[string]struct {
		main     string
		includes map[string]string
		want     string
	}{
		"not supplied":                  {extending812, nil, "line 2: EXTENDS Helper, and there's no Helper.tla beside the model"},
		"refused itself":                {extending812, map[string]string{"Helper.tla": strings.Replace(helper812, "Top == 3", "Top == 3\n Small(v) == TRUE", 1)}, "Helper.tla line 4: a second definition inside the definition of Top"},
		"named for another module":      {extending812, map[string]string{"Helper.tla": strings.Replace(helper812, "MODULE Helper", "MODULE Other", 1)}, "Helper.tla holds module Other"},
		"defining what the model does":  {extending812, map[string]string{"Helper.tla": helper812 + "\n"}, ""},
		"defining a name twice over":    {extending812, map[string]string{"Helper.tla": strings.Replace(helper812, "Top == 3", "Top == 3\nvars == 1", 1)}, "vars is defined in both Helper and Main"},
		"extending the model in turn":   {extending812, map[string]string{"Helper.tla": strings.Replace(helper812, "EXTENDS Naturals", "EXTENDS Naturals, Main", 1)}, "Helper extends Main, which extends it in turn"},
		"named like a module TLC ships": {extending812, map[string]string{"Helper.tla": helper812, "FiniteSets.tla": "---- MODULE FiniteSets ----\nCardinality(S) == 0\n====\n"}, "FiniteSets.tla: TLC has a module named FiniteSets, and would load this file in its place"},
		"a model named like one":        {strings.Replace(pool812, "MODULE Pool", "MODULE Sequences", 1), nil, "a model can't be named Sequences"},
	} {
		_, err := Read(c.main, c.includes)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("a module beside the model, %s: %v\nwant %q", name, err, c.want)
		}
	}
	for _, name := range []string{"FiniteSets.tla", "Naturals.tla", "TLC.tla", "Integers.tla", "Sequences.tla", "Bags.tla", "Reals.tla", "RealTime.tla", "Randomization.tla", "Toolbox.tla", "Strings.tla", "TransitiveClosure.tla", "AnySet.tla", "TLCExt.tla"} {
		if ValidIncludeName(name, "Main") {
			t.Errorf("%s is accepted beside a model, where TLC would load it in place of its own module", name)
		}
	}
}

// TLC's code for \div differs between Naturals and Integers when one side is
// negative, and which of them a model extends isn't pinned. So what a pin
// covers may use \div only where it means one thing.
func TestDivisionIsPinnedOnlyUnderIntegers812(t *testing.T) {
	model := strings.Replace(pool812, "AtMostOne == Cardinality(holders) <= 1", "Half(n) == n \\div 2\nAtMostOne == Half(Cardinality(holders)) = 0\nNote == TRUE \\* not a \\div b", 1)
	if _, err := PinHash(model, "AtMostOne", nil); err == nil || !strings.Contains(err.Error(), `Half uses \div, and the model doesn't extend Integers`) {
		t.Errorf("a pin over \\div under Naturals: %v", err)
	}
	for _, name := range []string{"Spec", "Note"} {
		if _, err := PinHash(model, name, nil); err != nil {
			t.Errorf("%s doesn't use \\div, and wasn't pinned: %v", name, err)
		}
	}
	if _, err := PinHash(strings.Replace(model, "EXTENDS Naturals, FiniteSets", "EXTENDS Integers, FiniteSets", 1), "AtMostOne", nil); err != nil {
		t.Errorf("a pin over \\div under Integers: %v", err)
	}
}
