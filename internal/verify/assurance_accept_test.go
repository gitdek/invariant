package verify_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// This slice corrects success claims on legacy reports, without inferring a
// component scope or changing successful legacy proof coverage. In particular,
// Unverified still lists the functions outside a successful legacy proof.
// Attribution to individual components remains the next slice of #229.
//
// Claim derives its words from results, including when an older JSON report
// carries a stale Assurance string: a successful proof with functions wins;
// otherwise a successful Agreement OR Conformance establishes bounded testing;
// otherwise the code is unchecked. Build success alone establishes neither.
// A failed Tried result prevents an exhaustive dashboard claim, but does not
// erase the successful conformance check of the transitions actually observed.
func TestReportClaimsRequireSuccessfulEvidence(t *testing.T) {
	proved := func(verifier string) *verify.Code {
		return &verify.Code{Verifier: verifier, Passed: true, Functions: []string{"Step"}, Contracts: []string{"Step"}, Unverified: []string{"Neighbor"}}
	}
	cases := []struct {
		name string
		r    verify.Report
		want string
	}{
		{"no results", verify.Report{Assurance: "proved"}, "unchecked"},
		{"model-only remains unchanged", verify.Report{ModelOnly: true}, ""},
		{"build alone", verify.Report{Assurance: "tested against the model", Build: verify.Build{Passed: true}}, "unchecked"},
		{"failed proof with discovered functions", verify.Report{Assurance: "proved", Code: &verify.Code{Verifier: "Gobra", Functions: []string{"Step"}, Contracts: []string{"Step"}}}, "unchecked"},
		{"successful empty proof", verify.Report{Assurance: "proved", Code: &verify.Code{Verifier: "Gobra", Passed: true}}, "unchecked"},
		{"failed agreement", verify.Report{Assurance: "tested against the model", Agreement: &verify.Agreement{States: 3}}, "unchecked"},
		{"failed exhaustive conformance", verify.Report{Assurance: "tested against the model", Conformance: &conformance.Result{Exhaustive: true, States: 3, ModelStates: 3}}, "unchecked"},
		{"agreement survives failed proof", verify.Report{Assurance: "proved", Code: &verify.Code{Functions: []string{"Step"}}, Agreement: &verify.Agreement{Passed: true}}, "tested against the model"},
		{"sampled conformance", verify.Report{Assurance: "proved", Conformance: &conformance.Result{Passed: true, Runs: 1, Steps: 2, States: 2, ModelStates: 3}}, "tested against the model"},
		{"exhaustive conformance", verify.Report{Conformance: &conformance.Result{Passed: true, Exhaustive: true, Steps: 4, States: 3, ModelStates: 3}}, "tested against the model"},
		{"checked transitions despite untried step", verify.Report{Conformance: &conformance.Result{Passed: true, Exhaustive: true, Steps: 4, States: 3, ModelStates: 3, Tried: &conformance.Tried{Passed: false}}}, "tested against the model"},
		{"agreement survives failed conformance", verify.Report{Agreement: &verify.Agreement{Passed: true}, Conformance: &conformance.Result{Passed: false}}, "tested against the model"},
		{"conformance survives failed agreement", verify.Report{Agreement: &verify.Agreement{Passed: false}, Conformance: &conformance.Result{Passed: true}}, "tested against the model"},
		{"legacy Go proof", verify.Report{Assurance: "unchecked", Code: proved("Gobra")}, "proved"},
		{"legacy Python proof", verify.Report{Code: proved("Nagini")}, "proved"},
		{"an unrelated gate failure preserves proof evidence", verify.Report{Passed: false, Assurance: "unchecked", Code: proved("Gobra"), Build: verify.Build{Passed: false}}, "proved"},
	}
	for _, tc := range cases {
		t.Logf("case: %s", tc.name)
		// Round-trip through the public JSON report, as receipt consumers do.
		before, err := json.Marshal(tc.r)
		if err != nil {
			t.Fatal(err)
		}
		var r verify.Report
		if err := json.Unmarshal(before, &r); err != nil {
			t.Fatal(err)
		}
		if got := r.Claim(); got != tc.want {
			t.Errorf("claim = %q, want %q from the successful results", got, tc.want)
		}
		after, _ := json.Marshal(r)
		if string(before) != string(after) {
			t.Error("reading the claim changed the report's recorded evidence")
		}
	}
}

