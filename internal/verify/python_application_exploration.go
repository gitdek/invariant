package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/gitdek/invariant/internal/conformance"
)

// PythonApplicationExploration is what an explored driver's harness declared
// and reached (#342), counted from its authenticated observations: each run
// replays, through the application, the path that first reached a state and
// tries one step there, or records that the environment's bounds rule the
// step out. It claims nothing by itself; TLC checks the attempts.
type PythonApplicationExploration struct {
	Steps    []PythonApplicationExplorationStep `json:"steps"`
	Runs     int                                `json:"runs"`
	States   int                                `json:"states"`
	Depth    int                                `json:"depth"`
	Attempts int                                `json:"attempts"`
	// Refusals are attempts that left the state as it was, with or without
	// the selected call. Omitted steps are the ones the driver left untried
	// where it said only the environment's bounds rule them out.
	Refusals int `json:"refusals"`
	Omitted  int `json:"omitted"`
}

// PythonApplicationExplorationStep is one model step the harness tried, with
// every argument list, and the application entry each attempt of it called.
type PythonApplicationExplorationStep struct {
	Name  string  `json:"name"`
	Args  [][]any `json:"args"`
	Entry string  `json:"entry,omitempty"`
}

// Preserve exact integer arguments when reports are read back through JSON.
func (s *PythonApplicationExplorationStep) UnmarshalJSON(b []byte) error {
	type step PythonApplicationExplorationStep
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode((*step)(s))
}

var pythonApplicationStepName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// pythonApplicationExplorer holds an explored stream to the harness's
// protocol as it's read: steps declared once before any run, runs one after
// another, each starting in its start's one initial state, replaying earlier
// attempts exactly and ending with one attempt or omission, and every state
// reached tried with every declared step and argument list exactly once.
type pythonApplicationExplorer struct {
	declared  bool
	steps     []PythonApplicationExplorationStep
	instances map[string]map[string]bool // step → argument lists, as JSON
	starts    map[int]string
	initial   map[string]bool
	inits     []string
	states    []string
	known     map[string]bool
	attempts  map[[3]string]string // from, step, arguments → to
	omitted   map[[3]string]bool
	edges     map[string][]string
	run       int
	done      bool
	called    bool
	stats     PythonApplicationExploration
}

