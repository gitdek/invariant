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
	svgHeading    = "Bounded reader statechart\nAll initial states and visible arrows; qualifications and evidence below."
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
	text                   svgText
	label                  svgBox
	fromX, fromY, toX, toY float64
	fromLane, toLane       float64
	path                   string
}

type svgLayout struct {
	nodes  []svgNode
	arrows []svgArrow
	index  map[string]int
	// number is each state's reading number, by its position in Result.States:
	// column by column as the layout draws them, then top to bottom (#626).
	number        []int
	report        []svgReportText
	width, height float64
}

type svgReportText struct {
	text svgText
	x, y float64
}

type svgPort struct {
	arrow int
	out   bool
}

type svgColumn struct {
	x, width, localWidth, gapWidth float64
	nearX, localX, farX            float64
	forwardX, labelX, arrivalX     float64
	nearCount                      int
	local, forward                 []int
}

// State sizes depend only on their text. Columns and the space between rows
// reserve separate corridors for forward labels, local detours and returns.
// Vertical legs stay outside every label area, including the return labels
// below the drawing. Routing therefore never needs to enlarge a state. The
// report is laid out separately, below the drawing, once states are numbered.
func layoutSVG(c *Result) svgLayout {
	l := svgLayout{index: map[string]int{}, number: make([]int, len(c.States))}
	for i, s := range c.States {
		l.index[s.ID] = i
	}
	// Layers come from the arrows alone, so states get their reading numbers
	// before their text, which sets their sizes, is built. Initial states form
	// the first column.
	layers, rank := svgLayers(c, l.index)
	n := 0
	for _, layer := range layers {
		for _, i := range layer {
			n++
			l.number[i] = n
		}
	}
	for i, s := range c.States {
		label := fmt.Sprintf("State %d · %s\n", l.number[i], svgCount(len(s.Members), "concrete state")) + s.Label
		if len(s.ViolatedRules) > 0 {
			label += "\nViolated invariants: " + strings.Join(s.ViolatedRules, ", ")
		}
		t := measureSVGText(label)
		l.nodes = append(l.nodes, svgNode{svgBox: svgBox{width: max(180, t.width+2*svgPadding), height: t.height + 2*svgPadding}, text: t})
	}
	columns := make([]svgColumn, len(layers))
	row := make([]int, len(c.States))
	rowCount := 0
	for k, layer := range layers {
		rowCount = max(rowCount, len(layer))
		for j, i := range layer {
			row[i] = j
			columns[k].width = max(columns[k].width, l.nodes[i].width)
		}
	}
	left, right := make([][]svgPort, len(c.States)), make([][]svgPort, len(c.States))
	local := make([][]int, len(c.States))
	var back []int
	for i, a := range c.Arrows {
		t := measureSVGText(a.Label)
		l.arrows = append(l.arrows, svgArrow{text: t, label: svgBox{width: t.width + svgPadding, height: t.height + svgPadding}})
		from, to := l.index[a.From], l.index[a.To]
		col := &columns[rank[from]]
		right[from] = append(right[from], svgPort{i, true})
		if rank[to] > rank[from] {
			left[to] = append(left[to], svgPort{i, false})
			col.forward = append(col.forward, i)
			col.gapWidth = max(col.gapWidth, l.arrows[i].label.width)
			continue
		}
		right[to] = append(right[to], svgPort{i, false})
		col.nearCount++
		columns[rank[to]].nearCount++
		if rank[to] == rank[from] {
			local[from] = append(local[from], i)
			col.local = append(col.local, i)
			col.localWidth = max(col.localWidth, l.arrows[i].label.width)
		} else {
			back = append(back, i)
			// Return labels use the preceding gap's label area. Return legs
			// run beside columns, so none can pass through these labels.
			gap := &columns[rank[from]-1]
			gap.gapWidth = max(gap.gapWidth, l.arrows[i].label.width)
		}
	}
	rowHeight, localHeight := make([]float64, rowCount), make([]float64, rowCount)
	for i, n := range l.nodes {
		rowHeight[row[i]] = max(rowHeight[row[i]], n.height)
		h := 0.0
		for _, a := range local[i] {
			h += l.arrows[a].label.height + svgPadding
		}
		localHeight[row[i]] = max(localHeight[row[i]], h)
	}
	rowY := make([]float64, rowCount)
	y := 112.0
	for j := range rowY {
		rowY[j] = y
		y += rowHeight[j] + localHeight[j] + 2*svgMargin
	}
	x := float64(svgNodeX)
	for k := range columns {
		col := &columns[k]
		col.x = x
		col.nearX = x + col.width + svgLaneGap
		col.localX = col.nearX + float64(col.nearCount)*svgLaneGap + svgPadding
		col.farX = col.localX + col.localWidth + svgLaneGap
		col.forwardX = col.farX + float64(len(col.local))*svgLaneGap
		col.labelX = col.forwardX + float64(len(col.forward))*svgLaneGap + svgPadding
		col.arrivalX = col.labelX + col.gapWidth + svgLaneGap
		x = col.arrivalX + float64(len(col.forward))*svgLaneGap + svgPadding
	}
	for i := range l.nodes {
		n, col := &l.nodes[i], &columns[rank[i]]
		n.x = col.x
		n.y = rowY[row[i]] + (rowHeight[row[i]]-n.height)/2
		y := rowY[row[i]] + rowHeight[row[i]] + svgMargin
		for _, a := range local[i] {
			l.arrows[a].label.x, l.arrows[a].label.y = col.localX, y
			y += l.arrows[a].label.height + svgPadding
		}
		for side, ports := range [][]svgPort{left[i], right[i]} {
			other := func(p svgPort) int {
				a := c.Arrows[p.arrow]
				if p.out {
					return l.index[a.To]
				}
				return l.index[a.From]
			}
			slices.SortStableFunc(ports, func(a, b svgPort) int {
				return cmp.Or(cmp.Compare(row[other(a)], row[other(b)]), cmp.Compare(a.arrow, b.arrow))
			})
			for j, p := range ports {
				a := &l.arrows[p.arrow]
				px := n.x
				if side == 1 {
					px += n.width
				}
				py := n.y + svgPadding + (n.height-2*svgPadding)*float64(j+1)/float64(len(ports)+1)
				if p.out {
					a.fromX, a.fromY = px, py
				} else {
					a.toX, a.toY = px, py
				}
			}
		}
	}
	for k, layer := range layers {
		col := &columns[k]
		lane := col.nearX
		for _, i := range layer {
			for _, p := range right[i] {
				a := c.Arrows[p.arrow]
				if rank[l.index[a.To]] > rank[l.index[a.From]] {
					continue
				}
				if p.out {
					l.arrows[p.arrow].fromLane = lane
				} else {
					l.arrows[p.arrow].toLane = lane
				}
				lane += svgLaneGap
			}
		}
		for j, i := range col.local {
			l.arrows[i].toLane = col.farX + float64(j)*svgLaneGap
		}
		// Preserve the vertical order of independent branches. Each label
		// receives its own row between the two sets of vertical legs.
		slices.SortStableFunc(col.forward, func(i, j int) int {
			a, b := l.arrows[i], l.arrows[j]
			return cmp.Or(cmp.Compare(a.fromY+a.toY, b.fromY+b.toY), cmp.Compare(i, j))
		})
		bottom := 112.0
		for j, i := range col.forward {
			a := &l.arrows[i]
			a.fromLane = col.forwardX + float64(j)*svgLaneGap
			a.toLane = col.arrivalX + float64(j)*svgLaneGap
			a.label.x = col.labelX
			a.label.y = max(bottom, (a.fromY+a.toY-a.label.height)/2)
			bottom = a.label.y + a.label.height + svgPadding
			y = max(y, bottom+svgMargin)
		}
	}
	for _, i := range back {
		a := &l.arrows[i]
		a.label.x = columns[rank[l.index[c.Arrows[i].From]]-1].labelX
		a.label.y = y
		y += a.label.height + svgPadding
	}
	l.width = measureSVGText(svgHeading).width + 2*svgMargin
	for _, n := range l.nodes {
		l.width = max(l.width, n.x+n.width+svgMargin)
	}
	for i := range l.arrows {
		a := &l.arrows[i]
		cy := a.label.y + a.label.height/2
		a.path = fmt.Sprintf("M %g %g L %g %g L %g %g L %g %g L %g %g L %g %g",
			a.fromX, a.fromY, a.fromLane, a.fromY, a.fromLane, cy,
			a.toLane, cy, a.toLane, a.toY, a.toX, a.toY)
		l.width = max(l.width, a.fromLane+svgMargin, a.toLane+svgMargin, a.label.x+a.label.width+svgMargin)
	}
	l.height = y + svgMargin
	return l
}

