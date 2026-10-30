package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/synth"
)

// RecordActivity keeps a run's public activity before its caller removes the
// agent's workspace. The backend's result, error and panic are unchanged. The
// factory records the step's end separately, after checking and publishing.
func RecordActivity(backend synth.Backend, logf func(string, ...any)) synth.Backend {
	return activityBackend{Backend: backend, logf: logf}
}

type activityBackend struct {
	synth.Backend
	logf func(string, ...any)
}

func (b activityBackend) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	// Every watcher draft and build writes directly to its named primary
	// transcript. Reviews must never replace that run's activity.
	if out, ok := job.Transcript.(*os.File); ok && !job.ReadOnly && filepath.Base(out.Name()) == "transcript.jsonl" {
		defer func() {
			if err := saveFinishedActivity(out.Name(), b.Name()); err != nil && b.logf != nil {
				b.logf("activity: %v", err)
			}
		}()
	}
	return b.Backend.Run(ctx, job)
}

func saveFinishedActivity(path, agent string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	t := newTranscript()
	t.codex = agent == "codex"
	// Rebuild from all complete events on every return, including a fallback.
	// Replacing the snapshot avoids adding the first attempt twice. Starting
	// another agent session resets its IDs, which a fallback may reuse.
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 64<<10), maxRead)
	lines.Split(func(data []byte, _ bool) (int, []byte, error) {
		if end := bytes.IndexByte(data, '\n'); end >= 0 {
			return end + 1, data[:end], nil
		}
		return 0, nil, nil // a partial event does not count
	})
	turns := 0
	for lines.Scan() {
		line := lines.Bytes()
		var start struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(line, &start) == nil && (start.Type == "invariant.workspace" || start.Type == "system" && start.Subtype == "init") {
			turns += len(t.ids)
			t.ids, t.calls, t.items = map[string]bool{}, map[string]bool{}, map[string]int{}
			t.open = nil
			t.endThinking()
			t.agent, t.now = "", ""
			t.codex = start.Type == "invariant.workspace"
		}
		t.line(line)
	}
	if err := lines.Err(); err != nil {
		return err
	}
	v := t.view()
	v.Turns += turns
	if info, err := f.Stat(); err == nil {
		last := info.ModTime().UTC()
		v.Last = &last
	}
	data, err := json.Marshal(publicActivity(v))
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".activity-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "activity.json"))
}

// readFinishedActivity uses only the public projection. Its paths passed the
// transcript reader's checks while the workspace existed; checking existence
// again would discard them after cleanup. Identity, checks and completion time
// always come from the watcher's named step, never from this snapshot.
func readFinishedActivity(dir string) (Live, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "activity.json"))
	if err != nil {
		return Live{}, false
	}
	var v Live
	if json.Unmarshal(data, &v) != nil {
		return Live{}, false
	}
	return publicActivity(v), true
}

// publicActivity deliberately copies only activity's public fields. Shell
// commands, workspace paths and any step metadata cannot enter the snapshot.
func publicActivity(v Live) Live {
	public := Live{Turns: v.Turns, Thinking: v.Thinking, ThinkingExact: v.ThinkingExact,
		Context: v.Context, Output: v.Output, Last: v.Last,
		Tools: map[string]int{}, Marks: []Mark{}, Files: []LiveFile{}}
	for kind, n := range v.Tools {
		if activityKind(kind) {
			public.Tools[kind] = n
		}
	}
	for _, mark := range v.Marks {
		if activityKind(mark.Kind) {
			public.Marks = append(public.Marks, mark)
		}
	}
	for _, file := range v.Files {
		if filepath.IsLocal(file.Path) && filepath.ToSlash(filepath.Clean(file.Path)) == file.Path {
			public.Files = append(public.Files, file)
		}
	}
	if activityKind(v.Now) || v.Now == "thinking" {
		public.Now = v.Now
	}
	switch v.Agent {
	case "finished", "stopped", "stopped at its spend cap", "stopped at its turn cap":
		public.Agent = v.Agent
	}
	return public
}

func activityKind(kind string) bool {
	switch kind {
	case "read", "search", "list", "build", "test", "git", "edit", "gate", "check", "other", "think":
		return true
	}
	return false
}
