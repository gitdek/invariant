package factory

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNodeDiagnosticRefusalComment788(t *testing.T) {
	const refusal = "Node execution refused: selected source has unattributed transform, import or type diagnostics"
	var problem strings.Builder
	problem.WriteString(refusal + "\n")
	var diagnostics []string
	for i := 1; i <= 48; i++ {
		diagnostic := fmt.Sprintf("future.test.ts:%d:8 TS2307: Cannot find module './hooks/new-module-%02d' or its corresponding type declarations.", i, i)
		diagnostics = append(diagnostics, diagnostic)
		problem.WriteString(diagnostic + "\n")
	}
	m := Marker{Kind: KindStuck}
	const closing = "Say what you'd like in a comment, then comment `/invariant revise` to try again."
	body := stuckComment(problem.String(), m)
	for _, diagnostic := range diagnostics {
		if !strings.Contains(body, diagnostic) {
			t.Errorf("factory comment lost a precheck diagnostic: %s", diagnostic)
		}
	}
	if len(body) > maxComment || !strings.Contains(body, closing) {
		t.Fatal("diagnostic comment lost its size bound or revise instruction")
	}
	if decoded, ok := DecodeMarker(body); !ok || decoded.Kind != KindStuck {
		t.Fatal("diagnostic comment lost its recovery marker")
	}
	if strings.Contains(body, "Some detail is left out") {
		t.Fatal("all 48 diagnostics fit comfortably in one comment")
	}

	// The total GitHub comment budget and its explicit omission notice still
	// apply; reporting all ordinary diagnostics cannot break publication.
	oversized := stuckComment(strings.Repeat(problem.String(), 30), m)
	if len(oversized) > maxComment || !utf8.ValidString(oversized) || !strings.Contains(oversized, closing) ||
		!strings.Contains(oversized, "Some detail is left out, because GitHub limits how long a comment can be.") {
		t.Fatal("oversized diagnostic comment lost bounded publication, omission notice or closing instructions")
	}
	if decoded, ok := DecodeMarker(oversized); !ok || decoded.Kind != KindStuck {
		t.Fatal("oversized diagnostic comment lost its recovery marker")
	}
}
