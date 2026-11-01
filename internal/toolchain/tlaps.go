package toolchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os/exec"
)

// TLAPS, the TLA+ Proof System, proves a ratified invariant of a model at
// every size (#360). Its only current builds are a rolling pre-release, so
// the tarball's SHA-256 pins the exact build, and TLAPSVersion is the tlapm
// commit it reports. Changing any of them is a decision.
const (
	TLAPSRelease       = "1.6.0-pre"
	TLAPSVersion       = "eef4b65"
	TLAPSTarballURL    = "https://github.com/tlaplus/tlapm/releases/download/1.6.0-pre/tlapm-1.6.0-pre-x86_64-linux-gnu.tar.gz"
	TLAPSTarballSHA256 = "b0038e59a6bddc9aa9a75e7c3320e20333f02fd7ba745ea64f1cc1c9800e43e8"
)

// TLAPSDockerfile is the recipe for the TLAPS sandbox: tlapm and the backends
// its release bundles, on the Temurin base TLC runs in (#360).
//
//go:embed tlaps.Dockerfile
var TLAPSDockerfile []byte

// TLAPSRecipe identifies the TLAPS sandbox by its recipe, as NaginiRecipe
// does Nagini's: laptops and CI build it themselves, so a receipt records the
// recipe, not a local image ID.
func TLAPSRecipe() string {
	sum := sha256.Sum256(TLAPSDockerfile)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TLAPSImage returns the TLAPS sandbox's tag, building it from the recipe the
// first time. Building needs the network; proving never does.
func TLAPSImage(ctx context.Context) (string, error) {
	tag := "invariant-tlaps:" + TLAPSRecipe()[len("sha256:"):][:12]
	if exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil {
		return tag, nil
	}
	cmd := Docker(ctx, "build", "--platform", "linux/amd64", "-t", tag, "-")
	cmd.Stdin = bytes.NewReader(TLAPSDockerfile)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building the TLAPS sandbox: %w\n%s", err, out.String())
	}
	return tag, nil
}
