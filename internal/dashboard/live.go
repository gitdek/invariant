package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Live is a step as it runs, for the page's live view (#97): how long it
// has run against its limit, the agent's turns and tokens, what it's doing
// now, every tool call and stretch of thinking in time, the files it read
// or changed, and the checks it ran. It's read from the watcher's work
// directory, and it holds counts, times and paths inside the agent's copy
// of the repository only: never code, the agent's words or thinking, a
// command, a tool's output or the model (D-0051, D-0052).
type Live struct {
	Repo     string         `json:"repo"`
	Issue    int            `json:"issue"`
	Doing    string         `json:"doing"`
	Since    *time.Time     `json:"since,omitempty"`
	Limit    float64        `json:"limit,omitempty"` // seconds the agent's run may take, when the watcher says
	Turns    int            `json:"turns"`
	Thinking int            `json:"thinking"`         // estimated thinking tokens so far
	Context  int            `json:"context"`          // tokens the agent holds now
	Output   int            `json:"output,omitempty"` // output tokens, once the agent's run has ended
	Tools    map[string]int `json:"tools"`            // tool calls by kind
	Now      string         `json:"now,omitempty"`    // thinking, or the kind of tool it's running
	Last     *time.Time     `json:"last,omitempty"`   // when the transcript last grew
	Marks    []Mark         `json:"marks"`
	Files    []LiveFile     `json:"files"`
	Runs     []RunMark      `json:"runs,omitempty"`
	RunsKind string         `json:"runsKind,omitempty"` // check, gate or test
	Agent    string         `json:"agent,omitempty"`    // how the agent's run ended, once it has
	Ended    *time.Time     `json:"ended,omitempty"`    // when the step ended, once it has
}

// Mark is one tool call, or one stretch of thinking between tool calls.
type Mark struct {
	At     time.Time  `json:"at"`
	Kind   string     `json:"kind"`             // read, search, edit, gate, check, test, other or think
	Until  *time.Time `json:"until,omitempty"`  // a stretch of thinking: when it ended, or nil while it goes on
	Tokens int        `json:"tokens,omitempty"` // a stretch of thinking: its estimated tokens
}

// LiveFile is a file the agent read or changed, by its path inside its copy
// of the repository.
type LiveFile struct {
	Path  string `json:"path"`
	Reads int    `json:"reads"`
	Edits int    `json:"edits"`
	Size  int64  `json:"size"`
	Lit   bool   `json:"lit,omitempty"` // the file it touched last
}

const (
	maxMarks = 2000
	maxFiles = 60
	// maxRead is the most of a transcript one read takes in, so a huge
	// backlog is read over several refreshes.
	maxRead = 32 << 20
)

// transcript is what the dashboard has read of one step's transcript. It
// reads only what's new since its last read, up to the last whole line, so
// a line still being written counts once it's whole, and nothing counts
// twice.
type transcript struct {
	offset   int64
	cwd      string // the agent's copy of the repository, from the transcript's first event
	ids      map[string]bool
	thinking int
	context  int
	output   int
	agent    string
	tools    map[string]int
	marks    []Mark
	think    int // the stretch of thinking going on, as an index into marks, or -1
	files    map[string]*touched
	touches  int
	last     time.Time // the newest time an event carried
	now      string
}

type touched struct {
	reads, edits int
	size         int64
	at           int // when it was last touched, counted in touches
}

func newTranscript() *transcript {
	return &transcript{ids: map[string]bool{}, tools: map[string]int{}, files: map[string]*touched{}, think: -1}
}

// read takes in whatever whole lines were added since the last read.
func (t *transcript) read(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < t.offset {
		*t = *newTranscript() // the file was replaced: start again
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxRead))
	if err != nil {
		return err
	}
	end := bytes.LastIndexByte(b, '\n')
	if end < 0 {
		return nil
	}
	for _, line := range bytes.Split(b[:end], []byte{'\n'}) {
		t.line(line)
	}
	t.offset += int64(end + 1)
	return nil
}

