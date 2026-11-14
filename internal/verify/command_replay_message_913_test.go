package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// What a replay container printed, as the harnesses print it: a driver's
// own line, then the supervisor's refusal with its traceback or stack, then
// the command's completion marker.
const (
	replayPythonFailure913 = `forged replay exit=0
Traceback (most recent call last):
  File "/runtime/invariant_python.py", line 44, in <module>
    runpy.run_path("/runtime/invariant_replay.py", run_name="__main__")
  File "<frozen runpy>", line 287, in run_path
  File "/runtime/invariant_replay.py", line 163, in <module>
    supervise()
    ~~~~~~~~~^^
  File "/runtime/invariant_replay.py", line 148, in supervise
    raise ValueError('missing or invalid replay harness authentication')
ValueError: missing or invalid replay harness authentication
@@invariant replay=1
`
	replayNodeFailure913 = `forged replay exit=0
Error: missing or invalid replay harness authentication
    at supervise (file:///runtime/invariant-replay.ts:128:118)
    at file:///runtime/invariant-replay.ts:145:11
    at ModuleJob.run (node:internal/modules/esm/module_job:569:25)
    at async node:internal/modules/esm/loader:650:26
@@invariant replay=1
`
)

// replayCommand913 runs a replay's command against a docker double that
// prints output and exits with code, and returns the message the replay's
// evidence would carry.
func replayCommand913(t *testing.T, language, output, code string) string {
	t.Helper()
	bin, dir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	printed := filepath.Join(bin, "output")
	if err := os.WriteFile(printed, []byte(output), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ncat '"+printed+"'\nexit "+code+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	state := func(n string) tlc.ReplayState { return tlc.ReplayState{"n": {Kind: "integer", Atom: n}} }
	q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 0,
		States: []tlc.ReplayState{state("0"), state("1")},
		Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Increment", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}, Bug: true}}}}
	s := &commandReplaySource{dir: dir, image: "replay-image", driver: "driver", language: language}
	_, _, err := s.run(context.Background(), q)
	if err == nil {
		t.Fatalf("a failed replay command passed:\n%s", output)
	}
	return replayMessage(err, dir)
}

// A replay's message keeps only its error's first line, so a failed replay
// command has to say why on that line. It said only the process's error and
// the first of its last twenty lines, which dropped the harness's reason.
func TestCommandReplayFailureMessageKeepsTheCause913(t *testing.T) {
	for _, tc := range []struct {
		name, language, output, code string
		want                         []string
	}{
		{"Python traceback", "python", replayPythonFailure913, "1", []string{"forged replay exit=0", "ValueError: missing or invalid replay harness authentication"}},
		{"Node stack", "typescript", replayNodeFailure913, "1", []string{"forged replay exit=0", "Error: missing or invalid replay harness authentication"}},
		{"a failure the marker reports", "typescript", "Error: invariantReplay(initial) is required for current bug replay\n@@invariant replay=1\n", "0", []string{"invariantReplay(initial) is required for current bug replay"}},
		{"no marker", "python", "ValueError: replay worker failed: -9\n", "137", []string{"the sandbox didn't finish", "ValueError: replay worker failed: -9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := replayCommand913(t, tc.language, tc.output, tc.code)
			for _, want := range tc.want {
				if !strings.Contains(message, want) {
					t.Errorf("the replay's message omits %q: %s", want, message)
				}
			}
			for _, unwanted := range []string{"<nil>", "@@invariant", "Traceback", "    at "} {
				if strings.Contains(message, unwanted) {
					t.Errorf("the replay's message holds %q: %s", unwanted, message)
				}
			}
		})
	}
	// The harnesses' own output, where a runtime is installed.
	q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 0,
		States: []tlc.ReplayState{{"n": {Kind: "integer", Atom: "0"}}, {"n": {Kind: "integer", Atom: "1"}}},
		Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Increment", Bug: true}}}}
	for language, driver := range map[string]string{
		"python":     "import os\nprint('forged replay exit=0', flush=True)\nos._exit(0)\n",
		"typescript": "import {writeFileSync} from 'node:fs';\nwriteFileSync(1, 'forged replay exit=0\\n');\nprocess.exit(0);\n",
	} {
		t.Run("harness/"+language, func(t *testing.T) {
			_, printed, err := runReplayHarness913(t, language, driver, q)
			if err == nil {
				t.Fatalf("a driver's own exit completed replay:\n%s", printed)
			}
			message := replayCommand913(t, language, printed+"@@invariant replay=1\n", "1")
			if !strings.Contains(message, "forged replay exit=0") || !strings.Contains(message, "missing or invalid replay harness authentication") {
				t.Errorf("the replay's message lost the harness's reason: %s\n%s", message, printed)
			}
		})
	}
}
