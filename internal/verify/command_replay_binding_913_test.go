package verify

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// A binding names a method on an application object. In Python a binding
// with no object can still name one of None's own methods: calling None's
// class returns None, which a refusal declared as None would match, so the
// offending step would read as refused without touching the application.
// The TypeScript harness already refuses a binding with no target.
const noTargetReplayPY913 = `class Counter:
    def __init__(self):
        self.n = 0
    def increment(self, delta):
        self.n += delta
        return True
    def read(self):
        return dict(n=dict(kind='integer', atom=str(self.n)))
def invariant_replay(initial):
    app = Counter()
    def bind(action, args):
        if app.n == 1:
            return dict(target=None, method='__class__', args=[], refusal=dict(equals=None))
        return dict(target=app, method='increment', args=[int(v['atom']) for v in args], refusal=dict(equals=False))
    return dict(read=app.read, bind=bind, close=lambda: None)
`

func TestCommandReplayRefusesBindingWithoutTarget913(t *testing.T) {
	state := func(n string) tlc.ReplayState { return tlc.ReplayState{"n": {Kind: "integer", Atom: n}} }
	q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 1,
		States: []tlc.ReplayState{state("0"), state("1"), state("2")},
		Steps: []tlc.ReplayStep{{From: 0, To: 1, Action: "Increment", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}},
			{From: 1, To: 2, Action: "Increment", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "1"}}, Bug: true}}}}
	_, output, err := runReplayHarness913(t, "python", noTargetReplayPY913, q)
	if err == nil || !strings.Contains(output, "bind must name a real application method") {
		t.Fatalf("a binding with no application object completed replay: %v\n%s", err, output)
	}
}
