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
// command, a tool's output or the model (D-0051, D-0052). Once the step has
// ended, it's the step as the watcher recorded it (#269).
type Live struct {
	Repo          string         `json:"repo"`
	Issue         int            `json:"issue"`
	Doing         string         `json:"doing"`
	Title         string         `json:"title"`         // what the card's heading says the step does, or did once it has ended
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
	turns         int // the turns of the agent's sessions before this one
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
		t.session()
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
		t.session()
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

// session starts an agent session, at its first event. A build's fallback
// and repairs run the agent again into the same transcript (D-0125), and
// each run numbers its messages and items afresh, so what's matched by them
// starts again, keeping the turns before it. A call the last session left
// waiting for its result never gets one.
func (t *transcript) session() {
	t.endThinking()
	t.turns += len(t.ids)
	t.ids, t.items, t.calls, t.open = map[string]bool{}, map[string]int{}, map[string]bool{}, nil
	t.agent, t.now = "", ""
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
	// Its size as it's touched, kept if the file goes before a view.
	if info, err := os.Stat(filepath.Join(t.cwd, filepath.FromSlash(rel))); err == nil && info.Mode().IsRegular() {
		f.size = info.Size()
	}
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
	v := Live{Turns: t.turns + len(t.ids), Thinking: t.thinking, ThinkingExact: t.thinkingExact, Context: t.context, Output: t.output, Now: t.now, Agent: t.agent,
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

// live keeps what the dashboard has read of the transcripts its cards show,
// and the last read it served. A card for a step that has ended comes from
// what its watcher recorded, never from what a page last asked for (#269).
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

// LiveJSON is every step each repository's watcher is running, or, where
// none is, the steps its watchers recorded as finishing last, longest-running
// first, as the page's live view reads it.
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
		if len(running) == 0 {
			running = s.finishedSteps(r, read)
		}
		steps = append(steps, running...)
	}
	// A transcript no card is read from anymore is forgotten.
	for path := range s.live.reads {
		if !read[path] {
			delete(s.live.reads, path)
		}
	}
	sort.SliceStable(steps, func(i, j int) bool {
		a, b := steps[i].Since, steps[j].Since
		return a != nil && (b == nil || a.Before(*b))
	})
	body, err := json.Marshal(map[string]any{"at": now.UTC(), "steps": steps})
	if err != nil {
		return err
	}
	s.live.at, s.live.body, s.live.steps = now, body, steps
	return nil
}

// liveSteps reads every step repo's watcher is running with an agent at
// work, and notes each transcript it reads in read.
func (s *Server) liveSteps(repo string, st Status, work string, read map[string]bool) []Live {
	var running []Live
	if st.Running() && work != "" {
		for _, step := range st.steps() {
			if step.Ended != nil {
				continue // a completion is never a running step
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
	v, ok := s.readTranscript(path, step.Agent, read, false)
	if !ok {
		return nil
	}
	v.Repo, v.Issue, v.Doing, v.Limit, v.By = repo, step.Issue, step.Doing, st.Limit, step.Agent
	v.Title = liveTitle(step.Doing, false)
	v.Run, v.path = step.Run, path
	if !step.Since.IsZero() {
		since := step.Since
		v.Since = &since
	}
	v.Runs, v.RunsKind = readRuns(dir, step.Doing)
	for i := range v.Marks {
		if v.Marks[i].At.IsZero() && v.Since != nil {
			v.Marks[i].At = *v.Since // thinking before the first tool call
		}
	}
	return &v
}

// readTranscript is a step's transcript as its reader has it after reading
// what's new, noted in read. Each agent writes its own transcript, so the
// view reads it as the step's agent writes it (#153). whole reads it to its
// end, however much no page read while the step ran. It's false for a
// transcript that can't be read.
func (s *Server) readTranscript(path, agent string, read map[string]bool, whole bool) (Live, bool) {
	t := s.live.reads[path]
	if t == nil {
		t = newTranscript()
		t.codex = agent == "codex"
		s.live.reads[path] = t
	}
	for {
		offset := t.offset
		if err := t.read(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				s.logf("live: %v", err)
			}
			return Live{}, false
		}
		if !whole || t.offset == offset {
			break
		}
	}
	read[path] = true
	v := t.view()
	if info, err := os.Stat(path); err == nil {
		m := info.ModTime().UTC()
		v.Last = &m
	}
	return v, true
}

// finishedSteps is r's cards once none of its steps is running: the steps
// its watchers recorded as ending last (#269). That's the latest end in any
// watcher's status, running or stopped, whatever the order of their work
// directories, and every step that ended at that same time, each read from
// its own watcher's run directory. What a page last saw chooses none of
// them, and a later end replaces them all.
func (s *Server) finishedSteps(r *Repo, read map[string]bool) []Live {
	type watched struct {
		status, work string
	}
	watchers := []watched{{r.Status, s.Work}}
	if len(s.Works) > 0 {
		watchers = nil
		for _, w := range s.Works {
			watchers = append(watchers, watched{StatusPath(w, r.Name), w})
		}
	}
	type finished struct {
		step Step
		work string
	}
	var latest []finished
	seen := map[string]bool{}
	for _, w := range watchers {
		st, err := ReadStatus(w.status)
		if err != nil {
			continue
		}
		for _, step := range st.Finished {
			dir := stepDir(w.work, r.Name, step)
			if !step.finished() || dir == "" || seen[dir] {
				continue
			}
			if len(latest) > 0 {
				switch first := *latest[0].step.Ended; {
				case step.Ended.Before(first):
					continue
				case step.Ended.After(first):
					latest, seen = nil, map[string]bool{}
				}
			}
			seen[dir] = true
			latest = append(latest, finished{step, w.work})
		}
	}
	var out []Live
	for _, f := range latest {
		out = append(out, s.finishedStep(r.Name, f.step, f.work, read))
	}
	return out
}

// finishedStep is the card of a step its watcher recorded as finished: what
// its agent did, as the watcher saved it before the agent's workspace was
// cleaned up, or, from a run that saved none, its whole transcript; its
// checks; and its identity, start, end and limit, as the watcher recorded
// them. It's still at its end, and says it ended.
func (s *Server) finishedStep(repo string, step Step, work string, read map[string]bool) Live {
	dir := stepDir(work, repo, step)
	path := filepath.Join(dir, "transcript.jsonl")
	v, ok := readFinishedActivity(dir)
	if !ok {
		if v, ok = s.readTranscript(path, step.Agent, read, true); !ok {
			// An agent that wrote nothing still ran until the step ended.
			v = Live{Tools: map[string]int{}, Marks: []Mark{}, Files: []LiveFile{}}
		}
	}
	ended := *step.Ended
	v.Repo, v.Issue, v.Doing, v.Limit, v.By = repo, step.Issue, step.Doing, step.Limit, step.Agent
	v.Title = liveTitle(step.Doing, true)
	v.Run, v.path, v.Ended, v.Now = step.Run, path, &ended, ""
	if !step.Since.IsZero() {
		since := step.Since
		v.Since = &since
	}
	v.Runs, v.RunsKind = readRuns(dir, step.Doing)
	// Thinking still going on in its last view ended by its last activity,
	// and nothing goes on past the step's end.
	stop := ended
	if v.Last != nil && v.Last.Before(ended) {
		stop = *v.Last
	}
	for i := range v.Marks {
		if v.Marks[i].At.IsZero() && v.Since != nil {
			v.Marks[i].At = *v.Since // thinking before the first tool call
		}
		if v.Marks[i].Kind == "think" && v.Marks[i].Until == nil {
			until := stop
			v.Marks[i].Until = &until
		}
	}
	return v
}

// liveTitle is what a card's heading says its step does, or did, once it
// has ended. An ended step's heading says only that it ended, since the
// attempt may have stopped or failed.
func liveTitle(doing string, ended bool) string {
	if ended {
		return map[string]string{"building": "Build step ended", "formalizing": "Draft step ended", "answering": "Answer step ended"}[doing]
	}
	return map[string]string{"building": "Building", "formalizing": "Drafting", "answering": "Answering"}[doing]
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
