// +gobra

package ids

// unite adds the source's occupied slots without changing existing decisions.
// The tables share an ID space, so there is room for every possible union.
// @ requires len(dst) == len(src) && len(dst) <= MaxCapacity
// @ requires forall i int :: { &dst[i] } 0 <= i && i < len(dst) ==> acc(&dst[i])
// @ requires forall i int :: { &src[i] } 0 <= i && i < len(src) ==> acc(&src[i], 1/2)
// @ ensures forall i int :: { &dst[i] } 0 <= i && i < len(dst) ==> acc(&dst[i])
// @ ensures forall i int :: { &src[i] } 0 <= i && i < len(src) ==> acc(&src[i], 1/2)
// @ ensures forall i int :: 0 <= i && i < len(src) ==> src[i] == old(src[i])
// @ ensures forall i int :: 0 <= i && i < len(dst) ==> dst[i] == ((old(dst[i].Number) == 0 && src[i].Number != 0) ? src[i] : old(dst[i]))
// @ ensures changed == (exists i int :: 0 <= i && i < len(dst) && old(dst[i].Number) == 0 && src[i].Number != 0)
// @ ensures !changed ==> forall i int :: 0 <= i && i < len(dst) ==> dst[i] == old(dst[i])
func unite(dst, src []Decision) (changed bool) {
	i := 0
	// @ invariant 0 <= i && i <= len(dst)
	// @ invariant forall j int :: { &dst[j] } 0 <= j && j < len(dst) ==> acc(&dst[j])
	// @ invariant forall j int :: { &src[j] } 0 <= j && j < len(src) ==> acc(&src[j], 1/2)
	// @ invariant forall j int :: 0 <= j && j < len(src) ==> src[j] == old(src[j])
	// @ invariant forall j int :: i <= j && j < len(dst) ==> dst[j] == old(dst[j])
	// @ invariant forall j int :: 0 <= j && j < i ==> dst[j] == ((old(dst[j].Number) == 0 && src[j].Number != 0) ? src[j] : old(dst[j]))
	// @ invariant changed == (exists j int :: 0 <= j && j < i && old(dst[j].Number) == 0 && src[j].Number != 0)
	// @ decreases len(dst) - i
	for i < len(dst) {
		if dst[i].Number == 0 && src[i].Number != 0 {
			dst[i] = src[i]
			changed = true
		}
		i++
	}
	return changed
}
