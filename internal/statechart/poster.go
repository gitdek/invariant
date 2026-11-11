package statechart

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

// PosterOptions chooses how a poster draws a checked chart.
type PosterOptions struct {
	// Title names the project the chart belongs to.
	Title string
	// Animated adds motion: light flowing along arrows, a pulsing start, a
	// drifting background and a token replaying one real behavior. Without it
	// the poster is still, for readers who prefer reduced motion.
	Animated bool
}

// Poster draws a checked chart for people to read, top to bottom: glass
// state cards colored by role, curves whose width follows each arrow's
// concrete steps, and steps that stay in a state as chips inside it. When
// animated, a token replays one real behavior made of the concrete steps the
// chart retains. The complete report stays in the evidence image and the
// record; a poster is presentation only and adds no claim. Like SVG, it
// validates the result first and draws nothing for an inconsistent one.
func (c *Result) Poster(o PosterOptions) (string, error) {
	if err := validateSVG(c); err != nil {
		return "", err
	}
	if err := validateSVGText(reflect.ValueOf(o.Title)); err != nil {
		return "", fmt.Errorf("statechart poster: title: %w", err)
	}
	return newPoster(c, o).draw(), nil
}

const (
	posterNodeW    = 290.0
	posterVirtualW = 36.0
	posterLayerGap = 168.0
	posterNodeGap  = 64.0
	posterPad      = 56.0
	posterFont     = "ui-sans-serif, -apple-system, BlinkMacSystemFont, 'Segoe UI', Inter, Roboto, sans-serif"
	posterMono     = "ui-monospace, 'SF Mono', Menlo, Consolas, monospace"
)

// Each role has a bright color for strokes and text, and a deep one for
// the bottom of its card's gradient.
var posterRoles = []struct{ name, bright, deep string }{
	{"start", "#22d3ee", "#0e7490"},
	{"state", "#818cf8", "#4338ca"},
	{"end", "#c084fc", "#7e22ce"},
	{"bad", "#fb7185", "#be123c"},
}

func posterColor(role string) string {
	for _, r := range posterRoles {
		if r.name == role {
			return r.bright
		}
	}
	return posterRoles[1].bright
}

type point struct{ x, y float64 }

// A curve is cubic Bézier segments, each from its first point to its last.
type curve [][4]point

type posterBox struct{ x, y, w, h float64 }

type posterEdge struct {
	arrow Arrow
	segs  curve
	back  bool
}

type poster struct {
	c       *Result
	o       PosterOptions
	states  []State
	byID    map[string]State
	moves   []Arrow
	loops   map[string][]Arrow
	roles   map[string]string
	lines   map[string][]string
	box     map[string]posterBox
	edges   []posterEdge
	w, h    float64
	bottom  float64        // the last row's lower edge
	layer   map[string]int // each state's layer, for the entrance
	maxStep int
	tour    []Arrow
	token   string
	dur     float64
	arrive  map[string][]float64
}

func newPoster(c *Result, o PosterOptions) *poster {
	p := &poster{c: c, o: o, states: c.States, byID: map[string]State{}, loops: map[string][]Arrow{},
		roles: map[string]string{}, lines: map[string][]string{}, box: map[string]posterBox{}, arrive: map[string][]float64{}}
	for _, s := range c.States {
		p.byID[s.ID] = s
	}
	outgoing := map[string]int{}
	for _, a := range c.Arrows {
		if a.From == a.To {
			p.loops[a.From] = append(p.loops[a.From], a)
		} else {
			p.moves = append(p.moves, a)
			outgoing[a.From]++
		}
		p.maxStep = max(p.maxStep, a.Count)
	}
	for _, s := range c.States {
		switch {
		case len(s.ViolatedRules) > 0:
			p.roles[s.ID] = "bad"
		case len(s.InitialMembers) > 0:
			p.roles[s.ID] = "start"
		case outgoing[s.ID] == 0:
			p.roles[s.ID] = "end"
		default:
			p.roles[s.ID] = "state"
		}
	}
	p.layout()
	if o.Animated {
		p.replay()
	}
	return p
}

func posterWrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, line)
			line = w
		} else if line == "" {
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return []string{text}
	}
	return lines
}

func posterTextW(s string, size, k float64) float64 {
	return float64(float64(utf8.RuneCountInString(s)) * size * k)
}

// FMA preserves the original arm64 rounding. Explicit float64 conversions
// keep other products rounded, even across assignments and inlined helpers.
func cubicAt(s [4]point, t float64) point {
	u := 1 - t
	return point{
		math.FMA(t*t*t, s[3].x, math.FMA(3*u*t*t, s[2].x, math.FMA(3*u*u*t, s[1].x, float64(u*u*u*s[0].x)))),
		math.FMA(t*t*t, s[3].y, math.FMA(3*u*t*t, s[2].y, math.FMA(3*u*u*t, s[1].y, float64(u*u*u*s[0].y)))),
	}
}

// posterHypot preserves Go 1.27.3's arm64 math.Hypot arithmetic. Other
// processors and fusion-disabled builds round 1+q*q differently in math.
func posterHypot(p, q float64) float64 {
	p, q = math.Abs(p), math.Abs(q)
	switch {
	case math.IsInf(p, 1) || math.IsInf(q, 1):
		return math.Inf(1)
	case math.IsNaN(p) || math.IsNaN(q):
		return math.NaN()
	}
	if p < q {
		p, q = q, p
	}
	if p == 0 {
		return 0
	}
	q /= p
	return float64(p * math.Sqrt(math.FMA(q, q, 1)))
}

func segLen(s [4]point) float64 {
	total, prev := 0.0, s[0]
	for i := 1; i <= 24; i++ {
		q := cubicAt(s, float64(i)/24)
		total += posterHypot(q.x-prev.x, q.y-prev.y)
		prev = q
	}
	return total
}

