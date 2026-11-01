package verify

import (
	_ "embed"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The gate's own copies of the connection pool's model and proof (D-0091),
// so a rebuild of the example can't change what these tests check (#360).
const tlapsPool = `---- MODULE ConnectionPool ----
EXTENDS Naturals, FiniteSets
CONSTANTS Size, Clients
VARIABLES held

vars == <<held>>

Conns == 1..Size

Out == UNION {held[c] : c \in Clients}

TypeOK == held \in [Clients -> SUBSET Conns]

NoSharedConnection == \A c, d \in Clients : c # d => held[c] \cap held[d] = {}

Init == held = [c \in Clients |-> {}]

Acquire(c) ==
    /\ held[c] = {}
    /\ \E k \in Conns \ Out : held' = [held EXCEPT ![c] = {k}]

Refuse(c) ==
    /\ Out = Conns
    /\ UNCHANGED vars

Release(c) ==
    \E k \in held[c] : held' = [held EXCEPT ![c] = held[c] \ {k}]

Next == \E c \in Clients : Acquire(c) \/ Refuse(c) \/ Release(c)

Spec == Init /\ [][Next]_vars
====
`

const tlapsPoolProof = `\* A comment, then the proof.
IndInv_NoSharedConnection == TypeOK /\ NoSharedConnection

LEMMA NoSharedConnection_Init == Init => IndInv_NoSharedConnection
BY DEF Init, IndInv_NoSharedConnection, TypeOK, NoSharedConnection

LEMMA NoSharedConnection_Step == IndInv_NoSharedConnection /\ [Next]_vars => IndInv_NoSharedConnection'
BY DEF IndInv_NoSharedConnection, TypeOK, NoSharedConnection, Next, vars, Acquire, Refuse, Release, Out, Conns

LEMMA NoSharedConnection_Implies == IndInv_NoSharedConnection => NoSharedConnection
BY DEF IndInv_NoSharedConnection
`

// tlapsEvilPool is the review's adversarial model: an unsafe Evil step
// guarded by \E z \in {Size \in Clients}. That set enumeration is one tlapm
// reads as the empty set (#235), so to the prover the guard is false and the
// step is dead, and a proof of NoSharedConnection goes through; TLC, which
// parses the set correctly, finds two clients sharing a connection at Size 4.
// The gate must turn the model away before any proof of it is checked.
func tlapsEvilPool() string {
	return strings.Replace(tlapsPool,
		"Next == \\E c \\in Clients : Acquire(c) \\/ Refuse(c) \\/ Release(c)",
		"Evil(c) ==\n    /\\ Size >= 4\n    /\\ \\E z \\in {Size \\in Clients} : held' = [held EXCEPT ![c] = Conns]\n\n"+
			"Next == (\\E c \\in Clients : Acquire(c) \\/ Refuse(c) \\/ Release(c)) \\/ (\\E c \\in Clients : Evil(c))", 1)
}

//go:embed tlaps_soundness.txt
var tlapsKnownSoundness string

// tlapsSoundness parses the embedded known-soundness list into issue → tests.
func tlapsSoundness(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(tlapsKnownSoundness), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		issue, test, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("a soundness line without an issue: %q", line)
		}
		out[issue] = append(out[issue], test)
	}
	return out
}

