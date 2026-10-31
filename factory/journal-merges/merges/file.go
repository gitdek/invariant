// +gobra

package merges

// The proofs here trigger on the addresses of lines, never on their values,
// and name a line before saying a file has it, so the solver always has a
// line to match.

// StartsWith says whether main's journal file is the start of the branch's:
// it has no more lines, and each is the branch's line in its place. It's
// the rule a branch's journal meets to merge (D-0103).
// @ requires len(main) <= MaxSize && len(branch) <= MaxSize
// @ requires forall k int :: { &main[k] } 0 <= k && k < len(main) ==> acc(&main[k], 1/4)
// @ requires forall k int :: { &branch[k] } 0 <= k && k < len(branch) ==> acc(&branch[k], 1/4)
// @ ensures forall k int :: { &main[k] } 0 <= k && k < len(main) ==> acc(&main[k], 1/4)
// @ ensures forall k int :: { &branch[k] } 0 <= k && k < len(branch) ==> acc(&branch[k], 1/4)
// @ ensures forall k int :: { &main[k] } 0 <= k && k < len(main) ==> main[k] == old(main[k])
// @ ensures forall k int :: { &branch[k] } 0 <= k && k < len(branch) ==> branch[k] == old(branch[k])
// @ ensures ok == (len(main) <= len(branch) && forall k int :: { &main[k] } 0 <= k && k < len(main) ==> main[k] == branch[k])
// @ decreases
func StartsWith(main, branch []Line) (ok bool) {
	return startsWith(main, len(main), branch, len(branch))
}

// startsWith says whether the first mlen lines of m are the start of the
// first blen lines of b.
// @ requires 0 <= mlen && mlen <= len(m) && len(m) <= MaxSize && 0 <= blen && blen <= len(b) && len(b) <= MaxSize
// @ requires forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ requires forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> m[k] == old(m[k])
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> b[k] == old(b[k])
// @ ensures ok == (mlen <= blen && forall k int :: { &m[k] } 0 <= k && k < mlen ==> m[k] == b[k])
// @ decreases
func startsWith(m []Line, mlen int, b []Line, blen int) (ok bool) {
	if mlen > blen {
		return false
	}
	i := 0
	// @ invariant 0 <= i && i <= mlen
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
	// @ invariant forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < len(m) ==> m[k] == old(m[k])
	// @ invariant forall k int :: { &b[k] } 0 <= k && k < len(b) ==> b[k] == old(b[k])
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < i ==> m[k] == b[k]
	// @ decreases mlen - i
	for i < mlen {
		if m[i] != b[i] {
			return false
		}
		i++
	}
	return true
}

// has says whether one of the first n lines of m is l.
// @ requires 0 <= n && n <= len(m) && len(m) <= MaxSize
// @ requires forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> m[k] == old(m[k])
// @ ensures found == (exists k int :: { &m[k] } 0 <= k && k < n && m[k] == l)
// @ decreases
func has(m []Line, n int, l Line) (found bool) {
	i := 0
	// @ invariant 0 <= i && i <= n
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < len(m) ==> m[k] == old(m[k])
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < i ==> m[k] != l
	// @ decreases n - i
	for i < n {
		if m[i] == l {
			return true
		}
		i++
	}
	return false
}

// copyLines copies every line of src into dst, which is as long. It reads
// src with the share q of its permission that it's given.
// @ requires q > 0 && len(dst) == len(src) && len(dst) <= MaxSize
// @ requires forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ requires forall k int :: { &src[k] } 0 <= k && k < len(src) ==> acc(&src[k], q)
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ ensures forall k int :: { &src[k] } 0 <= k && k < len(src) ==> acc(&src[k], q)
// @ ensures forall k int :: { &src[k] } 0 <= k && k < len(src) ==> src[k] == old(src[k])
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> dst[k] == old(src[k])
// @ decreases
func copyLines(dst, src []Line /*@, ghost q perm @*/) {
	i := 0
	// @ invariant 0 <= i && i <= len(dst)
	// @ invariant forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
	// @ invariant forall k int :: { &src[k] } 0 <= k && k < len(src) ==> acc(&src[k], q)
	// @ invariant forall k int :: { &src[k] } 0 <= k && k < len(src) ==> src[k] == old(src[k])
	// @ invariant forall k int :: { &dst[k] } 0 <= k && k < i ==> dst[k] == src[k]
	// @ decreases len(dst) - i
	for i < len(dst) {
		dst[i] = src[i]
		i++
	}
}

