package logbuffer

// Successors returns the state after every action enabled in s, the way TLC
// expands Next.
func Successors(s State) []State {
	var out []State
	for p := 0; p < NP; p++ {
		if s.Written[p] >= 0 && s.Written[p] < MaxLines &&
			s.BufLen >= 0 && s.BufLen < Capacity &&
			s.LogLen >= 0 && s.LogLen < MaxLog {
			out = append(out, Write(s, p))
		}
	}
	if s.BufLen > 0 && s.BufLen <= Capacity && s.SentLen >= 0 && s.SentLen < MaxLog {
		out = append(out, Ship(s))
	}
	if s.BufLen > 0 && !s.Retrying {
		out = append(out, ShipFail(s))
	}
	allDone := true
	for p := 0; p < NP; p++ {
		if s.Written[p] != MaxLines {
			allDone = false
		}
	}
	if allDone && s.BufLen == 0 {
		out = append(out, Done(s))
	}
	return out
}
