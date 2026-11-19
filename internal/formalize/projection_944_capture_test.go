package formalize_test

import (
	"encoding/json"
	"os"
	"testing"
)

// Capture is deliberately opt-in: these fixtures belong to unchanged main,
// never to a candidate implementation. Their immutable pins live in acceptance.
func TestReader944CaptureMain(t *testing.T) {
	if os.Getenv("INVARIANT_CAPTURE_MAIN_944") != "950fefa6" {
		return
	}
	if err := os.MkdirAll("testdata/reader944", 0755); err != nil {
		t.Fatal(err)
	}
	for name, g := range reader944SmallFixtures(t) {
		g = reader944Rendered(t, g)
		reader944PinnedBytes(t, name, g)
		b, err := json.MarshalIndent(g, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("testdata/reader944/"+name+".json", append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{
  "base_revision": "950fefa6",
  "command": "INVARIANT_CAPTURE_MAIN_944=950fefa6 go test ./internal/formalize -run '^TestReader944CaptureMain$' -count=1",
  "inputs": "reader603Workspace with unchanged complete-protocol reader603Tools; ordinary Limit=2; comparison Limit=3 with Working renamed Revised and Request{Previous:ordinary}; preview639Draft(t,1,nil), F1/A, Limit=1; exact source, view, includes, locks and receipts are in the evidence files",
  "pins": "TestReader944LargeAdmissionKeepsSmallBytes in immutable projection_944_accept_test.go"
}
`
	if err := os.WriteFile("testdata/reader944/manifest.json", []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
}
