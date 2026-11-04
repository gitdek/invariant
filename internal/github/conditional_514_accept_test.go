package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This fake implements gh's JSON output and --include HTTP envelope. Its
// server returns 304 only for the correct representation's validator. There
// is no network, and all credentials below are fixture strings.
type conditionalGH514 struct {
	dir string
	cli string
}

func shellQuote514(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func newConditionalGH514(t *testing.T) conditionalGH514 {
	t.Helper()
	dir := t.TempDir()
	script := `dir=` + shellQuote514(dir) + `
included=no
paginate=no
method=GET
endpoint=
validator=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --include|-i) included=yes ;;
    --paginate) paginate=yes ;;
    -X|--method) shift; method=$1 ;;
    -H|--header)
      shift
      case "$1" in
        [Ii][Ff]-[Nn][Oo][Nn][Ee]-[Mm][Aa][Tt][Cc][Hh]:*) validator=$(printf '%s' "$1" | cut -d: -f2- | sed 's/^ *//') ;;
      esac ;;
    repos/*|/repos/*|https://api.github.com/repos/*) endpoint=$1 ;;
  esac
  shift
done
case "$endpoint" in
  *issues/7/comments*) key=comments ;;
  *issues/7/events*) key=events ;;
  *issues/7/labels) key=issue ;;
  *issues/7) key=issue ;;
  *pulls/9) key=pull ;;
  *check-runs*) key=checks ;;
  *pulls\?*) key=merges ;;
  *issues\?*)
    case "$endpoint" in *state=closed*) key=closed ;; *) key=open ;; esac
    case "$endpoint" in *page=2*) key=${key}2 ;; esac ;;
  *) echo 'gh: unexpected fixture endpoint (HTTP 404)' >&2; exit 1 ;;
esac
case "$endpoint" in *repos/acme/gadgets/*) key=${key}-other ;; esac
case "${GH_TOKEN-}" in fixture-beta-514) key=${key}-beta ;; esac
printf '%s\t%s\t%s\n' "$method" "$endpoint" "$validator" >> "$dir/calls"
mode=ok
if [ -f "$dir/mode" ]; then mode=$(cat "$dir/mode"); fi
case "$mode" in
  fail) echo 'gh: Bad Gateway (HTTP 502)' >&2; exit 1 ;;
  forbidden) echo 'gh: Resource not accessible by integration (HTTP 403)' >&2; exit 1 ;;
esac
if [ "$method" != GET ]; then
  if [ -n "$validator" ]; then echo 'mutation had a GET validator (HTTP 400)' >&2; exit 1; fi
  cat > /dev/null
  printf '%s' '{"number":7,"title":"after write","labels":[{"name":"new"}]}' > "$dir/issue.json"
  printf '%s' '"after-write"' > "$dir/issue.etag"
  if [ "$included" = yes ]; then printf 'HTTP/2.0 200 OK\r\n\r\n'; fi
  printf '%s\n' '[{"name":"new"}]'
  exit 0
fi
serve() {
  etag=
  if [ -f "$dir/$key.etag" ]; then etag=$(cat "$dir/$key.etag"); fi
  status='200 OK'
  if [ -n "$etag" ] && [ "$validator" = "$etag" ]; then status='304 Not Modified'; fi
  if [ "$included" = yes ]; then
    printf 'HTTP/2.0 %s\r\n' "$status"
    if [ -n "$etag" ]; then printf 'ETag: %s\r\n' "$etag"; fi
    if [ "$status" = '200 OK' ] && [ -f "$dir/$key.link" ]; then printf 'Link: <%s>; rel="next"\r\n' "$(cat "$dir/$key.link")"; fi
    printf '\r\n'
  fi
  if [ "$status" = '200 OK' ]; then cat "$dir/$key.json"; printf '\n'; fi
}
serve
if [ "$paginate" = yes ] && [ -f "$dir/$key.link" ]; then
  endpoint=$(cat "$dir/$key.link")
  key=${key}2
  printf '%s\t%s\t%s\n' "$method" "$endpoint" "$validator" >> "$dir/calls"
  serve
fi
`
	return conditionalGH514{dir: dir, cli: fakeGH(t, script)}
}

func (g conditionalGH514) put(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (g conditionalGH514) representation(t *testing.T, key, etag, body string) {
	t.Helper()
	g.put(t, key+".etag", etag)
	g.put(t, key+".json", body)
}

func (g conditionalGH514) calls(t *testing.T) [][3]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	var out [][3]string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		p := strings.Split(line, "\t")
		if len(p) != 3 {
			t.Fatalf("bad fixture call %q", line)
		}
		out = append(out, [3]string{p[0], p[1], p[2]})
	}
	return out
}

func (g conditionalGH514) client() Client {
	return Client{Repo: "acme/widgets", GH: g.cli, Token: func(context.Context) (string, error) { return "fixture-alpha-514", nil }}
}