func pythonApplicationKey(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (x *pythonApplicationExplorer) declare(line []byte) error {
	fields, err := pythonApplicationJSON(line, "type", "steps")
	if err != nil {
		return err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(fields["steps"], &raw); err != nil || len(raw) == 0 {
		return fmt.Errorf("exploration needs a nonempty list of steps")
	}
	x.instances = map[string]map[string]bool{}
	for _, item := range raw {
		if _, err := pythonApplicationJSON(item, "name", "args"); err != nil {
			return err
		}
		var step PythonApplicationExplorationStep
		d := json.NewDecoder(bytes.NewReader(item))
		d.UseNumber()
		if err := d.Decode(&step); err != nil {
			return err
		}
		if !pythonApplicationStepName.MatchString(step.Name) || x.instances[step.Name] != nil || len(step.Args) == 0 {
			return fmt.Errorf("exploration steps need distinct TLA+ names and argument lists")
		}
		x.instances[step.Name] = map[string]bool{}
		for _, args := range step.Args {
			if args == nil {
				return fmt.Errorf("exploration step %s has a null argument list", step.Name)
			}
			for _, arg := range args {
				if _, err := conformance.Encode(arg); err != nil {
					return fmt.Errorf("exploration step %s has an argument TLA+ can't read: %w", step.Name, err)
				}
			}
			key := pythonApplicationKey(args)
			if x.instances[step.Name][key] {
				return fmt.Errorf("exploration step %s repeats an argument list", step.Name)
			}
			x.instances[step.Name][key] = true
		}
		x.steps = append(x.steps, step)
	}
	x.starts, x.initial, x.known = map[int]string{}, map[string]bool{}, map[string]bool{}
	x.attempts, x.omitted, x.edges = map[[3]string]string{}, map[[3]string]bool{}, map[string][]string{}
	x.declared = true
	return nil
}

func (x *pythonApplicationExplorer) state(key string) {
	if !x.known[key] {
		x.known[key] = true
		x.states = append(x.states, key)
	}
}

// instance names a row's step, as the harness declared it.
func (x *pythonApplicationExplorer) instance(row PythonApplicationObservation) (string, error) {
	if row.Args == nil {
		return "", fmt.Errorf("exploration step %s has no argument list", row.Step)
	}
	args := pythonApplicationKey(row.Args)
	if !x.instances[row.Step][args] {
		return "", fmt.Errorf("exploration tried %s with arguments the harness never declared", row.Step)
	}
	return args, nil
}

func (x *pythonApplicationExplorer) observe(row PythonApplicationObservation) error {
	switch row.Kind {
	case "initial":
		if x.run > 0 && !x.done {
			return fmt.Errorf("an exploration run ended without trying a step")
		}
		if row.Start == nil || *row.Start < 0 {
			return fmt.Errorf("an exploration run names no start")
		}
		x.run, x.done = row.Run, false
		x.stats.Runs++
		key := pythonApplicationKey(row.State)
		if previous, ok := x.starts[*row.Start]; ok {
			if previous != key {
				return fmt.Errorf("a start did not reproduce its initial state")
			}
		} else {
			if x.initial[key] {
				return fmt.Errorf("two starts reached the same initial state")
			}
			x.starts[*row.Start] = key
			x.initial[key] = true
			x.inits = append(x.inits, key)
		}
		x.state(key)
		return nil
	case "omitted":
		if row.Run != x.run || x.done {
			return fmt.Errorf("an exploration run tried more than one step")
		}
		args, err := x.instance(row)
		if err != nil {
			return err
		}
		k := [3]string{pythonApplicationKey(row.State), row.Step, args}
		if _, tried := x.attempts[k]; tried || x.omitted[k] {
			return fmt.Errorf("exploration tried %s twice in one state", row.Step)
		}
		x.omitted[k] = true
		x.done = true
		x.stats.Omitted++
		return nil
	}
	if row.Run != x.run || x.done || row.Replay == nil || row.Called == nil {
		return fmt.Errorf("an exploration step falls outside its run")
	}
	args, err := x.instance(row)
	if err != nil {
		return err
	}
	for i := range x.steps {
		if s := &x.steps[i]; s.Name == row.Step {
			if s.Entry == "" {
				s.Entry = row.Entry
			} else if s.Entry != row.Entry {
				return fmt.Errorf("exploration step %s called two application entries", row.Step)
			}
		}
	}
	from, to := pythonApplicationKey(row.Before), pythonApplicationKey(row.After)
	if !*row.Called && from != to {
		return fmt.Errorf("a step without the selected call changed the state")
	}
	k := [3]string{from, row.Step, args}
	if *row.Replay {
		// A replay repeats an attempt the exploration already made.
		if was, ok := x.attempts[k]; !ok || was != to {
			return fmt.Errorf("the application is not deterministic: a replay of %s reached another state", row.Step)
		}
		return nil
	}
	if _, tried := x.attempts[k]; tried || x.omitted[k] {
		return fmt.Errorf("exploration tried %s twice in one state", row.Step)
	}
	x.attempts[k] = to
	x.edges[from] = append(x.edges[from], to)
	x.state(to)
	x.done = true
	x.called = x.called || *row.Called
	x.stats.Attempts++
	if from == to {
		x.stats.Refusals++
	}
	return nil
}

func (x *pythonApplicationExplorer) finish() (*PythonApplicationExploration, error) {
	if !x.declared || x.stats.Runs == 0 {
		return nil, fmt.Errorf("the exploration made no runs")
	}
	if !x.done {
		return nil, fmt.Errorf("an exploration run ended without trying a step")
	}
	// Every state reached must have been tried with every declared step.
	for _, state := range x.states {
		for _, step := range x.steps {
			for _, args := range step.Args {
				k := [3]string{state, step.Name, pythonApplicationKey(args)}
				if _, tried := x.attempts[k]; !tried && !x.omitted[k] {
					return nil, fmt.Errorf("the exploration never tried %s in a state it reached", step.Name)
				}
			}
		}
	}
	if !x.called {
		return nil, fmt.Errorf("the exploration never called the selected target")
	}
	// Depth counts breadth-first levels from the initial states, the first
	// level 1, as TLC's does.
	level := map[string]int{}
	queue := append([]string(nil), x.inits...)
	for _, s := range queue {
		level[s] = 1
	}
	for i := 0; i < len(queue); i++ {
		for _, next := range x.edges[queue[i]] {
			if level[next] == 0 {
				level[next] = level[queue[i]] + 1
				queue = append(queue, next)
			}
		}
	}
	for _, l := range level {
		x.stats.Depth = max(x.stats.Depth, l)
	}
	x.stats.States = len(x.states)
	x.stats.Steps = x.steps
	stats := x.stats
	return &stats, nil
}

// pythonApplicationAttempts turns an explored execution's authenticated
// rows into the attempts conformance.Check reads (D-0085): the states read
// back, the ones runs started in, and every step the harness tried, in its
// spec encoding. Replays repeat attempts, so only the attempts count.
func pythonApplicationAttempts(x PythonApplicationExecutionResult) ([]byte, error) {
	if !x.Success || !x.Complete || x.ObservationMode != "explored" || x.Exploration == nil || len(x.Observations) == 0 {
		return nil, fmt.Errorf("no complete authenticated explored observations")
	}
	out := struct {
		States   []map[string]any `json:"states"`
		Init     []int            `json:"init"`
		Attempts [][]any          `json:"attempts"`
	}{States: []map[string]any{}, Init: []int{}, Attempts: [][]any{}}
	index := map[string]int{}
	add := func(row PythonApplicationObservation, state map[string]any) (int, error) {
		key := pythonApplicationKey(state)
		if i, ok := index[key]; ok {
			return i, nil
		}
		if row.Form == "module" {
			converted, err := pythonApplicationModelValue(state)
			if err != nil {
				return 0, err
			}
			state = converted.(map[string]any)
		}
		index[key] = len(out.States)
		out.States = append(out.States, state)
		return index[key], nil
	}
	started := map[int]bool{}
	for _, row := range x.Observations {
		switch {
		case row.Kind == "initial":
			i, err := add(row, row.State)
			if err != nil {
				return nil, err
			}
			if !started[i] {
				started[i] = true
				out.Init = append(out.Init, i)
			}
		case row.Kind == "transition" && row.Replay != nil && !*row.Replay:
			from, err := add(row, row.Before)
			if err != nil {
				return nil, err
			}
			to, err := add(row, row.After)
			if err != nil {
				return nil, err
			}
			args := row.Args
			if args == nil {
				args = []any{}
			}
			out.Attempts = append(out.Attempts, []any{from, row.Step, args, to})
		}
	}
	if len(out.Attempts) == 0 {
		return nil, fmt.Errorf("no explored attempts")
	}
	return json.Marshal(out)
}

// mergePythonApplicationAttempts joins several explored executions' attempts
// into one record, each state once, as the direct checker returns them.
func mergePythonApplicationAttempts(all [][]byte) ([]byte, error) {
	type record struct {
		States   []map[string]any `json:"states"`
		Init     []int            `json:"init"`
		Attempts [][]any          `json:"attempts"`
	}
	out := record{States: []map[string]any{}, Init: []int{}, Attempts: [][]any{}}
	index, started := map[string]int{}, map[int]bool{}
	for _, raw := range all {
		var in record
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&in); err != nil {
			return nil, err
		}
		local := make([]int, len(in.States))
		for i, s := range in.States {
			key := pythonApplicationKey(s)
			n, ok := index[key]
			if !ok {
				n = len(out.States)
				index[key] = n
				out.States = append(out.States, s)
			}
			local[i] = n
		}
		at := func(v any) (int, error) {
			n, ok := v.(json.Number)
			i, err := n.Int64()
			if !ok || err != nil || i < 0 || i >= int64(len(local)) {
				return 0, fmt.Errorf("explored attempts name a state they don't hold")
			}
			return local[i], nil
		}
		for _, i := range in.Init {
			if i < 0 || i >= len(local) {
				return nil, fmt.Errorf("explored attempts start in a state they don't hold")
			}
			if !started[local[i]] {
				started[local[i]] = true
				out.Init = append(out.Init, local[i])
			}
		}
		for _, a := range in.Attempts {
			if len(a) != 4 {
				return nil, fmt.Errorf("an explored attempt isn't [from, step, args, to]")
			}
			from, err := at(a[0])
			if err != nil {
				return nil, err
			}
			to, err := at(a[3])
			if err != nil {
				return nil, err
			}
			out.Attempts = append(out.Attempts, []any{from, a[1], a[2], to})
		}
	}
	return json.Marshal(out)
}
