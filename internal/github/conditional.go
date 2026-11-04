package github

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const conditionalEntries = 256
const conditionalBytes = 32 << 20

// The cache holds immutable response bytes, never objects handed to callers.
// Client has value receivers, so copies share this bounded cache, keyed by
// the exact request and a digest of the CLI's resolved authentication.
var conditionalCache responseCache

type representation struct {
	etag string
	next string
	body []byte
}

func (r representation) size() int { return len(r.etag) + len(r.next) + len(r.body) }

type cachedResponse struct {
	key [32]byte
	representation
}

type responseCache struct {
	mu      sync.Mutex
	entries map[[32]byte]*list.Element
	recent  list.List
	bytes   int
}

func (c *responseCache) get(key [32]byte) representation {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.recent.MoveToFront(e)
		return e.Value.(cachedResponse).representation
	}
	return representation{}
}

func (c *responseCache) put(key [32]byte, r representation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[[32]byte]*list.Element{}
	}
	if e := c.entries[key]; e != nil {
		c.remove(e)
	}
	if r.etag == "" || r.size() > conditionalBytes {
		return
	}
	for len(c.entries) >= conditionalEntries || c.bytes+r.size() > conditionalBytes {
		c.remove(c.recent.Back())
	}
	// Do not retain the HTTP envelope or a larger buffer behind a slice.
	r.body, r.etag, r.next = bytes.Clone(r.body), strings.Clone(r.etag), strings.Clone(r.next)
	c.entries[key] = c.recent.PushFront(cachedResponse{key, r})
	c.bytes += r.size()
}

func (c *responseCache) remove(e *list.Element) {
	r := e.Value.(cachedResponse)
	c.bytes -= r.size()
	delete(c.entries, r.key)
	c.recent.Remove(e)
}

// get always asks GitHub, even with a cached body. The saved snapshot is the
// one whose validator this request sent, even if another request replaces
// or evicts it while this one waits. Mutations never pass through here.
func (c Client) get(ctx context.Context, path, accept string, wantJSON bool) (representation, error) {
	env, err := c.environment(ctx)
	if err != nil {
		return representation{}, err
	}
	host := environmentValue(env, "GH_HOST")
	if u, err := url.Parse(path); err == nil && u.Host != "" {
		host = u.Hostname()
	}
	if host == "" || host == "api.github.com" {
		host = "github.com"
	}
	tokenName, fallback := "GH_TOKEN", "GITHUB_TOKEN"
	if host != "github.com" && !strings.HasSuffix(host, ".ghe.com") {
		tokenName, fallback = "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"
	}
	known := environmentValue(env, tokenName) != "" || environmentValue(env, fallback) != ""
	if !known {
		// A login can change without the Client changing. Resolve gh's
		// credential locally for each read and bind this request to it. If
		// gh cannot identify it, make an unconditional, uncached request.
		if token, err := c.runWithEnv(ctx, env, nil, "auth", "token", "--hostname", host); err == nil && len(bytes.TrimSpace(token)) > 0 {
			env = append(env, tokenName+"="+strings.TrimSpace(string(token)))
			known = true
		}
	}
	cwd, _ := os.Getwd()
	identity, _ := json.Marshal([]any{c.gh(), c.Repo, cwd, env, path, accept})
	key := sha256.Sum256(identity)
	var saved representation
	if known {
		saved = conditionalCache.get(key)
	}
	args := []string{"api", "--include", "-X", "GET", "-H", "Accept: " + accept}
	if saved.etag != "" {
		args = append(args, "-H", "If-None-Match: "+saved.etag)
	}
	args = append(args, path)
	stdout, err := c.runIncluded(ctx, env, args...)
	if err != nil {
		return representation{}, err
	}
	status, headers, body, err := includedResponse(stdout)
	if err != nil {
		return representation{}, err
	}
	if status == 304 {
		if saved.etag == "" {
			return representation{}, fmt.Errorf("HTTP 304 without a matching cached representation")
		}
		if etag := headers.Get("ETag"); etag != "" {
			saved.etag = etag
		}
		if links, ok := headers["Link"]; ok {
			saved.next = nextPage(strings.Join(links, ","))
		}
		conditionalCache.put(key, saved)
		return saved, nil
	}
	if status < 200 || status >= 300 {
		message := fmt.Sprintf("%s (HTTP %d)", strings.TrimSpace(string(body)), status)
		switch {
		case status == 404:
			return representation{}, fmt.Errorf("%w: %s", ErrNotFound, message)
		case strings.Contains(message, "Resource not accessible by integration"):
			return representation{}, fmt.Errorf("%w: %s", ErrNoPermission, message)
		default:
			return representation{}, fmt.Errorf("%s", message)
		}
	}
	if wantJSON && len(bytes.TrimSpace(body)) != 0 && !json.Valid(body) {
		return representation{}, fmt.Errorf("GitHub returned invalid JSON")
	}
	r := representation{etag: headers.Get("ETag"), next: nextPage(strings.Join(headers.Values("Link"), ",")), body: body}
	if known {
		conditionalCache.put(key, r)
	}
	return r, nil
}

// runIncluded runs gh api --include. gh exits nonzero for every status
// outside 2xx, 304 Not Modified included, after printing the response, so a
// printed status line is GitHub's answer for get to read. A run that printed
// none, or was cut off, failed in gh itself.
func (c Client) runIncluded(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.gh(), args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil && (ctx.Err() != nil || !bytes.HasPrefix(stdout.Bytes(), []byte("HTTP/"))) {
		return nil, ghFailure(err, stdout.String(), stderr.String())
	}
	return stdout.Bytes(), nil
}

func environmentValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], name+"="); ok {
			return value
		}
	}
	return ""
}

// gh --include prints the HTTP envelope followed by the decoded body.
// A CLI wrapper may supply only the body; it has no validator to retain.
func includedResponse(b []byte) (int, textproto.MIMEHeader, []byte, error) {
	if !bytes.HasPrefix(b, []byte("HTTP/")) {
		return 200, nil, b, nil
	}
	head, body, ok := bytes.Cut(b, []byte("\r\n\r\n"))
	if !ok {
		head, body, ok = bytes.Cut(b, []byte("\n\n"))
	}
	if !ok {
		return 0, nil, nil, fmt.Errorf("incomplete GitHub HTTP headers")
	}
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(bytes.Clone(head), '\n', '\n'))))
	line, err := r.ReadLine()
	if err != nil {
		return 0, nil, nil, err
	}
	var protocol string
	var status int
	if _, err := fmt.Sscanf(line, "%s %d", &protocol, &status); err != nil {
		return 0, nil, nil, err
	}
	headers, err := r.ReadMIMEHeader()
	return status, headers, body, err
}

func nextPage(link string) string {
	for _, part := range strings.Split(link, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ">")
		if !ok || !strings.HasPrefix(target, "<") {
			continue
		}
		for _, param := range strings.Split(params, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if ok && name == "rel" && strings.Trim(value, "\"") == "next" {
				return strings.TrimPrefix(target, "<")
			}
		}
	}
	return ""
}
