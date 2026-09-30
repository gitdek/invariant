// +gobra

package storejournal

// MaxSize is a machine limit that keeps lengths and line-number arithmetic
// within the signed integer range, including on 32-bit machines.
const MaxSize = 1 << 30

// Journal holds the store's lines and the lines present in each checkout.
// Lines have an origin (a checkout index) and a positive sequence number.
// Since an origin appends consecutive lines and merges take whole sets, a
// prefix length represents its lines exactly: n means lines 1 through n.
// Thus Store[origin] and Files[checkout][origin] represent sets of lines,
// rather than a history of operations. Abandonment leaves both sets intact.
//
// Capacity limits the line numbers each origin can allocate. The slices are
// separately owned by the journal; callers must preserve their shape,
// disjoint storage, and prefix ranges when restoring a journal from a snapshot.
// Methods are synchronous.
type Journal struct {
	Store     []int
	Files     [][]int
	Abandoned []bool
	Capacity  int
}

// New constructs an empty journal with the given checkout count and per-origin
// line capacity. Both sizes must be between zero and MaxSize, inclusive.
// @ requires 0 <= checkouts && checkouts <= MaxSize
// @ requires 0 <= capacity && capacity <= MaxSize
// @ ensures j != nil
// @ ensures acc(&j.Store) && acc(&j.Files) && acc(&j.Abandoned) && acc(&j.Capacity)
// @ ensures len(j.Store) == checkouts && len(j.Files) == checkouts && len(j.Abandoned) == checkouts
// @ ensures j.Capacity == capacity
// @ ensures forall c int :: { &j.Store[c] } 0 <= c && c < checkouts ==> acc(&j.Store[c])
// @ ensures forall c int :: { &j.Abandoned[c] } 0 <= c && c < checkouts ==> acc(&j.Abandoned[c])
// @ ensures forall c int :: { &j.Files[c] } 0 <= c && c < checkouts ==> acc(&j.Files[c]) && len(j.Files[c]) == checkouts
// @ ensures forall c, p int :: { &j.Files[c][p] } 0 <= c && c < checkouts && 0 <= p && p < checkouts ==> acc(&j.Files[c][p])
// @ ensures forall c int :: { j.Store[c] } 0 <= c && c < checkouts ==> j.Store[c] == 0 && !j.Abandoned[c]
// @ ensures forall c, p int :: { j.Files[c][p] } 0 <= c && c < checkouts && 0 <= p && p < checkouts ==> j.Files[c][p] == 0
func New(checkouts, capacity int) (j *Journal) {
	store := make([]int, checkouts)
	files := make([][]int, checkouts)
	abandoned := make([]bool, checkouts)
	i := 0
	// @ invariant 0 <= i && i <= checkouts
	// @ invariant len(files) == checkouts
	// @ invariant forall c int :: { &files[c] } 0 <= c && c < checkouts ==> acc(&files[c])
	// @ invariant forall c int :: { files[c] } 0 <= c && c < i ==> len(files[c]) == checkouts
	// @ invariant forall c, p int :: { &files[c][p] } 0 <= c && c < i && 0 <= p && p < checkouts ==> acc(&files[c][p])
	// @ invariant forall c, p int :: { files[c][p] } 0 <= c && c < i && 0 <= p && p < checkouts ==> files[c][p] == 0
	// @ decreases checkouts - i
	for i < checkouts {
		files[i] = make([]int, checkouts)
		i++
	}
	return &Journal{Store: store, Files: files, Abandoned: abandoned, Capacity: capacity}
}

// at is the ghost view of an origin's complete prefix of lines.
// @ ghost
// @ requires 0 <= p && p < len(prefixes) && acc(&prefixes[p], _)
// @ decreases
// @ pure func at(prefixes []int, p int) int { return prefixes[p] }

// @ ghost
// @ decreases
// @ pure func larger(a, b int) int { return a < b ? b : a }

// mergePrefixes journals every prefix in src without shortening any prefix in dst.
// It reports whether the set changed; an already contained set is refused.
// @ requires len(dst) == len(src) && len(dst) <= MaxSize
// @ requires forall p int :: { &dst[p] } 0 <= p && p < len(dst) ==> acc(&dst[p])
// @ requires forall p int :: { &src[p] } 0 <= p && p < len(src) ==> acc(&src[p])
// @ requires forall p, q int :: { &dst[p], &src[q] } 0 <= p && p < len(dst) && 0 <= q && q < len(src) ==> &dst[p] != &src[q]
// @ ensures forall p int :: { &dst[p] } 0 <= p && p < len(dst) ==> acc(&dst[p])
// @ ensures forall p int :: { &src[p] } 0 <= p && p < len(src) ==> acc(&src[p])
// @ ensures forall p int :: { at(dst, p) } 0 <= p && p < len(dst) ==> at(dst, p) == larger(old(at(dst, p)), old(at(src, p)))
// @ ensures forall p int :: { at(src, p) } 0 <= p && p < len(src) ==> at(src, p) == old(at(src, p))
// @ ensures changed == (exists p int :: 0 <= p && p < len(dst) && old(at(src, p)) > old(at(dst, p)))
// @ ensures !changed ==> forall p int :: { at(dst, p) } 0 <= p && p < len(dst) ==> at(dst, p) == old(at(dst, p))
func mergePrefixes(dst, src []int) (changed bool) {
	i := 0
	// @ invariant 0 <= i && i <= len(dst)
	// @ invariant forall p, q int :: { &dst[p], &src[q] } 0 <= p && p < len(dst) && 0 <= q && q < len(src) ==> &dst[p] != &src[q]
	// @ invariant forall p int :: { &dst[p] } 0 <= p && p < len(dst) ==> acc(&dst[p])
	// @ invariant forall p int :: { &src[p] } 0 <= p && p < len(src) ==> acc(&src[p])
	// @ invariant forall p int :: { at(src, p) } 0 <= p && p < len(src) ==> at(src, p) == old(at(src, p))
	// @ invariant forall p int :: { at(dst, p) } 0 <= p && p < i ==> at(dst, p) == larger(old(at(dst, p)), old(at(src, p)))
	// @ invariant forall p int :: { at(dst, p) } i <= p && p < len(dst) ==> at(dst, p) == old(at(dst, p))
	// @ invariant changed == (exists p int :: 0 <= p && p < i && old(at(src, p)) > old(at(dst, p)))
	// @ decreases len(dst) - i
	for i < len(dst) {
		// @ assert at(src, i) == src[i] && at(dst, i) == dst[i]
		// @ assert at(src, i) == old(at(src, i)) && at(dst, i) == old(at(dst, i))
		if src[i] > dst[i] {
			// @ assert old(at(src, i)) > old(at(dst, i))
			dst[i] = src[i]
			changed = true
		}
		// @ assert at(dst, i) == larger(old(at(dst, i)), old(at(src, i)))
		i++
	}
	return changed
}
