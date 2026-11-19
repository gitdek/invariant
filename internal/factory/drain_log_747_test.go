package factory

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// #747: a watcher logs a step's start only once the step is admitted, so a
// draining watcher logs none for work it leaves for its successor, and says
// once for each issue that the issue waits for the next watcher. The watcher,
// its polls, admission, the lease and the run records are the real ones;
// only GitHub and the coding agents are fakes.

// lines747 counts what was logged about issue n that starts with what.
func lines747(logs *logLines, n int, what string) int {
	count := 0
	for _, line := range logs.all() {
		if strings.HasPrefix(line, fmt.Sprintf("#%d: %s", n, what)) {
			count++
		}
	}
	return count
}

const waits747 = "waiting for the next watcher"

func TestDrainingWatcherLogsNoStartItNeverMakes747(t *testing.T) {
	for _, capacity := range []int{1, 3} {
		t.Run(fmt.Sprintf("slots-%d", capacity), func(t *testing.T) {
			p, h := slots455Rig(t, capacity)
			ratify := slots455Proposal(t, p, 3, slots455Model)
			logs := &logLines{t: t}
			p.f.Log = logs.add
			p.f.Holder, p.f.LeaseFor = "draining-747", 5*time.Minute
			// An admitted draft, held open, keeps the watcher draining.
			p.held.holdOpen(1)
			slots455Open(p, 1, slots455Model)
			w := drain463Start(t, p.f, 20*time.Millisecond)
			drain463Soon(t, "the admitted draft", func() bool { return p.held.started("draft", 1) == 1 })
			p.f.Drain()
			// A /invariant solve whose draft waits for the next watcher, and
			// a ratification it records, whose build waits too.
			slots455Open(p, 2, slots455Model)
			p.say(3, ratify)
			drain463Soon(t, "the ratification", func() bool { return p.last(3).Marker.Kind == KindRatified })
			reads2, reads3 := h.readCount(2), h.readCount(3)
			drain463Soon(t, "several more steps on each waiting issue", func() bool {
				return h.readCount(2) >= reads2+4 && h.readCount(3) >= reads3+4
			})
			w.stillRunning(t)
			check := func() {
				t.Helper()
				for _, c := range []struct {
					n     int
					start string
				}{{2, "formalizing"}, {3, "building "}} {
					if got := lines747(logs, c.n, c.start); got != 0 {
						t.Errorf("#%d: logged %d starts of a step the draining watcher never started", c.n, got)
					}
					if got := lines747(logs, c.n, waits747); got != 1 {
						t.Errorf("#%d: logged %d lines that it waits for the next watcher, want 1", c.n, got)
					}
				}
				if got := lines747(logs, 1, "formalizing"); got != 1 {
					t.Errorf("the admitted draft logged its start %d times, want 1", got)
				}
				if got := lines747(logs, 1, waits747); got != 0 {
					t.Errorf("the admitted draft logged %d waits", got)
				}
			}
			check()
			slots455NoRun(t, p, 2, "draft-issue")
			slots455NoRun(t, p, 3, buildStepName(p.last(3)))
			p.held.let(1)
			if err := w.wait(t); err != nil {
				t.Fatalf("a graceful drain returned %v", err)
			}
			check()
		})
	}
}

func TestUndrainedWatcherLogsEachStart747(t *testing.T) {
	p, _ := slots455Rig(t, 1)
	logs := &logLines{t: t}
	p.f.Log = logs.add
	for n, body := range map[int]string{1: slots455Model, 2: slots455Plumbing} {
		ratify := slots455Proposal(t, p, n, body)
		p.say(n, ratify)
		p.settle()
		p.expect(n, KindPR, LabelPR)
		if drafts, builds := lines747(logs, n, "formalizing"), lines747(logs, n, "building "); drafts != 1 || builds != 1 {
			t.Errorf("#%d: logged %d draft starts and %d build starts, want 1 of each", n, drafts, builds)
		}
		if got := lines747(logs, n, waits747); got != 0 {
			t.Errorf("#%d: an undrained watcher logged %d waits", n, got)
		}
	}
}
