package dashboard

import (
	"bytes"
	"crypto/sha256"
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
	Repo          string         `json:"repo"`
	Issue         int            `json:"issue"`
	Doing         string         `json:"doing"`
	Title         string         `json:"title"`         // explicitly distinguishes a running step from an ended attempt
	Watcher       string         `json:"watcher"`       // opaque identity of its work directory, never the directory path
	Run           string         `json:"run,omitempty"` // the watcher's opaque run directory basename
	Since         *time.Time     `json:"since,omitempty"`
	Limit         float64        `json:"limit,omitempty"` // seconds the agent's run may take, when the watcher says
	Turns         int            `json:"turns"`
	Thinking      int            `json:"thinking"`         // reported thinking tokens of completed turns, or estimates so far
	ThinkingExact bool           `json:"thinkingExact"`    // the thinking total comes from reported usage
	Context       int            `json:"context"`          // tokens the agent holds now
	Output        int            `json:"output,omitempty"` // output tokens, once the agent's run has ended
	Tools         map[string]int `json:"tools"`            // tool calls by kind
	Now           string         `json:"now,omitempty"`    // thinking, or the kind of tool it's running
	Last          *time.Time     `json:"last,omitempty"`   // when the transcript last grew
	Marks         []Mark         `json:"marks"`
	Files         []LiveFile     `json:"files"`
	Runs          []RunMark      `json:"runs,omitempty"`
	RunsKind      string         `json:"runsKind,omitempty"` // check, gate or test
	Agent         string         `json:"agent,omitempty"`    // how the agent's run ended, once it has
	By            string         `json:"by,omitempty"`       // the coding agent running the step, as the watcher names it (#153)
	Ended         *time.Time     `json:"ended,omitempty"`    // when the step ended, once it has

	path string // the transcript supplying this view; never serialized
}

// Mark is one tool call, or one stretch of thinking between tool calls.
type Mark struct {
	At     time.Time  `json:"at"`
	Kind   string     `json:"kind"`             // read, search, list, build, test, git, edit, gate, check, other or think
	Until  *time.Time `json:"until,omitempty"`  // when it ended: nil while it goes on, as a tool call waiting for its result
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
	offset        int64
	cwd           string // the agent's copy of the repository, from the transcript's first event
	ids           map[string]bool
	thinking      int
	thinkingExact bool
	context       int
	output        int
	agent         string
	tools         map[string]int
	marks         []Mark
	commands      map[int]string // shell text only, for the retained marks in the private view
	calls         map[string]bool
	think         int   // the stretch of thinking going on, as an index into marks, or -1
	open          []int // tool calls waiting for their results, oldest first, as indexes into marks
	files         map[string]*touched
	touches       int
	last          time.Time // the newest time an event carried
	now           string
	// codex reads the transcript as Codex writes it. Both agents' pending
	// tools are indexed by id; -1 means their mark was trimmed.
	codex bool
	items map[string]int
}

type touched struct {
	reads, edits int
	size         int64
	at           int // when it was last touched, counted in touches
}

func newTranscript() *transcript {
	return &transcript{ids: map[string]bool{}, tools: map[string]int{}, files: map[string]*touched{}, think: -1, items: map[string]int{}, commands: map[int]string{}, calls: map[string]bool{}}
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
		codex := t.codex
		*t = *newTranscript() // the file was replaced: start again
		t.codex = codex
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
	for i, line := range bytes.Split(b[:end], []byte{'\n'}) {
		// Codex's backend starts its transcript by naming the workspace, so
		// a reader made before the watcher named the step's agent still
		// reads it as Codex writes it.
		if i == 0 && t.offset == 0 && codexStart(line) {
			t.codex = true
		}
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
	ToolID    string `json:"tool_use_id"`
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
	Type   string `json:"type"`
	Name   string `json:"name"`
	ID     string `json:"id"`
	ToolID string `json:"tool_use_id"`
	Input  struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	} `json:"input"`
}