func TestEverySizeRequiresANonemptySuccessfulProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		code *verify.Code
		want bool
	}{
		{"missing", nil, false},
		{"failed", &verify.Code{Verifier: "Gobra", Functions: []string{"Step"}}, false},
		{"empty", &verify.Code{Verifier: "Gobra", Passed: true}, false},
		{"Gobra", &verify.Code{Verifier: "Gobra", Passed: true, Functions: []string{"Step"}}, true},
		{"Nagini", &verify.Code{Verifier: "Nagini", Passed: true, Functions: []string{"Step"}, Unverified: []string{"Explore"}}, true},
	} {
		t.Logf("case: %s", tc.name)
		r := verify.Report{Assurance: "proved", Code: tc.code, Larger: &verify.Larger{Passed: true}}
		if got := r.EverySize(); got != tc.want {
			t.Errorf("EverySize = %v, want %v", got, tc.want)
		}
		wantClaim := "unchecked"
		if tc.want {
			wantClaim = "proved at every size"
		}
		if got := r.Claim(); got != wantClaim {
			t.Errorf("claim = %q, want %q", got, wantClaim)
		}
		for _, larger := range []*verify.Larger{nil, {Passed: false}} {
			r.Larger = larger
			if r.EverySize() || strings.Contains(r.Claim(), "at every size") {
				t.Error("an absent or failed larger check established an every-size claim")
			}
		}
	}
}

