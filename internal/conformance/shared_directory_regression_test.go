package conformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestStreamingInvocationsShareCallerDirectory(t *testing.T) {
	dir := t.TempDir()
	reserved := filepath.Join(dir, ".invariant-conformance-1")
	if err := os.Mkdir(reserved, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(reserved, "caller")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan string, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	runner := declaredAttemptRunner(func(ctx context.Context, batch, module string, _ tlc.Config) (tlc.Result, error) {
		if _, err := os.Stat(filepath.Join(batch, module+".tla")); err != nil {
			return tlc.Result{}, err
		}
		entered <- batch
		select {
		case <-ctx.Done():
			return tlc.Result{}, ctx.Err()
		case <-release:
			return tlc.Result{Outcome: tlc.Passed}, nil
		}
	})
	for i := 0; i < 2; i++ {
		go func() {
			r, err := CheckReader(ctx, runner, dir, "Counter", nil, []string{"n"}, 1, strings.NewReader(`{"traces":[[{"n":0}]]}`), nil)
			if err == nil && !r.Passed {
				err = fmt.Errorf("conformance failed: %+v", r)
			}
			results <- err
		}()
	}
	var paths []string
	for i := 0; i < 2; i++ {
		select {
		case path := <-entered:
			paths = append(paths, path)
		case <-ctx.Done():
			close(release)
			t.Fatal("independent invocation did not start")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if paths[0] == paths[1] || filepath.Dir(paths[0]) == reserved || filepath.Dir(paths[1]) == reserved {
		t.Fatalf("invocations reused an owned directory: %v", paths)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "keep" {
		t.Fatalf("caller file changed: %q %v", raw, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(reserved) {
		t.Fatalf("invocation directories remain: %v %v", entries, err)
	}
}
