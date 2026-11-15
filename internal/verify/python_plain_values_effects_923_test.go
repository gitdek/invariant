package verify

import "testing"

// The decision's proof cannot establish the handler's effects. Exercise the
// documented handler with the shipped stand-in and inspect its ordinary state.
func TestPythonApplicationPlainValueHandlerEffects923(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pa342Write(t, root, "counter/__init__.py", "")
	for _, source := range []string{pa342Source, calls793Source} {
		pa342Write(t, root, source, pv923Block(t, source))
	}
	for name, body := range standIn908Files(t) {
		pa342Write(t, root, "nagini_contracts/"+name, string(body))
	}
	pv923Python(t, root, `from counter import core
for headers in [{}, {'Site': 'statue'}, {'Site': ''}, {'Site': 'elsewhere'}]:
    for requested in ['engine', 'other']:
        for busy in [False, True]:
            core.reset()
            if busy:
                core.submit(core.Request({}, 'engine'))
            before = (core.count, core.registry['engine'].active, dict(core.accepted))
            header = headers.get('Site')
            want = 403 if header is not None and header != 'statue' else 400 if requested != 'engine' else 409 if busy else 0
            try:
                core.submit(core.Request(headers, requested))
            except core.Refused as error:
                assert want != 0 and error.status_code == want
                assert (core.count, core.registry['engine'].active, core.accepted) == before
            else:
                assert want == 0
                assert core.count == 1
                assert core.registry['engine'].active == 1
                assert core.accepted == {'engine': 1}
            assert core.read() == core.count
core.reset()
assert core.count == 0 and core.registry['engine'].active == 0 and core.accepted == {}
`)
}