// copyPrefix copies the first n lines of src into dst.
// @ requires 0 <= n && n <= len(dst) && n <= len(src) && len(dst) <= MaxSize && len(src) <= MaxSize
// @ requires forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ requires forall k int :: { &src[k] } 0 <= k && k < len(src) ==> acc(&src[k], 1/4)
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ ensures forall k int :: { &src[k] } 0 <= k && k < len(src) ==> acc(&src[k], 1/4)
// @ ensures forall k int :: { &src[k] } 0 <= k && k < len(src) ==> src[k] == old(src[k])
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < n ==> dst[k] == src[k]
// @ decreases
func copyPrefix(dst, src []Line, n int) {
	i := 0
	// @ invariant 0 <= i && i <= n
	// @ invariant forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
	// @ invariant forall k int :: { &src[k] } 0 <= k && k < len(src) ==> acc(&src[k], 1/4)
	// @ invariant forall k int :: { &src[k] } 0 <= k && k < len(src) ==> src[k] == old(src[k])
	// @ invariant forall k int :: { &dst[k] } 0 <= k && k < i ==> dst[k] == src[k]
	// @ decreases n - i
	for i < n {
		dst[i] = src[i]
		i++
	}
}

// missing says, for each of the first blen lines of b, whether the first
// mlen lines of m lack it.
// @ requires 0 <= mlen && mlen <= len(m) && len(m) <= MaxSize
// @ requires 0 <= blen && blen <= len(b) && len(b) <= MaxSize
// @ requires forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ requires forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> m[k] == old(m[k])
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> b[k] == old(b[k])
// @ ensures len(kept) == blen
// @ ensures forall q int :: { &kept[q] } 0 <= q && q < len(kept) ==> acc(&kept[q])
// @ ensures forall q int :: { &kept[q] } { &b[q] } 0 <= q && q < blen ==> kept[q] == !(exists i int :: { &m[i] } 0 <= i && i < mlen && m[i] == b[q])
// @ decreases
func missing(m []Line, mlen int, b []Line, blen int) (kept []bool) {
	kept = make([]bool, blen)
	j := 0
	// @ invariant 0 <= j && j <= blen && len(kept) == blen
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
	// @ invariant forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
	// @ invariant forall q int :: { &kept[q] } 0 <= q && q < len(kept) ==> acc(&kept[q])
	// @ invariant forall k int :: { &m[k] } 0 <= k && k < len(m) ==> m[k] == old(m[k])
	// @ invariant forall k int :: { &b[k] } 0 <= k && k < len(b) ==> b[k] == old(b[k])
	// @ invariant forall q int :: { &kept[q] } { &b[q] } 0 <= q && q < j ==> kept[q] == !(exists i int :: { &m[i] } 0 <= i && i < mlen && m[i] == b[q])
	// @ decreases blen - j
	for j < blen {
		kept[j] = !has(m, mlen, b[j])
		j++
	}
	return kept
}

// appendKept writes into dst, after its first n0 lines, each of the first
// blen lines of b that kept marks, in order, and returns how many lines dst
// then holds. It records where each line it wrote came from in b, in from,
// and where each line it took went in dst, in to.
// @ requires 0 <= n0 && 0 <= blen && blen <= len(b) && len(b) <= MaxSize
// @ requires n0 + blen <= len(dst) && len(dst) <= MaxSize && len(kept) == blen
// @ requires len(from) == len(dst) && len(to) == len(b)
// @ requires forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ requires forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ requires forall q int :: { &kept[q] } 0 <= q && q < len(kept) ==> acc(&kept[q], 1/4)
// @ requires forall k int :: { &from[k] } 0 <= k && k < len(from) ==> acc(&from[k])
// @ requires forall k int :: { &to[k] } 0 <= k && k < len(to) ==> acc(&to[k])
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ ensures forall q int :: { &kept[q] } 0 <= q && q < len(kept) ==> acc(&kept[q], 1/4)
// @ ensures forall k int :: { &from[k] } 0 <= k && k < len(from) ==> acc(&from[k])
// @ ensures forall k int :: { &to[k] } 0 <= k && k < len(to) ==> acc(&to[k])
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> b[k] == old(b[k])
// @ ensures forall q int :: { &kept[q] } 0 <= q && q < len(kept) ==> kept[q] == old(kept[q])
// @ ensures n0 <= n && n <= n0 + blen
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < n0 ==> dst[k] == old(dst[k])
// @ ensures forall k int :: { &from[k] } n0 <= k && k < n ==> 0 <= from[k] && from[k] < blen
// @ ensures forall k int :: { &from[k] } n0 <= k && k < n ==> kept[from[k]]
// @ ensures forall k int :: { &dst[k] } n0 <= k && k < n ==> dst[k] == b[from[k]]
// @ ensures forall q int :: { &to[q] } 0 <= q && q < blen && kept[q] ==> n0 <= to[q] && to[q] < n
// @ ensures forall q int :: { &to[q] } 0 <= q && q < blen && kept[q] ==> dst[to[q]] == b[q]
// @ decreases
func appendKept(dst []Line, n0 int, b []Line, blen int, kept []bool, from, to []int) (n int) {
	n = n0
	j := 0
	// @ invariant 0 <= j && j <= blen && n0 <= n && n <= n0 + j
	// @ invariant forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
	// @ invariant forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
	// @ invariant forall q int :: { &kept[q] } 0 <= q && q < len(kept) ==> acc(&kept[q], 1/4)
	// @ invariant forall k int :: { &from[k] } 0 <= k && k < len(from) ==> acc(&from[k])
	// @ invariant forall k int :: { &to[k] } 0 <= k && k < len(to) ==> acc(&to[k])
	// @ invariant forall k int :: { &b[k] } 0 <= k && k < len(b) ==> b[k] == old(b[k])
	// @ invariant forall q int :: { &kept[q] } 0 <= q && q < len(kept) ==> kept[q] == old(kept[q])
	// @ invariant forall k int :: { &dst[k] } 0 <= k && k < n0 ==> dst[k] == old(dst[k])
	// @ invariant forall k int :: { &from[k] } n0 <= k && k < n ==> 0 <= from[k] && from[k] < j
	// @ invariant forall k int :: { &from[k] } n0 <= k && k < n ==> kept[from[k]]
	// @ invariant forall k int :: { &dst[k] } n0 <= k && k < n ==> dst[k] == b[from[k]]
	// @ invariant forall q int :: { &to[q] } 0 <= q && q < j && kept[q] ==> n0 <= to[q] && to[q] < n
	// @ invariant forall q int :: { &to[q] } 0 <= q && q < j && kept[q] ==> dst[to[q]] == b[q]
	// @ decreases blen - j
	for j < blen {
		if kept[j] {
			dst[n] = b[j]
			from[n] = j
			to[j] = n
			n++
		}
		j++
	}
	return n
}

