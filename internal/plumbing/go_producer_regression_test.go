package plumbing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoProducerPackageDirectoryAndRuntimeParent(t *testing.T) {
	ctx := context538(t)
	source := `package fixture
import ("testing"; "os"; "fmt")
func TestReady(t *testing.T) {
 b, err := os.ReadFile("fixture.txt")
 if err != nil || string(b) != "package data" { t.Fatalf("package cwd: %q %v", b, err) }
 t.Run("one/two", func(t *testing.T) {
  fmt.Fprint(t.Output(), "output writer\x00\x1e\npartial")
  t.Cleanup(func(){ t.Log("cleanup still belongs to child") })
 })
}
`
	f := fixtureFor538(t, t.TempDir(), source)
	// Output was added in Go 1.25; discovery keeps Go's standard-version vet.
	write538(t, f.root, "api/go.mod", "module example.invalid/producer538\n\ngo 1.25\n")
	write538(t, f.root, "api/nested/fixture.txt", "package data")
	env := openTransport538(t, f, ctx)
	if out, err := env.Run(ctx, []string{"go", "env", "GOTELEMETRY"}); err != nil || strings.TrimSpace(string(out)) != "off" {
		t.Fatalf("Go can still start a detached telemetry sidecar: %q %v", out, err)
	}
	got := f.execute(ctx)
	t.Run("package directory", func(t *testing.T) {
		terminal538(t, got, "TestReady", "pass")
	})
	t.Run("runtime parent", func(t *testing.T) {
		terminal538(t, got, "TestReady/one/two", "pass")
		for _, event := range got.Events {
			if event.Name == "TestReady/one/two" && event.Parent != "TestReady" {
				t.Fatalf("inferred parent from slash instead of runtime: %+v", event)
			}
		}
	})
	t.Run("output bytes", func(t *testing.T) {
		if !strings.Contains(text538(got), "output writer\x00\x1e\npartial\n") || !strings.Contains(text538(got), "cleanup still belongs to child") {
			t.Fatalf("lost raw testing.Output bytes or final partial line: %+v", got.Events)
		}
	})
	g := fixtureFor538(t, f.cache, `package fixture
import "testing"
func TestReady(t *testing.T) {
 t.Cleanup(func(){panic("ancestor cleanup panic")})
 t.Run("child", func(t *testing.T){panic("child body panic")})
}
`)
	panicked := g.execute(ctx)
	t.Run("ancestor cleanup panic", func(t *testing.T) {
		terminal538(t, panicked, "TestReady/child", "fail")
		terminal538(t, panicked, "TestReady", "fail")
		if !strings.Contains(text538(panicked), "ancestor cleanup panic") || !strings.Contains(text538(panicked), "child body panic") {
			t.Fatalf("lost nested panic explanation: %+v", panicked.Events)
		}
	})
}

func TestGoTestingRefusesUnreviewedSourceAndRetainsTruncatedEvents(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "src", "testing", "testing.go"))
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(source), "func (t *T) Parallel() {", "func (t *T) Parallel() {\n panic(\"changed\")", 1)
	if _, err := instrumentGoTesting(t.Context(), changed); err == nil {
		t.Fatal("unreviewed testing implementation was instrumented")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := instrumentGoTesting(ctx, string(source)); !errors.Is(err, context.Canceled) {
		t.Fatalf("instrumentation ignored cancellation: %v", err)
	}
	complete := goTestingProtocol + "9:TestReady0:5:start0:"
	for _, tail := range []string{"9:Test", "9:TestReady0:4:pass2:x", "999999999999999999999999:"} {
		events, err := decodeGoTesting([]byte(complete + tail))
		if err == nil || len(events) != 1 || events[0].Action != "start" {
			t.Fatalf("truncation lost prior observations or invented completion: %+v %v", events, err)
		}
	}
}
