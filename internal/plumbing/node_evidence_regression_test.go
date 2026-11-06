package plumbing

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"
)

var nodeEvidenceKey = bytes.Repeat([]byte{37}, 32)

func nodeEvidenceFixture(t *testing.T, framework, outcome string) (RepositoryAcceptanceInvocation, []nodeEvent, RepositoryProcessOutput) {
	t.Helper()
	in := RepositoryAcceptanceInvocation{Binding: RepositoryAcceptanceBinding{InvocationID: "fresh", Part: "web", Framework: framework,
		File: "web/ready.test.ts", Selector: "outer ready", SourceDigest: dependencyHash([]byte("pinned")), ExecutionDigest: dependencyHash([]byte("policy"))}}
	code, result := 0, "passed"
	if outcome != "passed" {
		code, result = 1, "failed"
	}
	events := []nodeEvent{
		{Actor: "parent", Event: "begin"},
		{Actor: "worker", Event: "collection", IDs: []string{in.Binding.Selector}},
		{Actor: "worker", Event: "body_start"},
		{Actor: "worker", Event: "body_end", Outcome: outcome, Reason: "observed callback"},
		{Actor: "worker", Event: "hooks", Outcome: "passed"},
		{Actor: "worker", Event: "suite_end", Outcome: "passed"},
		{Actor: "parent", Event: "session_end", Code: code, Outcome: result},
		{Actor: "parent", Event: "finish", Code: code},
	}
	seq := map[string]int{}
	for i := range events {
		seq[events[i].Actor]++
		events[i].Binding, events[i].Seq = in.Binding, seq[events[i].Actor]
	}
	return in, events, RepositoryProcessOutput{Started: true, Exited: true, Drained: true, ExitCode: code}
}

func encodeNodeEvents(t *testing.T, events []nodeEvent) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, event := range events {
		event.MAC = ""
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if event.Actor == "parent" {
			mac := hmac.New(sha256.New, nodeEvidenceKey)
			_, _ = mac.Write(data)
			data = append(data[:len(data)-1], []byte(`,"MAC":"`+hex.EncodeToString(mac.Sum(nil))+`"}`)...)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func TestNodeCollectorRequiresActualLifecycle(t *testing.T) {
	for _, framework := range []string{"vitest", "jest"} {
		for _, outcome := range []string{"passed", "assertion", "proposed_api"} {
			in, events, out := nodeEvidenceFixture(t, framework, outcome)
			out.Events = encodeNodeEvents(t, events)
			evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey)
			if err != nil || !evidence.Complete {
				t.Fatalf("complete %s %s: %v %+v", framework, outcome, err, evidence)
			}
			for _, mode := range []string{"no call", "no hooks", "no suite", "no session", "early finish", "wrong observer", "duplicate", "replay", "wrong source", "wrong execution", "ambiguous", "hook failure", "exception", "skip", "exit", "drain", "console"} {
				t.Run(framework+"/"+outcome+"/"+mode, func(t *testing.T) {
					changed, output := slices.Clone(events), out
					switch mode {
					case "no call":
						changed = append(changed[:2], changed[3:]...)
					case "no hooks":
						changed = append(changed[:4], changed[5:]...)
					case "no suite":
						changed = append(changed[:5], changed[6:]...)
					case "no session":
						changed = append(changed[:6], changed[7:]...)
					case "early finish":
						changed[4], changed[7] = changed[7], changed[4]
					case "wrong observer":
						changed[3].Actor = "parent"
					case "duplicate":
						changed = append(changed, changed...)
					case "replay":
						changed[0].Binding.InvocationID = "old"
					case "wrong source":
						changed[3].Binding.SourceDigest = dependencyHash([]byte("other"))
					case "wrong execution":
						changed[3].Binding.ExecutionDigest = dependencyHash([]byte("other"))
					case "ambiguous":
						changed[1].IDs = []string{in.Binding.Selector, in.Binding.Selector}
					case "hook failure":
						changed[4].Outcome = "failed"
					case "exception":
						changed[3].Outcome = "error"
					case "skip":
						changed[3].Outcome = "skipped"
					case "exit":
						output.ExitCode = 19
					case "drain":
						output.Drained = false
					}
					output.Events = encodeNodeEvents(t, changed)
					if mode == "console" {
						output.Stdout, output.Events = output.Events, nil
					}
					if evidence, err := collectNodeAcceptance(in, output, nodeEvidenceKey); err == nil || evidence.Complete {
						t.Fatalf("accepted %s: %v %+v", mode, err, evidence)
					}
				})
			}
		}
	}
}

func TestNodeCollectorCompileCannotBecomeRuntimePass(t *testing.T) {
	in, _, out := nodeEvidenceFixture(t, "jest", "assertion")
	events := []nodeEvent{
		{Binding: in.Binding, Actor: "parent", Seq: 1, Event: "begin"},
		{Binding: in.Binding, Actor: "parent", Seq: 2, Event: "compile", IDs: []string{in.Binding.Selector}, Outcome: "attributed", Reason: "structured missing export"},
		{Binding: in.Binding, Actor: "parent", Seq: 3, Event: "session_end", Code: 1, Outcome: "failed"},
		{Binding: in.Binding, Actor: "parent", Seq: 4, Event: "finish", Code: 1},
	}
	out.Events = encodeNodeEvents(t, events)
	evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey)
	if err != nil || len(evidence.Records) != 2 || evidence.Records[1].Kind != "compile" || evidence.Records[1].Status != "failed" {
		t.Fatalf("lost attributed compilation: %v %+v", err, evidence)
	}
	for _, replacement := range []string{`"Seq":1,"Seq":1`, `"seq":1`} {
		changed := out
		changed.Events = bytes.Replace(out.Events, []byte(`"Seq":1`), []byte(replacement), 1)
		if evidence, err := collectNodeAcceptance(in, changed, nodeEvidenceKey); err == nil || evidence.Complete {
			t.Fatal("ambiguous JSON supplied evidence")
		}
	}
	events[2].Outcome = "passed"
	conflict := out
	conflict.Events = encodeNodeEvents(t, events)
	if evidence, err := collectNodeAcceptance(in, conflict, nodeEvidenceKey); err == nil || evidence.Complete {
		t.Fatal("conflicting compile/session outcome supplied evidence")
	}
	out.ExitCode = 0
	if evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey); err == nil || evidence.Complete {
		t.Fatal("compiler success substituted for a runtime pass")
	}
}
