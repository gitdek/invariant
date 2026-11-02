package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
)

func TestPythonApplicationExistingSnapshotHash(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"pkg/a.py":          "a\n",
		"pkg/a/bin/data":    "\x00\xff\n",
		"pkg/out/result":    "result",
		"pkgextra/other.py": "outside selection",
		"settings.py":       "setting\n",
	})
	_, files, err := pythonApplicationSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonApplicationHashExisting(root, []string{"pkg", "settings.py", "pkg/a"}, files)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(frame string) string {
		h := sha256.Sum256([]byte(frame))
		return "sha256:" + hex.EncodeToString(h[:])
	}
	// Exact frames cover binary resources, lexical file order (a.py before
	// a/bin), nested selections and the boundary between pkg and pkgextra.
	want := []ExistingCode{
		{Path: "pkg", Files: 3, SHA256: digest("pkg/a.py\n2\na\npkg/a/bin/data\n3\n\x00\xff\npkg/out/result\n6\nresult")},
		{Path: "settings.py", Files: 1, SHA256: digest("settings.py\n8\nsetting\n")},
		{Path: "pkg/a", Files: 1, SHA256: digest("pkg/a/bin/data\n3\n\x00\xff\n")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("existing snapshot evidence = %+v, want %+v", got, want)
	}
}