// event is the part of a transcript line the live view reads. Everything
// else, such as what the agent wrote and what its tools returned, is never
// decoded.
type event struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Cwd       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	Delta     int    `json:"estimated_tokens_delta"`
	ToolName  string `json:"tool_name"`
	IsError   bool   `json:"is_error"`
	Usage     struct {
		Output int `json:"output_tokens"`
	} `json:"usage"`
	Message struct {
		ID    string `json:"id"`
		Usage struct {
			Input         int `json:"input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is the part of a message's content the live view reads: which
// tool was called, and on which file.
type block struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
}

func (t *transcript) line(b []byte) {
	var e event
	if json.Unmarshal(b, &e) != nil {
		return // not an event: skip it
	}
	switch {
	case e.Type == "system" && e.Subtype == "init":
		if e.Cwd != "" {
			t.cwd = filepath.Clean(e.Cwd)
		}
	case e.Type == "system" && e.Subtype == "thinking_tokens":
		t.thinking += e.Delta
		if t.think < 0 {
			t.marks = append(t.marks, Mark{At: t.last, Kind: "think"})
			t.think = len(t.marks) - 1
		}
		t.marks[t.think].Tokens += e.Delta
		t.now = "thinking"
	case e.Type == "assistant":
		t.stamp(e.Timestamp)
		if e.Message.ID != "" {
			t.ids[e.Message.ID] = true
		}
		u := e.Message.Usage
		if n := u.Input + u.CacheRead + u.CacheCreation; n > 0 {
			t.context = n
		}
		var blocks []block
		if bytes.HasPrefix(bytes.TrimSpace(e.Message.Content), []byte("[")) {
			json.Unmarshal(e.Message.Content, &blocks)
		}
		for _, bl := range blocks {
			if bl.Type != "tool_use" {
				continue
			}
			kind := toolKind(bl.Name)
			t.tools[kind]++
			t.marks = append(t.marks, Mark{At: t.last, Kind: kind})
			t.now = kind
			p := bl.Input.FilePath
			if p == "" {
				p = bl.Input.NotebookPath
			}
			t.touch(p, kind)
		}
	case e.Type == "user":
		t.stamp(e.Timestamp)
		t.now = ""
	case e.Type == "tool_progress":
		t.now = toolKind(e.ToolName)
	case e.Type == "result":
		t.endThinking()
		t.output = e.Usage.Output
		t.agent = agentEnded(e.Subtype, e.IsError)
		t.now = ""
	}
	if len(t.marks) > maxMarks {
		drop := len(t.marks) - maxMarks
		t.marks = append([]Mark(nil), t.marks[drop:]...)
		if t.think >= 0 {
			t.think -= drop
		}
	}
}

// stamp moves the transcript's clock to an event's time, which ends any
// stretch of thinking going on.
func (t *transcript) stamp(ts string) {
	if at, err := time.Parse(time.RFC3339Nano, ts); err == nil && at.After(t.last) {
		t.last = at.UTC()
	}
	t.endThinking()
}

func (t *transcript) endThinking() {
	if t.think >= 0 && t.think < len(t.marks) {
		until := t.last
		t.marks[t.think].Until = &until
	}
	t.think = -1
}

// touch counts a read or an edit of a file, if it's inside the agent's
// copy. A path outside it is dropped whole, since it can name the machine's
// user.
func (t *transcript) touch(p, kind string) {
	if kind != "read" && kind != "edit" {
		return
	}
	rel, ok := inside(t.cwd, p)
	if !ok {
		return
	}
	f := t.files[rel]
	if f == nil {
		f = &touched{size: -1}
		t.files[rel] = f
	}
	if kind == "edit" {
		f.edits++
	} else {
		f.reads++
	}
	t.touches++
	f.at = t.touches
}

// inside is p's path inside dir, if it's there.
func inside(dir, p string) (string, bool) {
	if dir == "" || p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	// On macOS the temporary directory is reached through /private too.
	unprivate := func(s string) string {
		s = filepath.Clean(s)
		if strings.HasPrefix(s, "/private/") {
			return s[len("/private"):]
		}
		return s
	}
	rel, err := filepath.Rel(unprivate(dir), unprivate(p))
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// toolKind sorts a tool into what the page colors it by.
func toolKind(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		switch k := name[strings.LastIndex(name, "__")+2:]; k {
		case "gate", "check", "test":
			return k
		}
		return "other"
	}
	switch name {
	case "Read", "NotebookRead":
		return "read"
	case "Glob", "Grep", "LS":
		return "search"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "edit"
	}
	return "other"
}

// agentEnded says how an agent's run ended, from its result's subtype.
func agentEnded(subtype string, isError bool) string {
	switch {
	case subtype == "success" && !isError:
		return "finished"
	case strings.Contains(subtype, "budget"):
		return "stopped at its spend cap"
	case strings.Contains(subtype, "turns"):
		return "stopped at its turn cap"
	}
	return "stopped"
}

// view is the transcript as the page shows it. It reads each file's size
// now, in the agent's copy, and keeps the last size it read once the copy
// is gone.
func (t *transcript) view() Live {
	v := Live{Turns: len(t.ids), Thinking: t.thinking, Context: t.context, Output: t.output, Now: t.now, Agent: t.agent,
		Tools: map[string]int{}, Marks: append([]Mark{}, t.marks...), Files: []LiveFile{}}
	for k, n := range t.tools {
		v.Tools[k] = n
	}
	paths := make([]string, 0, len(t.files))
	for p := range t.files {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return t.files[paths[i]].at > t.files[paths[j]].at })
	if len(paths) > maxFiles {
		paths = paths[:maxFiles]
	}
	for i, p := range paths {
		f := t.files[p]
		if info, err := os.Stat(filepath.Join(t.cwd, filepath.FromSlash(p))); err == nil && info.Mode().IsRegular() {
			f.size = info.Size()
		}
		v.Files = append(v.Files, LiveFile{Path: p, Reads: f.reads, Edits: f.edits, Size: max(f.size, 0), Lit: i == 0})
	}
	return v
}

// live keeps what the dashboard has read of each repository's running
// step, and the last step it saw, which the page shows until the next one
// starts.
type live struct {
	mu    sync.Mutex
	reads map[string]*transcript // by transcript path
	path  map[string]string      // each repository's current transcript
	last  map[string]*Live       // each repository's last step
	at    time.Time
	body  []byte
}

// liveEvery is how long a read of the running steps is served before the
// dashboard reads again, however many pages ask.
const liveEvery = 2 * time.Second

// LiveJSON is every repository's running step, or the last one it ran, as
// the page's live view reads it.
func (s *Server) LiveJSON(now time.Time) ([]byte, error) {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if s.live.body != nil && now.Sub(s.live.at) < liveEvery {
		return s.live.body, nil
	}
	if s.live.reads == nil {
		s.live.reads, s.live.path, s.live.last = map[string]*transcript{}, map[string]string{}, map[string]*Live{}
	}
	steps := []Live{}
	for _, r := range s.Repos {
		st, work := s.watcherOf(r)
		if v := s.liveStep(r.Name, st, work, now); v != nil {
			steps = append(steps, *v)
		}
	}
	body, err := json.Marshal(map[string]any{"at": now.UTC(), "steps": steps})
	if err != nil {
		return nil, err
	}
	s.live.at, s.live.body = now, body
	return body, nil
}

// liveStep reads the step repo's watcher is running. Once the step ends, it
// shows the last read of it, marked ended, until the next step starts.
func (s *Server) liveStep(repo string, st Status, work string, now time.Time) *Live {
	prefix, _ := stepFiles(st.Doing)
	if st.Running() && st.Issue != 0 && prefix != "" && work != "" {
		if dir := newestStep(work, repo, st.Issue, prefix); dir != "" {
			path := filepath.Join(dir, "transcript.jsonl")
			if old := s.live.path[repo]; old != path {
				delete(s.live.reads, old)
				s.live.path[repo] = path
			}
			t := s.live.reads[path]
			if t == nil {
				t = newTranscript()
				s.live.reads[path] = t
			}
			if err := t.read(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.logf("live: %v", err)
			}
			v := t.view()
			v.Repo, v.Issue, v.Doing, v.Since, v.Limit = repo, st.Issue, st.Doing, st.Since, st.Limit
			if info, err := os.Stat(path); err == nil {
				m := info.ModTime().UTC()
				v.Last = &m
			}
			v.Runs, v.RunsKind = readRuns(dir, st.Doing)
			for i := range v.Marks {
				if v.Marks[i].At.IsZero() && v.Since != nil {
					v.Marks[i].At = *v.Since // thinking before the first tool call
				}
			}
			s.live.last[repo] = &v
			return &v
		}
	}
	last := s.live.last[repo]
	if last == nil {
		return nil
	}
	if last.Ended == nil {
		ended := now.UTC()
		last.Ended, last.Now = &ended, ""
		for i := range last.Marks {
			if last.Marks[i].Kind == "think" && last.Marks[i].Until == nil {
				last.Marks[i].Until = last.Last
			}
		}
	}
	v := *last
	return &v
}

// stepFiles is the prefix of a step's directory in the work directory, and
// the file it logs its checks to, for what a watcher is doing.
func stepFiles(doing string) (prefix string, runs []string) {
	switch doing {
	case "building":
		// A modeled build logs gate runs, and a plumbing build logs test
		// runs (D-0105).
		return "build-", []string{"gate-runs.jsonl", "test-runs.jsonl"}
	case "formalizing", "answering":
		return "formalize-", []string{"check-runs.jsonl"}
	}
	return "", nil
}

// newestStep is the directory of issue n's newest step with the prefix,
// under a watcher's work directory.
func newestStep(work, repo string, n int, prefix string) string {
	dirs, _ := filepath.Glob(filepath.Join(work, filepath.FromSlash(repo), "issues", fmt.Sprintf("issue-%d", n), prefix+"*"))
	if len(dirs) == 0 {
		return ""
	}
	sort.Strings(dirs)
	return dirs[len(dirs)-1]
}

// readRuns reads the checks a step has run so far, from its log in the
// step's directory, and says what kind of check they are.
func readRuns(dir, doing string) ([]RunMark, string) {
	_, files := stepFiles(doing)
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var runs []RunMark
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var r RunMark
			if json.Unmarshal([]byte(line), &r) == nil && r.Run > 0 {
				runs = append(runs, r)
			}
		}
		return runs, strings.TrimSuffix(name, "-runs.jsonl")
	}
	return nil, runsKindOf(doing)
}

func runsKindOf(doing string) string {
	if doing == "building" {
		return "gate"
	}
	if doing != "" {
		return "check"
	}
	return ""
}

// watcherOf is the watcher to show for r, with its work directory. With
// several watchers, it's the one working on something, or else the first
// that's running.
func (s *Server) watcherOf(r *Repo) (Status, string) {
	works := s.Works
	if len(works) == 0 {
		st, _ := ReadStatus(r.Status)
		return st, s.Work
	}
	var first, running *Status
	var firstWork, runningWork string
	for _, w := range works {
		st, err := ReadStatus(StatusPath(w, r.Name))
		if err != nil || st.PID == 0 {
			continue
		}
		if st.Running() && st.Doing != "" {
			return st, w
		}
		if first == nil {
			first, firstWork = &st, w
		}
		if running == nil && st.Running() {
			st := st
			running, runningWork = &st, w
		}
	}
	switch {
	case running != nil:
		return *running, runningWork
	case first != nil:
		return *first, firstWork
	}
	return Status{}, ""
}
