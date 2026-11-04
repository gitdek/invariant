package github

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestConditionalConcurrentReadsHaveIndependentResults(t *testing.T) {
	g := newConditionalGH514(t)
	g.representation(t, "issue", `"one"`, `{"number":7,"labels":[{"name":"original"}]}`)
	c := g.client()
	if _, err := c.Issue(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			issue, err := c.Issue(context.Background(), 7)
			if err != nil || len(issue.Labels) != 1 || issue.Labels[0].Name != "original" {
				t.Errorf("concurrent read: %+v, %v", issue, err)
				return
			}
			issue.Labels[0].Name = "caller's change"
		}()
	}
	wg.Wait()
	for _, call := range g.calls(t)[1:] {
		if call[2] != `"one"` {
			t.Errorf("concurrent read didn't revalidate: %v", call)
		}
	}
}

func TestConditionalMalformedResponseDoesNotReplaceSuccess(t *testing.T) {
	g := newConditionalGH514(t)
	g.representation(t, "issue", `"good"`, `{"number":7,"title":"good"}`)
	c := g.client()
	ctx := context.Background()
	if _, err := c.Issue(ctx, 7); err != nil {
		t.Fatal(err)
	}
	g.representation(t, "issue", `"broken"`, `{"number":`)
	if _, err := c.Issue(ctx, 7); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
	g.representation(t, "issue", `"good"`, `{"number":7,"title":"good"}`)
	got, err := c.Issue(ctx, 7)
	if err != nil || got.Title != "good" {
		t.Fatalf("read after malformed response: %+v, %v", got, err)
	}
	calls := g.calls(t)
	if calls[len(calls)-1][2] != `"good"` {
		t.Fatal("malformed response replaced the last valid representation")
	}
}

func TestConditionalUnsolicited304IsAnError(t *testing.T) {
	c := Client{Repo: "o/r", GH: fakeGH(t, `printf 'HTTP/2.0 304 Not Modified\r\nETag: "unknown"\r\n\r\n'`),
		Token: func(context.Context) (string, error) { return "fixture", nil }}
	if _, err := c.Issue(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "304") {
		t.Fatalf("unsolicited 304: %v", err)
	}
}

func TestConditionalResolvesTokenOncePerRequest(t *testing.T) {
	g := newConditionalGH514(t)
	g.representation(t, "issue", `"alpha"`, `{"number":7,"title":"alpha"}`)
	g.representation(t, "issue-beta", `"beta"`, `{"number":7,"title":"beta"}`)
	c := g.client()
	count := 0
	c.Token = func(context.Context) (string, error) {
		count++
		if count%2 == 0 {
			return "fixture-beta-514", nil
		}
		return "fixture-alpha-514", nil
	}
	for i, want := range []string{"alpha", "beta", "alpha", "beta"} {
		got, err := c.Issue(context.Background(), 7)
		if err != nil || got.Title != want || count != i+1 {
			t.Fatalf("read %d: %+v, %v; token calls %d", i, got, err, count)
		}
	}
	calls := g.calls(t)
	if calls[2][2] != `"alpha"` || calls[3][2] != `"beta"` {
		t.Fatalf("validators didn't follow the request's resolved token: %v", calls)
	}
}

func TestConditionalLocalLoginChangesHaveSeparateValidators(t *testing.T) {
	for _, name := range []string{"GH_HOST", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(name, "")
	}
	g := newConditionalGH514(t)
	g.representation(t, "issue", `"alpha"`, `{"number":7,"title":"alpha"}`)
	g.representation(t, "issue-beta", `"beta"`, `{"number":7,"title":"beta"}`)
	cli := fakeGH(t, `if [ "$1" = auth ]; then
  cat `+shellQuote514(g.dir+"/login")+`
else
  exec `+shellQuote514(g.cli)+` "$@"
fi
`)
	c := Client{Repo: "acme/widgets", GH: cli}
	for _, login := range []string{"alpha", "alpha", "beta", "beta"} {
		g.put(t, "login", "fixture-"+login+"-514")
		got, err := c.Issue(context.Background(), 7)
		if err != nil || got.Title != login {
			t.Fatalf("login %s: %+v, %v", login, got, err)
		}
	}
	calls := g.calls(t)
	for i, want := range []string{"", `"alpha"`, "", `"beta"`} {
		if calls[i][2] != want {
			t.Fatalf("read %d sent %q; want %q", i, calls[i][2], want)
		}
	}
}

func TestConditionalCacheEvictsByCountAndSize(t *testing.T) {
	var cache responseCache
	key := func(i int) [32]byte { return sha256.Sum256([]byte(fmt.Sprint(i))) }
	for i := 0; i <= conditionalEntries; i++ {
		cache.put(key(i), representation{etag: "v", body: []byte("{}")})
	}
	if cache.get(key(0)).etag != "" || cache.get(key(1)).etag != "v" {
		t.Fatal("response count didn't evict the least recently used entry")
	}
	large := representation{etag: "large", body: make([]byte, conditionalBytes/2)}
	cache.put(key(-1), large)
	cache.put(key(-2), large)
	if cache.get(key(-1)).etag != "" || cache.get(key(-2)).etag != "large" {
		t.Fatal("response bytes didn't bound the cache")
	}
	cache.put(key(-2), representation{})
	if cache.get(key(-2)).etag != "" {
		t.Fatal("a response without a validator retained the old one")
	}
}
