package logbuffer

// The explorer is the environment: the producers, what they've written, and
// what the shipper has sent. It keeps the model's bounds, so the code
// doesn't have to. Agreement explores it the way TLC explores the model.

// The bounds TLC checks within: Producers = {p1, p2}, Capacity = 2,
// MaxLines = 2.
const (
	NP       = 2
	Capacity = 2
	MaxLines = 2
	MaxLog   = NP * MaxLines
)

// State is the model's state: the buffer's lines, oldest first, and the
// environment's history.
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

// Successors returns the state after every action enabled in s, the way TLC
// expands Next. The code does the buffer's part of each step.
func Successors(s State) []State {
	var out []State
	for p := 0; p < NP; p++ {
		// Write(p): the producer has lines left, and the code has room.
		if s.Written[p] < MaxLines && s.BufLen < Capacity {
			b := buffer(s)
			line := Line{P: p, N: s.Written[p] + 1}
			b.Write(line)
			t := s
			read(b, &t)
			t.Log[t.LogLen], t.LogLen = line, t.LogLen+1
			t.Written[p]++
			out = append(out, t)
		}
	}
	if s.BufLen > 0 {
		// Ship: the code gives up its oldest line, and it's sent.
		b := buffer(s)
		l := b.Ship()
		t := s
		read(b, &t)
		t.Sent[t.SentLen], t.SentLen = l, t.SentLen+1
		t.Retrying = false
		out = append(out, t)
		// ShipFail: sending fails, and the buffer keeps the line.
		if !s.Retrying {
			u := s
			u.Retrying = true
			out = append(out, u)
		}
	}
	done := s.BufLen == 0
	for p := 0; p < NP; p++ {
		done = done && s.Written[p] == MaxLines
	}
	if done {
		out = append(out, s)
	}
	return out
}
