package twophase

// The explorer is the environment the transaction runs in: the resource
// managers that exist and the network that carries their messages. The gate
// explores the code from Init through Try, breadth first, and checks every
// attempt against the spec. Gobra doesn't verify this file (it has no +gobra
// header).

import "fmt"

// The bounds TLC checks: RM = {r1, r2, r3}.
const (
	RM = 3
)

// State holds the spec's variables at the bounds. The transaction's part is
// copied out of a Tx; msgs is the network's, and so the explorer's own. A
// message is never removed once sent, and the only ones there can be are one
// Prepared per resource manager, Commit and Abort.
type State struct {
	RMs         [RM]RMState // rmState
	TM          TMState     // tmState
	TMPrepared  [RM]bool    // r \in tmPrepared
	PreparedMsg [RM]bool    // [type |-> "Prepared", rm |-> r] \in msgs
	CommitMsg   bool        // [type |-> "Commit"] \in msgs
	AbortMsg    bool        // [type |-> "Abort"] \in msgs
}

// Init is the model's initial state, read out of a new transaction.
func Init() State {
	return store(New(RM), State{})
}

// load makes the transaction whose state s holds.
func load(s State) *Tx {
	t := New(RM)
	t.TM = s.TM
	for r := 0; r < RM; r++ {
		t.RM[r] = s.RMs[r]
		t.TMPrepared[r] = s.TMPrepared[r]
	}
	return t
}

// store is s with the transaction's part read back from t.
func store(t *Tx, s State) State {
	s.TM = t.TM
	for r := 0; r < RM; r++ {
		s.RMs[r] = t.RM[r]
		s.TMPrepared[r] = t.TMPrepared[r]
	}
	return s
}

func mv(r int) map[string]any {
	return map[string]any{"$mv": fmt.Sprintf("r%d", r+1)}
}

// Try tries every step Next names, for every resource manager, and reports
// the state each reaches: s itself where the code refuses, or where a message
// it would receive hasn't been sent.
func Try(s State, tried func(step string, args []any, next State)) {
	t := load(s)
	next := s
	if t.TMCommit() {
		next = store(t, s)
		next.CommitMsg = true
	}
	tried("TMCommit", []any{}, next)

	t = load(s)
	next = s
	if t.TMAbort() {
		next = store(t, s)
		next.AbortMsg = true
	}
	tried("TMAbort", []any{}, next)

	for r := 0; r < RM; r++ {
		args := []any{mv(r)}

		next = s
		if s.PreparedMsg[r] {
			t = load(s)
			if t.TMRcvPrepared(r) {
				next = store(t, s)
			}
		}
		tried("TMRcvPrepared", args, next)

		t = load(s)
		next = s
		if t.RMPrepare(r) {
			next = store(t, s)
			next.PreparedMsg[r] = true
		}
		tried("RMPrepare", args, next)

		t = load(s)
		next = s
		if t.RMChooseToAbort(r) {
			next = store(t, s)
		}
		tried("RMChooseToAbort", args, next)

		next = s
		if s.CommitMsg {
			t = load(s)
			t.RMRcvCommitMsg(r)
			next = store(t, s)
		}
		tried("RMRcvCommitMsg", args, next)

		next = s
		if s.AbortMsg {
			t = load(s)
			t.RMRcvAbortMsg(r)
			next = store(t, s)
		}
		tried("RMRcvAbortMsg", args, next)
	}
}

var rmNames = map[RMState]string{
	Working:   "working",
	Prepared:  "prepared",
	Committed: "committed",
	Aborted:   "aborted",
}

// Abstract is s in the spec's vocabulary.
func Abstract(s State) map[string]any {
	rmState := []any{}
	tmPrepared := []any{}
	msgs := []any{}
	for r := 0; r < RM; r++ {
		rmState = append(rmState, []any{mv(r), rmNames[s.RMs[r]]})
		if s.TMPrepared[r] {
			tmPrepared = append(tmPrepared, mv(r))
		}
		if s.PreparedMsg[r] {
			msgs = append(msgs, map[string]any{"type": "Prepared", "rm": mv(r)})
		}
	}
	if s.CommitMsg {
		msgs = append(msgs, map[string]any{"type": "Commit"})
	}
	if s.AbortMsg {
		msgs = append(msgs, map[string]any{"type": "Abort"})
	}
	tmState := "init"
	if s.TM == TMDone {
		tmState = "done"
	}
	return map[string]any{
		"rmState":    map[string]any{"$fn": rmState},
		"tmState":    tmState,
		"tmPrepared": map[string]any{"$set": tmPrepared},
		"msgs":       map[string]any{"$set": msgs},
	}
}
