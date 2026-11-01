package receipt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/verify"
)

// TLAPS's evidence is about the model alone, at every size (#360). Its row
// stands beside TLC's, and never changes what the receipt claims of the code.

func tlapsOK(r *verify.Report) bool {
	for _, t := range r.TLAPS {
		if !t.Proved {
			return false
		}
	}
	return true
}

func tlapsResult(r *verify.Report) string {
	proved := count(r.TLAPS, func(t verify.TLAPSProof) bool { return t.Proved })
	switch {
	case proved == len(r.TLAPS) && proved == 1:
		return "model invariant proved at every size (TLAPS)"
	case proved == len(r.TLAPS):
		return fmt.Sprintf("%d model invariants proved at every size (TLAPS)", proved)
	default:
		return fmt.Sprintf("%d of %d model invariants proved at every size (TLAPS)", proved, len(r.TLAPS))
	}
}

// tlapsEvidence names each invariant, how many obligations tlapm proved and
// by which backends, and the library facts the proof rests on unproved here.
func (render renderer) tlapsEvidence(r *verify.Report) string {
	var parts []string
	for _, t := range r.TLAPS {
		if !t.Proved {
			parts = append(parts, fmt.Sprintf("%s not proved", render.literal(t.Invariant)))
			continue
		}
		var by []string
		names := make([]string, 0, len(t.Provers))
		for name := range t.Provers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			by = append(by, fmt.Sprintf("%d %s", t.Provers[name], name))
		}
		s := fmt.Sprintf("%s: %d obligations", render.literal(t.Invariant), t.Obligations)
		if len(by) > 0 {
			s += " (" + strings.Join(by, ", ") + ")"
		}
		if len(t.Cites) > 0 {
			var cites []string
			for _, c := range t.Cites {
				cites = append(cites, render.literal(strings.Replace(c, " ", "!", 1)))
			}
			s += ", citing " + strings.Join(cites, ", ") + ", which TLAPS's library states"
		}
		parts = append(parts, s)
	}
	if t := r.Toolchain.TLAPS; t != nil {
		parts = append(parts, "tlapm "+render.literal(t.Version))
	}
	return strings.Join(parts, " · ")
}

// tlapsProved is the sentence the receipt adds to what it claims outside
// the bounds, when TLAPS proved an invariant there.
func (render renderer) tlapsProved(r *verify.Report) string {
	var names []string
	for _, t := range r.TLAPS {
		if t.Proved {
			names = append(names, render.literal(t.Invariant))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf(" TLAPS proves %s of the model at every size, under its own trust model; that says nothing of the code.", strings.Join(names, ", "))
}
