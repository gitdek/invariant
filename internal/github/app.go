package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// App is the factory's own identity: a GitHub App installed on the
// repository (D-0041). It signs a short-lived JWT with the App's private key
// and trades it for an installation token, which lasts an hour. The key never
// leaves the file it's in, and the token is handed to gh and git through
// their environment, never on a command line.
type App struct {
	ID      int64  // the App's ID
	KeyPath string // the App's private key, a PEM file only its owner can read
	Repo    string // owner/name, where the App is installed
	API     string // the REST API's base URL; https://api.github.com when empty

	mu      sync.Mutex
	key     *rsa.PrivateKey
	token   string
	expires time.Time
}

// Identity is who the App acts as.
type Identity struct {
	Slug  string // the App's slug, such as invariant-factory
	Login string // its bot's login, such as invariant-factory[bot]
	ID    int64  // its bot's user ID
}

// Email is the address GitHub attributes the bot's commits to.
func (i Identity) Email() string {
	return fmt.Sprintf("%d+%s@users.noreply.github.com", i.ID, i.Login)
}

// Identity looks up the App's slug and its bot user.
func (a *App) Identity(ctx context.Context) (Identity, error) {
	jwt, err := a.jwt(time.Now())
	if err != nil {
		return Identity{}, err
	}
	var app struct {
		Slug string `json:"slug"`
	}
	if err := a.do(ctx, "GET", "/app", "Bearer "+jwt, &app); err != nil {
		return Identity{}, err
	}
	login := app.Slug + "[bot]"
	var bot struct {
		ID int64 `json:"id"`
	}
	if err := a.do(ctx, "GET", "/users/"+login, "", &bot); err != nil {
		return Identity{}, err
	}
	return Identity{Slug: app.Slug, Login: login, ID: bot.ID}, nil
}

// Token is a current installation token, minted anew when the last one has
// less than five minutes left.
func (a *App) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Until(a.expires) > 5*time.Minute {
		return a.token, nil
	}
	jwt, err := a.jwt(time.Now())
	if err != nil {
		return "", err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	if err := a.do(ctx, "GET", "/repos/"+a.Repo+"/installation", "Bearer "+jwt, &inst); err != nil {
		return "", fmt.Errorf("the App isn't installed on %s: %w", a.Repo, err)
	}
	var tok struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := a.do(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", inst.ID), "Bearer "+jwt, &tok); err != nil {
		return "", err
	}
	a.token, a.expires = tok.Token, tok.ExpiresAt
	return a.token, nil
}

// jwt is the App's proof of identity: RS256 over its ID, valid for nine
// minutes, backdated a minute for clock drift.
func (a *App) jwt(now time.Time) (string, error) {
	if a.key == nil {
		b, err := os.ReadFile(a.KeyPath)
		if err != nil {
			return "", fmt.Errorf("the App's private key: %w", err)
		}
		if a.key, err = parseKey(b); err != nil {
			return "", err
		}
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]int64{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": a.ID})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func parseKey(b []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("the App's private key isn't a PEM file")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the App's private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the App's private key isn't an RSA key")
	}
	return key, nil
}

func (a *App) do(ctx context.Context, method, path, auth string, out any) error {
	base := a.API
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s %s: %w", method, path, ErrNotFound)
	}
	if resp.StatusCode == http.StatusForbidden && bytes.Contains(body, []byte("Resource not accessible by integration")) {
		return fmt.Errorf("%s %s: %w", method, path, ErrNoPermission)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(body))
	}
	return json.Unmarshal(body, out)
}