// takeIn writes into dst main's first mlen lines, where they are, then each
// of the branch's first blen lines that main's don't have, in order, and
// returns how many lines dst then holds. Each line it writes after main's is
// the branch's line from[k], and each of the branch's lines that main's lack
// is dst's line to[q].
// @ requires 0 <= mlen && mlen <= len(m) && len(m) <= MaxSize
// @ requires 0 <= blen && blen <= len(b) && len(b) <= MaxSize
// @ requires mlen + blen <= len(dst) && len(dst) <= MaxSize
// @ requires len(from) == len(dst) && len(to) == len(b)
// @ requires forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ requires forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ requires forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ requires forall k int :: { &from[k] } 0 <= k && k < len(from) ==> acc(&from[k])
// @ requires forall k int :: { &to[k] } 0 <= k && k < len(to) ==> acc(&to[k])
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < len(dst) ==> acc(&dst[k])
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> acc(&m[k], 1/4)
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> acc(&b[k], 1/4)
// @ ensures forall k int :: { &from[k] } 0 <= k && k < len(from) ==> acc(&from[k])
// @ ensures forall k int :: { &to[k] } 0 <= k && k < len(to) ==> acc(&to[k])
// @ ensures forall k int :: { &m[k] } 0 <= k && k < len(m) ==> m[k] == old(m[k])
// @ ensures forall k int :: { &b[k] } 0 <= k && k < len(b) ==> b[k] == old(b[k])
// @ ensures mlen <= n && n <= mlen + blen
// @ ensures forall k int :: { &dst[k] } 0 <= k && k < mlen ==> dst[k] == m[k]
// @ ensures forall k int :: { &from[k] } mlen <= k && k < n ==> 0 <= from[k] && from[k] < blen
// @ ensures forall k int :: { &dst[k] } mlen <= k && k < n ==> dst[k] == b[from[k]]
// @ ensures forall k, i int :: { &dst[k], &m[i] } mlen <= k && k < n && 0 <= i && i < mlen ==> dst[k] != m[i]
// @ ensures forall q int :: { &to[q] } 0 <= q && q < blen && !(exists i int :: { &m[i] } 0 <= i && i < mlen && m[i] == b[q]) ==> mlen <= to[q] && to[q] < n
// @ ensures forall q int :: { &to[q] } 0 <= q && q < blen && !(exists i int :: { &m[i] } 0 <= i && i < mlen && m[i] == b[q]) ==> dst[to[q]] == b[q]
// @ decreases
func takeIn(dst []Line, m []Line, mlen int, b []Line, blen int, from, to []int) (n int) {
	copyPrefix(dst, m, mlen)
	kept := missing(m, mlen, b, blen)
	return appendKept(dst, mlen, b, blen, kept, from, to)
}