func (cv curve) d() string {
	var b strings.Builder
	fmt.Fprintf(&b, "M%.1f %.1f", cv[0][0].x, cv[0][0].y)
	for _, s := range cv {
		fmt.Fprintf(&b, " C%.1f %.1f %.1f %.1f %.1f %.1f", s[1].x, s[1].y, s[2].x, s[2].y, s[3].x, s[3].y)
	}
	return b.String()
}

// layout layers the states by their longest path from the initial states,
// threads long arrows through a slot in every layer they skip, orders each
// layer by its neighbors and places the cards, ports and curves.
func (p *poster) layout() {
	ids := make([]string, len(p.states))
	for i, s := range p.states {
		ids[i] = s.ID
	}
	var initial []string
	for _, s := range p.states {
		if len(s.InitialMembers) > 0 {
			initial = append(initial, s.ID)
		}
	}
	out := map[string][]string{}
	for _, a := range p.moves {
		out[a.From] = append(out[a.From], a.To)
	}
	mark, back := map[string]int{}, map[[2]string]bool{}
	var order []string
	var dfs func(string)
	dfs = func(u string) {
		mark[u] = 1
		for _, v := range out[u] {
			if mark[v] == 1 {
				back[[2]string{u, v}] = true
			} else if mark[v] == 0 {
				dfs(v)
			}
		}
		mark[u] = 2
		order = append(order, u)
	}
	for _, u := range append(append([]string{}, initial...), ids...) {
		if mark[u] == 0 {
			dfs(u)
		}
	}
	depth := map[string]int{}
	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		for _, v := range out[u] {
			if !back[[2]string{u, v}] {
				depth[v] = max(depth[v], depth[u]+1)
			}
		}
	}
	deepest := 0
	p.layer = map[string]int{}
	for _, u := range ids {
		deepest = max(deepest, depth[u])
		p.layer[u] = depth[u]
	}
	rows := make([][]string, deepest+1)
	for _, u := range ids {
		rows[depth[u]] = append(rows[depth[u]], u)
	}
	size := map[string][2]float64{}
	for _, s := range p.states {
		lines := posterWrap(s.Label, 26)
		p.lines[s.ID] = lines
		h := math.FMA(float64(len(lines)), 21, 36) + 20
		if chips := p.loops[s.ID]; len(chips) > 0 {
			h += 14
			for _, a := range chips {
				h += math.FMA(float64(len(posterWrap(a.Label, 30))), 17, 10)
			}
		}
		size[s.ID] = [2]float64{posterNodeW, h}
	}
	chains := make([][]string, len(p.moves))
	virtual := 0
	for i, a := range p.moves {
		if back[[2]string{a.From, a.To}] {
			continue
		}
		for d := depth[a.From] + 1; d < depth[a.To]; d++ {
			name := fmt.Sprintf("~v%d", virtual)
			virtual++
			rows[d] = append(rows[d], name)
			size[name] = [2]float64{posterVirtualW, 0}
			chains[i] = append(chains[i], name)
		}
	}
	preds, succs := map[string][]string{}, map[string][]string{}
	for i, a := range p.moves {
		if back[[2]string{a.From, a.To}] {
			continue
		}
		seq := append(append([]string{a.From}, chains[i]...), a.To)
		for k := 0; k+1 < len(seq); k++ {
			preds[seq[k+1]] = append(preds[seq[k+1]], seq[k])
			succs[seq[k]] = append(succs[seq[k]], seq[k+1])
		}
	}
	for sweep := 0; sweep < 8; sweep++ {
		pos := map[string]float64{}
		for _, row := range rows {
			for k, u := range row {
				pos[u] = float64(k)
			}
		}
		nb, sweepRows := preds, rows[1:]
		if sweep%2 == 1 {
			nb = succs
			sweepRows = nil
			for k := len(rows) - 2; k >= 0; k-- {
				sweepRows = append(sweepRows, rows[k])
			}
		}
		for _, row := range sweepRows {
			key := func(u string) float64 {
				if len(nb[u]) == 0 {
					return pos[u]
				}
				sum := 0.0
				for _, n := range nb[u] {
					sum += pos[n]
				}
				return sum / float64(len(nb[u]))
			}
			sort.SliceStable(row, func(i, j int) bool { return key(row[i]) < key(row[j]) })
		}
	}
	inner := 420.0
	widths := make([]float64, len(rows))
	for k, row := range rows {
		for i, u := range row {
			widths[k] += size[u][0]
			if i > 0 {
				widths[k] += posterNodeGap
			}
		}
		inner = max(inner, widths[k])
	}
	p.w = math.Floor(max(inner+posterPad*2+64, 600))
	y := 168.0
	for k, row := range rows {
		x := (p.w - widths[k]) / 2
		hmax := 40.0
		for _, u := range row {
			hmax = max(hmax, size[u][1])
		}
		for _, u := range row {
			w, h := size[u][0], size[u][1]
			if strings.HasPrefix(u, "~") {
				p.box[u] = posterBox{x, y, w, hmax}
			} else {
				p.box[u] = posterBox{x, math.FMA(hmax-h, 0.5, y), w, h}
			}
			x += w + posterNodeGap
		}
		y += hmax + posterLayerGap
	}
	p.bottom = y - posterLayerGap

	// Each arrow leaves and enters at a port of its own, ordered by where its
	// next and previous waypoints lie.
	type portUse struct {
		x     float64
		label string
		i     int
	}
	outPorts, inPorts := map[string][]portUse{}, map[string][]portUse{}
	waypoints := func(i int) []string {
		return append(append([]string{p.moves[i].From}, chains[i]...), p.moves[i].To)
	}
	for i, a := range p.moves {
		if back[[2]string{a.From, a.To}] {
			continue
		}
		wp := waypoints(i)
		next, prev := p.box[wp[1]], p.box[wp[len(wp)-2]]
		outPorts[a.From] = append(outPorts[a.From], portUse{math.FMA(next.w, 0.5, next.x), a.Label, i})
		inPorts[a.To] = append(inPorts[a.To], portUse{math.FMA(prev.w, 0.5, prev.x), a.Label, i})
	}
	for _, ports := range []map[string][]portUse{outPorts, inPorts} {
		for _, list := range ports {
			sort.SliceStable(list, func(i, j int) bool {
				if list[i].x != list[j].x {
					return list[i].x < list[j].x
				}
				if list[i].label != list[j].label {
					return list[i].label < list[j].label
				}
				return list[i].i < list[j].i
			})
		}
	}
	port := func(u string, i int, ports map[string][]portUse, top bool) point {
		b := p.box[u]
		list := ports[u]
		k := 0
		for n, e := range list {
			if e.i == i {
				k = n
			}
		}
		x := b.x + 28 + float64((b.w-56)*float64(k+1)/float64(len(list)+1))
		if top {
			return point{x, b.y}
		}
		return point{x, b.y + b.h}
	}
	backs := 0
	for i, a := range p.moves {
		if back[[2]string{a.From, a.To}] {
			f, t := p.box[a.From], p.box[a.To]
			p0, p3 := point{f.x + f.w, math.FMA(f.h, 0.5, f.y)}, point{t.x + t.w, math.FMA(t.h, 0.5, t.y)}
			bulge := math.FMA(34, float64(backs), 110)
			backs++
			p.edges = append(p.edges, posterEdge{a, curve{{p0, {p0.x + bulge, p0.y}, {p3.x + bulge, p3.y}, p3}}, true})
			continue
		}
		pts := []point{port(a.From, i, outPorts, false)}
		for _, name := range chains[i] {
			v := p.box[name]
			pts = append(pts, point{math.FMA(v.w, 0.5, v.x), v.y}, point{math.FMA(v.w, 0.5, v.x), v.y + v.h})
		}
		pts = append(pts, port(a.To, i, inPorts, true))
		var cv curve
		for k := 0; k+1 < len(pts); k++ {
			p0, p3 := pts[k], pts[k+1]
			dy := max(40, (p3.y-p0.y)*0.5)
			cv = append(cv, [4]point{p0, {p0.x, p0.y + dy}, {p3.x, p3.y - dy}, p3})
		}
		p.edges = append(p.edges, posterEdge{a, cv, false})
	}
	for _, e := range p.edges {
		if !e.back {
			continue
		}
		far := 0.0
		for t := 0.0; t <= 1; t += 0.05 {
			far = max(far, cubicAt(e.segs[0], t).x)
		}
		p.w = max(p.w, math.Ceil(far+14+posterLabelW(e.arrow)+posterPad))
	}
}

