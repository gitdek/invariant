package found

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"path/filepath"
	"slices"
	"testing"
)

// todaysFounding929 is the SHA-256 of every file a founding without a spec
// committed on main before #929, each as its path, a NUL, its bytes and a
// NUL, in path order, taken before any of #929's changes. A founding
// declares no macOS part, so it writes these files still.
const todaysFounding929 = "6b5a57ac84abaf4bf07f66161c1a3ce5a2b4b033f188b14190affb54a73d478b"

func TestAFoundingWritesTodaysFiles929(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if out, err := w.found(Options{}); err != nil {
		t.Fatalf("the founding failed: %v\n%s", err, out)
	}
	files := w.tree(filepath.Join(w.root, "widgets"))
	h := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(files)) {
		h.Write([]byte(name + "\x00" + files[name] + "\x00"))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != todaysFounding929 {
		t.Errorf("a founding's files changed: sha256 %s, was %s; they are %q", got, todaysFounding929, slices.Sorted(maps.Keys(files)))
	}
}
