package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestPreview639OrdinaryReceiptStatementsUnchanged(t *testing.T) {
	const says = "Keep <limit> & result | `literal` **rule**.\nExact wording."
	const want = "\n<details>\n<summary>Ratified statements</summary>\n\n" +
		"| Statement | Kind | Says | Pin |\n| :-- | :-- | :-- | :-- |\n" +
		"| `Safe` | invariant | " + says + " | ✅ `sha256:aa` |\n\n</details>"
	for _, tc := range []struct {
		name      string
		modelOnly bool
		reader    *verify.Reader
	}{
		{"legacy", false, nil},
		{"model-only", true, nil},
		{"ordinary reader", false, &verify.Reader{Version: 1}},
		{"selected model", true, &verify.Reader{Version: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &verify.Report{
				Project: "ordinary", Passed: true, ModelOnly: tc.modelOnly, Reader: tc.reader,
				Pins: []verify.Pin{{Name: "Safe", Kind: "invariant", Says: says, Want: "sha256:aa", Got: "sha256:aa", Match: true}},
			}
			md := Markdown(r)
			if !strings.Contains(md, want) {
				t.Fatalf("ordinary receipt changed its statement heading or rendering:\n%s", md)
			}
			if strings.Contains(md, "Candidate rules") || strings.Contains(md, "Unratified alternative") {
				t.Fatal("ordinary receipt acquired preview labels")
			}
		})
	}
}
