// Package toolchain pins the verifiers Invariant runs, so a local run and a
// CI run check the same things with the same tools.
package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

// Pins. Changing any of them changes what a receipt certifies, so each change
// is a decision in decisions/log.md.
const (
	// JavaImage runs TLC. eclipse-temurin:21-jre, multi-arch index digest.
	JavaImage = "eclipse-temurin@sha256:49e21e16e3c86eb7816a44a67549910ed090fbeb40c29c525d58bf5e02e91b0f"

	// TLC v1.7.4 is the latest stable release of the TLA+ tools (D-0016).
	TLCRelease   = "v1.7.4"
	TLCJarURL    = "https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar"
	TLCJarSHA256 = "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"

	// GobraImage is ghcr.io/viperproject/gobra, built 2026-09-08. linux/amd64 only.
	GobraImage = "ghcr.io/viperproject/gobra@sha256:775879e8483561186291653d4c8a818c222eafd38f4b50a42d4b8873eebc9481"

	// GoImage builds, tests and explores Go implementations in a sandbox.
	// golang:1.27-alpine (Go 1.27.1), multi-arch index digest.
	GoImage = "golang@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414"
)

// Toolchain is a ready-to-use set of pinned verifiers.
type Toolchain struct {
	JavaImage  string
	TLCJar     string // local path, checksum verified
	GobraImage string
	GoImage    string
}

// Ensure checks that Docker is running and fetches the TLC jar into the user
// cache if it isn't there, verifying its checksum either way.
func Ensure(ctx context.Context) (Toolchain, error) {
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		return Toolchain{}, fmt.Errorf("docker isn't available (is the daemon running?): %w", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Toolchain{}, err
	}
	jar := filepath.Join(cache, "invariant", "tla2tools-"+TLCRelease+".jar")
	if err := fetch(ctx, TLCJarURL, jar, TLCJarSHA256); err != nil {
		return Toolchain{}, fmt.Errorf("TLC %s: %w", TLCRelease, err)
	}
	return Toolchain{JavaImage: JavaImage, TLCJar: jar, GobraImage: GobraImage, GoImage: GoImage}, nil
}

// fetch makes sure path holds the file at url with the given SHA-256.
func fetch(ctx context.Context, url, path, want string) error {
	if got, err := fileSHA256(path); err == nil && got == want {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, want)
	}
	return os.Rename(tmp.Name(), path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
