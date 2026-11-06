package plumbing

import "testing"

func TestGoDeclarationImportPolicy668Regression(t *testing.T) {
	const file = "app/ready_accept_test.go"
	for _, tc := range []struct {
		name, source   string
		legacy, native bool
	}{
		{"raw import spelling", "import `testing`\nfunc TestReady(t *testing.T) {}", true, false},
		{"blank import qualifier", "import _ \"testing\"\nfunc TestReady(t *_.T) {}", true, true},
		{"last testing import wins", "import first \"testing\"\nimport last \"testing\"\nfunc TestReady(t *first.T) {}", true, false},
		{"last testing import matches", "import first \"testing\"\nimport last \"testing\"\nfunc TestReady(t *last.T) {}", true, true},
		{"empty result list", "import \"testing\"\nfunc TestReady(t *testing.T) () {}", true, false},
		{"nonfunction collision", "import \"testing\"\nvar TestReady int\nfunc TestReady(t *testing.T) {}", true, true},
		{"invalid duplicate first", "import \"testing\"\nfunc TestReady() {}\nfunc TestReady(t *testing.T) {}", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package app\n" + tc.source + "\n"
			test := Test{Name: "TestReady", File: file}
			if err := declares(file, source, []Test{test}); (err == nil) != tc.legacy {
				t.Fatalf("legacy admission changed: %v", err)
			}
			test.Name, test.Part, test.Framework, test.Selector = "label", "app", "go", "TestReady/child"
			if err := acceptanceDeclarations(file, source, []Test{test}); (err == nil) != tc.native {
				t.Fatalf("native admission changed: %v", err)
			}
		})
	}
}
