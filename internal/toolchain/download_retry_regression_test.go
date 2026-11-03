package toolchain

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedFetchStopsAtPermanentRequestFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		url    string
		calls  int
		detail string
	}{
		{"malformed URL", "%", 0, "invalid URL escape"},
		{"unsupported scheme", "ftp://cdn.example.invalid/pinned", 1, "unsupported protocol scheme"},
		{"missing host", "https:///pinned", 1, "no Host in request URL"},
		{"TLS certificate", "", 1, "certificate"},
		{"untrusted certificate", "", 1, "certificate"},
		{"redirect loop", "", 10, "stopped after 10 redirects"},
		{"redirect policy", "", 1, "redirect refused"},
		{"malformed redirect", "", 1, "failed to parse Location header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pinned.jar")
			calls := 0
			var bodies []*download433Body
			download433Client(t, func(r *http.Request) (*http.Response, error) {
				calls++
				switch tc.name {
				case "malformed URL", "unsupported scheme", "missing host":
					// Invalid request URLs fail in the transport before dialing.
					return (&http.Transport{}).RoundTrip(r)
				case "TLS certificate":
					return nil, &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
				case "untrusted certificate":
					return nil, x509.UnknownAuthorityError{}
				default:
					body := &download433Body{Reader: strings.NewReader("")}
					bodies = append(bodies, body)
					resp := download433Response(http.StatusFound, "Found", body)
					location := "/pinned"
					if tc.name == "malformed redirect" {
						location = "%"
					}
					resp.Header.Set("Location", location)
					return resp, nil
				}
			})
			if tc.name == "redirect policy" {
				http.DefaultClient.CheckRedirect = func(*http.Request, []*http.Request) error {
					return errors.New("redirect refused")
				}
			}
			address := tc.url
			if address == "" {
				address = "https://cdn.example.invalid/pinned"
			}
			err := fetch(context.Background(), address, path, download433Hash("wanted"))
			if err == nil || !strings.Contains(err.Error(), tc.detail) || calls != tc.calls {
				t.Fatalf("requests=%d, error=%v; want %d requests and %q without retries", calls, err, tc.calls, tc.detail)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("redirect response body was not closed")
				}
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("failed download published a destination: %v", err)
			}
			download433NoTemps(t, dir)
		})
	}
}

func TestPinnedFetchStopsAtLocalFilesystemFailure(t *testing.T) {
	for _, phase := range []string{"cache read", "publication"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pinned.jar")
			if phase == "cache read" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			body := &download433Body{Reader: strings.NewReader("wanted")}
			download433Client(t, func(*http.Request) (*http.Response, error) {
				calls++
				if phase == "publication" && calls == 1 {
					// A directory appears after the initial cache check. The
					// verified temporary file cannot replace it.
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				return download433Response(200, "OK", body), nil
			})
			err := fetch(context.Background(), "https://cdn.example.invalid/pinned", path, download433Hash("wanted"))
			wantCalls := 0
			if phase == "publication" {
				wantCalls = 1
			}
			var pathErr *os.PathError
			var linkErr *os.LinkError
			if err == nil || (!errors.As(err, &pathErr) && !errors.As(err, &linkErr)) || calls != wantCalls {
				t.Fatalf("requests=%d, error=%v; want %d requests and the filesystem error", calls, err, wantCalls)
			}
			if calls > 0 && !body.closed {
				t.Error("response body was not closed")
			}
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				t.Fatalf("destination directory changed: %v, %v", info, err)
			}
			download433NoTemps(t, dir)
		})
	}
}

type downloadCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r downloadCancelReader) Read(p []byte) (int, error) {
	r.cancel()
	return r.Reader.Read(p)
}

func TestPinnedFetchCancellationDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pinned.jar")
	if err := os.WriteFile(path, []byte("old file"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	body := &download433Body{Reader: downloadCancelReader{Reader: strings.NewReader("wanted"), cancel: cancel}}
	download433Client(t, func(*http.Request) (*http.Response, error) {
		calls++
		return download433Response(200, "OK", body), nil
	})
	err := fetch(ctx, "https://cdn.example.invalid/pinned", path, download433Hash("wanted"))
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("requests=%d, error=%v; want cancellation after one request", calls, err)
	}
	if !body.closed {
		t.Error("response body was not closed")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "old file" {
		t.Fatalf("cancelled download changed the destination: %q, %v", got, err)
	}
	download433NoTemps(t, dir)
}