func posterLabelW(a Arrow) float64 {
	label := posterTextW(a.Label, 13, 0.56)
	count := float64(utf8.RuneCountInString(fmt.Sprintf("×%d", a.Count))) * 12
	return math.FMA(count, 0.62, label) + 36
}

type posterWalk struct {
	at     string
	path   []Arrow
	states []string
	arrows []string
}

func addSorted(list []string, v string) []string {
	k := sort.SearchStrings(list, v)
	if k < len(list) && list[k] == v {
		return list
	}
	out := append(append(append([]string{}, list[:k]...), v), list[k:]...)
	return out
}

func arrowKey(a Arrow) string { return a.From + "\x00" + a.Label + "\x00" + a.To }

// replay finds one real behavior to animate: concrete steps the chart
// retains, chained from an initial concrete state. A small beam search keeps
// the walks that visit the most chart states, then the most chart arrows,
// and the shortest of those.
func (p *poster) replay() {
	owner := map[string]string{}
	for _, s := range p.states {
		for _, m := range s.Members {
			owner[m] = s.ID
		}
	}
	type option struct {
		to string
		a  Arrow
	}
	steps := map[string][]option{}
	for _, a := range p.c.Arrows {
		for _, st := range a.Members {
			steps[st.From] = append(steps[st.From], option{st.To, a})
		}
	}
	for _, list := range steps {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].a.Label != list[j].a.Label {
				return list[i].a.Label < list[j].a.Label
			}
			return list[i].to < list[j].to
		})
	}
	var starts []string
	for _, s := range p.states {
		starts = append(starts, s.InitialMembers...)
	}
	if len(starts) == 0 {
		return
	}
	sort.Strings(starts)
	better := func(a, b posterWalk) bool {
		if len(a.states) != len(b.states) {
			return len(a.states) > len(b.states)
		}
		if len(a.arrows) != len(b.arrows) {
			return len(a.arrows) > len(b.arrows)
		}
		return len(a.path) < len(b.path)
	}
	walks := []posterWalk{{at: starts[0], states: []string{owner[starts[0]]}}}
	best := walks[0]
	for depth := 0; depth < 12; depth++ {
		var next []posterWalk
		for _, w := range walks {
			for _, o := range steps[w.at] {
				key := arrowKey(o.a)
				k := sort.SearchStrings(w.arrows, key)
				seen := k < len(w.arrows) && w.arrows[k] == key
				if o.a.From == o.a.To && seen {
					continue
				}
				next = append(next, posterWalk{o.to, append(append([]Arrow{}, w.path...), o.a), addSorted(w.states, owner[o.to]), addSorted(w.arrows, key)})
			}
		}
		if len(next) == 0 {
			break
		}
		sort.SliceStable(next, func(i, j int) bool { return better(next[i], next[j]) })
		walks = nil
		seen := map[string]bool{}
		for _, w := range next {
			sig := w.at + "\x01" + strings.Join(w.states, "\x00") + "\x01" + strings.Join(w.arrows, "\x00")
			if !seen[sig] {
				seen[sig] = true
				walks = append(walks, w)
			}
			if len(walks) >= 64 {
				break
			}
		}
		if better(walks[0], best) {
			best = walks[0]
		}
	}
	tour := best.path
	for len(tour) > 1 && tour[len(tour)-1].From == tour[len(tour)-1].To {
		tour = tour[:len(tour)-1]
	}
	if len(tour) == 0 {
		return
	}
	p.tour = tour
	byKey := map[string]posterEdge{}
	for _, e := range p.edges {
		byKey[arrowKey(e.arrow)] = e
	}
	first := p.box[tour[0].From]
	cur := point{math.FMA(first.w, 0.5, first.x), first.y + 28}
	var b strings.Builder
	fmt.Fprintf(&b, "M%.1f %.1f", cur.x, cur.y)
	total := 0.0
	type mark struct {
		t     float64
		state string
	}
	marks := []mark{{0, tour[0].From}}
	for _, a := range tour {
		if e, ok := byKey[arrowKey(a)]; ok {
			p0 := e.segs[0][0]
			fmt.Fprintf(&b, " L%.1f %.1f", p0.x, p0.y)
			total += posterHypot(p0.x-cur.x, p0.y-cur.y)
			for _, s := range e.segs {
				fmt.Fprintf(&b, " C%.1f %.1f %.1f %.1f %.1f %.1f", s[1].x, s[1].y, s[2].x, s[2].y, s[3].x, s[3].y)
				total += segLen(s)
			}
			t := p.box[a.To]
			dest := point{math.FMA(t.w, 0.5, t.x), t.y + 28}
			end := e.segs[len(e.segs)-1][3]
			fmt.Fprintf(&b, " L%.1f %.1f", dest.x, dest.y)
			total += posterHypot(dest.x-end.x, dest.y-end.y)
			cur = dest
		} else {
			// A step that stays: a small orbit in the card's corner.
			f := p.box[a.From]
			c0 := point{f.x + f.w - 36, f.y + 28}
			fmt.Fprintf(&b, " L%.1f %.1f a12 12 0 1 1 0.1 0", c0.x, c0.y)
			total += posterHypot(c0.x-cur.x, c0.y-cur.y) + 2*math.Pi*12
			cur = c0
		}
		marks = append(marks, mark{total, a.To})
	}
	p.token = b.String()
	p.dur = math.FMA(1.7, float64(len(tour)), 2.5)
	for _, m := range marks {
		f := 0.0
		if total > 0 {
			f = m.t / total
		}
		p.arrive[m.state] = append(p.arrive[m.state], f)
	}
}

