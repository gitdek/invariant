package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "app.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return key, path
}

// The JWT is what GitHub expects: RS256 over the App's ID, short-lived.
func TestAppJWT(t *testing.T) {
	key, path := testKey(t)
	a := &App{ID: 12345, KeyPath: path}
	now := time.Unix(1_800_000_000, 0)
	jwt, err := a.jwt(now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %q", jwt)
	}
	enc := base64.RawURLEncoding
	var claims map[string]int64
	b, _ := enc.DecodeString(parts[1])
	json.Unmarshal(b, &claims)
	if claims["iss"] != 12345 || claims["iat"] != now.Unix()-60 || claims["exp"] != now.Unix()+540 {
		t.Errorf("claims = %v", claims)
	}
	sig, _ := enc.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Errorf("the signature doesn't verify: %v", err)
	}
}

// A token is minted through the installation, cached, and minted again only
// when it's about to expire.
func TestAppToken(t *testing.T) {
	_, path := testKey(t)
	minted := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && r.URL.Path != "/users/invariant-factory[bot]" {
			http.Error(w, "no jwt", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/gitdek/invariant/installation":
			w.Write([]byte(`{"id": 42}`))
		case "/app/installations/42/access_tokens":
			minted++
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test", "expires_at": time.Now().Add(time.Hour)})
		case "/app":
			w.Write([]byte(`{"slug": "invariant-factory"}`))
		case "/users/invariant-factory[bot]":
			w.Write([]byte(`{"id": 999}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &App{ID: 1, KeyPath: path, Repo: "gitdek/invariant", API: srv.URL}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		tok, err := a.Token(ctx)
		if err != nil || tok != "ghs_test" {
			t.Fatalf("token = %q, %v", tok, err)
		}
	}
	if minted != 1 {
		t.Errorf("minted %d tokens; the first should be reused", minted)
	}
	id, err := a.Identity(ctx)
	if err != nil || id.Login != "invariant-factory[bot]" || id.Email() != "999+invariant-factory[bot]@users.noreply.github.com" {
		t.Errorf("identity = %+v, %v", id, err)
	}
	a.Repo = "gitdek/elsewhere"
	a.token = ""
	if _, err := a.Token(ctx); err == nil || !strings.Contains(err.Error(), "isn't installed on gitdek/elsewhere") {
		t.Errorf("err = %v", err)
	}
}
