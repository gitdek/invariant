package twophase

// Successors returns the state after every action enabled in s, the way TLC
// expands the spec's Next. The gate's agreement check explores the code
// through it and compares what it reaches with what TLC reaches. Gobra
// doesn't verify this file (it has no +gobra header); the agreement check
// covers it.
func Successors(s State) []State {
	var next []State
	if CanTMCommit(s) {
		next = append(next, TMCommit(s))
	}
	if s.TM == TMInit {
		next = append(next, TMAbort(s))
	}
	for r := 0; r < N; r++ {
		if s.TM == TMInit && s.PreparedMsg[r] {
			next = append(next, TMRcvPrepared(s, r))
		}
		if s.RM[r] == Working {
			next = append(next, RMPrepare(s, r), RMChooseToAbort(s, r))
		}
		if s.CommitMsg {
			next = append(next, RMRcvCommitMsg(s, r))
		}
		if s.AbortMsg {
			next = append(next, RMRcvAbortMsg(s, r))
		}
	}
	return next
}
