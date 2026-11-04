package github

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Bodies are immutable strings, decoded anew for every caller. The cache
// is shared by Client values, bounded by both bytes and entries, and never
// serves a read without asking GitHub to validate that exact representation.
const conditionalEntries = 1024
const conditionalBytes = 32 << 20

type representation struct {
	etag string
	body string
	next string
}

func (r representation) size() int { return len(r.etag) + len(r.body) + len(r.next) }

type conditionalEntry struct {
	key [32]byte
	representation
}

type conditionalCache struct {
	mu      sync.Mutex
	entries map[[32]byte]*list.Element
	order   list.List
	bytes   int
}

var restCache conditionalCache

func (c *conditionalCache) lookup(key [32]byte) representation {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.order.MoveToFront(e)
		return e.Value.(conditionalEntry).representation
	}
	return representation{}
}

func (c *conditionalCache) save(key [32]byte, r representation) {
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
		c.remove(c.order.Back())
	}
	c.entries[key] = c.order.PushFront(conditionalEntry{key, r})
	c.bytes += r.size()
}

func (c *conditionalCache) remove(e *list.Element) {
	v := e.Value.(conditionalEntry)
	c.bytes -= v.size()
	delete(c.entries, v.key)
	c.order.Remove(e)
}

// readEnv resolves credentials once, for both the cache key and the actual
// request. gh's stored login can change too, so ask its local credential
// store when no environment token was supplied. If it can't identify one,
// make an ordinary read without caching it.
func (c Client) readEnv(ctx context.Context) ([]string, string, string, error) {
	env := os.Environ()
	value := func(key string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(env[i], key+"="); ok {
				return v
			}
		}
		return ""
	}
	host := value("GH_HOST")
	if host == "" {
		host = "github.com"
	}
	key, fallback := "GH_TOKEN", "GITHUB_TOKEN"
	if host != "github.com" && !strings.HasSuffix(host, ".ghe.com") {
		key, fallback = "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"
	}
	token := value(key)
	if token == "" {
		token = value(fallback)
	}
	if c.Token != nil {
		var err error
		token, err = c.Token(ctx)
		if err != nil {
			return nil, "", "", err
		}
	} else if token == "" {
		b, err := c.runEnv(ctx, nil, env, "auth", "token", "--hostname", host)
		if err == nil {
			token = strings.TrimSpace(string(b))
		}
		if ctx.Err() != nil {
			return nil, "", "", ctx.Err()
		}
	}
	if token != "" {
		env = append(env, key+"="+token)
	}
	return env, host, token, nil
}

func (c Client) get(ctx context.Context, path, accept string) (representation, error) {
	env, host, token, err := c.readEnv(ctx)
	if err != nil {
		return representation{}, fmt.Errorf("GET %s: %w", path, err)
	}
	gh, err := exec.LookPath(c.gh())
	if err != nil {
		gh = c.gh()
	}
	identity, _ := json.Marshal([]string{gh, host, c.Repo, path, accept, token})
	key := sha256.Sum256(identity)
	var saved representation
	if token != "" {
		saved = restCache.lookup(key)
	}
	args := []string{"api", "-X", "GET", "--include", "-H", "Accept: " + accept}
	if saved.etag != "" {
		args = append(args, "-H", "If-None-Match: "+saved.etag)
	}
	args = append(args, path)
	stdout, err := c.runEnv(ctx, nil, env, args...)
	if err != nil {
		return representation{}, fmt.Errorf("GET %s: %w", path, err)
	}
	status, header, body, err := includedResponse(stdout)
	if err != nil {
		return representation{}, fmt.Errorf("GET %s: %w", path, err)
	}
	if status == http.StatusNotModified {
		if saved.etag == "" {
			return representation{}, fmt.Errorf("GET %s: HTTP 304 without a matching cached response", path)
		}
		// A 304 may omit Link. A 200, even without Link, replaces it.
		if _, ok := header["Link"]; ok {
			saved.next = nextPage(header.Get("Link"))
		}
		if etag := header.Get("Etag"); etag != "" {
			saved.etag = etag
		}
		restCache.save(key, saved)
		return saved, nil
	}
	if status < 200 || status >= 300 {
		return representation{}, fmt.Errorf("GET %s: HTTP %d: %s", path, status, body)
	}
	r := representation{etag: header.Get("Etag"), body: body, next: nextPage(header.Get("Link"))}
	if token != "" {
		restCache.save(key, r)
	}
	return r, nil
}

// gh's --include envelope uses HTTP/2.0 as well as HTTP/1.1, so read its
// status line and MIME headers without treating its body as wire framing.
func includedResponse(b []byte) (int, textproto.MIMEHeader, string, error) {
	if !bytes.HasPrefix(b, []byte("HTTP/")) {
		// Wrappers that return only the body provide no validator.
		return http.StatusOK, nil, string(b), nil
	}
	r := bufio.NewReader(bytes.NewReader(b))
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, nil, "", err
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, nil, "", fmt.Errorf("invalid HTTP status line")
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, nil, "", err
	}
	header, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil {
		return 0, nil, "", err
	}
	body, err := io.ReadAll(r)
	return status, header, string(body), err
}

func nextPage(link string) string {
	for _, part := range strings.Split(link, ",") {
		fields := strings.Split(part, ";")
		for _, field := range fields[1:] {
			if strings.TrimSpace(field) == `rel="next"` {
				return strings.Trim(strings.TrimSpace(fields[0]), "<>")
			}
		}
	}
	return ""
}
