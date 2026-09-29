package synth

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// replyPhrases is what a prompt tells an agent that writes files about its
// replies (#120): a reply holds about 128,000 tokens, thinking included, and
// one that runs out of room loses what it didn't write, so the agent writes
// each file as soon as it's decided, which costs none of its checks.
var replyPhrases = []string{
	"128,000 tokens of output, thinking included",
	"reply that runs out of room loses what it didn't write",
	"each file as soon as you've decided what it holds",
	"files early costs none of your",
}

// At max effort, #91's build agent spent whole replies thinking, and lost
// each one at the output limit before it wrote a file. Every synthesis
// prompt says how replies work, and still gives the agent its gate runs. New
// code starts from the model.
func TestSynthesisTellsTheAgentToWriteAsItGoes(t *testing.T) {
	lock := project.Lock{Bounds: map[string]string{"Cap": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "s"}}}
	for _, m := range []project.Manifest{
		{Module: ".invariant/specs/Buf.tla", Code: "buffer", Language: "go"},
		{Module: ".invariant/specs/Buf.tla", Code: "src", Language: "typescript", Conformance: "conformance.ts", Exhaustive: true},
		{Module: ".invariant/specs/Buf.tla", Code: "buffer", Language: "python", Conformance: "conformance.py", Exhaustive: true},
		{Module: ".invariant/specs/Leases.tla", Code: ".", Language: "typescript", Conformance: "conformance.ts", Existing: []string{"src/lib"}},
	} {
		name, want := m.Language, append([]string{"You have 4 gate runs"}, replyPhrases...)
		if len(m.Existing) > 0 {
			name = "existing-code"
		} else {
			want = append(want, "the model first")
		}
		p := &project.Project{Dir: t.TempDir(), Manifest: m, Lock: lock}
		got := Prompt(p, "---- MODULE Buf ----\n====\n", "# Add a buffer", 4, true, false)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("the %s prompt lacks %q", name, w)
			}
		}
	}
}
