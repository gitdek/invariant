package verify

import (
	"strings"
	"testing"
)

// An application entry is the application's own code (#879). A driver once
// could step through a helper beside it in the verification project, or
// through a test file, which called the selected function itself: every
// check passed while the application's own entry, guards and all, never ran,
// and the receipt said the existing code was run as it is.
func TestPythonApplicationEntryIsTheApplicationsOwnCode879(t *testing.T) {
	t.Parallel()
	const shortcut = `from tasks import lifecycle as app
def submit(request, *, status):
    key = app.reserve(request)
    app.advance(key, status)
`
	driver := func(module, name string) string {
		return strings.NewReplacer("from tasks import lifecycle as app\n", "from tasks import lifecycle as app\nfrom "+module+" import "+name+"\n",
			"app.submit", name+".submit").Replace(pa321Driver)
	}
	for _, c := range []struct {
		name, file, module, symbol string
		selected                   bool
		want                       string
	}{
		{"a helper beside the driver", pa315Project + "/shortcut.py", "verification.lifecycle", "shortcut", false,
			"an application entry must be the application's own code, and " + pa315Project + "/shortcut.py is the verification project's or a test's"},
		{"a test file", "tests/test_shortcut.py", "tests", "test_shortcut", false,
			"an application entry must be the application's own code, and tests/test_shortcut.py is the verification project's or a test's"},
		// Code under the verification project that the manifest selects as
		// existing is the application's, as a proof's scan reads it.
		{"a helper the application selects", pa315Project + "/shortcut.py", "verification.lifecycle", "shortcut", true, ""},
		{"the application's own module", "tasks/shortcut.py", "tasks", "shortcut", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root, p := pa321Fixture(t, pa321State+pa321Worker, driver(c.module, c.symbol))
			pa310Write(t, root, c.file, shortcut)
			if c.selected {
				p.Manifest.Existing = append(p.Manifest.Existing, c.file)
			}
			pa310Save(t, p)
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			if c.want == "" {
				if err != nil || r == nil || !r.Success || !r.Complete {
					t.Fatalf("an entry that is the application's own was refused: %+v, %v", r, err)
				}
				return
			}
			// No row of such a run is evidence, and the run says why.
			pa318Reject(t, r, err)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}
