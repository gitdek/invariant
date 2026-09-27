package protocol

func isDirector(a int) bool {
	return a == ActorAlice
}

func headGate(s State) int8 {
	if s.Head == NoHead {
		return GateNone
	}
	return s.Gate[s.Head-1]
}

// Successors returns the state after every action enabled in s, the way TLC
// expands Next.
func Successors(s State) []State {
	var out []State
	prOrFailed := s.Kind == KindPROpen || s.Kind == KindFailed
	for a := 0; a < NActors; a++ {
		if !isDirector(a) {
			continue
		}
		if s.Kind == KindNone || s.Kind == KindStuck || s.Kind == KindUnsupported || s.Kind == KindClosed {
			for d := 0; d < NDrafts; d++ {
				out = append(out, Solve(s, a, d))
			}
		}
		if s.Kind == KindAsked || s.Kind == KindProposed || s.Kind == KindStuck || s.Kind == KindUnsupported || s.Kind == KindClosed {
			for d := 0; d < NDrafts; d++ {
				out = append(out, Revise(s, a, d))
			}
		}
		if s.Kind == KindFailed {
			out = append(out, Retry(s, a))
		}
		if s.Kind == KindAsked {
			for q := 0; q < NQuestions; q++ {
				if !s.Open[q] {
					continue
				}
				if s.Open[1-q] {
					out = append(out, Choose(s, a, q, 0))
				} else {
					for d := 0; d < NDrafts; d++ {
						out = append(out, Choose(s, a, q, d))
					}
				}
			}
		}
		if s.Kind == KindProposed && !s.Open[0] && !s.Open[1] && s.Base == s.Amends {
			for p := 0; p < NProposals; p++ {
				if int(s.Proposal) == p+1 {
					out = append(out, Ratify(s, a, p))
				}
			}
		}
	}
	if s.Kind == KindRatified {
		for h := 0; h < NHeads; h++ {
			out = append(out, Build(s, h))
		}
	}
	if prOrFailed {
		for h := 0; h < NHeads; h++ {
			if int(s.Head) == h+1 {
				continue
			}
			for l := 0; l < NLocks; l++ {
				for sc := 0; sc < 2; sc++ {
					out = append(out, Push(s, h, l, sc))
				}
			}
		}
		if headGate(s) == GatePending {
			for r := 0; r < 2; r++ {
				out = append(out, CIGate(s, r))
			}
		}
		out = append(out, OthersMerge(s))
		out = append(out, OthersClose(s))
	}
	if s.Kind == KindPROpen && headGate(s) == GateFail {
		out = append(out, NoticeFail(s))
	}
	if s.Kind == KindPROpen && headGate(s) == GatePass && s.Scope == ScopeOne && s.PrLock == s.Ratified {
		out = append(out, Merge(s))
	}
	for l := 0; l < NLocks; l++ {
		if int(s.Base) != l {
			out = append(out, BaseMoves(s, l))
		}
	}
	if s.Kind == KindMerged {
		out = append(out, Finished(s))
	}
	return out
}
