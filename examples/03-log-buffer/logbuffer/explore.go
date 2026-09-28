package logbuffer

import "fmt"

// The explorer is the environment: the producers, what they've written, and
// what the shipper has sent. It keeps the model's bounds, so the code
// doesn't have to. The gate explores it the way TLC explores the model.

// The bounds TLC checks within, named after the TLA+ constants: a set of
// model values, such as Producers = {p1, p2}, by its size. Capacity is the
// code's own parameter: Try still tries a write into a full buffer.
const (
	Producers = 2
	Capacity  = 2
	MaxLines  = 2
)

// MaxLog is the most lines ever written in all.
const MaxLog = Producers * MaxLines

// State is the model's state: the buffer's lines, oldest first, and the
// environment's history.
type State struct {
	Buf      [Capacity]Line
	BufLen   int
	Sent     [MaxLog]Line
	SentLen  int
	Log      [MaxLog]Line
	LogLen   int
	Written  [Producers]int
	Retrying bool
}

// Init mirrors the TLA+ Init.
func Init() State { return State{} }

// buffer makes the code's buffer holding s's lines.
func buffer(s State) *Buffer {
	b := New(Capacity)
	for i := 0; i < s.BufLen; i++ {
		b.Write(s.Buf[i])
	}
	return b
}

// read puts the code's buffer back into the model's state, oldest first.
func read(b *Buffer, s *State) {
	s.Buf, s.BufLen = [Capacity]Line{}, b.N
	for i := 0; i < b.N; i++ {
		s.Buf[i] = b.Slots[(b.Head+i)%len(b.Slots)]
	}
}

// producer is producer p as the model value it stands for.
func producer(p int) any { return map[string]any{"$mv": fmt.Sprintf("p%d", p+1)} }

// Try attempts every step Next names from s and lets the code refuse. Only
// MaxLines, the environment's bound, leaves a producer's write untried: a
// write into a full buffer is still tried, and the code refuses it.
func Try(s State, tried func(step string, args []any, next State)) {
	for p := 0; p < Producers; p++ {
		if s.Written[p] == MaxLines && s.BufLen < Capacity {
			continue
		}
		// Write(p): the code takes the line, or refuses when it's full.
		b := buffer(s)
		line := Line{P: p, N: s.Written[p] + 1}
		t := s
		if b.Write(line) {
			read(b, &t)
			t.Log[t.LogLen], t.LogLen = line, t.LogLen+1
			t.Written[p]++
		}
		tried("Write", []any{producer(p)}, t)
	}

	// Ship: the code gives up its oldest line, and it's sent.
	b := buffer(s)
	t := s
	if l, ok := b.Ship(); ok {
		read(b, &t)
		t.Sent[t.SentLen], t.SentLen = l, t.SentLen+1
		t.Retrying = false
	}
	tried("Ship", []any{}, t)

	// ShipFail: sending the oldest line fails, and the buffer keeps it.
	u := s
	if s.BufLen > 0 && !s.Retrying {
		u.Retrying = true
	}
	tried("ShipFail", []any{}, u)

	// Done: nothing changes, whether or not everything has been shipped.
	tried("Done", []any{}, s)
}

func lines(ls []Line) any {
	out := []any{}
	for _, l := range ls {
		out = append(out, map[string]any{"$seq": []any{producer(l.P), l.N}})
	}
	return map[string]any{"$seq": out}
}

// Abstract is s in the spec's vocabulary.
func Abstract(s State) map[string]any {
	written := []any{}
	for p := 0; p < Producers; p++ {
		written = append(written, []any{producer(p), s.Written[p]})
	}
	return map[string]any{
		"buf":      lines(s.Buf[:s.BufLen]),
		"sent":     lines(s.Sent[:s.SentLen]),
		"log":      lines(s.Log[:s.LogLen]),
		"written":  map[string]any{"$fn": written},
		"retrying": s.Retrying,
	}
}
