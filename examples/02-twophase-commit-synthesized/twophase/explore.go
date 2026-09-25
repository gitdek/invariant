package twophase

// Successors returns the state after every action enabled in s, the way TLC
// expands Next.
func Successors(s State) []State {
	var out []State
	if !s.TMDone {
		all := true
		for j := 0; j < N; j++ {
			if !s.TMPrepared[j] {
				all = false
			}
		}
		if all {
			out = append(out, TMCommit(s))
		}
		out = append(out, TMAbort(s))
	}
	for r := 0; r < N; r++ {
		if !s.TMDone && s.MsgPrepared[r] {
			out = append(out, TMRcvPrepared(s, r))
		}
		if s.RM[r] == Working {
			out = append(out, RMPrepare(s, r))
			out = append(out, RMChooseToAbort(s, r))
		}
		if s.MsgCommit {
			out = append(out, RMRcvCommitMsg(s, r))
		}
		if s.MsgAbort {
			out = append(out, RMRcvAbortMsg(s, r))
		}
	}
	return out
}
