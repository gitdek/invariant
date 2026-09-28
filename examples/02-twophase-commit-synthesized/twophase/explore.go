package twophase

import "strconv"

// The model's bounds: RM = {r1, r2, r3}.
const (
	RM = 3
)

// State mirrors the spec's variables one to one. RMState, TMDone and
// TMPrepared are copied out of the code; the messages are the network's.
type State struct {
	// RMState[r] is rmState[r].
	RMState [RM]int
	// TMDone is tmState = "done"; false means "init".
	TMDone bool
	// TMPrepared[r] is r \in tmPrepared.
	TMPrepared [RM]bool
	// MsgPrepared[r] is [type |-> "Prepared", rm |-> r] \in msgs.
	MsgPrepared [RM]bool
	// MsgCommit is [type |-> "Commit"] \in msgs.
	MsgCommit bool
	// MsgAbort is [type |-> "Abort"] \in msgs.
	MsgAbort bool
}

// Init is the model's initial state.
func Init() State {
	var s State
	for r := 0; r < RM; r++ {
		s.RMState[r] = Working
	}
	return s
}

func coordinator(s State) *Coordinator {
	c := &Coordinator{Done: s.TMDone, Prepared: make([]bool, RM)}
	copy(c.Prepared, s.TMPrepared[:])
	return c
}

func (s State) withCoordinator(c *Coordinator) State {
	s.TMDone = c.Done
	copy(s.TMPrepared[:], c.Prepared)
	return s
}

func participants(s State) *Participants {
	p := &Participants{States: make([]int, RM)}
	copy(p.States, s.RMState[:])
	return p
}

func (s State) withParticipants(p *Participants) State {
	copy(s.RMState[:], p.States)
	return s
}

func rm(r int) map[string]any {
	return map[string]any{"$mv": "r" + strconv.Itoa(r+1)}
}

// Try tries every step Next names, with every argument, on the code.
func Try(s State, tried func(step string, args []any, next State)) {
	{
		next := s
		c := coordinator(s)
		if c.Commit() {
			next = s.withCoordinator(c)
			next.MsgCommit = true
		}
		tried("TMCommit", []any{}, next)
	}
	{
		next := s
		c := coordinator(s)
		if c.Abort() {
			next = s.withCoordinator(c)
			next.MsgAbort = true
		}
		tried("TMAbort", []any{}, next)
	}
	for r := 0; r < RM; r++ {
		args := []any{rm(r)}
		{
			// The TM can receive only a Prepared message r has sent.
			next := s
			if s.MsgPrepared[r] {
				c := coordinator(s)
				if c.RcvPrepared(r) {
					next = s.withCoordinator(c)
				}
			}
			tried("TMRcvPrepared", args, next)
		}
		{
			next := s
			p := participants(s)
			if p.Prepare(r) {
				next = s.withParticipants(p)
				next.MsgPrepared[r] = true
			}
			tried("RMPrepare", args, next)
		}
		{
			next := s
			p := participants(s)
			if p.ChooseToAbort(r) {
				next = s.withParticipants(p)
			}
			tried("RMChooseToAbort", args, next)
		}
		{
			// r can follow only a Commit decision the TM has sent.
			next := s
			if s.MsgCommit {
				p := participants(s)
				p.RcvCommit(r)
				next = s.withParticipants(p)
			}
			tried("RMRcvCommitMsg", args, next)
		}
		{
			// r can follow only an Abort decision the TM has sent.
			next := s
			if s.MsgAbort {
				p := participants(s)
				p.RcvAbort(r)
				next = s.withParticipants(p)
			}
			tried("RMRcvAbortMsg", args, next)
		}
	}
}

var rmStateNames = [...]string{Working: "working", Prepared: "prepared", Committed: "committed", Aborted: "aborted"}

// Abstract is s in the spec's vocabulary.
func Abstract(s State) map[string]any {
	rmState := []any{}
	tmPrepared := []any{}
	msgs := []any{}
	for r := 0; r < RM; r++ {
		rmState = append(rmState, []any{rm(r), rmStateNames[s.RMState[r]]})
		if s.TMPrepared[r] {
			tmPrepared = append(tmPrepared, rm(r))
		}
		if s.MsgPrepared[r] {
			msgs = append(msgs, map[string]any{"type": "Prepared", "rm": rm(r)})
		}
	}
	if s.MsgCommit {
		msgs = append(msgs, map[string]any{"type": "Commit"})
	}
	if s.MsgAbort {
		msgs = append(msgs, map[string]any{"type": "Abort"})
	}
	tmState := "init"
	if s.TMDone {
		tmState = "done"
	}
	return map[string]any{
		"rmState":    map[string]any{"$fn": rmState},
		"tmState":    tmState,
		"tmPrepared": map[string]any{"$set": tmPrepared},
		"msgs":       map[string]any{"$set": msgs},
	}
}