func (t *transcript) line(b []byte) {
	if t.codex {
		t.codexLine(b)
		t.trim()
		return
	}
	var e event
	if json.Unmarshal(b, &e) != nil {
		return // not an event: skip it
	}
	switch {
	case e.Type == "system" && e.Subtype == "init":
		if e.Cwd != "" {
			t.workspace(e.Cwd)
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
			if _, pending := t.items[bl.ID]; bl.ID != "" && (pending || bl.Name == "Bash" && t.calls[bl.ID]) {
				continue
			}
			kind := toolKind(bl.Name)
			if bl.Name == "Bash" {
				t.beginShell(bl.ID, bl.Input.Command, eventTime(e.Timestamp, t.last))
			} else {
				t.begin(bl.ID, kind, eventTime(e.Timestamp, t.last))
			}
			p := bl.Input.FilePath
			if p == "" {
				p = bl.Input.NotebookPath
			}
			if bl.Name != "Bash" {
				t.touch(p, kind)
			}
		}
	case e.Type == "user":
		t.stamp(e.Timestamp)
		var blocks []block
		if bytes.HasPrefix(bytes.TrimSpace(e.Message.Content), []byte("[")) {
			json.Unmarshal(e.Message.Content, &blocks)
		}
		for _, bl := range blocks {
			if bl.Type == "tool_result" && t.finish(bl.ToolID, eventTime(e.Timestamp, t.last)) {
				t.now = t.pendingKind("")
			}
		}
	case e.Type == "tool_progress":
		if i, ok := t.items[e.ToolID]; ok && i >= 0 {
			t.now = t.marks[i].Kind
		} else if e.ToolName == "Bash" {
			t.now = t.pendingKind(t.now)
		} else {
			t.now = toolKind(e.ToolName)
		}
	case e.Type == "result":
		t.endThinking()
		t.output = e.Usage.Output
		t.agent = agentEnded(e.Subtype, e.IsError)
		t.now = ""
	}
	t.trim()
}

func (t *transcript) begin(id, kind string, at time.Time) {
	t.tools[kind]++
	t.marks = append(t.marks, Mark{At: at, Kind: kind})
	i := len(t.marks) - 1
	t.open = append(t.open, i)
	if id != "" {
		t.items[id] = i
	}
	t.now = kind
}

func (t *transcript) beginShell(id, command string, at time.Time) {
	t.begin(id, t.shell(command), at)
	t.commands[len(t.marks)-1] = command
	if id != "" {
		t.calls[id] = true
	}
}

// finish matches a result to its call. Only older results without an id
// fall back to the oldest call still waiting.
func (t *transcript) finish(id string, at time.Time) bool {
	i, ok := t.items[id]
	if id == "" && len(t.open) > 0 {
		i, ok = t.open[0], true
	}
	if !ok {
		return false
	}
	if i >= 0 {
		t.marks[i].Until = &at
	}
	for j, pending := range t.open {
		if pending == i {
			t.open = append(t.open[:j], t.open[j+1:]...)
			break
		}
	}
	if id != "" {
		delete(t.items, id)
	} else {
		for key, pending := range t.items {
			if pending == i {
				delete(t.items, key)
			}
		}
	}
	return true
}

func (t *transcript) pendingKind(fallback string) string {
	if len(t.open) > 0 {
		return t.marks[t.open[len(t.open)-1]].Kind
	}
	return fallback
}

func eventTime(ts string, fallback time.Time) time.Time {
	if at, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return at.UTC()
	}
	return fallback
}

// trim keeps the newest marks, up to maxMarks.
func (t *transcript) trim() {
	if len(t.marks) > maxMarks {
		drop := len(t.marks) - maxMarks
		t.marks = append([]Mark(nil), t.marks[drop:]...)
		commands := map[int]string{}
		for i, command := range t.commands {
			if i >= drop {
				commands[i-drop] = command
			}
		}
		t.commands = commands
		if t.think >= 0 {
			t.think -= drop
		}
		open := t.open[:0]
		for _, i := range t.open {
			if i >= drop {
				open = append(open, i-drop)
			}
		}
		t.open = open
		for id, i := range t.items {
			if i < drop {
				t.items[id] = -1
			} else {
				t.items[id] = i - drop
			}
		}
	}
}

// codexEvent is the part of a line of Codex's transcript the live view
// reads (#153). Shell commands are retained only with their recent marks
// for the opted-in private view. Tool output is never decoded. Reasoning
// is measured raw for tokens.
type codexEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	Usage     struct {
		Output    int  `json:"output_tokens"`
		Reasoning *int `json:"reasoning_output_tokens"`
	} `json:"usage"`
	Item struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Server  string          `json:"server"`
		Tool    string          `json:"tool"`
		Command string          `json:"command"`
		Text    json.RawMessage `json:"text"`
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	} `json:"item"`
}

// codexStart says whether a transcript's first line is the one Codex's
// backend starts it with.
func codexStart(line []byte) bool {
	var e struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(line, &e) == nil && e.Type == "invariant.workspace"
}