// Multi-source breadth-first search uses only visible arrows. Forward arrows
// therefore join adjacent layers. Stable barycentre sweeps bring neighbours
// into the same vertical order without changing those shortest distances.
func svgLayers(c *Result, index map[string]int) ([][]int, []int) {
	rank := make([]int, len(c.States))
	next, previous := make([][]int, len(rank)), make([][]int, len(rank))
	for i := range rank {
		rank[i] = -1
	}
	for _, a := range c.Arrows {
		from, to := index[a.From], index[a.To]
		next[from] = append(next[from], to)
		previous[to] = append(previous[to], from)
	}
	var queue []int
	for _, id := range c.Initial {
		i := index[id]
		rank[i] = 0
		queue = append(queue, i)
	}
	last := 0
	for j := 0; j < len(queue); j++ {
		from := queue[j]
		for _, to := range next[from] {
			if rank[to] < 0 {
				rank[to] = rank[from] + 1
				last = max(last, rank[to])
				queue = append(queue, to)
			}
		}
	}
	layers := make([][]int, last+1)
	for i, k := range rank {
		layers[k] = append(layers[k], i)
	}
	position := make([]float64, len(rank))
	update := func(k int) {
		for j, i := range layers[k] {
			position[i] = float64(j+1) / float64(len(layers[k])+1)
		}
	}
	for k := range layers {
		update(k)
	}
	order := func(k, adjacent int, neighbours [][]int) {
		scores := make(map[int]float64, len(layers[k]))
		for _, i := range layers[k] {
			sum, count := 0.0, 0
			for _, other := range neighbours[i] {
				if rank[other] == adjacent {
					sum += position[other]
					count++
				}
			}
			scores[i] = position[i]
			if count > 0 {
				scores[i] = sum / float64(count)
			}
		}
		slices.SortStableFunc(layers[k], func(i, j int) int { return cmp.Compare(scores[i], scores[j]) })
		update(k)
	}
	for range 4 {
		for k := 1; k < len(layers); k++ {
			order(k, k-1, previous)
		}
		for k := len(layers) - 2; k >= 0; k-- {
			order(k, k+1, next)
		}
	}
	return layers, rank
}

// Balance complete text blocks in up to three columns below the drawing.
// Literal lines are never wrapped, split across columns, or scaled down.
func (l *svgLayout) layoutReport(report []string) {
	const spacing = 6
	texts := make([]svgText, len(report))
	total := 0.0
	for i, s := range report {
		texts[i] = measureSVGText(s)
		total += texts[i].height + spacing
	}
	columns := min(3, max(1, int(total/1400)+1))
	target := total / float64(columns)
	x, top, y, width := float64(svgMargin), l.height+svgMargin, 0.0, 0.0
	column := 1
	for _, t := range texts {
		if y > 0 && y+t.height > target && column < columns {
			x += width + 2*svgMargin
			y, width = 0, 0
			column++
		}
		l.report = append(l.report, svgReportText{t, x, top + y})
		width = max(width, t.width)
		y += t.height + spacing
		l.width = max(l.width, x+width+svgMargin)
		l.height = max(l.height, top+y+svgMargin)
	}
}
