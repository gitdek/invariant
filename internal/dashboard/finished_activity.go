package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/gitdek/invariant/internal/synth"
)

const (
	// maxEvent is the longest transcript line a run's record reads: the
	// longest event either agent's reader takes in, 64 MiB, with room for
	// the time Codex's backend stamps on it. A longer line is no event an
	// agent's reader wrote.
	maxEvent = 64<<20 + 1<<10
	// maxActivity is the most of a saved activity the dashboard reads.
	maxActivity = 16 << 20
)

// RecordActivity has backend's runs keep their agent's public activity in
// the run's directory, in activity.json beside its transcript, before the
// caller cleans up the agent's workspace (#269). The record reads each whole
// event as the backend writes it, with the live view's checks, while the
// workspace still holds what the event names, and saves what it has read
// each time a run returns, whether it succeeded, failed, was called off or
// panicked. A fallback, or a plumbing build's repair, runs into the same
// transcript, so its events add to what the runs before it saved, and a
// workspace that has changed since can't take back what they showed. Only a
// run that writes to a transcript.jsonl is recorded: a review, which only
// reads, writes a transcript of its own and never touches the build's. The
// backend's name, result, error and panic stay its own, and when the step
// ended is the watcher's to record, not this.
func RecordActivity(backend synth.Backend, logf func(string, ...any)) synth.Backend {
	return &activityBackend{Backend: backend, logf: logf, runs: map[*os.File]*activityRun{}}
}

type activityBackend struct {
	synth.Backend
	logf func(string, ...any)
	mu   sync.Mutex
	runs map[*os.File]*activityRun // by the open transcript the runs write
}

func (b *activityBackend) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if run := b.recording(job); run != nil {
		job.Transcript = activityWriter{job.Transcript, run}
		defer run.save(b.logf)
	}
	// RunAgent, with no fallback of its own, runs the backend as its caller
	// would: a build's shared time limit still stops its clock while Codex
	// waits for its account's turn (D-0154).
	usage, _, err := synth.RunAgent(ctx, b.Backend, job, job.Timeout, "")
	return usage, err
}

// recording is the record of the run job writes, or nil for a run that isn't
// recorded. The records of transcripts their callers have closed are let go,
// since no run writes them again.
func (b *activityBackend) recording(job synth.Job) *activityRun {
	b.mu.Lock()
	defer b.mu.Unlock()
	for f := range b.runs {
		if _, err := f.Stat(); errors.Is(err, os.ErrClosed) {
			delete(b.runs, f)
		}
	}
	out, ok := job.Transcript.(*os.File)
	if !ok || job.ReadOnly || filepath.Base(out.Name()) != "transcript.jsonl" {
		return nil
	}
	run := b.runs[out]
	if run == nil {
		t := newTranscript()
		t.codex = b.Name() == "codex"
		run = &activityRun{path: out.Name(), t: t}
		b.runs[out] = run
	}
	return run
}

// activityWriter passes what a backend writes on to its transcript, and lets
// the run's record read what was written.
type activityWriter struct {
	out io.Writer
	run *activityRun
}

func (w activityWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if n > 0 && n <= len(p) {
		w.run.take(p[:n])
	}
	return n, err
}

// activityRun is what the runs into one transcript have shown.
type activityRun struct {
	mu      sync.Mutex
	path    string      // the transcript
	t       *transcript // its whole lines, read as they were written
	partial []byte      // the start of a line still being written
	long    bool        // the line being written is longer than any event
	started bool        // its first line has been read
	broken  bool        // a line couldn't be read, so nothing more is
}

// take reads each line p completes, as the live view reads them, and keeps
// the start of one it doesn't complete for the next write.
func (r *activityRun) take(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.broken {
		return
	}
	defer func() {
		// The record is the dashboard's, and must never cost the agent its
		// run, so a line it can't read stops only the record.
		if recover() != nil {
			r.broken, r.partial = true, nil
		}
	}()
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		if end < 0 {
			if !r.long {
				r.partial = append(r.partial, p...)
				if len(r.partial) > maxEvent {
					r.partial, r.long = nil, true
				}
			}
			return
		}
		if !r.long {
			line := p[:end]
			if len(r.partial) > 0 {
				line = append(r.partial, line...)
			}
			if len(line) <= maxEvent {
				r.line(line)
			}
		}
		r.partial, r.long = nil, false
		p = p[end+1:]
	}
}

// line reads one whole line. Codex's backend starts its transcript by
// naming the workspace, so a record made for another agent's name still
// reads it as Codex writes it.
func (r *activityRun) line(b []byte) {
	if !r.started {
		r.started = true
		if codexStart(b) {
			r.t.codex = true
		}
	}
	r.t.line(b)
}

// save replaces the run's activity.json with what its record has shown,
// in one step, so a reader never sees half of it. When it can't, what it
// saved before goes too, since that misses this run, and the run's card
// reads its transcript instead.
func (r *activityRun) save(logf func(string, ...any)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := filepath.Join(filepath.Dir(r.path), "activity.json")
	if err := r.write(path); err != nil {
		if gone := os.Remove(path); gone != nil && !errors.Is(gone, os.ErrNotExist) {
			err = errors.Join(err, gone)
		}
		if logf != nil {
			logf("activity: %v", err)
		}
	}
}

func (r *activityRun) write(path string) (err error) {
	// A run that panicked is still on its way out, so saving must not panic
	// in its place.
	defer func() {
		if p := recover(); p != nil {
			err = errors.Join(err, fmt.Errorf("the record of the agent's activity couldn't be saved: %v", p))
		}
	}()
	if r.broken {
		return fmt.Errorf("a line of %s couldn't be read, so its card reads the transcript", r.path)
	}
	v := r.t.view()
	if info, err := os.Stat(r.path); err == nil {
		last := info.ModTime().UTC()
		v.Last = &last
	}
	data, err := json.Marshal(publicActivity(v))
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".activity-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// readFinishedActivity is the activity a record saved for the run in dir,
// with only its public fields. Its paths passed the live view's checks while
// the workspace was there, and checking them again would lose them once it's
// gone. The step's identity, checks and end always come from its watcher.
func readFinishedActivity(dir string) (Live, bool) {
	if dir == "" {
		return Live{}, false
	}
	f, err := os.Open(filepath.Join(dir, "activity.json"))
	if err != nil {
		return Live{}, false
	}
	defer f.Close()
	var v Live
	if err := json.NewDecoder(io.LimitReader(f, maxActivity)).Decode(&v); err != nil {
		return Live{}, false
	}
	return publicActivity(v), true
}

// publicActivity copies only an activity's public fields: counts, times,
// kinds of tool and paths inside the agent's copy (D-0051, D-0116). No
// command, no path outside the copy and nothing of the step's identity can
// enter a saved activity or come out of one.
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

// activityKind says whether kind is one the live view sorts tool calls and
// thinking into.
func activityKind(kind string) bool {
	switch kind {
	case "read", "search", "list", "build", "test", "git", "edit", "gate", "check", "other", "think":
		return true
	}
	return false
}
