package factory

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeDiagnosticDraftRetainsDetailWithoutLocalPaths(t *testing.T) {
	r := newRig(t)
	root := t.TempDir()
	r.f.Work = filepath.Join(root, localUser, "factory-work")
	r.form.fail = "Node execution refused: selected source has unattributed transform, import or type diagnostics\n" +
		strings.Repeat("future.test.ts:2:24 TS2307: Cannot find module './future'.\n", 48) +
		fmt.Sprintf("future.test.ts:9:3 TS2322: Type is incompatible with %s.\n", filepath.Join(r.f.Work, "issue-1", "types.ts")) +
		"  The chained explanation refers to /Users/" + localUser + "/private/types.d.ts.\n" +
		"TS5109: [no source location] END_OF_DIAGNOSTICS788"
	r.gh.open(1, "gitdek", "Add a TypeScript module", "A new module.\n\n/invariant solve")
	r.poll()
	stuck := r.expect(1, KindStuck, LabelHumanReview)
	for what, text := range map[string]string{"the comment": stuck.Comment.Body, "the draft's record": savedRecords(t, r.origin)} {
		mustSay(t, what, text, "TS2307", "future.test.ts:9:3 TS2322", "issue-1/types.ts", "types.d.ts", "TS5109: [no source location] END_OF_DIAGNOSTICS788")
	}
	nothingLocal(t, r, root)
}
