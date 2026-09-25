package tlc

// TraceFile is a counterexample as data: the format the Trace Explorer
// replays and `invariant trace` prints.
type TraceFile struct {
	Module   string       `json:"module"`
	Check    string       `json:"check"`
	Violated string       `json:"violated,omitempty"`
	TLC      string       `json:"tlc"`
	States   []TraceState `json:"states"`
}

// TraceState is one step of a TraceFile.
type TraceState struct {
	Index   int               `json:"index"`
	Action  string            `json:"action"`
	Changed []string          `json:"changed"` // variables whose value differs from the previous step
	Vars    map[string]any    `json:"vars"`    // values as data
	TLA     map[string]string `json:"tla"`     // values as TLC printed them
}

// TraceFile turns the result's counterexample into a TraceFile.
func (r Result) TraceFile(module, check string) (TraceFile, error) {
	f := TraceFile{Module: module, Check: check, Violated: r.Invariant, TLC: r.Version, States: []TraceState{}}
	prev := map[string]string{}
	for _, s := range r.Trace {
		ts := TraceState{Index: s.Index, Action: s.Action, Changed: []string{}, Vars: map[string]any{}, TLA: map[string]string{}}
		for _, v := range s.Vars {
			value, err := ParseValue(v.Value)
			if err != nil {
				return TraceFile{}, err
			}
			ts.Vars[v.Name], ts.TLA[v.Name] = value, v.Value
			if old, seen := prev[v.Name]; seen && old != v.Value {
				ts.Changed = append(ts.Changed, v.Name)
			}
			prev[v.Name] = v.Value
		}
		f.States = append(f.States, ts)
	}
	return f, nil
}
