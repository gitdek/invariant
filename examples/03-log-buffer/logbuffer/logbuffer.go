// +gobra

package logbuffer

// This package implements the LogBuffer TLA+ model: producers write log
// lines into a bounded buffer and a shipper sends them on, oldest first.

const (
	// NP is the number of producers (p1, p2 map to 0, 1).
	NP = 2
	// Capacity is the buffer capacity.
	Capacity = 2
	// MaxLines is how many lines each producer writes.
	MaxLines = 2
	// MaxLog is the most lines ever written in total.
	MaxLog = NP * MaxLines
)

// Line identifies a log line by its producer and per-producer number (from 1).
// The zero Line marks an unused slot.
type Line struct {
	P int
	N int
}

// State mirrors the spec variables. Sequences are stored as a fixed array plus
// a length; slots past the length are always the zero Line.
type State struct {
	Buf      [Capacity]Line
	BufLen   int
	Sent     [MaxLog]Line
	SentLen  int
	Log      [MaxLog]Line
	LogLen   int
	Written  [NP]int
	Retrying bool
}

// Init mirrors the TLA+ Init.
// @ ensures t.BufLen == 0 && t.SentLen == 0 && t.LogLen == 0 && !t.Retrying
// @ ensures forall i int :: 0 <= i && i < NP ==> t.Written[i] == 0
func Init() (t State) {
	return State{}
}

// Write mirrors the TLA+ action Write(p).
// @ requires 0 <= p && p < NP
// @ requires 0 <= s.Written[p] && s.Written[p] < MaxLines
// @ requires 0 <= s.BufLen && s.BufLen < Capacity
// @ requires 0 <= s.LogLen && s.LogLen < MaxLog
// @ ensures t.Buf[s.BufLen].P == p && t.Buf[s.BufLen].N == s.Written[p] + 1
// @ ensures forall i int :: 0 <= i && i < Capacity && i != s.BufLen ==> t.Buf[i] == s.Buf[i]
// @ ensures t.BufLen == s.BufLen + 1
// @ ensures t.Log[s.LogLen].P == p && t.Log[s.LogLen].N == s.Written[p] + 1
// @ ensures forall i int :: 0 <= i && i < MaxLog && i != s.LogLen ==> t.Log[i] == s.Log[i]
// @ ensures t.LogLen == s.LogLen + 1
// @ ensures t.Written[p] == s.Written[p] + 1
// @ ensures forall q int :: 0 <= q && q < NP && q != p ==> t.Written[q] == s.Written[q]
// @ ensures t.Sent == s.Sent && t.SentLen == s.SentLen
// @ ensures t.Retrying == s.Retrying
func Write(s State, p int) (t State) {
	t = s
	line := Line{P: p, N: s.Written[p] + 1}
	t.Buf[s.BufLen] = line
	t.BufLen = s.BufLen + 1
	t.Log[s.LogLen] = line
	t.LogLen = s.LogLen + 1
	t.Written[p] = s.Written[p] + 1
	return t
}

// Ship mirrors the TLA+ action Ship.
// @ requires 0 < s.BufLen && s.BufLen <= Capacity
// @ requires 0 <= s.SentLen && s.SentLen < MaxLog
// @ ensures t.Sent[s.SentLen] == s.Buf[0]
// @ ensures forall i int :: 0 <= i && i < MaxLog && i != s.SentLen ==> t.Sent[i] == s.Sent[i]
// @ ensures t.SentLen == s.SentLen + 1
// @ ensures t.Buf[0] == s.Buf[1]
// @ ensures t.Buf[1].P == 0 && t.Buf[1].N == 0
// @ ensures t.BufLen == s.BufLen - 1
// @ ensures !t.Retrying
// @ ensures t.Log == s.Log && t.LogLen == s.LogLen
// @ ensures t.Written == s.Written
func Ship(s State) (t State) {
	t = s
	t.Sent[s.SentLen] = s.Buf[0]
	t.SentLen = s.SentLen + 1
	t.Buf[0] = s.Buf[1]
	t.Buf[1] = Line{}
	t.BufLen = s.BufLen - 1
	t.Retrying = false
	return t
}

// ShipFail mirrors the TLA+ action ShipFail.
// @ requires 0 < s.BufLen
// @ requires !s.Retrying
// @ ensures t.Retrying
// @ ensures t.Buf == s.Buf && t.BufLen == s.BufLen
// @ ensures t.Sent == s.Sent && t.SentLen == s.SentLen
// @ ensures t.Log == s.Log && t.LogLen == s.LogLen
// @ ensures t.Written == s.Written
func ShipFail(s State) (t State) {
	t = s
	t.Retrying = true
	return t
}

// Done mirrors the TLA+ action Done.
// @ requires forall p int :: 0 <= p && p < NP ==> s.Written[p] == MaxLines
// @ requires s.BufLen == 0
// @ ensures t == s
func Done(s State) (t State) {
	return s
}
