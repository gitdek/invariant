package dashboard

import (
	"strconv"
	"strings"
)

// Tile is one of the numbers row's six tiles, in the words the page shows
// (#152). A number of a million or more shows short, as 1.29M, and its small
// text and tooltip lead with it exactly.
type Tile struct {
	Key   string  `json:"key"`             // bad, states, stmts, bugs, proved or dec
	Label string  `json:"label"`           // what the number counts
	Shown string  `json:"shown"`           // what the tile reads: 999,999, 30/30 or 1.29M
	Small string  `json:"small"`           // its small text
	Title string  `json:"title,omitempty"` // its tooltip, which only a number shown short has
	Ems   float64 `json:"ems"`             // the most room Shown can take, in em, which the page fits to the tile
}

// Tiles are the numbers row's tiles for t, in the order the page shows them
// and in the words it has always used. The decisions tile says who made the
// calls, from who.
func Tiles(t Totals, who Who) []Tile {
	merged := "CI's gate passed on the exact head of all " + inFull(t.MergesChecked) + " pull requests the factory merged. "
	if t.BadLocks == 0 {
		merged += "All " + inFull(t.LocksChecked) + " factory locks are exactly what a person ratified."
	} else {
		merged += inFull(t.BadLocks) + " locks differ from what was ratified."
	}
	logged := "none logged in this repository yet"
	if t.Decisions > 0 {
		logged = inFull(who.Ratified) + " ratified by @gitdek, " + inFull(who.Logged) + " decided by agents as they built"
	}
	return []Tile{
		tile("bad", "merges without a green gate", "merges without a green gate.", merged, t.BadMerges),
		tile("states", "states TLC explored", "states,", "across "+inFull(t.Models)+" models, exhaustively, within the ratified bounds", t.States),
		tile("stmts", "statements people ratified", "statements,", "each pinned by hash, so the factory can't change them", t.Statements),
		tile("bugs", "planted bugs caught", "planted bugs caught,", "each one breaks an invariant, and TLC finds the step", t.BugsCaught, t.Bugs),
		tile("proved", "functions proved", "functions proved,", "by Gobra and Nagini, against contracts that restate the model", t.Proved),
		tile("dec", "decisions logged", "decisions,", logged, t.Decisions),
	}
}

// tile is the tile for n, or for n[0] of n[1], written n[0]/n[1]. Below a
// million, a number shows in full, over small. From a million it shows
// short, and the small text and the tooltip give the tile's number exactly
// first, then what it counts and the mark that joins that to small:
// 1,285,100 states, across 15 models.
func tile[N int | int64](key, label, counts, small string, n ...N) Tile {
	shown, exact, short := make([]string, len(n)), make([]string, len(n)), false
	for i, v := range n {
		exact[i], shown[i] = inFull(v), inFull(v)
		if v >= 1000000 {
			shown[i], short = inShort(int64(v)), true
		}
	}
	x := Tile{Key: key, Label: label, Shown: strings.Join(shown, "/"), Small: small}
	if short {
		x.Small = strings.Join(exact, "/") + " " + counts + " " + small
		x.Title = x.Small
	}
	x.Ems = ems(x.Shown)
	return x
}

// inFull writes n as the page writes a number in full: 1,285,100.
func inFull[N int | int64](n N) string {
	s := strconv.FormatInt(int64(n), 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// inShort writes n, a million or more, to three significant figures with no
// trailing zeros, and M, B or T: 1.29M, 1.5M, 123M or 1B.
func inShort(n int64) string {
	// Round n to its first three figures, which carries 999.5M up to 1B.
	figures := len(strconv.FormatInt(n, 10))
	scale := int64(1)
	for i := 3; i < figures; i++ {
		scale *= 10
	}
	first := n / scale
	if n%scale >= scale/2 {
		first++
	}
	if first == 1000 {
		first, figures = 100, figures+1
	}
	// Then write them in the largest unit they reach, with the figures
	// before the point that unit leaves.
	s, unit, whole := strconv.FormatInt(first, 10), "M", figures-6
	for _, u := range []string{"B", "T"} {
		if whole <= 3 {
			break
		}
		unit, whole = u, whole-3
	}
	if whole < 3 {
		return strings.TrimRight(strings.TrimRight(s[:whole]+"."+s[whole:], "0"), ".") + unit
	}
	return s + strings.Repeat("0", whole-3) + unit
}

// ems is the most room s can take, in em, in the page's font: Space Grotesk
// at weight 600 with tabular figures, or a system font in its place, with no
// credit for the page's tighter letter spacing. A figure takes at most 0.7
// em, a comma or a point 0.4, a slash 0.6, and any other sign, as M, 1. It
// counts in tenths, so the sum is exact.
func ems(s string) float64 {
	tenths := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			tenths += 7
		case r == ',' || r == '.':
			tenths += 4
		case r == '/':
			tenths += 6
		default:
			tenths += 10
		}
	}
	return float64(tenths) / 10
}