func json514(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGitHub514ConditionalReads(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		key, first, changed string
		read                func(Client) (any, error)
	}{
		{"issue", `{"number":7,"title":"before","labels":[{"name":"old"}]}`, `{"number":7,"title":"after","labels":[{"name":"new"}]}`, func(c Client) (any, error) { return c.Issue(ctx, 7) }},
		{"comments", `[{"id":1,"body":"before"}]`, `[{"id":1,"body":"edited"},{"id":2,"body":"new"}]`, func(c Client) (any, error) { return c.Comments(ctx, 7) }},
		{"events", `[{"event":"labeled"}]`, `[{"event":"unlabeled"}]`, func(c Client) (any, error) { return c.Events(ctx, 7) }},
		{"pull", `{"number":9,"head":{"sha":"a"}}`, `{"number":9,"head":{"sha":"b"}}`, func(c Client) (any, error) { return c.PullRequest(ctx, 9) }},
		{"checks", `{"check_runs":[{"id":3,"status":"in_progress"}]}`, `{"check_runs":[{"id":3,"status":"completed","conclusion":"success"}]}`, func(c Client) (any, error) { return c.CheckRuns(ctx, "abc", "invariant/gate") }},
		{"open", `[{"number":7,"title":"before"}]`, `[{"number":7,"title":"after"},{"number":8}]`, func(c Client) (any, error) { return c.OpenIssues(ctx) }},
		{"merges", `[{"number":9,"merged_at":"2026-11-03T12:00:00Z"}]`, `[{"number":10,"merged_at":"2026-11-03T12:01:00Z"}]`, func(c Client) (any, error) { return c.MergedPullRequests(ctx) }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			g := newConditionalGH514(t)
			c := g.client()
			g.representation(t, tc.key, `"first"`, tc.first)
			first, err := tc.read(c)
			if err != nil {
				t.Fatal(err)
			}
			wantFirst := json514(t, first)
			if is, ok := first.(Issue); ok {
				is.Labels[0].Name = "caller mutation"
			}
			same, err := tc.read(c)
			if err != nil || json514(t, same) != wantFirst {
				t.Fatalf("304 lost or aliased the representation: %v, %v", same, err)
			}
			g.representation(t, tc.key, `W/"changed"`, tc.changed)
			changed, err := tc.read(c)
			if err != nil || json514(t, changed) == wantFirst {
				t.Fatalf("changed representation was not returned: %v, %v", changed, err)
			}
			same, err = tc.read(c)
			if err != nil || !reflect.DeepEqual(same, changed) {
				t.Fatalf("new 304 did not reuse the new body: %v, %v", same, err)
			}
			calls := g.calls(t)
			if len(calls) != 4 {
				t.Fatalf("each read must revalidate with GitHub, got %v", calls)
			}
			for i, want := range []string{"", `"first"`, `"first"`, `W/"changed"`} {
				if calls[i][2] != want {
					t.Errorf("read %d sent validator %q; want %q", i+1, calls[i][2], want)
				}
			}
			// A 200 without an ETag retires the old validator.
			g.representation(t, tc.key, "", tc.first)
			if _, err := tc.read(c); err != nil {
				t.Fatal(err)
			}
			g.put(t, tc.key+".json", tc.changed)
			if _, err := tc.read(c); err != nil {
				t.Fatal(err)
			}
			calls = g.calls(t)
			if calls[len(calls)-1][2] != "" {
				t.Error("a response without an ETag left an obsolete validator")
			}
		})
	}
}

