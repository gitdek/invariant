package formalize

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CheckContextReference binds a checker to one run's captured caller context.
// It carries no workspace path or operation that can replace that context.
type CheckContextReference struct {
	Endpoint string
	Token    string
	SHA256   string
}

// LoadCheckContext fetches the exact captured JSON before a checker starts.
// Only an entirely absent reference selects standalone checking.
func LoadCheckContext(ctx context.Context, ref CheckContextReference, parent int) (CheckContext, error) {
	if ref == (CheckContextReference{}) {
		return CheckContext{ParentPlan: parent}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return CheckContext{}, fmt.Errorf("caller draft context: %w", err)
	}
	if ref.Endpoint == "" || ref.Token == "" || ref.SHA256 == "" {
		return CheckContext{}, fmt.Errorf("caller draft context needs endpoint, token and sha256")
	}
	u, err := url.Parse(ref.Endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return CheckContext{}, fmt.Errorf("caller draft context needs a loopback HTTP endpoint")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return CheckContext{}, fmt.Errorf("caller draft context needs a loopback port")
	}
	digest, err := hex.DecodeString(strings.TrimPrefix(ref.SHA256, "sha256:"))
	if err != nil || !strings.HasPrefix(ref.SHA256, "sha256:") || len(digest) != sha256.Size {
		return CheckContext{}, fmt.Errorf("caller draft context has an invalid sha256 digest")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.Endpoint, nil)
	if err != nil {
		return CheckContext{}, fmt.Errorf("caller draft context request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ref.Token)
	// Neither proxies nor redirects may send this run's credential elsewhere.
	transport := &http.Transport{Proxy: nil, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Do(req)
	if err != nil {
		return CheckContext{}, fmt.Errorf("fetching caller draft context: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return CheckContext{}, fmt.Errorf("caller draft context service returned HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return CheckContext{}, fmt.Errorf("reading caller draft context: %w", err)
	}
	if fmt.Sprintf("sha256:%x", sha256.Sum256(b)) != ref.SHA256 {
		return CheckContext{}, fmt.Errorf("caller draft context sha256 mismatch")
	}
	var authority *CheckContext
	if err := json.Unmarshal(b, &authority); err != nil {
		return CheckContext{}, fmt.Errorf("invalid caller draft context: %w", err)
	}
	if authority == nil || authority.ParentPlan != parent {
		return CheckContext{}, fmt.Errorf("caller draft context is null or has the wrong parent plan")
	}
	if err := ctx.Err(); err != nil {
		return CheckContext{}, fmt.Errorf("caller draft context: %w", err)
	}
	return *authority, nil
}

// serveCheckContext owns immutable bytes in the caller process, outside the
// drafting workspace. It lives across retries and checker restarts, and closes
// on cancellation even when the backend has not returned yet.
func serveCheckContext(ctx context.Context, capture string) (CheckContextReference, func(), error) {
	if err := ctx.Err(); err != nil {
		return CheckContextReference{}, nil, err
	}
	var credential [32]byte
	if _, err := rand.Read(credential[:]); err != nil {
		return CheckContextReference{}, nil, err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return CheckContextReference{}, nil, fmt.Errorf("serving caller draft context: %w", err)
	}
	ref := CheckContextReference{
		Endpoint: "http://" + ln.Addr().String() + "/check-context",
		Token:    hex.EncodeToString(credential[:]),
		SHA256:   fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(capture))),
	}
	var connections sync.WaitGroup
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		ConnState: func(_ net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				connections.Add(1)
			case http.StateClosed:
				connections.Done()
			}
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+ref.Token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				http.Error(w, "only GET is allowed", http.StatusMethodNotAllowed)
				return
			}
			if r.URL.RequestURI() != "/check-context" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Length", strconv.Itoa(len(capture)))
			_, _ = io.WriteString(w, capture)
		}),
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(ln)
	}()
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
		case <-stop:
		case <-served:
		}
		_ = server.Close()
		<-served
		// Serve has stopped admitting connections, so no more can be added.
		connections.Wait()
	}()
	var once sync.Once
	return ref, func() {
		once.Do(func() { close(stop) })
		<-done
	}, nil
}
