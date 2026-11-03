package toolchain

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
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

type downloadRetryTransport func(*http.Request) (*http.Response, error)

func (f downloadRetryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPinnedFetchStopsAtPublishFailure(t *testing.T) {
	const contents = "verified contents"
	dir := t.TempDir()
	path := filepath.Join(dir, "destination")
	// Publishing a file over a directory is a permanent local error, even
	// when the response and its checksum are valid.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: downloadRetryTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(contents)),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = old })
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
	if err := fetch(context.Background(), "https://example.invalid/pinned", path, want); err == nil {
		t.Fatal("publishing over a directory succeeded")
	}
	if calls != 1 {
		t.Errorf("requests = %d, want 1 for a permanent publication failure", calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "destination" || !entries[0].IsDir() {
		t.Errorf("publication failure changed the destination or left temporary files: %v", entries)
	}
}

func TestPinnedFetchRejectsInvalidURLsWithoutRequests(t *testing.T) {
	calls := 0
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: downloadRetryTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, io.ErrUnexpectedEOF
	})}
	t.Cleanup(func() { http.DefaultClient = old })
	for _, url := range []string{"://invalid", "ftp://example.invalid/pinned", "https:///pinned"} {
		t.Run(url, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			err := fetch(ctx, url, filepath.Join(t.TempDir(), "pinned"), "unused")
			if err == nil || errors.Is(err, context.DeadlineExceeded) || calls != 0 {
				t.Fatalf("invalid URL sent requests or waited to retry: calls=%d, err=%v", calls, err)
			}
		})
	}
}

func TestPinnedFetchStopsAtCertificateAndRedirectFailures(t *testing.T) {
	for _, kind := range []string{"certificate", "redirect limit"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			certificateErr := &tls.CertificateVerificationError{Err: errors.New("untrusted certificate")}
			old := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: downloadRetryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if kind == "certificate" {
					return nil, certificateErr
				}
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{r.URL.String()}},
					Body:       io.NopCloser(strings.NewReader("redirect")),
				}, nil
			})}
			t.Cleanup(func() { http.DefaultClient = old })
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			err := fetch(ctx, "https://example.invalid/pinned", filepath.Join(t.TempDir(), "pinned"), "unused")
			want := 10 // The HTTP client's redirect limit is one failed acquisition.
			if kind == "certificate" {
				want = 1
				if !errors.Is(err, certificateErr) {
					t.Errorf("lost certificate diagnostic: %v", err)
				}
			}
			if err == nil || errors.Is(err, context.DeadlineExceeded) || calls != want {
				t.Fatalf("permanent client failure was retried: requests=%d, want %d; err=%v", calls, want, err)
			}
		})
	}
}