// TestTLAPSClosesEveryKnownSoundnessIssue checks that every unsoundness the
// pinned tlapm marks "known" is one the gate rules out, each by a refusal a
// representative triggers or by a reason it can't arise that this test
// checks (#360, D-0231). TestTLAPSRealKnownSoundnessTestsMatch keeps the list
// itself honest against the pinned tlapm's own suite.
func TestTLAPSClosesEveryKnownSoundnessIssue(t *testing.T) {
	known := tlapsSoundness(t)
	refuse := func(body, want string) func(*testing.T) {
		return func(t *testing.T) {
			_, err := tlapsModule("ConnectionPool", "Spec", "NoSharedConnection", tlapsPoolProof+"\n"+body+"\n")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("tlapsModule accepted %q: %v (want a refusal naming %q)", body, err, want)
			}
		}
	}
	closed := map[string]func(*testing.T){
		// The len/DOMAIN unsoundness needs the old SMT encoding, reached only
		// by "--debug oldsmt"; the gate passes neither flag, and no proof
		// pragma selects the old encoding.
		"51": func(t *testing.T) {
			if slices.Contains(tlapsOptions, "--debug") || slices.Contains(tlapsOptions, "oldsmt") {
				t.Fatalf("the gate's tlapm options could reach the old SMT encoding: %v", tlapsOptions)
			}
		},
		"235": func(t *testing.T) {
			refuse("Bad == {x \\in Clients}", "#235")(t)
			if err := tlapsModel(strings.Replace(tlapsPool, "Conns == 1..Size", "Conns == {x \\in 1..Size}", 1), "Spec"); err == nil || !strings.Contains(err.Error(), "#235") {
				t.Fatalf("tlapsModel accepted a dropped set element: %v", err)
			}
		},
		"300": refuse("Bad == 2 ^ Size", "exponent"),
		"304": refuse("Bad == CASE TRUE -> 1 [] OTHER -> 2", "CASE"),
		"306": refuse("Bad == CASE TRUE -> 1 [] OTHER -> 2", "CASE"),
		"309": refuse("Bad == ENABLED Next", "ENABLED"),
		"310": func(t *testing.T) {
			// SMT, Zenon and capture all need a temporal operator in the
			// obligation; the PTL route needs the temporal backend. The gate
			// refuses both.
			refuse("Bad == []TypeOK", "temporal operator")(t)
			refuse("LEMMA Bad == TypeOK => TypeOK\nBY PTL", "#310")(t)
		},
		"320": refuse("I == INSTANCE Naturals", "INSTANCE"),
	}
	for issue := range known {
		if closed[issue] == nil {
			t.Errorf("tlapm issue %s is a known unsoundness with no gate refusal or checked reason", issue)
		}
	}
	for issue, check := range closed {
		if known[issue] == nil {
			t.Errorf("issue %s is mapped but no longer in the known-soundness list", issue)
		}
		t.Run(issue, check)
	}
}

func TestTLAPSRefusesTheAdversarialModel(t *testing.T) {
	// The review's attack, through the real gate: the model is turned away
	// for its dropped set element before any proof of it runs (#235, D-0231).
	if err := tlapsModel(tlapsEvilPool(), "Spec"); err == nil || !strings.Contains(err.Error(), "#235") {
		t.Fatalf("tlapsModel accepted the adversarial model: %v", err)
	}
	got, _ := tlapsProve(t, tlapsEvilPool(), tlapsPoolProof)
	if got.Proved || !strings.Contains(got.Message, "#235") {
		t.Fatalf("proveTLAPS didn't refuse the adversarial model: %+v", got)
	}
}

func TestTLAPSDoesNotCiteALocalLemma(t *testing.T) {
	// A helper lemma named like a library fact is the proof's own, and the
	// gate records it as such, not as a fact the proof rests on.
	b, err := tlapsModule("ConnectionPool", "Spec", "NoSharedConnection", tlapsPoolProof+"\nLEMMA SetExtensionality == TypeOK => TypeOK\nOBVIOUS\n")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(b.cites, "TLAPS SetExtensionality") {
		t.Errorf("the proof's own lemma was recorded as a library citation: %v", b.cites)
	}
}

func tlapsTexts(t *testing.T, src string) []string {
	t.Helper()
	toks, err := lexTLA(src)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tok := range toks {
		out = append(out, tok.text)
	}
	return out
}

func TestTLAPSLexerReadsWhatTlapmReads(t *testing.T) {
	// A word in a comment or a string never counts, nested comments close
	// where TLA+ closes them, and a comment opener inside a string doesn't
	// hide the code after it.
	got := tlapsTexts(t, `A (* x (* AXIOM *) still a comment *) B \* AXIOM
C == "(*" D "\"*)" E`)
	want := []string{"A", "B", "C", "==", `"(*"`, "D", `"\"*)"`, "E"}
	if !slices.Equal(got, want) {
		t.Fatalf("lexTLA = %q, want %q", got, want)
	}
	for _, src := range []string{"A (* never closed", `A "never closed`} {
		if _, err := lexTLA(src); err == nil {
			t.Errorf("lexTLA(%q) accepted an unterminated comment or string", src)
		}
	}
	// Symbols are read whole, and a step label starting a line is one token.
	got = tlapsTexts(t, "<1>2. x <=> <<y>> /\\ [Next]_vars ~> z -+-> w\n<2>. QED")
	want = []string{"<1>2.", "x", "<=>", "<<", "y", ">>", "/\\", "[", "Next", "]", "_vars", "~>", "z", "-+->", "w", "<2>.", "QED"}
	if !slices.Equal(got, want) {
		t.Fatalf("lexTLA = %q, want %q", got, want)
	}
}