// codexLine reads one line of Codex's transcript: its tool calls in time,
// by kind, as Claude Code's are, the files it changed, its reasoning as
// stretches of thinking, and how its run ended. Certain shell operands
// identify reads; Codex doesn't report how much context it holds.
func (t *transcript) codexLine(b []byte) {
	var e codexEvent
	if json.Unmarshal(b, &e) != nil {
		return // not an event: skip it
	}
	before := t.last
	at := eventTime(e.Timestamp, t.last)
	if at.After(t.last) {
		t.last = at
	}
	switch e.Type {
	case "invariant.workspace":
		if e.Cwd != "" {
			t.workspace(e.Cwd)
		}
	case "turn.started":
		t.agent, t.now = "", "thinking"
	case "item.started":
		if _, pending := t.items[e.Item.ID]; e.Item.ID != "" && (pending || t.ids[e.Item.ID]) {
			return
		}
		if kind := codexKind(e.Item.Type, e.Item.Server, e.Item.Tool); kind != "" {
			if e.Item.Type == "command_execution" {
				t.beginShell(e.Item.ID, e.Item.Command, at)
			} else {
				t.begin(e.Item.ID, kind, at)
			}
		}
	case "item.completed":
		switch e.Item.Type {
		case "reasoning":
			// Its thinking came between the event before and this one.
			until, tokens := t.last, len(e.Item.Text)/4
			if !t.thinkingExact {
				t.thinking += tokens
			}
			t.marks = append(t.marks, Mark{At: before, Kind: "think", Until: &until, Tokens: tokens})
			return
		case "todo_list", "error":
			return
		}
		if e.Item.ID != "" && t.ids[e.Item.ID] {
			return
		}
		t.ids[e.Item.ID] = true // a step, as the backend counts its turns
		if kind := codexKind(e.Item.Type, e.Item.Server, e.Item.Tool); kind != "" && !t.finish(e.Item.ID, at) {
			if e.Item.Type == "command_execution" {
				t.beginShell(e.Item.ID, e.Item.Command, at)
			} else {
				t.begin(e.Item.ID, kind, at)
			}
			t.finish(e.Item.ID, at)
		}
		for _, c := range e.Item.Changes {
			t.touch(c.Path, "edit")
		}
		t.now = t.pendingKind("thinking")
	case "turn.completed":
		if e.Usage.Reasoning != nil {
			if !t.thinkingExact {
				t.thinking = 0 // the first reported count replaces estimates
				t.thinkingExact = true
			}
			t.thinking += *e.Usage.Reasoning
		}
		t.output, t.agent, t.now = e.Usage.Output, "finished", ""
	case "turn.failed":
		t.agent, t.now = "stopped", ""
	}
}

