// +gobra

package pool

// MaxSize keeps index arithmetic far from overflow. It's the machine's
// limit, not the model's.
const MaxSize = 1 << 30

// Pool lends connections 0..len(Owner)-1 to clients 0..len(Held)-1.
// Owner[k] is 1 + the client holding connection k, or 0 when k is free.
// Held[c] is 1 + the connection client c holds, or 0 when it holds none.
type Pool struct {
	Owner []int
	Held  []int
}

// New makes a pool of size connections, all free, for the given number of clients.
// @ requires 0 <= size && size <= MaxSize && 0 <= clients && clients <= MaxSize
// @ ensures acc(&p.Owner) && acc(&p.Held)
// @ ensures len(p.Owner) == size && len(p.Held) == clients
// @ ensures forall k int :: { &p.Owner[k] } 0 <= k && k < len(p.Owner) ==> acc(&p.Owner[k]) && p.Owner[k] == 0
// @ ensures forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c]) && p.Held[c] == 0
// @ decreases
func New(size, clients int) (p *Pool) {
	owner := make([]int, size)
	held := make([]int, clients)
	p = &Pool{Owner: owner, Held: held}
	return p
}

// Acquire lends connection conn to client, unless the client already holds
// one or conn is out. When every connection is out, every request is refused.
// @ requires acc(&p.Owner, 1/2) && acc(&p.Held, 1/2)
// @ requires len(p.Owner) <= MaxSize && len(p.Held) <= MaxSize
// @ requires forall k int :: { &p.Owner[k] } 0 <= k && k < len(p.Owner) ==> acc(&p.Owner[k])
// @ requires forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c])
// @ requires forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> 0 <= p.Owner[k] && p.Owner[k] <= len(p.Held)
// @ requires forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> 0 <= p.Held[c] && p.Held[c] <= len(p.Owner)
// @ requires 0 <= client && client < len(p.Held) && 0 <= conn && conn < len(p.Owner)
// @ ensures acc(&p.Owner, 1/2) && acc(&p.Held, 1/2)
// @ ensures len(p.Owner) == old(len(p.Owner)) && len(p.Held) == old(len(p.Held))
// @ ensures forall k int :: { &p.Owner[k] } 0 <= k && k < len(p.Owner) ==> acc(&p.Owner[k])
// @ ensures forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c])
// @ ensures forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> 0 <= p.Owner[k] && p.Owner[k] <= len(p.Held)
// @ ensures forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> 0 <= p.Held[c] && p.Held[c] <= len(p.Owner)
// @ ensures ok == (old(p.Held[client]) == 0 && old(p.Owner[conn]) == 0)
// @ ensures ok ==> p.Held[client] == conn+1 && p.Owner[conn] == client+1
// @ ensures forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) && (!ok || k != conn) ==> p.Owner[k] == old(p.Owner[k])
// @ ensures forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) && (!ok || c != client) ==> p.Held[c] == old(p.Held[c])
// @ decreases
func (p *Pool) Acquire(client, conn int) (ok bool) {
	if p.Held[client] != 0 || p.Owner[conn] != 0 {
		return false
	}
	p.Held[client] = conn + 1
	p.Owner[conn] = client + 1
	return true
}