func TestTLAPSWritesTheTheoremAroundTheProof(t *testing.T) {
	b, err := tlapsModule("ConnectionPool", "Spec", "NoSharedConnection", tlapsPoolProof)
	if err != nil {
		t.Fatal(err)
	}
	want := "---- MODULE TLAPS_NoSharedConnection ----\nEXTENDS ConnectionPool, TLAPS\n" + strings.TrimRight(tlapsPoolProof, "\n") +
		"\nTHEOREM NoSharedConnection_AtEverySize == Spec => []NoSharedConnection\n" +
		"BY NoSharedConnection_Init, NoSharedConnection_Step, NoSharedConnection_Implies, PTL DEF Spec\n====\n"
	if b.module != want {
		t.Fatalf("module =\n%s\nwant\n%s", b.module, want)
	}
	if b.name != "TLAPS_NoSharedConnection" || b.theorem != "THEOREM NoSharedConnection_AtEverySize == Spec => []NoSharedConnection" {
		t.Errorf("name %q, theorem %q", b.name, b.theorem)
	}
	// Line n of the proof is line n+2 of the module, so tlapm's line
	// numbers map back to the proof.
	lines := strings.Split(b.module, "\n")
	for n, line := range strings.Split(tlapsPoolProof, "\n") {
		if n+tlapsBodyLines < len(lines) && line != "" && lines[n+tlapsBodyLines] != line {
			t.Fatalf("proof line %d is %q in the module, want %q", n+1, lines[n+tlapsBodyLines], line)
		}
	}
	if got := tlapsLines("File \"./TLAPS_NoSharedConnection.tla\", line 9, characters 1-2"); !strings.Contains(got, "line 7,") {
		t.Errorf("tlapsLines = %q, want the proof's line 7", got)
	}
}

func TestTLAPSRecordsTheLibraryItRestsOn(t *testing.T) {
	proof := "EXTENDS FiniteSetTheorems, NaturalsInduction\n\n" + tlapsPoolProof +
		"\nLEMMA Helper == ASSUME NEW S, IsFiniteSet(S) PROVE Cardinality(S) \\in Nat\nBY FS_CardinalityType\n"
	b, err := tlapsModule("ConnectionPool", "Spec", "NoSharedConnection", proof)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.libraries, []string{"FiniteSetTheorems", "NaturalsInduction"}) || !slices.Equal(b.cites, []string{"FiniteSetTheorems FS_CardinalityType"}) {
		t.Errorf("libraries %v, cites %v", b.libraries, b.cites)
	}
	if !strings.HasPrefix(b.module, "---- MODULE TLAPS_NoSharedConnection ----\nEXTENDS ConnectionPool, TLAPS, FiniteSetTheorems, NaturalsInduction\n") {
		t.Errorf("module header:\n%s", b.module[:200])
	}
	// The proof's own EXTENDS line becomes blank, keeping its lines.
	if lines := strings.Split(b.module, "\n"); strings.TrimSpace(lines[2]) != "" || lines[4] != "\\* A comment, then the proof." {
		t.Errorf("the proof's lines moved: %q", lines[:5])
	}
}

