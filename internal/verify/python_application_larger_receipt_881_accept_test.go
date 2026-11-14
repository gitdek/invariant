package verify_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationLargerReceiptExplainsWrongStep881(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			r := verify.LargerCounterReport881(t, "wrong step", explicit)
			if r.Passed || r.Larger == nil || !r.Larger.Required || r.Larger.Passed {
				t.Fatalf("receipt claims agreement for the larger 2-to-4 step: %+v", r.Larger)
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var decoded verify.Report
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Application.Larger, r.Application.Larger) || verify.Fingerprint(decoded) != r.Fingerprint || !reflect.DeepEqual(decoded.Coverage(), r.Coverage()) {
				t.Fatal("JSON round trip lost larger evidence, fingerprint or base coverage")
			}
			md := receipt.Markdown(&decoded)
			_, failure, found := strings.Cut(md, "**What failed**")
			if !found {
				t.Fatalf("receipt hides the larger conformance failure:\n%s", md)
			}
			// Ignore later evidence blocks: the explanation itself must say
			// which invocation and actual step failed, even when counts agree.
			failure = strings.SplitN(failure, "\n##", 2)[0]
			for _, want := range []string{"larger", "Inc", "count", "2", "4", decoded.Application.Larger[0].Key.ID} {
				if !strings.Contains(failure, want) {
					t.Errorf("What failed omits %q:\n%s", want, failure)
				}
			}
			// Mutating only the new checked evidence must alter the portable
			// fingerprint. Resource scheduling metadata remains incidental.
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			application := wire["application"].(map[string]any)
			larger := application["larger"].([]any)[0].(map[string]any)
			conformance, ok := larger["conformance"].(map[string]any)
			if !ok {
				t.Fatal("receipt omits larger conformance evidence")
			}
			result := conformance["result"].(map[string]any)
			result["message"] = "a changed larger conformance verdict"
			changed, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var altered verify.Report
			if err := json.Unmarshal(changed, &altered); err != nil {
				t.Fatal(err)
			}
			if verify.Fingerprint(altered) == r.Fingerprint {
				t.Fatal("receipt fingerprint does not bind larger conformance evidence")
			}
		})
	}
}
