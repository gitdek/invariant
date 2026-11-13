package verify_test

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// The receipt of an application whose state couldn't be written says so,
// where the person had nothing to read before (#850).
func TestUnencodableStateReceipt850(t *testing.T) {
	r := verify.UnencodableStateReport850(t, true)
	md := receipt.Markdown(r)
	for _, want := range []string{"❌ Fail", "cannot encode as TLA+", "field &#34;count&#34;"} {
		if !strings.Contains(md, want) && !strings.Contains(md, strings.ReplaceAll(want, "&#34;", `"`)) {
			t.Fatalf("the receipt lacks %q:\n%s", want, md)
		}
	}
}