func TestTLAPSRefusesWhatAProofMayNotDo(t *testing.T) {
	for _, tc := range []struct{ name, extra, want string }{
		{"an axiom", "AXIOM FalseIsTrue == FALSE", "assumption"},
		{"an assumption", "ASSUMPTION A == FALSE", "assumption"},
		{"a top-level ASSUME", "ASSUME FALSE", "assumption, ASSUME"},
		{"a constant", "CONSTANT Extra", "declares constant"},
		{"a variable", "VARIABLE extra", "declares variable"},
		{"an instance", "I == INSTANCE Naturals", "INSTANCE"},
		{"a local definition", "LOCAL L == 1", "LOCAL"},
		{"a recursive operator", "RECURSIVE R(_)", "RECURSIVE"},
		{"another module", "---- MODULE Inner ----\n====", "module header"},
		{"an omitted proof", "LEMMA Omit == TRUE\nPROOF OMITTED", "omits"},
		{"ENABLED", "E == ENABLED Next", "ENABLED"},
		{`\cdot`, `D == Next \cdot Next`, `\cdot`},
		{"always", "A == []TypeOK", "temporal operator"},
		{"eventually", "A == <>TypeOK", "temporal operator"},
		{"leads-to", "A == TypeOK ~> TypeOK", "temporal operator"},
		{"while-plus", "A == TypeOK -+-> TypeOK", "temporal operator"},
		{"fairness", "A == WF_vars(Next)", "fairness"},
		{"temporal quantification", `A == \EE x : TypeOK`, "temporal quantification"},
		{"a temporal declaration", "LEMMA T == ASSUME TEMPORAL F PROVE F => F\nOBVIOUS", "temporal declaration"},
		{"a CASE expression", "C == CASE TRUE -> 1 [] OTHER -> 2", "CASE"},
		{"an exponent", "P == 2^3", "exponent"},
		{"a temporal rule", "LEMMA R == TRUE\nBY RuleINV1", "temporal rule"},
		{"the PTL backend", "LEMMA R == TypeOK => TypeOK\nBY PTL", "#310"},
		{"the LS4 backend", "LEMMA R == TypeOK => TypeOK\nBY LS4", "#310"},
		{"a dropped set element", "Bad == {x \\in Clients}", "#235"},
		{"a dropped set element among others", "Bad == {held, x \\in Clients}", "#235"},
		{"a dropped tuple element", "Bad == {<<x, y>> \\in Clients}", "#235"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tlapsModule("ConnectionPool", "Spec", "NoSharedConnection", tlapsPoolProof+"\n"+tc.extra+"\n")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("tlapsModule = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ name, proof, want string }{
		{"a library the proof may not extend", "EXTENDS TLC\n" + tlapsPoolProof, "may extend only"},
		{"the model again", "EXTENDS ConnectionPool\n" + tlapsPoolProof, "may extend only"},
		{"no inductive invariant", strings.Replace(tlapsPoolProof, "IndInv_NoSharedConnection ==", "Other ==", 1), "doesn't define IndInv_NoSharedConnection"},
		{"a weakened step", strings.Replace(tlapsPoolProof, "NoSharedConnection_Step == IndInv_NoSharedConnection /\\ [Next]_vars => IndInv_NoSharedConnection'", "NoSharedConnection_Step == TRUE", 1), "the gate's statement"},
		{"a weakened conclusion", strings.Replace(tlapsPoolProof, "IndInv_NoSharedConnection => NoSharedConnection", "IndInv_NoSharedConnection => TRUE", 1), "the gate's statement"},
		{"a missing lemma", strings.Replace(tlapsPoolProof, "LEMMA NoSharedConnection_Init ==", "LEMMA Other ==", 1), "doesn't prove lemma NoSharedConnection_Init"},
		{"a lemma with no proof", strings.Replace(tlapsPoolProof, "BY DEF Init, IndInv_NoSharedConnection, TypeOK, NoSharedConnection\n", "", 1), "NoSharedConnection_Init has no proof"},
		{"its own theorem", tlapsPoolProof + "THEOREM Mine == Spec => []TRUE\nOBVIOUS\n", "temporal operator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tlapsModule("ConnectionPool", "Spec", "NoSharedConnection", tc.proof)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("tlapsModule = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}

func TestTLAPSAcceptsOrdinaryProofSteps(t *testing.T) {
	// ASSUME ... PROVE states something the proof then proves, NEW binds
	// within a statement, CASE after a step label is a proof step, and
	// keywords in comments and strings never count.
	extra := `LEMMA Sequent == ASSUME NEW CONSTANT X, NEW x \in X PROVE x \in X
OBVIOUS

THEOREM ASSUME NEW S PROVE S = S
OBVIOUS

LEMMA Steps == TypeOK => TypeOK
<1> SUFFICES ASSUME TypeOK PROVE TypeOK
  OBVIOUS
<1>1. CASE held = held
  OBVIOUS
<1>2. ASSUME TypeOK PROVE TypeOK
  OBVIOUS
<1>. QED BY <1>1
\* AXIOM ENABLED [] CASE in a comment, (* and ^ in a block *)
Name == "AXIOM [] CASE ^"

\* Ordinary set constructs the parser reads correctly, so the gate leaves
\* them alone: a comprehension, a replacement, a quantified element, a
\* parenthesised membership, the empty set, and a nested enumeration.
Sets == /\ {c \in Clients : held[c] = {}} = {}
        /\ {held[c] : c \in Clients} = {}
        /\ {\E k \in Conns : k \in held[c]} = {}
        /\ {(k \in Conns)} = {}
        /\ {{k} : k \in Conns} = {}
`
	if _, err := tlapsModule("ConnectionPool", "Spec", "NoSharedConnection", tlapsPoolProof+"\n"+extra); err != nil {
		t.Fatal(err)
	}
}

func TestTLAPSChecksTheModelItRestsOn(t *testing.T) {
	if err := tlapsModel(tlapsPool, "Spec"); err != nil {
		t.Fatalf("the connection pool's model: %v", err)
	}
	for _, tc := range []struct{ name, old, new, want string }{
		{"a spec with fairness", "Spec == Init /\\ [][Next]_vars", "Spec == Init /\\ [][Next]_vars /\\ WF_vars(Next)", "the spec must be"},
		{"ENABLED", "Out == UNION", "Ready == ENABLED Next\nOut == UNION", "ENABLED"},
		{"a liveness property", "Out == UNION", "Live == TypeOK ~> TypeOK\nOut == UNION", "temporal operator"},
		{"a CASE expression", "Conns == 1..Size", "Conns == CASE Size > 0 -> 1..Size [] OTHER -> {}", "CASE"},
		{"an exponent", "Conns == 1..Size", "Conns == 1..(2^Size)", "exponent"},
		{"an unratified assumption", "VARIABLES held", "VARIABLES held\nASSUME Size \\in Nat", "assumption"},
		{"an axiom", "VARIABLES held", "VARIABLES held\nAXIOM Size \\in Nat", "assumption"},
		{"an unproved theorem", "VARIABLES held", "VARIABLES held\nTHEOREM Size \\in Nat", "states a fact"},
		{"another module", "EXTENDS Naturals, FiniteSets", "EXTENDS Naturals, FiniteSets, Helpers", "extends Helpers"},
		{"an instance", "VARIABLES held", "VARIABLES held\nH == INSTANCE Naturals", "INSTANCE"},
		{"a dropped set element", "Conns == 1..Size", "Conns == {x \\in 1..Size}", "#235"},
		{"the adversarial Evil step", "Next == \\E c \\in Clients : Acquire(c) \\/ Refuse(c) \\/ Release(c)",
			"Evil(c) == Size >= 4 /\\ \\E z \\in {Size \\in Clients} : held' = [held EXCEPT ![c] = Conns]\nNext == (\\E c \\in Clients : Acquire(c) \\/ Refuse(c) \\/ Release(c)) \\/ (\\E c \\in Clients : Evil(c))", "#235"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tlapsModel(strings.Replace(tlapsPool, tc.old, tc.new, 1), "Spec")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("tlapsModel = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}

// tlapsOutput is tlapm's toolbox output, trimmed to what the gate reads.
func tlapsOutput(count int, obligations [][3]string, tail ...string) string {
	var b strings.Builder
	b.WriteString("\\* TLAPM version 4744c0f\n")
	b.WriteString("@!!BEGIN\n@!!type:obligationsnumber\n@!!count:" + strconv.Itoa(count) + "\n@!!END\n")
	for _, o := range obligations {
		b.WriteString("@!!BEGIN\n@!!type:obligation\n@!!id:" + o[0] + "\n@!!status:" + o[1] + "\n@!!prover:" + o[2] + "\n@!!END\n")
	}
	for _, l := range tail {
		b.WriteString(l + "\n")
	}
	return b.String()
}

func TestTLAPSAcceptsOnlyEveryObligationProved(t *testing.T) {
	proved := [][3]string{{"1", "proved", "smt"}, {"2", "trivial", "tlapm"}, {"3", "proved", "ls4"}}
	if ok, msg := parseTLAPS(tlapsOutput(3, proved, "[INFO]: All 3 obligations proved.")).proved(); !ok {
		t.Fatalf("every obligation proved, yet: %s", msg)
	}
	for _, tc := range []struct {
		name string
		run  tlapsRun
		want string
	}{
		{"a failed obligation", parseTLAPS(tlapsOutput(3, [][3]string{{"1", "proved", "smt"}, {"2", "trivial", "tlapm"}, {"3", "failed", "isabelle"}})), "obligation 3 failed"},
		{"a missing proof", parseTLAPS(tlapsOutput(3, proved, "Missing proof at File \"./M.tla\", line 11, character 59", "[ERROR]: Proof incomplete in module \"M\": 1 missing, 0 omitted proof step(s).")), "Missing proof"},
		{"a reported error, even with every obligation proved", parseTLAPS(tlapsOutput(3, proved) + "@!!BEGIN\n@!!type:error\n@!!url:http://x\n@!!msg:a parse error\n@!!END\n"), "a parse error"},
		{"an obligation it never settled", parseTLAPS(tlapsOutput(4, proved)), "3 of its 4"},
		{"no obligations", parseTLAPS(tlapsOutput(0, nil)), "no obligations"},
		{"a nonzero exit", func() tlapsRun { r := parseTLAPS(tlapsOutput(3, proved)); r.exit = 10; return r }(), "status 10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ok, msg := tc.run.proved(); ok || !strings.Contains(msg, tc.want) {
				t.Fatalf("proved() = %v, %q; want a failure naming %q", ok, msg, tc.want)
			}
		})
	}
	if r := parseTLAPS(tlapsOutput(3, proved)); r.versionLine != "4744c0f" || r.prover[3] != "ls4" {
		t.Errorf("version %q, provers %v", r.versionLine, r.prover)
	}
}

func TestTLAPSEvidenceIsBoundToTheModelAndProof(t *testing.T) {
	base := Report{Project: "pool", TLAPS: []TLAPSProof{{Invariant: "NoSharedConnection", Proved: true, Obligations: 8,
		Model: sha(tlapsPool), Proof: sha(tlapsPoolProof), Provers: map[string]int{"smt": 3, "ls4": 1, "trivial": 4}}}}
	fp := Fingerprint(base)
	// Which backend got there first is timing; it doesn't change what was
	// proved, so it doesn't change the fingerprint.
	timing := base
	timing.TLAPS = []TLAPSProof{base.TLAPS[0]}
	timing.TLAPS[0].Provers = map[string]int{"zenon": 3, "ls4": 1, "trivial": 4}
	if Fingerprint(timing) != fp {
		t.Error("the backends' timing changed the fingerprint")
	}
	if base.TLAPS[0].Provers["smt"] != 3 {
		t.Error("fingerprinting changed the report")
	}
	// A changed model, proof or outcome is different evidence.
	for name, change := range map[string]func(*TLAPSProof){
		"model":   func(p *TLAPSProof) { p.Model = sha(strings.Replace(tlapsPool, "c # d", "TRUE", 1)) },
		"proof":   func(p *TLAPSProof) { p.Proof = sha(tlapsPoolProof + "\n") },
		"outcome": func(p *TLAPSProof) { p.Proved = false },
	} {
		r := base
		r.TLAPS = []TLAPSProof{base.TLAPS[0]}
		change(&r.TLAPS[0])
		if Fingerprint(r) == fp {
			t.Errorf("a changed %s left the fingerprint alone", name)
		}
	}
	// A proof that didn't hold fails the gate's model checks.
	failed := base
	failed.TLAPS = []TLAPSProof{{Invariant: "NoSharedConnection"}}
	if modelPassed(&failed) || !modelPassed(&base) {
		t.Error("an unproved invariant didn't fail the model checks, or a proved one did")
	}
}