func TestGitHub514ConditionalPages(t *testing.T) {
	g := newConditionalGH514(t)
	c := g.client()
	ctx := context.Background()
	page2 := "https://api.github.com/repos/acme/widgets/issues?state=open&per_page=100&page=2"
	g.representation(t, "open", `"page-one"`, `[{"number":1}]`)
	g.put(t, "open.link", page2)
	g.representation(t, "open2", `"page-two"`, `[{"number":2}]`)
	read := func(want ...int) {
		t.Helper()
		issues, err := c.OpenIssues(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got []int
		for _, is := range issues {
			got = append(got, is.Number)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paginated issues = %v; want %v", got, want)
		}
	}
	read(1, 2)
	read(1, 2)
	calls := g.calls(t)
	if len(calls) != 4 || calls[2][2] != `"page-one"` || calls[3][2] != `"page-two"` {
		t.Errorf("each unchanged page needs its own validator: %v", calls)
	}
	g.representation(t, "open2", `"page-two-new"`, `[{"number":3}]`)
	read(1, 3) // First page's 304 must retain its link to the changed second page.
	g.representation(t, "open", `"short"`, `[{"number":4}]`)
	if err := os.Remove(filepath.Join(g.dir, "open.link")); err != nil {
		t.Fatal(err)
	}
	before := len(g.calls(t))
	read(4)
	if len(g.calls(t)) != before+1 {
		t.Error("a removed next page was fetched from obsolete pagination")
	}
	g.representation(t, "open", `"long"`, `[{"number":5}]`)
	g.put(t, "open.link", page2)
	g.representation(t, "open2", `"page-two-last"`, `[{"number":6}]`)
	read(5, 6)
}

func TestGitHub514CacheIsolationAndErrors(t *testing.T) {
	g := newConditionalGH514(t)
	c := g.client()
	ctx := context.Background()
	g.representation(t, "issue", `"alpha"`, `{"number":7,"title":"alpha"}`)
	g.representation(t, "issue-beta", `"beta"`, `{"number":7,"title":"beta"}`)
	g.representation(t, "issue-other", `"other"`, `{"number":7,"title":"other"}`)
	read := func(client Client, want string) {
		t.Helper()
		is, err := client.Issue(ctx, 7)
		if err != nil || is.Title != want {
			t.Fatalf("read = %+v, %v; want %s", is, err, want)
		}
	}
	read(c, "alpha")
	read(c, "alpha")
	beta := c
	beta.Token = func(context.Context) (string, error) { return "fixture-beta-514", nil }
	read(beta, "beta")
	other := c
	other.Repo = "acme/gadgets"
	read(other, "other")
	calls := g.calls(t)
	if len(calls) != 4 || calls[1][2] != `"alpha"` || calls[2][2] != "" || calls[3][2] != "" {
		t.Errorf("validators did not respect request and credential identity: %v", calls)
	}
	g.put(t, "mode", "fail")
	if _, err := c.Issue(ctx, 7); err == nil || !Transient(err) {
		t.Errorf("a 502 must remain a transient error, got %v", err)
	}
	g.put(t, "mode", "forbidden")
	if _, err := c.Issue(ctx, 7); !errors.Is(err, ErrNoPermission) {
		t.Errorf("a permission refusal became cached success: %v", err)
	}
	g.put(t, "mode", "ok")
	read(c, "alpha")
	if err := c.AddLabels(ctx, 7, "new"); err != nil {
		t.Fatal(err)
	}
	read(c, "after write")
	read(c, "after write")
	calls = g.calls(t)
	if calls[len(calls)-1][2] != `"after-write"` {
		t.Error("the representation after a mutation was not revalidated")
	}
	// Token providers can refresh credentials on the same Client value.
	current := "fixture-alpha-514"
	c.Token = func(context.Context) (string, error) { return current, nil }
	read(c, "after write")
	current = "fixture-beta-514"
	read(c, "beta")
	calls = g.calls(t)
	if last := calls[len(calls)-1][2]; last != "" && last != `"beta"` {
		t.Errorf("a refreshed credential received another credential's validator: %q", last)
	}
}

func TestGitHub514ClosedIssuesSince(t *testing.T) {
	g := newConditionalGH514(t)
	c := g.client()
	// This assertion gives a behavioral failure on the old API, while the
	// intended public method remains explicit for the implementation.
	reader, ok := any(c).(interface {
		ClosedIssues(context.Context, string) ([]Issue, error)
	})
	if !ok {
		t.Fatal("github.Client needs ClosedIssues(ctx, since) for one closed-issue scan")
	}
	g.representation(t, "closed", `"closed-one"`, `[{"number":1,"state":"closed","labels":[{"name":"invariant:asking"}]},{"number":2,"state":"closed","labels":[{"name":"bug"}]},{"number":3,"pull_request":{}}]`)
	g.representation(t, "closed2", `"closed-two"`, `[{"number":4,"state":"closed","labels":[{"name":"invariant:pr-open"}]}]`)
	since := "2026-11-03T12:00:00+01:00"
	for _, from := range []string{"", since} {
		query := url.Values{"state": {"closed"}, "per_page": {"100"}, "page": {"2"}}
		if from != "" {
			query.Set("since", from)
		}
		g.put(t, "closed.link", "https://api.github.com/repos/acme/widgets/issues?"+query.Encode())
		before := 0
		if _, err := os.Stat(filepath.Join(g.dir, "calls")); err == nil {
			before = len(g.calls(t))
		}
		got, err := reader.ClosedIssues(context.Background(), from)
		if err != nil {
			t.Fatal(err)
		}
		var numbers []int
		for _, is := range got {
			numbers = append(numbers, is.Number)
		}
		if !reflect.DeepEqual(numbers, []int{1, 2, 4}) {
			t.Fatalf("closed query dropped labels/pages or included PRs: %v", numbers)
		}
		calls := g.calls(t)[before:]
		if len(calls) != 2 {
			t.Fatalf("want one two-page query, got %v", calls)
		}
		for _, call := range calls {
			u, err := url.Parse(call[1])
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			if q.Get("state") != "closed" || q.Get("since") != from || q.Has("labels") {
				t.Errorf("wrong closed query: %s; since=%q", call[1], from)
			}
		}
	}
}
