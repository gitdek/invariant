package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A refusal of the runtime stand-in is a gate failure, which a receipt or a
// factory post quotes where anyone can read it. Published, it names no
// directory of the machine that ran verification: the destination is
// relative to the application's root. The developer who ran the gate still
// reads it as it is there, as #908's tests pin (#922).
func TestPythonApplicationStandInPublishedDestination922(t *testing.T) {
	files := standIn908Files(t)
	for _, tc := range []struct{ name, remove, write, body string }{
		{name: "absent package", remove: "nagini_contracts"},
		{name: "changed contracts", write: "nagini_contracts/contracts.py", body: "# changed\n"},
		{name: "second package", write: "vendor/nagini_contracts/__init__.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, _ := pa342Fake(t)
			gp359Verifier(t, false)
			f := gp359New(t, false)
			if tc.remove != "" {
				if err := os.RemoveAll(filepath.Join(f.root, tc.remove)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.write != "" {
				pa342Write(t, f.root, tc.write, tc.body)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_, err := Run(ctx, f.p.Dir, "", tools)
			if err == nil {
				t.Fatal("the gate admitted an application that doesn't ship the stand-in as it is")
			}
			standIn908Diagnostic(t, err.Error(), f.root, files)
			published := Public(err)
			text := published.Error()
			for _, local := range []string{filepath.Dir(f.root), filepath.Clean(os.TempDir())} {
				if strings.Contains(text, local) {
					t.Errorf("the published refusal names %s, a directory of the machine that ran verification:\n%s", local, text)
				}
			}
			// Word for word the developer's, but for the destination.
			want := strings.ReplaceAll(err.Error(), filepath.Join(f.root, "nagini_contracts"), "nagini_contracts")
			if !strings.Contains(text, "\nDestination: nagini_contracts\n") || text != want {
				t.Errorf("the published refusal doesn't give the destination relative to the application's root:\n%s", text)
			}
			if !errors.Is(published, err) {
				t.Error("the published refusal doesn't wrap the gate's own")
			}
		})
	}
	for _, err := range []error{nil, context.Canceled} {
		if Public(err) != err {
			t.Errorf("Public changed %v, which names no directory", err)
		}
	}
}
