package dashboard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A Codex run shows in the live view as a Claude Code run does (#153). The
// watcher's status names the step's agent, and the view reads the
// transcript as Codex's backend writes it: its steps, each tool call in
// time by kind, a shell command as other and a file change as an edit, the
// files it changed inside its copy, its reasoning as stretches of thinking
// with their estimated tokens, its output tokens and how its run ended. The
// page names the agent. Nothing private reaches it: no command or its
// output, none of the agent's words or reasoning, no path outside its copy.
func TestTheLiveViewReadsACodexRun(t *testing.T) {
	f := newLiveStepFixture(t)
	st := Status{PID: os.Getpid(), Repo: "gitdek/app", Started: f.since, Heartbeat: f.since, Every: 30, Limit: 9000,
		Steps: []Step{{Issue: 7, Doing: "building", Since: f.since, Agent: "codex"}}}
	if err := WriteStatus(f.status, st); err != nil {
		t.Fatal(err)
	}
	at := func(s int) time.Time { return f.since.Add(time.Duration(s) * time.Second) }
	stamp := func(s int) string { return at(s).Format(time.RFC3339Nano) }
	reasoning := `"SECRETREASONING ` + strings.Repeat("x", 380) + `"`
	lines := []string{
		`{"timestamp":"` + stamp(0) + `","type":"invariant.workspace","cwd":"` + f.ws + `"}`,
		`{"timestamp":"` + stamp(1) + `","type":"thread.started","thread_id":"t1"}`,
		`{"timestamp":"` + stamp(1) + `","type":"turn.started"}`,
		`{"timestamp":"` + stamp(9) + `","type":"item.completed","item":{"id":"item_0","type":"reasoning","text":` + reasoning + `}}`,
		`{"timestamp":"` + stamp(10) + `","type":"item.started","item":{"id":"item_1","type":"command_execution","command":"cat SECRETCOMMAND","status":"in_progress"}}`,
		`{"timestamp":"` + stamp(12) + `","type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"cat SECRETCOMMAND","aggregated_output":"SECRETOUTPUT","exit_code":0,"status":"completed"}}`,
		`{"timestamp":"` + stamp(20) + `","type":"item.completed","item":{"id":"item_2","type":"file_change","changes":[{"path":"` + filepath.Join(f.ws, "internal", "app", "app.go") + `","kind":"update"},{"path":"/etc/SECRETPATH","kind":"update"}],"status":"completed"}}`,
		`{"timestamp":"` + stamp(30) + `","type":"item.started","item":{"id":"item_3","type":"mcp_tool_call","server":"invariant","tool":"gate","arguments":{},"status":"in_progress"}}`,
		`{"timestamp":"` + stamp(90) + `","type":"item.completed","item":{"id":"item_3","type":"mcp_tool_call","server":"invariant","tool":"gate","arguments":{},"result":{"content":[{"type":"text","text":"SECRETGATE"}]},"status":"completed"}}`,
		`{"timestamp":"` + stamp(95) + `","type":"item.started","item":{"id":"item_4","type":"mcp_tool_call","server":"invariant","tool":"gate","arguments":{},"status":"in_progress"}}`,
	}
	writeLines := func() {
		t.Helper()
		if err := os.WriteFile(f.transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLines()
	l := f.one(t)
	if l.By != "codex" || l.Turns != 3 || l.Now != "gate" || l.Agent != "" {
		t.Errorf("by %q, %d turns, now %q, agent %q; want codex, 3 steps, the gate it's running, and still running", l.By, l.Turns, l.Now, l.Agent)
	}
	if want := map[string]int{"other": 1, "edit": 1, "gate": 2}; !reflect.DeepEqual(l.Tools, want) {
		t.Errorf("tool calls %v, want %v", l.Tools, want)
	}
	var kinds []string
	for _, m := range l.Marks {
		kinds = append(kinds, m.Kind)
	}
	if want := []string{"think", "other", "edit", "gate", "gate"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("marks %v, want %v", kinds, want)
	}
	think, shell, edit, gate, going := l.Marks[0], l.Marks[1], l.Marks[2], l.Marks[3], l.Marks[4]
	if !think.At.Equal(at(1)) || think.Until == nil || !think.Until.Equal(at(9)) || think.Tokens != len(reasoning)/4 || l.Thinking != think.Tokens {
		t.Errorf("the reasoning %+v, %d thinking tokens: want from 1s to 9s, %d estimated tokens", think, l.Thinking, len(reasoning)/4)
	}
	if !shell.At.Equal(at(10)) || shell.Until == nil || !shell.Until.Equal(at(12)) {
		t.Errorf("the shell command %+v: want from 10s to 12s", shell)
	}
	if !edit.At.Equal(at(20)) || edit.Until == nil {
		t.Errorf("the file change %+v: want at 20s, and ended", edit)
	}
	if !gate.At.Equal(at(30)) || gate.Until == nil || !gate.Until.Equal(at(90)) || !going.At.Equal(at(95)) || going.Until != nil {
		t.Errorf("the gate calls %+v and %+v: want 30s to 90s, and one going since 95s", gate, going)
	}
	if len(l.Files) != 1 || l.Files[0].Path != "internal/app/app.go" || l.Files[0].Edits != 1 || l.Files[0].Reads != 0 {
		t.Errorf("files %+v, want the one changed inside the copy", l.Files)
	}

	lines = append(lines, `{"timestamp":"`+stamp(120)+`","type":"turn.completed","usage":{"input_tokens":9000,"cached_input_tokens":5000,"cache_write_input_tokens":0,"output_tokens":777,"reasoning_output_tokens":300}}`)
	writeLines()
	steps, out := f.read(t)
	if len(steps) != 1 || steps[0].Agent != "finished" || steps[0].Output != 777 || steps[0].Now != "" {
		t.Fatalf("after the run ended: %+v; want it finished, with 777 output tokens", steps)
	}
	for _, secret := range []string{"SECRET", "cat ", f.ws, f.work} {
		if strings.Contains(out, secret) {
			t.Errorf("the live view's JSON holds %q", secret)
		}
	}

	// A step whose agent the watcher hasn't named yet, as before its run
	// starts, is still read as Codex writes it, from its first line.
	g := newLiveStepFixture(t)
	g.setDoing(t, "building")
	if err := os.WriteFile(g.transcript, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if early := g.one(t); early.Turns != 0 {
		t.Fatalf("an empty transcript has %d turns", early.Turns)
	}
	lines[0] = strings.Replace(lines[0], f.ws, g.ws, 1)
	if err := os.WriteFile(g.transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if later := g.one(t); later.Turns != 3 || later.Agent != "finished" || later.Tools["gate"] != 2 {
		t.Errorf("read before its agent was named: %d turns, agent %q, tools %v; want Codex's run", later.Turns, later.Agent, later.Tools)
	}
}