// highlight lights a card each time the replay reaches its state.
func (p *poster) highlight(id string) string {
	arrivals := append([]float64{}, p.arrive[id]...)
	sort.Float64s(arrivals)
	values, times := []string{"0"}, []float64{0}
	for _, f := range arrivals {
		a0, a1, a2 := max(0, f-0.03), min(1, f+0.005), min(1, f+0.12)
		if a0 > times[len(times)-1] && a2 > a1 && a1 > a0 {
			values = append(values, "0", "1", "0")
			times = append(times, a0, a1, a2)
		}
	}
	if len(times) < 3 {
		return ""
	}
	if times[len(times)-1] < 1 {
		values = append(values, "0")
		times = append(times, 1)
	}
	keys := make([]string, len(times))
	for i, t := range times {
		keys[i] = fmt.Sprintf("%.3f", t)
	}
	return fmt.Sprintf(`<animate attributeName="opacity" dur="%.1fs" repeatCount="indefinite" values="%s" keyTimes="%s"/>`, p.dur, strings.Join(values, ";"), strings.Join(keys, ";"))
}

// Entrances start with the document and hold their first frame until their
// delay, so the static attributes stay the finished chart: a viewer that
// doesn't animate SVG still sees every card and arrow.
func riseIn(delay float64) string {
	total := delay + 0.7
	k := delay / total
	return fmt.Sprintf(`<animate attributeName="opacity" values="0;0;1" keyTimes="0;%.3f;1" dur="%.2fs" fill="freeze"/><animateTransform attributeName="transform" type="translate" values="0 14;0 14;0 0" keyTimes="0;%.3f;1" dur="%.2fs" fill="freeze"/>`, k, total, k, total)
}

func drawIn(delay float64) string {
	total := delay + 0.9
	k := delay / total
	return fmt.Sprintf(`<animate attributeName="stroke-dasharray" values="1;1" dur="%.2fs" fill="freeze"/><animate attributeName="stroke-dashoffset" values="1;1;0" keyTimes="0;%.3f;1" dur="%.2fs" fill="freeze"/>`, total, k, total)
}

func (p *poster) strokeW(count int) float64 {
	return 1.7 + float64(3.4*posterLog1p(count)/posterLog1p(max(p.maxStep, 1)))
}

// Adapted from Go's src/math/log1p.go:
// Copyright 2010 The Go Authors. All rights reserved.
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
// * Redistributions of source code must retain the above copyright notice,
//   this list of conditions and the following disclaimer.
// * Redistributions in binary form must reproduce the above copyright
//   notice, this list of conditions and the following disclaimer in the
//   documentation and/or other materials provided with the distribution.
// * Neither the name of Google LLC nor the names of its contributors may
//   be used to endorse or promote products derived from this software
//   without specific prior written permission.
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
// ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
// LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
// CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
// SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
// INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
// CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
// ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.
//
// The original algorithm and constants carry this notice:
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this software is freely
// granted, provided that this notice is preserved.

