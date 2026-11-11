package plumbing

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

// The controller's diagnostic events carry display text, not test evidence.
// Reason chunks concatenate verbatim before its authenticated refused event.
// Use the existing trusted environment fixture to exercise collection and
// public planning feedback without requiring a locally installed Node runtime.
func TestNodeDiagnosticRefusalsReachPlanning788(t *testing.T) {
	const refusal = "selected source has unattributed transform, import or type diagnostics"
	diagnostics := "future.test.ts:2:24 TS2307: Cannot find module './hooks/use-statue-task' or its corresponding type declarations.\n" +
		"future.test.ts:8:7 TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"product.ts:12:3 TS2345: Argument is not assignable to the requested type.\n  Types of property 'value' are incompatible. " + strings.Repeat("Complete nested explanation. ", 50) + "END_OF_CHAIN_788\n" +
		"TS5109: [no source location] Option 'moduleResolution' must match 'module'.\n"
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			part := nodePart633(framework, "web", "node@"+hash468("788"))
			root, _, snapshot := fixture484(t, []repository.Part{part}, nil)
			plan := n503Plan(t, framework, "web", "web/future.test.ts", "loads future module",
				nodeImports633(framework)+"import {future} from './hooks/use-statue-task';\ntest('loads future module',()=>expect(future()).toBe(7));\n")
			plan.Files = []string{"web/hooks/use-statue-task.ts"}
			plan = n503Read(t, plan, snapshot)
			runner := &runner515{runner484: newRunner484(t)}
			sandbox := sandbox484(t, root, snapshot, runner.runner484)
			sandbox.Repository.Dependencies.Runner = runner
			runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
				events := diagnosticEvents788(in.Binding, diagnostics, refusal)
				for _, event := range events {
					encoded := encodeNodeEvents(t, []nodeEvent{event})
					if len(encoded) > 4096 {
						t.Fatal("fixture exceeded the existing atomic event limit")
					}
				}
				out := RepositoryProcessOutput{Started: true, Exited: true, Drained: true, ExitCode: 1,
					Events: encodeNodeEvents(t, events), Stderr: []byte(diagnostics)}
				evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey)
				if err == nil || evidence.Complete {
					t.Fatal("diagnostics became a qualifying completed failure")
				}
				return evidence, out.Stderr, err
			}
			checked, err := Check(t.Context(), n503Draft(t, plan), root, sandbox)
			if err != nil || checked == nil || checked.Problem == "" || len(runner.invocations) != 1 {
				t.Fatalf("expected one explained precheck refusal: %v %+v", err, checked)
			}
			record := result515(t, checked.Run, plan.Tests[0])
			if record.Status != "refused" || checked.Run.Passed() || checked.Run.Accepted[plan.Tests[0].Identity()] {
				t.Fatalf("precheck refusal became acceptance evidence: %+v", record)
			}
			encoded, err := json.Marshal(checked)
			if err != nil {
				t.Fatal(err)
			}
			var restored Checked
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			for name, text := range map[string]string{
				"check problem":    checked.Problem,
				"test reason":      record.Reason,
				"planner feedback": checked.Feedback(),
				"run output":       checked.Run.Output,
				"saved feedback":   restored.Feedback(),
			} {
				if !strings.Contains(text, diagnostics) {
					t.Errorf("%s lost or clipped diagnostic details:\n%s", name, text)
				}
			}
			closed484(t, runner.runner484)

			// Additional reporting must not widen evidence admission. A normal
			// assertion and the existing attributed-export case still qualify;
			// diagnostic text on its own cannot qualify either outcome.
			for _, outcome := range []string{"assertion", "proposed_api", "passed"} {
				in, events, out := nodeEvidenceFixture(t, framework, outcome)
				out.Events = encodeNodeEvents(t, events)
				if evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey); err != nil || !evidence.Complete {
					t.Fatalf("existing %s lifecycle changed: %v %+v", outcome, err, evidence)
				}
				changed := append([]nodeEvent{events[0], {Binding: in.Binding, Actor: "parent", Event: "diagnostic", Reason: diagnostics}}, events[1:]...)
				sequence := map[string]int{}
				for i := range changed {
					sequence[changed[i].Actor]++
					changed[i].Seq = sequence[changed[i].Actor]
				}
				out.Events = encodeNodeEvents(t, changed)
				if evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey); err == nil || evidence.Complete {
					t.Fatalf("diagnostic events supplied %s evidence", outcome)
				}
			}
			in, _, out := nodeEvidenceFixture(t, framework, "assertion")
			events := diagnosticEvents788(in.Binding, diagnostics, refusal)
			for _, mode := range []string{"worker", "wrong binding", "replay", "no refusal", "console only"} {
				bad, output := slices.Clone(events), out
				switch mode {
				case "worker":
					bad[1].Actor, bad[1].Seq = "worker", 1
				case "wrong binding":
					bad[1].Binding.InvocationID = "some-other-invocation"
				case "replay":
					bad[1].Seq = 1
				case "no refusal":
					bad = bad[:len(bad)-1]
				}
				output.Events = encodeNodeEvents(t, bad)
				if mode == "console only" {
					output.Stdout, output.Events = output.Events, nil
				}
				if evidence, err := collectNodeAcceptance(in, output, nodeEvidenceKey); err == nil || evidence.Complete {
					t.Fatalf("%s diagnostics qualified as acceptance", mode)
				}
			}
		})
	}
}

func diagnosticEvents788(binding RepositoryAcceptanceBinding, text, refusal string) []nodeEvent {
	events := []nodeEvent{{Binding: binding, Actor: "parent", Seq: 1, Event: "begin"}}
	runes := []rune(text)
	for len(runes) > 0 {
		n := min(256, len(runes))
		events = append(events, nodeEvent{Binding: binding, Actor: "parent", Seq: len(events) + 1,
			Event: "diagnostic", Reason: string(runes[:n])})
		runes = runes[n:]
	}
	return append(events, nodeEvent{Binding: binding, Actor: "parent", Seq: len(events) + 1,
		Event: "refused", Reason: refusal})
}
