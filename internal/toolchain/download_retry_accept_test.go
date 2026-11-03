package toolchain

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type download433Transport func(*http.Request) (*http.Response, error)

func (f download433Transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type download433Body struct {
	io.Reader
	closed bool
}

func (b *download433Body) Close() error { b.closed = true; return nil }

type download433BrokenReader struct{}

func (download433BrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func download433Client(t *testing.T, f download433Transport) {
	t.Helper()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: f}
	t.Cleanup(func() { http.DefaultClient = old })
}

func download433Response(code int, detail string, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: code, Status: fmt.Sprintf("%d %s", code, detail), Body: body, Header: make(http.Header)}
}

func download433Hash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

func download433NoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".download-") {
			t.Errorf("partial download remains: %s", entry.Name())
		}
	}
}

func TestPinnedFetchRecoversTransientFailures(t *testing.T) {
	for _, failure := range []string{"502", "500", "503", "429", "403", "network", "body"} {
		t.Run(failure, func(t *testing.T) {
			const contents = "the pinned file\n"
			var calls []time.Time
			var bodies []*download433Body
			download433Client(t, func(r *http.Request) (*http.Response, error) {
				calls = append(calls, time.Now())
				if r.Method != http.MethodGet || r.URL.String() != "https://cdn.example.invalid/pinned" {
					t.Errorf("request changed: %s %s", r.Method, r.URL)
				}
				if len(calls) <= 2 && failure == "network" {
					return nil, io.ErrUnexpectedEOF
				}
				body := &download433Body{Reader: strings.NewReader(contents)}
				bodies = append(bodies, body)
				if len(calls) <= 2 {
					if failure == "body" {
						body.Reader = io.MultiReader(strings.NewReader("partial"), download433BrokenReader{})
					} else {
						code := 0
						fmt.Sscan(failure, &code)
						return download433Response(code, http.StatusText(code), body), nil
					}
				}
				return download433Response(200, "OK", body), nil
			})
			dir := t.TempDir()
			path := filepath.Join(dir, "pinned.jar")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if err := fetch(ctx, "https://cdn.example.invalid/pinned", path, download433Hash(contents)); err != nil {
				t.Fatalf("transient failure was not recovered: %v", err)
			}
			if len(calls) != 3 {
				t.Fatalf("requests = %d, want 3", len(calls))
			}
			// Generous lower bounds distinguish real backoff from an immediate loop.
			if calls[1].Sub(calls[0]) < 800*time.Millisecond || calls[2].Sub(calls[1]) < 1600*time.Millisecond {
				t.Errorf("missing one- then two-second backoff: %v", calls)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != contents {
				t.Fatalf("download = %q, %v", got, err)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("response body was not closed")
				}
			}
			download433NoTemps(t, dir)
		})
	}
}

func TestPinnedFetchBoundsRetriesAndKeepsLastError(t *testing.T) {
	calls := 0
	download433Client(t, func(*http.Request) (*http.Response, error) {
		calls++
		return download433Response(502, fmt.Sprintf("failure-%d", calls), io.NopCloser(strings.NewReader("unavailable"))), nil
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "pinned.jar")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	err := fetch(ctx, "https://cdn.example.invalid/pinned", path, download433Hash("wanted"))
	if calls != 3 || err == nil || !strings.Contains(err.Error(), "failure-3") || ctx.Err() != nil {
		t.Fatalf("requests=%d, error=%v, context=%v; want three tries and their last error", calls, err, ctx.Err())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("failed download published a destination: %v", err)
	}
	download433NoTemps(t, dir)
}

func TestPinnedFetchStopsAtIntegrityOrPermanentFailure(t *testing.T) {
	for _, kind := range []string{"transient then mismatch", "immediate mismatch", "404", "cached"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pinned.jar")
			old := "old file"
			if kind == "cached" {
				old = "wanted"
			}
			if err := os.WriteFile(path, []byte(old), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			download433Client(t, func(*http.Request) (*http.Response, error) {
				calls++
				code := 200
				if kind == "transient then mismatch" && calls == 1 {
					code = 502
				}
				if kind == "404" {
					code = 404
				}
				return download433Response(code, http.StatusText(code), io.NopCloser(strings.NewReader("corrupt"))), nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			err := fetch(ctx, "https://cdn.example.invalid/pinned", path, download433Hash("wanted"))
			wantCalls := 1
			if kind == "transient then mismatch" {
				wantCalls = 2
			}
			if kind == "cached" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Errorf("requests=%d, want %d", calls, wantCalls)
			}
			if kind == "cached" {
				if err != nil {
					t.Errorf("valid cache failed: %v", err)
				}
			} else if err == nil {
				t.Error("invalid download succeeded")
			} else if strings.Contains(kind, "mismatch") && !strings.Contains(err.Error(), "checksum mismatch") {
				t.Errorf("lost integrity failure: %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != old {
				t.Errorf("destination changed on failure: %q, %v", got, readErr)
			}
			download433NoTemps(t, dir)
		})
	}
}

func TestPinnedFetchCancellationInterruptsBackoff(t *testing.T) {
	first := make(chan struct{})
	calls := 0
	download433Client(t, func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			close(first)
		}
		return download433Response(502, "Bad Gateway", io.NopCloser(strings.NewReader("unavailable"))), nil
	})
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- fetch(ctx, "https://cdn.example.invalid/pinned", filepath.Join(dir, "pinned.jar"), download433Hash("wanted"))
	}()
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	// Cancellation occurs inside the first backoff, well before the second request.
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("requests=%d, error=%v; want cancellation after one request", calls, err)
		}
		if time.Since(started) > 500*time.Millisecond {
			t.Error("cancellation waited for the backoff")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not interrupt backoff")
	}
	download433NoTemps(t, dir)
}
