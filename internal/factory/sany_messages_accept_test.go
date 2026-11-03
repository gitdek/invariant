package factory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// The formalizer's check stages a real draft and runs the model gate. Only
// Docker is replaced: its SANY-formatted output must travel through the TLC
// parser into the feedback, receipt, and factory posts that people see.
func TestSANYErrorsReachCheckReceiptAndFactoryPosts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Docker stand-in is a shell script")
	}
	const location = "line 7, col 14 to line 7, col 28 of module SanyConsumer"
	const diagnostic = "Unknown operator: `MissingOperator'."
	const closing = "Parsing or semantic analysis failed."
	const output = `@!@!@STARTMSG 2262:0 @!@!@
TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)
@!@!@ENDMSG 2262 @!@!@
@!@!@STARTMSG 2220:0 @!@!@
Starting SANY...
@!@!@ENDMSG 2220 @!@!@
Parsing file /work/SanyConsumer.tla
Semantic processing of module Naturals
Semantic processing of module SanyConsumer
Semantic errors:

*** Errors: 1

line 7, col 14 to line 7, col 28 of module SanyConsumer

Unknown operator: ` + "`MissingOperator'." + `

@!@!@STARTMSG 2219:0 @!@!@
SANY finished.
@!@!@ENDMSG 2219 @!@!@
@!@!@STARTMSG 3002:1 @!@!@
Parsing or semantic analysis failed.
@!@!@ENDMSG 3002 @!@!@
`
	const model = `---- MODULE SanyConsumer ----
EXTENDS Naturals
CONSTANT Max
VARIABLE x
vars == <<x>>
Init == x = 0
Next == x' = MissingOperator(x)
Safe == x <= Max
Spec == Init /\ [][Next]_vars
====
`
	const proposalJSON = `{
  "name": "SANY diagnostic propagation",
  "slug": "sany-diagnostic-propagation",
  "module": "SanyConsumer",
  "package": "counter",
  "bounds": {"Max": "2"},
  "statements": [
    {"name": "Spec", "kind": "spec", "says": "Every step is allowed by Next."},
    {"name": "Safe", "kind": "invariant", "says": "The counter does not exceed Max."}
  ]
}`
	ws, bin := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{"SanyConsumer.tla": model, "proposal.json": proposalJSON} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\n" +
		"case \" $* \" in\n" +
		"  *' tlc2.TLC '*' SanyConsumer.tla '*) ;;\n" +
		"  *) printf '%s\\n' 'unexpected Docker invocation' >&2; exit 99 ;;\n" +
		"esac\n" +
		"cat <<'INVARIANT_SANY_DIAGNOSTIC'\n" + output +
		"INVARIANT_SANY_DIAGNOSTIC\nexit 150\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	proposal, report, err := formalize.Check(ctx, ws, toolchain.Toolchain{
		JavaImage: "fixture-java", TLCJar: filepath.Join(ws, "tla2tools.jar"),
	})
	if err != nil {
		t.Fatalf("the draft should reach TLC, not fail before its semantic check: %v", err)
	}
	if proposal == nil || report == nil || !report.ModelOnly || report.Passed || report.Design.Outcome != string(tlc.Failed) {
		t.Fatalf("expected a model-only TLC failure; proposal=%+v report=%+v", proposal, report)
	}
	for _, pin := range report.Pins {
		if !pin.Match || pin.Error != "" {
			t.Fatalf("the fixture must keep valid pins so TLC can report its own error: %+v", pin)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded verify.Report
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	feedback := verify.Feedback(report)
	result := &synth.Result{Final: report}
	marker := Marker{Kind: KindFailed, Project: "examples/sany-diagnostic-propagation", Hash: proposal.Hash}
	for _, surface := range []struct {
		name string
		text string
	}{
		{"model check result", report.Design.Message},
		{"formalizer check feedback", feedback},
		{"JSON receipt", decoded.Design.Message},
		{"Markdown receipt", receipt.Markdown(&decoded)},
		{"failed draft post", stuckComment(feedback, Marker{Kind: KindStuck})},
		{"failed build post", buildFailedComment(nil, result, nil, marker)},
		{"pull request receipt", pullRequestBody(Thread{Issue: github.Issue{Number: 450}}, marker, result, proposal, nil)},
	} {
		t.Run(surface.name, func(t *testing.T) {
			previous := -1
			for _, want := range []string{location, diagnostic, closing} {
				at := strings.Index(surface.text, want)
				if at < 0 {
					t.Fatalf("lost SANY diagnostic %q:\n%s", want, surface.text)
				}
				if at <= previous {
					t.Fatalf("SANY's location and explanation must precede TLC's closing line:\n%s", surface.text)
				}
				previous = at
			}
		})
	}
}
