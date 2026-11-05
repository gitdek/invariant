package github

import (
	"context"
	"errors"
	"testing"
)

// The real gh exits 1 for every status outside 2xx, 304 Not Modified
// included, after printing the response with --include. A validated read
// must still reuse its cached body, as #514's CI found when every repeated
// read failed with "gh: HTTP 304".
func TestConditionalReadsSurviveGHsNonzeroExit(t *testing.T) {
	gh := fakeGH(t, `path=
validator=
while [ $# -gt 0 ]; do
  case "$1" in
    -H) case "$2" in "If-None-Match: "*) validator=${2#If-None-Match: } ;; esac; shift ;;
    repos/*) path=$1 ;;
  esac
  shift
done
case "$path" in
repos/acme/exit514/issues/7)
  if [ "$validator" = '"v1"' ]; then
    printf 'HTTP/2.0 304 Not Modified\r\nEtag: "v1"\r\n\r\n'
    echo 'gh: HTTP 304' >&2
    exit 1
  fi
  printf 'HTTP/2.0 200 OK\r\nEtag: "v1"\r\n\r\n{"number":7}\n' ;;
repos/acme/exit514/issues/8)
  printf 'HTTP/2.0 404 Not Found\r\n\r\n{"message":"Not Found"}\n'
  echo 'gh: Not Found (HTTP 404)' >&2
  exit 1 ;;
*)
  echo 'gh: Bad Gateway (HTTP 502)' >&2
  exit 1 ;;
esac
`)
	c := Client{Repo: "acme/exit514", GH: gh, Token: func(context.Context) (string, error) { return "fixture-exit-514", nil }}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		got, err := c.get(ctx, "repos/acme/exit514/issues/7", "application/vnd.github+json", true)
		if err != nil || string(got.body) != "{\"number\":7}\n" {
			t.Fatalf("read %d: %q, %v", i+1, got.body, err)
		}
	}
	if _, err := c.get(ctx, "repos/acme/exit514/issues/8", "application/vnd.github+json", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a printed 404 isn't ErrNotFound: %v", err)
	}
	if _, err := c.get(ctx, "repos/acme/exit514/issues/9", "application/vnd.github+json", true); err == nil {
		t.Fatal("a run that printed no response succeeded")
	}
}