// codexKind sorts one of Codex's items into what the page colors it by: a
// file change is an edit, Invariant's gate, check and test are themselves,
// and anything else is other. Shell commands are classified separately.
// A message is no tool call.
func codexKind(item, server, tool string) string {
	switch item {
	case "agent_message", "reasoning", "todo_list", "error", "":
		return ""
	case "file_change":
		return "edit"
	case "mcp_tool_call":
		return toolKind("mcp__" + server + "__" + tool)
	}
	return "other"
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
	if p != "" && t.cwd != "" {
		if !filepath.IsAbs(p) {
			p = filepath.Join(t.cwd, p)
		}
		p = canonical(p)
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

func (t *transcript) workspace(dir string) {
	t.cwd = canonical(dir)
}

// canonical resolves p's symbolic links as far as p exists, so a file that
// doesn't, as one an agent made and removed, still compares with the
// workspace it was in. On macOS, temporary directories are under /var, a
// link to /private/var.
func canonical(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	for dir, rest := p, ""; ; {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// inside compares canonical paths; callers resolve filesystem aliases.
func inside(dir, p string) (string, bool) {
	if dir == "" || p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	rel, err := filepath.Rel(dir, p)
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
	v := Live{Turns: len(t.ids), Thinking: t.thinking, ThinkingExact: t.thinkingExact, Context: t.context, Output: t.output, Now: t.now, Agent: t.agent,
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

// live keeps incremental transcript readers and a short-lived response
// cache. A card's completion always comes from persisted watcher status.
type live struct {
	mu    sync.Mutex
	reads map[string]*transcript // by transcript path
	at    time.Time
	body  []byte
	steps []Live
}

// liveEvery is how long a read of the running steps is served before the
// dashboard reads again, however many pages ask.
const liveEvery = 2 * time.Second

// LiveJSON is every step each repository's selected watcher is running, or
// its latest recorded completions across watchers, in original start order.
func (s *Server) LiveJSON(now time.Time) ([]byte, error) {
	return s.liveJSON(now, false)
}

// liveJSON shares the public snapshot, but adds commands only to a private
// response. No serialized command text enters the public response cache.
func (s *Server) liveJSON(now time.Time, commands bool) ([]byte, error) {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if s.live.body == nil || now.Sub(s.live.at) >= liveEvery {
		if err := s.refreshLive(now); err != nil {
			return nil, err
		}
	}
	if !commands {
		return s.live.body, nil
	}
	steps := []privateLive{}
	for _, v := range s.live.steps {
		step := privateLive{Live: v}
		if t := s.live.reads[v.path]; v.Ended == nil && t != nil {
			for i, mark := range v.Marks {
				if command, ok := t.commands[i]; ok {
					step.Commands = append(step.Commands, shellCommand{Command: command, Kind: mark.Kind, At: mark.At, Until: mark.Until})
				}
			}
		}
		steps = append(steps, step)
	}
	return json.Marshal(map[string]any{"at": s.live.at.UTC(), "steps": steps})
}

type shellCommand struct {
	Command string     `json:"command"`
	Kind    string     `json:"kind"`
	At      time.Time  `json:"at"`
	Until   *time.Time `json:"until,omitempty"`
}

type privateLive struct {
	Live
	Commands []shellCommand `json:"commands,omitempty"`
}

func (s *Server) refreshLive(now time.Time) error {
	if s.live.reads == nil {
		s.live.reads = map[string]*transcript{}
	}
	steps := []Live{}
	read := map[string]bool{}
	for _, r := range s.Repos {
		st, work := s.watcherOf(r)
		running := s.liveSteps(r.Name, st, work, read)
		if len(running) > 0 {
			steps = append(steps, running...)
		} else {
			for _, done := range s.finishedOf(r) {
				if v := s.liveStep(r.Name, done.st, done.step, done.work, read); v != nil {
					steps = append(steps, *v)
				}
			}
		}
	}
	// Forget readers no displayed running or finished step uses anymore.
	for path := range s.live.reads {
		if !read[path] {
			delete(s.live.reads, path)
		}
	}
	sort.SliceStable(steps, func(i, j int) bool {
		a, b := steps[i].Since, steps[j].Since
		if a != nil && b != nil && a.Equal(*b) || a == nil && b == nil {
			if steps[i].Watcher != steps[j].Watcher {
				return steps[i].Watcher < steps[j].Watcher
			}
			if steps[i].Issue != steps[j].Issue {
				return steps[i].Issue < steps[j].Issue
			}
			return steps[i].Run < steps[j].Run
		}
		return a != nil && (b == nil || a.Before(*b))
	})
	body, err := json.Marshal(map[string]any{"at": now.UTC(), "steps": steps})
	if err != nil {
		return err
	}
	s.live.at, s.live.body, s.live.steps = now, body, steps
	return nil
}

// liveSteps reads running agent steps from the selected watcher only.
func (s *Server) liveSteps(repo string, st Status, work string, read map[string]bool) []Live {
	var running []Live
	if st.Running() && work != "" {
		for _, step := range st.steps() {
			if step.Ended != nil {
				continue
			}
			if v := s.liveStep(repo, st, step, work, read); v != nil {
				running = append(running, *v)
			}
		}
	}
	return running
}

// liveStep reads one step from its own issue's named run directory: its
// transcript, only as far as it's new, and its checks' log. It's nil for a
// step with no agent at work, doing nothing yet or ratifying, and for one
// whose directory isn't there yet.
func (s *Server) liveStep(repo string, st Status, step Step, work string, read map[string]bool) *Live {
	prefix, _ := stepFiles(step.Doing)
	if step.Issue == 0 || prefix == "" {
		return nil
	}
	dir := stepDir(work, repo, step)
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, "transcript.jsonl")
	var v Live
	saved := false
	if step.Ended != nil {
		v, saved = readFinishedActivity(dir)
	}
	if !saved {
		t := s.live.reads[path]
		if t == nil {
			t = newTranscript()
			// Each agent writes its own transcript (#153).
			t.codex = step.Agent == "codex"
			s.live.reads[path] = t
		}
		for {
			offset := t.offset
			if err := t.read(path); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					s.logf("live: %v", err)
				}
				return nil
			}
			// A finished card must include the final transcript even when
			// no page read its backlog while the step ran.
			if step.Ended == nil || t.offset == offset {
				break
			}
		}
		read[path] = true
		v = t.view()
		if info, err := os.Stat(path); err == nil {
			m := info.ModTime().UTC()
			v.Last = &m
		}
	}
	v.Repo, v.Issue, v.Doing, v.Limit, v.By = repo, step.Issue, step.Doing, step.Limit, step.Agent
	if v.Limit == 0 && step.Ended == nil {
		v.Limit = st.Limit
	}
	v.Title = liveTitle(step)
	v.Watcher = fmt.Sprintf("%x", sha256.Sum256([]byte(canonical(work))))
	v.Run, v.path = step.Run, path
	if !step.Since.IsZero() {
		since := step.Since
		v.Since = &since
	}
	v.Runs, v.RunsKind = readRuns(dir, step.Doing)
	if step.Ended != nil {
		ended := *step.Ended
		v.Ended, v.Now = &ended, ""
	}
	for i := range v.Marks {
		if v.Marks[i].At.IsZero() && v.Since != nil {
			v.Marks[i].At = *v.Since // thinking before the first tool call
		}
		if v.Ended != nil && v.Marks[i].Until == nil {
			v.Marks[i].Until = v.Ended
		}
	}
	return &v
}

func liveTitle(step Step) string {
	if step.Ended != nil {
		return map[string]string{"formalizing": "Draft step ended", "answering": "Answer step ended", "building": "Build step ended"}[step.Doing]
	}
	return map[string]string{"formalizing": "Drafting", "answering": "Answering", "building": "Building"}[step.Doing]
}

type finishedStep struct {
	st   Status
	step Step
	work string
}

// finishedOf selects the greatest recorded completion across every watcher,
// including stopped watchers, and keeps each tie with its own work directory.
// Running-watcher selection and directory order cannot choose the finisher.
func (s *Server) finishedOf(r *Repo) []finishedStep {
	works := s.Works
	if len(works) == 0 {
		works = []string{s.Work}
	}
	var out []finishedStep
	var latest time.Time
	seen := map[string]bool{}
	for _, work := range works {
		path := StatusPath(work, r.Name)
		if len(s.Works) == 0 {
			path = r.Status
		}
		st, err := ReadStatus(path)
		if err != nil {
			continue
		}
		for _, step := range st.Finished {
			if !step.finished() || step.Ended.Before(latest) {
				continue
			}
			if step.Ended.After(latest) {
				latest, out, seen = *step.Ended, nil, map[string]bool{}
			}
			key := stepDir(canonical(work), r.Name, step)
			if !seen[key] {
				seen[key] = true
				out = append(out, finishedStep{st: st, step: step, work: work})
			}
		}
	}
	return out
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

// stepDir selects only the basename the watcher published for this step.
// Run names are opaque: neither their spelling nor their times select a run.
func stepDir(work, repo string, step Step) string {
	if work == "" || step.Issue == 0 || !validRun(step.Run) {
		return ""
	}
	return filepath.Join(work, filepath.FromSlash(repo), "issues", fmt.Sprintf("issue-%d", step.Issue), step.Run)
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
// several watchers, it's the running one whose status names the holder of
// r's lease. When the lease is unknown or names none of them, it's the one
// working on something, or else the first that's running.
func (s *Server) watcherOf(r *Repo) (Status, string) {
	works := s.Works
	if len(works) == 0 {
		st, _ := ReadStatus(r.Status)
		return st, s.Work
	}
	type watcher struct {
		st   Status
		work string
	}
	holder := s.holderOf(r.Name)
	var working, running, first *watcher
	for _, w := range works {
		st, err := ReadStatus(StatusPath(w, r.Name))
		if err != nil || st.PID == 0 {
			continue
		}
		on := st.Running()
		if on && holder != "" && st.Holder == holder {
			return st, w
		}
		this := &watcher{st, w}
		if working == nil && on && st.Doing != "" {
			working = this
		}
		if running == nil && on {
			running = this
		}
		if first == nil {
			first = this
		}
	}
	for _, w := range []*watcher{working, running, first} {
		if w != nil {
			return w.st, w.work
		}
	}
	return Status{}, ""
}

// holding keeps who holds repo's lease, as the dashboard last read it, where
// the live view, which is served apart from the reads, can see it.
func (s *Server) holding(repo string, l *Lease) {
	holder := ""
	if l != nil {
		holder = l.Holder
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holders == nil {
		s.holders = map[string]string{}
	}
	s.holders[repo] = holder
}

// holderOf is who holds repo's lease, as last read, or "" when that isn't
// known.
func (s *Server) holderOf(repo string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.holders[repo]
}