// posterLog1p preserves Go 1.27.3's arm64 math.Log1p arithmetic for
// nonnegative integer counts. Only zero and the argument-reduction branches
// are reachable for these inputs. Rounding outside math.Log1p cannot control
// the fusion inside its polynomial and reconstruction.
func posterLog1p(count int) float64 {
	const (
		ln2Hi = 6.93147180369123816490e-01
		ln2Lo = 1.90821492927058770002e-10
		lp1   = 6.666666666666735130e-01
		lp2   = 3.999999999940941908e-01
		lp3   = 2.857142874366239149e-01
		lp4   = 2.222219843214978396e-01
		lp5   = 1.818357216161805012e-01
		lp6   = 1.531383769920937332e-01
		lp7   = 1.479819860511658591e-01
	)
	if count == 0 {
		return 0
	}
	x := float64(count)
	u, c := x, 0.0
	if x < 1<<53 {
		u = 1 + x
		c = (1 - (u - x)) / u
	}
	iu := math.Float64bits(u)
	k := int((iu >> 52) - 1023)
	iu &= 0x000fffffffffffff
	if iu < 0x0006a09e667f3bcd {
		u = math.Float64frombits(iu | 0x3ff0000000000000)
	} else {
		k++
		u = math.Float64frombits(iu | 0x3fe0000000000000)
		iu = (0x0010000000000000 - iu) >> 2
	}
	f := u - 1
	hfsq := float64(0.5 * f * f)
	if iu == 0 {
		if f == 0 {
			c = math.FMA(float64(k), ln2Lo, c)
			return math.FMA(float64(k), ln2Hi, c)
		}
		r := math.FMA(-0.66666666666666666, f, 1)
		return math.FMA(float64(k), ln2Hi, -(math.FMA(hfsq, r, -math.FMA(float64(k), ln2Lo, c)) - f))
	}
	s := f / (2 + f)
	z := float64(s * s)
	r := math.FMA(z, lp7, lp6)
	r = math.FMA(z, r, lp5)
	r = math.FMA(z, r, lp4)
	r = math.FMA(z, r, lp3)
	r = math.FMA(z, r, lp2)
	r = math.FMA(z, r, lp1)
	r = math.FMA(z, r, hfsq)
	r = math.FMA(s, r, math.FMA(float64(k), ln2Lo, c))
	return math.FMA(float64(k), ln2Hi, -(math.FMA(0.5*f, f, -r) - f))
}