func assuranceWrite(t *testing.T, root, name, body string, mode os.FileMode) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// Run the actual gate and its output parsers, replacing only external tools.
// The fixture has a selected Gobra file plus an ordinary neighboring function.
// A failed or absent Gobra success message must never turn source discovery
// (Functions/Contracts) into a proof. No Docker daemon, network or prover is used.
func TestGateRecordsOnlyAssuranceEstablishedByToolResults(t *testing.T) {
	dir := t.TempDir()
	src := "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0 /\\ n <= Limit\n====\n"
	statements := []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}
	for i := range statements {
		h, err := tla.PinHash(src, statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		statements[i].SHA256 = h
	}
	lock, err := json.Marshal(project.Lock{Decision: "D-0013", Bounds: map[string]string{"Limit": "1"}, Statements: statements})
	if err != nil {
		t.Fatal(err)
	}
	assuranceWrite(t, dir, ".invariant/invariant.json", `{"name":"assurance fixture","language":"go","code":"core","module":".invariant/specs/Counter.tla"}`, 0o644)
	assuranceWrite(t, dir, ".invariant/ratified.lock", string(lock), 0o644)
	assuranceWrite(t, dir, ".invariant/specs/Counter.tla", src, 0o644)
	assuranceWrite(t, dir, "go.mod", "module example.test/assurance\n\ngo 1.27.1\n", 0o644)
	assuranceWrite(t, dir, "core/core.go", "// +gobra\npackage core\n//@ ensures result >= 0\nfunc Step() (result int) { return 0 }\n", 0o644)
	assuranceWrite(t, dir, "core/neighbor.go", "package core\nfunc Neighbor() {}\n", 0o644)
	bin := t.TempDir()
	assuranceWrite(t, bin, "go", "#!/bin/sh\necho 'go version go1.27.1 linux/amd64'\n", 0o755)
	assuranceWrite(t, bin, "docker", `#!/bin/sh
if [ "$1" = image ] || [ "$1" = rm ]; then exit 0; fi
for arg in "$@"; do
  case "$arg" in
    accept-java)
      cat <<'TLC'
@!@!@STARTMSG 2193:0 @!@!@
Model checking completed. No error has been found.
@!@!@ENDMSG 2193 @!@!@
@!@!@STARTMSG 2199:0 @!@!@
1 states generated, 1 distinct states found, 0 states left on queue.
@!@!@ENDMSG 2199 @!@!@
@!@!@STARTMSG 2194:0 @!@!@
The depth of the complete state graph search is 1.
@!@!@ENDMSG 2194 @!@!@
TLC
      exit 0 ;;
    accept-gobra)
      case "$ASSURANCE_ACCEPT_PROOF" in
        success) echo 'Gobra found 0 errors'; exit 0 ;;
        failure) echo 'Gobra found 1 errors'; exit 1 ;;
        missing) echo 'Gobra started but wrote no result'; exit 0 ;;
      esac
      exit 92 ;;
    accept-go)
      printf '@@invariant vet=0\n@@invariant test=0\n'
      if [ "$ASSURANCE_ACCEPT_AGREEMENT" = success ]; then
        printf 'invariant-agreement states=1 depth=1\n@@invariant agree=0\n'
      else
        printf 'exploration did not finish\n@@invariant agree=1\n'
      fi
      exit 0 ;;
  esac
done
echo 'unexpected tool invocation' >&2
exit 93
`, 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tc := toolchain.Toolchain{JavaImage: "accept-java", GobraImage: "accept-gobra", GoImage: "accept-go", TLCJar: filepath.Join(dir, "tla.jar")}
	for _, c := range []struct {
		proof, agreement, want string
		passed                 bool
	}{
		{"failure", "success", "tested against the model", false},
		{"missing", "success", "tested against the model", false},
		{"failure", "failure", "unchecked", false},
		{"missing", "failure", "unchecked", false},
		{"success", "success", "proved", true},
		{"success", "failure", "proved", false},
	} {
		t.Logf("case: proof %s, agreement %s", c.proof, c.agreement)
		t.Setenv("ASSURANCE_ACCEPT_PROOF", c.proof)
		t.Setenv("ASSURANCE_ACCEPT_AGREEMENT", c.agreement)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := verify.Run(ctx, dir, "", tc)
		if err != nil || r == nil {
			t.Fatalf("gate failed to return the tool results: report=%#v err=%v", r, err)
		}
		if !r.Design.Passed || !r.Build.Passed || r.Code == nil || r.Agreement == nil || !reflect.DeepEqual(r.Code.Functions, []string{"Step"}) || !reflect.DeepEqual(r.Code.Unverified, []string{"Neighbor"}) {
			t.Fatalf("fixture did not reach the expected checks: %#v", r)
		}
		if r.Code.Passed != (c.proof == "success") || r.Agreement.Passed != (c.agreement == "success") {
			t.Fatalf("fixture tool output was not parsed as expected: code=%#v agreement=%#v", r.Code, r.Agreement)
		}
		if r.Assurance != c.want || r.Claim() != c.want {
			t.Errorf("Assurance=%q Claim=%q, want %q", r.Assurance, r.Claim(), c.want)
		}
		if r.Passed != c.passed {
			t.Errorf("gate Passed=%v, want %v; correcting claims must not weaken the gate", r.Passed, c.passed)
		}
		var reportJSON map[string]json.RawMessage
		b, _ := json.Marshal(r)
		if err := json.Unmarshal(b, &reportJSON); err != nil {
			t.Fatal(err)
		}
		if string(reportJSON["assurance"]) != `"`+c.want+`"` {
			t.Errorf("JSON assurance = %s, want %q", reportJSON["assurance"], c.want)
		}
		if r.Fingerprint != verify.Fingerprint(*r) {
			t.Error("the fingerprint was computed before the final assurance was recorded")
		}
		otherRun := *r
		otherRun.GeneratedAt, otherRun.Agent, otherRun.Build.Output, otherRun.Toolchain.Go = "tomorrow", "another agent", "different test timing", "different patch version"
		if verify.Fingerprint(otherRun) != r.Fingerprint {
			t.Error("volatile run metadata changed the evidence fingerprint")
		}
	}
}
