// Package toolchain pins the verifiers Invariant runs, so a local run and a
// CI run check the same things with the same tools.
package toolchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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

	// NodeImage runs TypeScript code, its tests and its conformance driver.
	// node:24-alpine (Node 24.21, which runs .ts directly), multi-arch digest.
	NodeImage = "node@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1"

	// PythonImage runs Python code, its tests and its conformance driver.
	// python:3.13-alpine (Python 3.13.15), multi-arch digest.
	PythonImage = "python@sha256:79e7a9b9ff1cbceff819f856fb374477792a5967759d94df266de7b7b4120e6f"

	// PythonVersion is the Python that PythonImage runs, so the two move
	// together. The plumbing sandbox is built from PythonImage, and CI runs
	// the Python tests again under this minor version (#332).
	PythonVersion = "3.13.15"
)

// Toolchain is a ready-to-use set of pinned verifiers.
type Toolchain struct {
	JavaImage   string
	TLCJar      string // local path, checksum verified
	GobraImage  string
	GoImage     string
	NodeImage   string
	PythonImage string
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
	return Toolchain{JavaImage: JavaImage, TLCJar: jar, GobraImage: GobraImage, GoImage: GoImage, NodeImage: NodeImage, PythonImage: PythonImage}, nil
}

// fetch makes sure path holds the file at url with the given SHA-256.
func fetch(ctx context.Context, url, path, want string) error {
	if got, err := fileSHA256(path); err == nil && got == want {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		retry, err := fetchOnce(ctx, url, path, want)
		if !retry || attempt == 3 {
			return err
		}
		if err := acquisitionBackoff(ctx, attempt); err != nil {
			return err
		}
	}
}

// acquisitionBackoff waits one then two seconds, without extending the caller's
// deadline. Only file downloads and pinned image pulls use it.
func acquisitionBackoff(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func fetchOnce(ctx context.Context, url, path, want string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return false, fmt.Errorf("download %s: unsupported protocol scheme %q", url, req.URL.Scheme)
	}
	if req.URL.Host == "" {
		return false, fmt.Errorf("download %s: missing host", url)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// A redirect refusal has a response; retrying it cannot repair the URL.
		var certificateErr *tls.CertificateVerificationError
		return resp == nil && !errors.As(err, &certificateErr), err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode >= 500 && resp.StatusCode <= 599 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden
		return retry, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		var fileErr *os.PathError
		return !errors.As(err, &fileErr), err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return false, fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, want)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, os.Rename(tmp.Name(), path)
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

// PlumbingDockerfile is the recipe for the plumbing sandbox: PythonImage,
// so a build tests collector code under the Python it runs under in
// production (#332), with GoImage's toolchain, and git, since the
// repository's tests build git repositories (D-0108).
//
//go:embed plumbing.Dockerfile
var PlumbingDockerfile []byte

// PlumbingImage returns the plumbing sandbox's tag, building it from its
// recipe the first time. Building needs the network; running never does.
func PlumbingImage(ctx context.Context) (string, error) {
	sum := sha256.Sum256(PlumbingDockerfile)
	tag := "invariant-plumbing:" + hex.EncodeToString(sum[:])[:12]
	if exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil {
		return tag, nil
	}
	cmd := Docker(ctx, "build", "-t", tag, "-")
	cmd.Stdin = bytes.NewReader(PlumbingDockerfile)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building the plumbing sandbox: %w\n%s", err, out.String())
	}
	return tag, nil
}

// NaginiDockerfile is the recipe for the Nagini sandbox: a Python base pinned
// by digest, the Java runtime from JavaImage, and Nagini, with every Python
// package pinned by wheel hash (D-0031, D-0032).
//
//go:embed nagini.Dockerfile
var NaginiDockerfile []byte

// NaginiRecipe identifies the Nagini sandbox by its recipe. Laptops and CI
// build it themselves, so the recipe, not a local image ID, is what a
// receipt records.
func NaginiRecipe() string {
	sum := sha256.Sum256(NaginiDockerfile)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NaginiImage returns the Nagini sandbox's tag, building it from the recipe
// the first time. Building needs the network; verifying never does.
func NaginiImage(ctx context.Context) (string, error) {
	tag := "invariant-nagini:" + NaginiRecipe()[len("sha256:"):][:12]
	if exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil {
		return tag, nil
	}
	cmd := Docker(ctx, "build", "--platform", "linux/amd64", "-t", tag, "-")
	cmd.Stdin = bytes.NewReader(NaginiDockerfile)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building the Nagini sandbox: %w\n%s", err, out.String())
	}
	return tag, nil
}
