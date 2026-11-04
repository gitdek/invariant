package github

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConditionalConcurrentReads(t *testing.T) {
	gh := fakeGH(t, `case "$*" in
*'If-None-Match: "shared"'*) printf 'HTTP/2.0 304 Not Modified\r\n\r\n' ;;
*) printf 'HTTP/2.0 200 OK\r\nETag: "shared"\r\n\r\n{"number":7,"labels":[{"name":"original"}]}' ;;
esac
`)
	var tokens atomic.Int32
	c := Client{GH: gh, Repo: "o/r", Token: func(context.Context) (string, error) {
		tokens.Add(1)
		return "concurrent-fixture", nil
	}}
	ctx := context.Background()
	if _, err := c.Issue(ctx, 7); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			issue, err := c.Issue(ctx, 7)
			if err != nil || len(issue.Labels) != 1 || issue.Labels[0].Name != "original" {
				t.Errorf("conditional read = %+v, %v", issue, err)
				return
			}
			issue.Labels[0].Name = "caller edit"
		})
	}
	wg.Wait()
	if got := tokens.Load(); got != 17 {
		t.Fatalf("resolved credentials %d times for 17 reads", got)
	}
}

func TestConditionalCacheBounds(t *testing.T) {
	var cache conditionalCache
	key := func(n int) [32]byte { return sha256.Sum256([]byte(fmt.Sprint(n))) }
	for n := 0; n < conditionalEntries; n++ {
		cache.save(key(n), representation{etag: "etag", body: "body"})
	}
	cache.lookup(key(0)) // The least recently used entry is now 1.
	cache.save(key(conditionalEntries), representation{etag: "etag", body: "body"})
	if cache.lookup(key(0)).etag == "" || cache.lookup(key(1)).etag != "" {
		t.Fatal("entry bound did not evict the least recently used response")
	}
	large := representation{etag: "etag", body: strings.Repeat("x", conditionalBytes/2)}
	cache.save(key(-1), large)
	cache.save(key(-2), large)
	if cache.bytes > conditionalBytes || cache.lookup(key(-1)).etag != "" || cache.lookup(key(-2)).etag == "" {
		t.Fatal("byte bound did not evict older responses")
	}
	cache.save(key(-2), representation{body: "no validator"})
	if cache.lookup(key(-2)).etag != "" {
		t.Fatal("a response without a validator kept the old entry")
	}
	cache.save(key(-3), representation{etag: "large", body: strings.Repeat("x", conditionalBytes+1)})
	if cache.lookup(key(-3)).etag != "" {
		t.Fatal("an oversized response was cached")
	}
}

func TestConditionalRejectsUnmatched304(t *testing.T) {
	gh := fakeGH(t, `printf 'HTTP/2.0 304 Not Modified\r\nETag: "unknown"\r\n\r\n'`)
	c := Client{GH: gh, Repo: "o/r", Token: func(context.Context) (string, error) { return "fixture", nil }}
	if _, err := c.Issue(context.Background(), 7); err == nil {
		t.Fatal("a 304 without the matching body was accepted")
	}
}