// Refuse turns client away when every connection is out, and changes
// nothing. While some connection is free, it is refused itself.
// @ requires acc(&p.Owner, 1/2) && acc(&p.Held, 1/2)
// @ requires len(p.Owner) <= MaxSize && len(p.Held) <= MaxSize
// @ requires forall k int :: { &p.Owner[k] } 0 <= k && k < len(p.Owner) ==> acc(&p.Owner[k])
// @ requires forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c])
// @ requires forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> 0 <= p.Owner[k] && p.Owner[k] <= len(p.Held)
// @ requires forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> 0 <= p.Held[c] && p.Held[c] <= len(p.Owner)
// @ requires 0 <= client && client < len(p.Held)
// @ ensures acc(&p.Owner, 1/2) && acc(&p.Held, 1/2)
// @ ensures len(p.Owner) == old(len(p.Owner)) && len(p.Held) == old(len(p.Held))
// @ ensures forall k int :: { &p.Owner[k] } 0 <= k && k < len(p.Owner) ==> acc(&p.Owner[k])
// @ ensures forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c])
// @ ensures forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> 0 <= p.Owner[k] && p.Owner[k] <= len(p.Held)
// @ ensures forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> 0 <= p.Held[c] && p.Held[c] <= len(p.Owner)
// @ ensures ok == (forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> p.Owner[k] != 0)
// @ ensures forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> p.Owner[k] == old(p.Owner[k])
// @ ensures forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> p.Held[c] == old(p.Held[c])
// @ decreases
func (p *Pool) Refuse(client int) (ok bool) {
	// @ invariant acc(&p.Owner, 1/2) && acc(&p.Held, 1/2)
	// @ invariant forall j int :: { &p.Owner[j] } 0 <= j && j < len(p.Owner) ==> acc(&p.Owner[j])
	// @ invariant forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c])
	// @ invariant len(p.Owner) == old(len(p.Owner)) && len(p.Held) == old(len(p.Held))
	// @ invariant forall j int :: { p.Owner[j] } 0 <= j && j < len(p.Owner) ==> p.Owner[j] == old(p.Owner[j])
	// @ invariant forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> p.Held[c] == old(p.Held[c])
	// @ invariant 0 <= k && k <= len(p.Owner)
	// @ invariant forall j int :: { p.Owner[j] } 0 <= j && j < k ==> p.Owner[j] != 0
	// @ decreases len(p.Owner) - k
	for k := 0; k < len(p.Owner); k++ {
		if p.Owner[k] == 0 {
			return false
		}
	}
	return true
}

// Release gives back the connection client holds, freeing exactly that one.
// A client that holds none is refused.
// @ requires acc(&p.Owner, 1/2) && acc(&p.Held, 1/2)
// @ requires len(p.Owner) <= MaxSize && len(p.Held) <= MaxSize
// @ requires forall k int :: { &p.Owner[k] } 0 <= k && k < len(p.Owner) ==> acc(&p.Owner[k])
// @ requires forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c])
// @ requires forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> 0 <= p.Owner[k] && p.Owner[k] <= len(p.Held)
// @ requires forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> 0 <= p.Held[c] && p.Held[c] <= len(p.Owner)
// @ requires 0 <= client && client < len(p.Held)
// @ ensures acc(&p.Owner, 1/2) && acc(&p.Held, 1/2)
// @ ensures len(p.Owner) == old(len(p.Owner)) && len(p.Held) == old(len(p.Held))
// @ ensures forall k int :: { &p.Owner[k] } 0 <= k && k < len(p.Owner) ==> acc(&p.Owner[k])
// @ ensures forall c int :: { &p.Held[c] } 0 <= c && c < len(p.Held) ==> acc(&p.Held[c])
// @ ensures forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) ==> 0 <= p.Owner[k] && p.Owner[k] <= len(p.Held)
// @ ensures forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) ==> 0 <= p.Held[c] && p.Held[c] <= len(p.Owner)
// @ ensures ok == (old(p.Held[client]) != 0)
// @ ensures ok ==> p.Held[client] == 0 && p.Owner[old(p.Held[client])-1] == 0
// @ ensures forall k int :: { p.Owner[k] } 0 <= k && k < len(p.Owner) && (!ok || k != old(p.Held[client])-1) ==> p.Owner[k] == old(p.Owner[k])
// @ ensures forall c int :: { p.Held[c] } 0 <= c && c < len(p.Held) && (!ok || c != client) ==> p.Held[c] == old(p.Held[c])
// @ decreases
func (p *Pool) Release(client int) (ok bool) {
	h := p.Held[client]
	if h == 0 {
		return false
	}
	p.Owner[h-1] = 0
	p.Held[client] = 0
	return true
}
