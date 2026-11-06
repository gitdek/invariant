package dashboard

import (
	"strings"
	"testing"
)

// A slice the README says is in progress shows as in progress on the road,
// even after the first slice still to do; one it doesn't stays later (#681).
func TestEverySliceInProgressShowsSo(t *testing.T) {
	readme := "- [x] **Slice 12 · One decision graph.** Done.\n" +
		"- [ ] **Slice 13 · The factory takes every change.** Plumbing goes through the factory. In progress: it plans its own plumbing.\n" +
		"- [x] **Slice 14 · Codex on chosen projects.** Done.\n" +
		"- [ ] **Slice 15 · Verification inside ordinary code.** Ordinary code stays as it is. In progress: the Python path is delivered.\n" +
		"- [ ] **Slice 16 · Type checking.** Not started.\n"
	var got []string
	for _, s := range ParseSlices(readme, "") {
		got = append(got, s.ID+" "+s.Status)
	}
	if want := "12 done, 13 now, 14 done, 15 now, 16 later"; strings.Join(got, ", ") != want {
		t.Errorf("got %s, want %s", strings.Join(got, ", "), want)
	}
}
