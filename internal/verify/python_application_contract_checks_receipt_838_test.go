package verify_test

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// The receipt says what checking a proved function's contract on the
// recorded calls found, and derives it again from those calls (#838): a
// report read back from JSON says the same, a recorded call edited to break
// the contract fails the receipt whatever its statuses say, and a contract
// edited to ask less changes the report's fingerprint.
func TestContractChecksReceipt838(t *testing.T) {
	r := verify.ContractChecksReport838(t, "engine")
	if !r.Passed {
		t.Fatal("the fixture's callers meet the contract")
	}
	decoded := calls793DecodeReport(t, calls793ReportWire(t, r))
	if verify.Fingerprint(*decoded) != verify.Fingerprint(*r) {
		t.Fatal("the contract or its checked calls changed on a report's round trip")
	}
	md := receipt.Markdown(decoded)
	for _, want := range []string{
		"### Python application · callers checked against proved contracts",
		"3 calls recorded, 3 by the application's own code. Each of 27 checks held.",
		"<code>requested: str</code>",
		"| <code>Requires(requested == 'engine' or requested == 'boat')</code> | evaluated |",
		"| <code>Ensures(Implies(busy, Result() == 409))</code> | evaluated where the call returned |",
		"says nothing about any other call",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("the receipt lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "**What failed**") {
		t.Fatalf("a passing receipt lists a failure:\n%s", md)
	}

	// A recorded call edited to break the contract, with every status left
	// as it was.
	wire := calls793ReportWire(t, r)
	edited := 0
	for _, x := range wire["application"].(map[string]any)["executions"].([]any) {
		rows, _ := x.(map[string]any)["observations"].([]any)
		for _, row := range rows {
			calls, _ := row.(map[string]any)["calls"].([]any)
			for _, call := range calls {
				call.(map[string]any)["arguments"].(map[string]any)["requested"] = "car"
				edited++
			}
		}
	}
	if edited == 0 {
		t.Fatal("the report records no call")
	}
	broken := calls793DecodeReport(t, wire)
	md = receipt.Markdown(broken)
	for _, want := range []string{"**What failed**", "didn't meet Requires(requested == 'engine' or requested == 'boat')", `requested="car"`} {
		if !strings.Contains(md, want) {
			t.Fatalf("the receipt of an edited call lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "checks held") {
		t.Fatalf("the receipt of an edited call still says every clause held:\n%s", md)
	}

	// A contract edited to ask nothing of its callers.
	wire = calls793ReportWire(t, r)
	for _, p := range wire["application"].(map[string]any)["proofs"].([]any) {
		contract := p.(map[string]any)["input"].(map[string]any)["contract"].(map[string]any)
		contract["obligations"] = []any{}
	}
	if verify.Fingerprint(*calls793DecodeReport(t, wire)) == verify.Fingerprint(*r) {
		t.Fatal("a contract that asks less left the report's fingerprint as it was")
	}
}

// A call that broke the contract is in the receipt as the gate found it.
func TestContractChecksReceiptOfABrokenContract838(t *testing.T) {
	r := verify.ContractChecksReport838(t, "car")
	if r.Passed {
		t.Fatal("the fixture's caller breaks the contract")
	}
	md := receipt.Markdown(calls793DecodeReport(t, calls793ReportWire(t, r)))
	for _, want := range []string{
		"**What failed**",
		"a call from counter/core.py didn't meet Requires(requested == 'engine' or requested == 'boat')",
		"3 calls recorded, 3 by the application's own code. 27 checks settled, and what's listed below failed the gate.",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("the receipt lacks %q:\n%s", want, md)
		}
	}
}