func plural(n int64, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// subtitle uses the final width, including room for returning arrows. Only
// whole items wrap; an item too wide for a line keeps a literal rune prefix.
func (p *poster) subtitle() []string {
	items := []string{"Reader statechart", plural(p.c.Report.Counts["tlc_states"], "checked state")}
	names := make([]string, 0, len(p.c.Report.Source.Config.Constants))
	for name := range p.c.Report.Source.Config.Constants {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		items = append(items, name+" = "+p.c.Report.Source.Config.Constants[name])
	}
	width := int((p.w - 2*posterPad) / (14.5 * 0.53))
	var lines []string
	line := ""
	for _, item := range items {
		if utf8.RuneCountInString(item) > width {
			item = string([]rune(item)[:width-1]) + "…"
		}
		if line != "" && utf8.RuneCountInString(line)+3+utf8.RuneCountInString(item) > width {
			lines = append(lines, line)
			line = item
		} else if line == "" {
			line = item
		} else {
			line += " · " + item
		}
	}
	return append(lines, line)
}

func (p *poster) draw() string {
	var b strings.Builder
	add := func(format string, args ...any) { fmt.Fprintf(&b, format, args...); b.WriteByte('\n') }
	W := p.w
	subtitle := p.subtitle()
	const subtitleGap = 22.0
	// The conversion rounds the product here, so no processor fuses it into
	// the sums below (#792).
	extra := float64(subtitleGap * float64(len(subtitle)-1))
	animated := p.o.Animated
	explain := "Arrows are checked TLC steps, thicker for more concrete steps; ↻ marks steps that stay in a state."
	tail := "Bounded model, explored completely; it adds no proof claim."
	if n := len(p.tour); n > 0 {
		tail = fmt.Sprintf("The moving light replays one real behavior from TLC's graph, %s long. ", plural(int64(n), "step")) + tail
	}
	explainLines := posterWrap(explain, int((W-2*posterPad)/(13*0.53)))
	tailLines := posterWrap(tail, int((W-2*posterPad)/(12.5*0.53)))
	sep := p.bottom + 70
	H := math.Ceil(math.FMA(19, float64(len(tailLines)), math.FMA(20, float64(len(explainLines)), sep+28+30)+4) + 22 + extra)
	p.h = H
	counts := p.c.Report.Counts
	add(`<svg xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="poster-title poster-desc" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" font-family="%s">`, W, H, W, H, svgEscape(posterFont))
	add(`<title id="poster-title">%s: checked reader statechart</title>`, svgEscape(p.o.Title))
	add(`<desc id="poster-desc">%s and %s over the %s TLC explored completely at the checked bounds. It adds no proof claim.</desc>`,
		plural(int64(len(p.states)), "reader state"), plural(int64(len(p.c.Arrows)), "arrow"), plural(counts["tlc_states"], "concrete state"))
	add(`<defs>`)
	add(`<linearGradient id="poster-bg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#090d1c"/><stop offset="1" stop-color="#141a36"/></linearGradient>`)
	for _, a := range []struct {
		id, color string
		opacity   float64
	}{{"poster-aurora-1", "#22d3ee", 0.32}, {"poster-aurora-2", "#a855f7", 0.30}, {"poster-aurora-3", "#6366f1", 0.24}} {
		add(`<radialGradient id="%s"><stop offset="0" stop-color="%s" stop-opacity="%.2f"/><stop offset="1" stop-color="%s" stop-opacity="0"/></radialGradient>`, a.id, a.color, a.opacity, a.color)
	}
	add(`<pattern id="poster-dots" width="26" height="26" patternUnits="userSpaceOnUse"><circle cx="1.5" cy="1.5" r="1.2" fill="#ffffff" fill-opacity="0.05"/></pattern>`)
	add(`<filter id="poster-glow" x="-50%%" y="-50%%" width="200%%" height="200%%"><feGaussianBlur stdDeviation="9" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>`)
	add(`<filter id="poster-shadow" x="-20%%" y="-20%%" width="140%%" height="160%%"><feDropShadow dx="0" dy="10" stdDeviation="14" flood-color="#000000" flood-opacity="0.45"/></filter>`)
	add(`<clipPath id="poster-clip"><rect width="%.0f" height="%.0f" rx="28"/></clipPath>`, W, H)
	add(`<radialGradient id="poster-token"><stop offset="0" stop-color="#ffffff"/><stop offset="0.35" stop-color="#e0f2fe"/><stop offset="1" stop-color="#22d3ee" stop-opacity="0"/></radialGradient>`)
	for _, r := range posterRoles {
		add(`<linearGradient id="poster-card-%s" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="%s" stop-opacity="0.30"/><stop offset="1" stop-color="%s" stop-opacity="0.10"/></linearGradient>`, r.name, r.bright, r.deep)
		add(`<marker id="poster-head-%s" viewBox="0 0 12 12" refX="10" refY="6" markerWidth="9" markerHeight="9" orient="auto-start-reverse"><path d="M1 1 L11 6 L1 11 Q4 6 1 1 Z" fill="%s"/></marker>`, r.name, r.bright)
	}
	for i, e := range p.edges {
		p0, p3 := e.segs[0][0], e.segs[len(e.segs)-1][3]
		add(`<linearGradient id="poster-edge-%d" gradientUnits="userSpaceOnUse" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"><stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient>`,
			i, p0.x, p0.y, p3.x, p3.y+0.01, posterColor(p.roles[e.arrow.From]), posterColor(p.roles[e.arrow.To]))
	}
	add(`</defs>`)

	// Background: a night gradient, a drifting aurora and a fine dot grid.
	add(`<rect width="%.0f" height="%.0f" rx="28" fill="url(#poster-bg)"/>`, W, H)
	add(`<g clip-path="url(#poster-clip)">`)
	for i, a := range []struct {
		id         string
		fx, fy, rr float64
	}{{"poster-aurora-1", 0.15, 0.2, 0.45}, {"poster-aurora-2", 0.85, 0.35, 0.5}, {"poster-aurora-3", 0.5, 0.9, 0.55}} {
		motion := ""
		if animated {
			dx, dy := 70, 50
			if i%2 == 1 {
				dx, dy = -60, 70
			}
			motion = fmt.Sprintf(`<animateTransform attributeName="transform" type="translate" values="0 0;%d %d;0 0" dur="%ds" repeatCount="indefinite" calcMode="spline" keyTimes="0;0.5;1" keySplines="0.45 0 0.55 1;0.45 0 0.55 1"/>`, dx, dy, 19+6*i)
		}
		add(`<circle cx="%.0f" cy="%.0f" r="%.0f" fill="url(#%s)">%s</circle>`, W*a.fx, H*a.fy, max(W, H)*a.rr, a.id, motion)
	}
	// A faint star field, placed by a fixed sequence so the same chart
	// always draws the same sky.
	seed := uint32(2166136261)
	next := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(float64(seed>>8) / float64(1<<24))
	}
	for i := 0; i < int(W*H/9000); i++ {
		x, y, r := math.FMA(next(), W-24, 12), math.FMA(next(), H-24, 12), math.FMA(next(), 1.1, 0.6)
		twinkle := ""
		if animated {
			d := math.FMA(next(), 5, 2.5)
			twinkle = fmt.Sprintf(`<animate attributeName="opacity" values="0.15;0.85;0.15" dur="%.1fs" begin="-%.1fs" repeatCount="indefinite"/>`, d, next()*d)
		}
		add(`<circle cx="%.1f" cy="%.1f" r="%.2f" fill="#e0e7ff" opacity="0.35">%s</circle>`, x, y, r, twinkle)
	}
	add(`</g>`)
	add(`<rect width="%.0f" height="%.0f" rx="28" fill="url(#poster-dots)"/>`, W, H)
	add(`<rect x="0.5" y="0.5" width="%.0f" height="%.0f" rx="28" fill="none" stroke="#ffffff" stroke-opacity="0.08"/>`, W-1, H-1)

	// Header.
	title := p.o.Title
	if title != "" {
		r, n := utf8.DecodeRuneInString(title)
		title = strings.ToUpper(string(r)) + title[n:]
	}
	add(`<text x="%.0f" y="74" fill="#f8fafc" font-size="32" font-weight="700" letter-spacing="-0.6">%s</text>`, posterPad, svgEscape(title))
	for i, line := range subtitle {
		add(`<text x="%.0f" y="%.0f" fill="#94a3b8" font-size="14.5">%s</text>`, posterPad, math.FMA(subtitleGap, float64(i), 104), svgEscape(line))
	}
	bx := W - posterPad - 214
	pulse := ""
	if animated {
		pulse = `<animate attributeName="opacity" values="1;0.3;1" dur="2.4s" repeatCount="indefinite"/>`
	}
	add(`<rect x="%.0f" y="50" width="214" height="34" rx="17" fill="#10b981" fill-opacity="0.12" stroke="#34d399" stroke-opacity="0.45"/>`, bx)
	add(`<circle cx="%.0f" cy="67" r="5" fill="#34d399">%s</circle>`, bx+22, pulse)
	add(`<text x="%.0f" y="72" fill="#a7f3d0" font-size="13" font-weight="600">Complete TLC exploration</text>`, bx+36)

	// Translate the whole drawing, including its replay and legend, so the
	// extra header lines cannot change routes or label placement.
	if extra > 0 {
		add(`<g transform="translate(0 %.0f)">`, extra)
	}

	// Arrows between states.
	for i, e := range p.edges {
		d := e.segs.d()
		sw := p.strokeW(e.arrow.Count)
		add(`<g data-from="%s" data-to="%s" data-label="%s"><title>%s: %s</title>`, svgEscape(e.arrow.From), svgEscape(e.arrow.To), svgEscape(e.arrow.Label), svgEscape(e.arrow.Label), plural(int64(e.arrow.Count), "concrete step"))
		add(`<path d="%s" fill="none" stroke="url(#poster-edge-%d)" stroke-opacity="0.32" stroke-width="%.1f" stroke-linecap="round" filter="url(#poster-glow)"/>`, d, i, sw+5)
		if animated {
			add(`<path d="%s" fill="none" stroke="url(#poster-edge-%d)" stroke-width="%.1f" stroke-linecap="round" marker-end="url(#poster-head-%s)" pathLength="1">%s</path>`, d, i, sw, p.roles[e.arrow.To], drawIn(math.FMA(0.22, float64(p.layer[e.arrow.From]), 0.35)))
		} else {
			add(`<path d="%s" fill="none" stroke="url(#poster-edge-%d)" stroke-width="%.1f" stroke-linecap="round" marker-end="url(#poster-head-%s)"/>`, d, i, sw, p.roles[e.arrow.To])
		}
		if animated {
			add(`<path d="%s" fill="none" stroke="#ffffff" stroke-opacity="0.8" stroke-width="%.1f" stroke-linecap="round" stroke-dasharray="1.5 18"><animate attributeName="stroke-dashoffset" values="39;0" dur="1.5s" repeatCount="indefinite"/></path>`, d, max(1.7, sw*0.55))
		}
		add(`</g>`)
	}

	// Label pills, clear of the cards and of each other.
	var obstacles, placed []posterBox
	for u, bx := range p.box {
		if !strings.HasPrefix(u, "~") {
			obstacles = append(obstacles, posterBox{bx.x - 6, bx.y - 6, bx.w + 12, bx.h + 12})
		}
	}
	sort.Slice(obstacles, func(i, j int) bool {
		if obstacles[i].x != obstacles[j].x {
			return obstacles[i].x < obstacles[j].x
		}
		return obstacles[i].y < obstacles[j].y
	})
	free := func(r posterBox) bool {
		for _, o := range append(append([]posterBox{}, obstacles...), placed...) {
			if !(r.x+r.w < o.x || o.x+o.w < r.x || r.y+r.h < o.y || o.y+o.h < r.y) {
				return false
			}
		}
		return true
	}
	for _, e := range p.edges {
		count := fmt.Sprintf("×%d", e.arrow.Count)
		w := posterLabelW(e.arrow)
		h := 28.0
		order := make([]int, len(e.segs))
		for k := range order {
			order[k] = k
		}
		sort.SliceStable(order, func(i, j int) bool { return segLen(e.segs[order[i]]) > segLen(e.segs[order[j]]) })
		var cands []point
		for _, k := range order {
			for _, t := range []float64{0.5, 0.38, 0.62, 0.28, 0.72} {
				cands = append(cands, cubicAt(e.segs[k], t))
			}
		}
		// A back arc's label sits outside its arc, so it can't read as the
		// forward arrow's.
		shifts := []float64{0, math.FMA(-w, 0.5, -14), math.FMA(w, 0.5, 14)}
		if e.back {
			shifts = []float64{math.FMA(w, 0.5, 14), 0}
		}
		var spot *posterBox
	search:
		for _, dy := range []float64{0, -h - 6, h + 6, -2*h - 12, 2*h + 12} {
			for _, c := range cands {
				for _, dx := range shifts {
					r := posterBox{math.FMA(-w, 0.5, c.x) + dx, c.y - h/2 + dy, w, h}
					if r.x > 12 && r.x+w < W-12 && free(r) {
						spot = &r
						break search
					}
				}
			}
		}
		if spot == nil {
			spot = &posterBox{math.FMA(-w, 0.5, cands[0].x), cands[0].y - h/2, w, h}
		}
		placed = append(placed, *spot)
		add(`<g><rect x="%.1f" y="%.1f" width="%.1f" height="28" rx="14" fill="#0a1022" fill-opacity="0.94" stroke="#ffffff" stroke-opacity="0.16"/>`, spot.x, spot.y, w)
		add(`<text x="%.1f" y="%.1f" fill="#e2e8f0" font-size="13">%s <tspan fill="#94a3b8" font-family="%s" font-size="12">%s</tspan></text></g>`, spot.x+15, spot.y+18.5, svgEscape(e.arrow.Label), svgEscape(posterMono), count)
	}

	// State cards.
	for _, s := range p.states {
		bx := p.box[s.ID]
		role := p.roles[s.ID]
		c := posterColor(role)
		if animated {
			add(`<g data-state-id="%s"><title>%s</title>%s`, svgEscape(s.ID), svgEscape(s.Label), riseIn(float64(0.22*float64(p.layer[s.ID]))))
		} else {
			add(`<g data-state-id="%s"><title>%s</title>`, svgEscape(s.ID), svgEscape(s.Label))
		}
		add(`<rect x="%.1f" y="%.1f" width="%.0f" height="%.0f" rx="18" fill="#0a1022" filter="url(#poster-shadow)"/>`, bx.x, bx.y, bx.w, bx.h)
		add(`<rect x="%.1f" y="%.1f" width="%.0f" height="%.0f" rx="18" fill="url(#poster-card-%s)" stroke="%s" stroke-opacity="0.65" stroke-width="1.5"/>`, bx.x, bx.y, bx.w, bx.h, role, c)
		add(`<path d="M%.1f %.1f h%.1f" stroke="%s" stroke-width="2.5" stroke-linecap="round" opacity="0.9"/>`, bx.x+22, bx.y+1, bx.w-44, c)
		if animated {
			add(`<rect x="%.1f" y="%.1f" width="%.0f" height="%.0f" rx="20" fill="%s" fill-opacity="0.10" stroke="%s" stroke-width="3" opacity="0" filter="url(#poster-glow)">%s</rect>`, bx.x-3, bx.y-3, bx.w+6, bx.h+6, c, c, p.highlight(s.ID))
		}
		ty := bx.y + 34
		if len(s.InitialMembers) > 0 {
			add(`<rect x="%.1f" y="%.1f" width="64" height="22" rx="11" fill="#0a1022" stroke="%s" stroke-opacity="0.8"/><rect x="%.1f" y="%.1f" width="64" height="22" rx="11" fill="%s" fill-opacity="0.22"/><text x="%.1f" y="%.1f" fill="%s" font-size="10.5" font-weight="700" letter-spacing="1.4">START</text>`,
				bx.x+bx.w-84, bx.y-11, c, bx.x+bx.w-84, bx.y-11, c, bx.x+bx.w-69, bx.y+4, c)
		}
		for _, line := range p.lines[s.ID] {
			add(`<text x="%.1f" y="%.1f" fill="#f8fafc" font-size="16.5" font-weight="600">%s</text>`, bx.x+20, ty, svgEscape(line))
			ty += 21
		}
		caption := plural(int64(len(s.Members)), "concrete state")
		if len(s.ViolatedRules) > 0 {
			caption += " · breaks " + strings.Join(s.ViolatedRules, ", ")
		}
		add(`<text x="%.1f" y="%.1f" fill="%s" fill-opacity="0.9" font-size="12.5">%s</text>`, bx.x+20, ty+2, c, svgEscape(caption))
		ty += 16
		chips := append([]Arrow{}, p.loops[s.ID]...)
		sort.SliceStable(chips, func(i, j int) bool { return chips[i].Count > chips[j].Count })
		if len(chips) > 0 {
			ty += 8
			add(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#ffffff" stroke-opacity="0.09"/>`, bx.x+16, ty, bx.x+bx.w-16, ty)
			ty += 12
		}
		for _, a := range chips {
			lines := posterWrap(a.Label, 30)
			add(`<g data-from="%s" data-to="%s" data-label="%s"><title>%s: %s stay in this state</title>`, svgEscape(a.From), svgEscape(a.To), svgEscape(a.Label), svgEscape(a.Label), plural(int64(a.Count), "concrete step"))
			cx, cy := bx.x+30, ty+6
			spin := ""
			if animated {
				spin = fmt.Sprintf(`<animateTransform attributeName="transform" type="rotate" values="0 %.1f %.1f;360 %.1f %.1f" dur="%ds" repeatCount="indefinite"/>`, cx, cy, cx, cy, 5+a.Count%4)
			}
			add(`<path d="M%.1f %.1f a7 7 0 1 1 6.5 4.6" fill="none" stroke="%s" stroke-width="1.8" stroke-linecap="round" marker-end="url(#poster-head-%s)">%s</path>`, cx-6.5, cy+2.4, c, role, spin)
			yy := ty + 11
			for _, line := range lines {
				add(`<text x="%.1f" y="%.1f" fill="#cbd5e1" font-size="12.5">%s</text>`, bx.x+46, yy, svgEscape(line))
				yy += 17
			}
			add(`<text x="%.1f" y="%.1f" fill="#94a3b8" font-family="%s" font-size="11.5" text-anchor="end">×%d</text>`, bx.x+bx.w-18, ty+11, svgEscape(posterMono), a.Count)
			add(`</g>`)
			ty += math.FMA(float64(len(lines)), 17, 10)
		}
		add(`</g>`)
	}
	for _, s := range p.states {
		if len(s.InitialMembers) == 0 {
			continue
		}
		bx := p.box[s.ID]
		c := posterColor("start")
		add(`<g data-initial="%s"><circle cx="%.1f" cy="%.1f" r="7" fill="%s"/>`, svgEscape(s.ID), bx.x-32, bx.y+30, c)
		if animated {
			add(`<circle cx="%.1f" cy="%.1f" r="7" fill="none" stroke="%s" stroke-width="2"><animate attributeName="r" values="7;22" dur="2.2s" repeatCount="indefinite"/><animate attributeName="opacity" values="0.9;0" dur="2.2s" repeatCount="indefinite"/></circle>`, bx.x-32, bx.y+30, c)
		}
		add(`<path d="M%.1f %.1f L%.1f %.1f" stroke="%s" stroke-width="2" marker-end="url(#poster-head-start)"/></g>`, bx.x-23, bx.y+30, bx.x-4, bx.y+30, c)
	}
	if p.token != "" {
		add(`<g>`)
		// The tail follows the token a little later along the same path:
		// each copy started a little less than one cycle earlier.
		for k := 5; k >= 1; k-- {
			begin := math.FMA(-0.045, float64(k), p.dur)
			add(`<circle r="%.1f" fill="#a5f3fc" opacity="%.2f"><animateMotion dur="%.1fs" begin="-%.3fs" repeatCount="indefinite" path="%s"/></circle>`, math.FMA(-0.7, float64(k), 5.5), math.FMA(-0.08, float64(k), 0.55), p.dur, begin, p.token)
		}
		add(`<circle r="18" fill="url(#poster-token)" opacity="0.95"><animateMotion dur="%.1fs" repeatCount="indefinite" path="%s"/></circle>`, p.dur, p.token)
		add(`<circle r="4.5" fill="#ffffff"><animateMotion dur="%.1fs" repeatCount="indefinite" path="%s"/></circle>`, p.dur, p.token)
		add(`</g>`)
	}

	// Legend.
	fy := sep + 28
	add(`<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="#ffffff" stroke-opacity="0.08"/>`, posterPad, sep, W-posterPad, sep)
	lx := posterPad
	for _, r := range []struct{ role, name string }{{"start", "start"}, {"state", "state"}, {"end", "final"}, {"bad", "breaks a rule"}} {
		add(`<circle cx="%.0f" cy="%.0f" r="6" fill="%s"/><text x="%.0f" y="%.1f" fill="#cbd5e1" font-size="13">%s</text>`, lx+6, fy, posterColor(r.role), lx+18, fy+4.5, r.name)
		lx += 36 + math.Floor(posterTextW(r.name, 13, 0.56))
	}
	ly := fy + 30
	for _, line := range explainLines {
		add(`<text x="%.0f" y="%.0f" fill="#94a3b8" font-size="13">%s</text>`, posterPad, ly, svgEscape(line))
		ly += 20
	}
	ly += 4
	for _, line := range tailLines {
		add(`<text x="%.0f" y="%.0f" fill="#64748b" font-size="12.5">%s</text>`, posterPad, ly, svgEscape(line))
		ly += 19
	}
	if extra > 0 {
		add(`</g>`)
	}
	add(`</svg>`)
	return b.String()
}
