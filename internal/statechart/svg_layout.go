package statechart

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

const (
	svgFontSize   = 14
	svgLineHeight = 22
	svgPadding    = 16
	svgMargin     = 32
	svgNodeX      = 112
	svgLaneGap    = 28
)

type svgText struct {
	lines         []string
	width, height float64
}

// Keep lines intact, including unbroken labels. A full em per ASCII rune and
// two per other rune leave room for fallback fonts without host font metrics.
func measureSVGText(s string) svgText {
	t := svgText{lines: strings.Split(s, "\n")}
	for _, line := range t.lines {
		width := 0.0
		for _, r := range line {
			switch {
			case r == '\t':
				width += 8 * svgFontSize
			case r > 127:
				width += 2 * svgFontSize
			default:
				width += svgFontSize
			}
		}
		t.width = max(t.width, width)
	}
	t.height = float64(len(t.lines)) * svgLineHeight
	return t
}

type svgBox struct {
	x, y, width, height float64
}

type svgNode struct {
	svgBox
	text svgText
}

type svgArrow struct {
	text              svgText
	fromY, toY, laneX float64
}

type svgLayout struct {
	nodes                  []svgNode
	arrows                 []svgArrow
	index                  map[string]int
	report                 []svgText
	width, height, reportY float64
}

// Every incidence has its own port, and each outgoing port reserves a whole
// label row. Vertical routes stay beyond all labels. Interval packing reuses a
// lane only when routes are disjoint, including their clearance, so loops and
// parallel/return arrows cannot coincide or run through a label or node.
func layoutSVG(c *Result, report []string) svgLayout {
	l := svgLayout{index: map[string]int{}}
	type port struct {
		arrow int
		out   bool
	}
	ports := make([][]port, len(c.States))
	nodeWidth := 180.0
	for i, s := range c.States {
		l.index[s.ID] = i
		label := fmt.Sprintf("State %d · %d concrete states\n", i+1, len(s.Members)) + s.Label
		if len(s.ViolatedRules) > 0 {
			label += "\nViolated invariants: " + strings.Join(s.ViolatedRules, ", ")
		}
		t := measureSVGText(label)
		nodeWidth = max(nodeWidth, t.width+2*svgPadding)
		l.nodes = append(l.nodes, svgNode{text: t})
	}
	labelWidth := 0.0
	for i, a := range c.Arrows {
		t := measureSVGText(a.Label)
		l.arrows = append(l.arrows, svgArrow{text: t})
		labelWidth = max(labelWidth, t.width)
		from, to := l.index[a.From], l.index[a.To]
		ports[from] = append(ports[from], port{i, true})
		ports[to] = append(ports[to], port{i, false})
	}
	y := 112.0
	for i := range l.nodes {
		n := &l.nodes[i]
		portHeight := 0.0
		for _, p := range ports[i] {
			h := float64(svgLaneGap)
			if p.out {
				h = l.arrows[p.arrow].text.height + 2*svgPadding
			}
			portHeight += h
		}
		n.svgBox = svgBox{svgNodeX, y, nodeWidth, max(n.text.height, portHeight) + 2*svgPadding}
		py := y + (n.height-portHeight)/2
		for _, p := range ports[i] {
			a := &l.arrows[p.arrow]
			h := float64(svgLaneGap)
			if p.out {
				h = a.text.height + 2*svgPadding
				a.fromY = py + h/2
			} else {
				a.toY = py + h/2
			}
			py += h
		}
		y += n.height + 2*svgMargin
	}
	order := make([]int, len(l.arrows))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int {
		a, b := l.arrows[i], l.arrows[j]
		return cmp.Or(cmp.Compare(min(a.fromY, a.toY), min(b.fromY, b.toY)), cmp.Compare(i, j))
	})
	var ends []float64
	for _, i := range order {
		a := &l.arrows[i]
		start, end := min(a.fromY, a.toY), max(a.fromY, a.toY)
		lane := 0
		for lane < len(ends) && ends[lane]+svgLaneGap >= start {
			lane++
		}
		if lane == len(ends) {
			ends = append(ends, end)
		} else {
			ends[lane] = end
		}
		a.laneX = svgNodeX + nodeWidth + labelWidth + 4*svgPadding + float64(lane)*svgLaneGap
	}
	l.width = svgNodeX + nodeWidth + svgMargin
	for _, a := range l.arrows {
		l.width = max(l.width, a.laneX+svgMargin)
	}
	l.reportY = y
	for _, s := range report {
		t := measureSVGText(s)
		l.report = append(l.report, t)
		l.width = max(l.width, t.width+2*svgMargin)
		y += t.height + svgPadding
	}
	l.height = y + svgMargin
	return l
}
